package tunnel

import (
	"context"
	"fmt"
	"io"
)

func (rt *clientRuntime) appendClientBytes(payload []byte) (uint64, string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	offset := rt.c2sNext
	rt.c2sNext += uint64(len(payload))
	rt.c2sBuf = append(rt.c2sBuf, payload...)
	return offset, rt.sessionID
}

func (rt *clientRuntime) markClientFin() {
	rt.mu.Lock()
	rt.c2sFin = true
	rt.c2sFinAt = rt.c2sNext
	rt.mu.Unlock()
}

func (rt *clientRuntime) sendClientFin() {
	for {
		conn, err := rt.waitActive(context.Background())
		if err != nil {
			return
		}
		rt.mu.Lock()
		sessionID := rt.sessionID
		offset := rt.c2sFinAt
		rt.mu.Unlock()
		if err := conn.send(frameHeader{Type: "fin", SessionID: sessionID, Direction: dirClientToServer, Offset: offset}, nil); err != nil {
			rt.dropActive(conn)
			continue
		}
		return
	}
}

func (rt *clientRuntime) advanceC2SAckLocked(offset uint64) error {
	if offset < rt.c2sAck {
		return nil
	}
	if offset > rt.c2sNext {
		return fmt.Errorf("client_to_server ack is ahead of sender")
	}
	drop := offset - rt.c2sBufFrom
	if drop > uint64(len(rt.c2sBuf)) {
		return fmt.Errorf("client_to_server ack outside buffer")
	}
	rt.c2sBuf = rt.c2sBuf[drop:]
	rt.c2sBufFrom = offset
	rt.c2sAck = offset
	return nil
}

func (rt *clientRuntime) waitActive(ctx context.Context) (*clientConn, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for rt.active == nil {
		select {
		case <-rt.done:
			if rt.doneErr != nil {
				return nil, rt.doneErr
			}
			return nil, io.EOF
		default:
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rt.cond.Wait()
	}
	return rt.active, nil
}

func (rt *clientRuntime) waitBuffer(ctx context.Context) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for len(rt.c2sBuf) >= rt.cfg.Resume.BufferBytes {
		select {
		case <-rt.done:
			if rt.doneErr != nil {
				return rt.doneErr
			}
			return io.EOF
		default:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rt.cond.Wait()
	}
	return nil
}

func (rt *clientRuntime) dropActive(conn *clientConn) {
	rt.mu.Lock()
	dropped := false
	if rt.active == conn {
		rt.active = nil
		dropped = true
		rt.cond.Broadcast()
	}
	rt.mu.Unlock()
	if dropped {
		select {
		case rt.reconnectSignal <- struct{}{}:
		default:
		}
	}
	_ = conn.close()
}

func (rt *clientRuntime) finish(err error) {
	rt.doneOnce.Do(func() {
		rt.mu.Lock()
		active := rt.active
		rt.active = nil
		rt.doneErr = err
		rt.finished = true
		rt.cond.Broadcast()
		rt.mu.Unlock()
		if active != nil {
			_ = active.close()
		}
		close(rt.done)
	})
}

func (rt *clientRuntime) wait() error {
	<-rt.done
	return rt.doneErr
}
