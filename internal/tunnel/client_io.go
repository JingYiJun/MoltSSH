package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
)

func (rt *clientRuntime) stdinLoop(ctx context.Context) {
	buf := make([]byte, dataChunkBytes)
	for {
		conn, err := rt.waitActive(ctx)
		if err != nil {
			rt.finish(err)
			return
		}
		if err := rt.waitBuffer(ctx); err != nil {
			rt.finish(err)
			return
		}
		n, err := rt.stdin.Read(buf)
		if n > 0 {
			payload := append([]byte(nil), buf[:n]...)
			offset, sessionID := rt.appendClientBytes(payload)
			if sendErr := conn.send(frameHeader{Type: "data", SessionID: sessionID, Direction: dirClientToServer, Offset: offset}, payload); sendErr != nil {
				rt.dropActive(conn)
			}
		}
		if errors.Is(err, io.EOF) {
			rt.markClientFin()
			rt.sendClientFin()
			return
		}
		if err != nil {
			rt.finish(err)
			return
		}
	}
}

func (rt *clientRuntime) replayClientBytes(conn *clientConn) error {
	rt.mu.Lock()
	offset := rt.c2sBufFrom
	buf := append([]byte(nil), rt.c2sBuf...)
	fin := rt.c2sFin
	finAt := rt.c2sFinAt
	sessionID := rt.sessionID
	rt.mu.Unlock()

	for len(buf) > 0 {
		n := min(len(buf), maxPayloadBytes)
		if err := conn.send(frameHeader{Type: "data", SessionID: sessionID, Direction: dirClientToServer, Offset: offset}, buf[:n]); err != nil {
			rt.dropActive(conn)
			return err
		}
		offset += uint64(n)
		buf = buf[n:]
	}
	if fin {
		if err := conn.send(frameHeader{Type: "fin", SessionID: sessionID, Direction: dirClientToServer, Offset: finAt}, nil); err != nil {
			rt.dropActive(conn)
			return err
		}
	}
	return nil
}

func (rt *clientRuntime) handleServerData(conn *clientConn, f frameHeader, payload []byte) error {
	if f.Direction != dirServerToClient {
		return fmt.Errorf("bad data direction")
	}
	rt.mu.Lock()
	rx := rt.s2cRx
	rt.mu.Unlock()
	if f.Offset < rx {
		if f.Offset+uint64(len(payload)) <= rx {
			return conn.send(frameHeader{Type: "ack", SessionID: rt.sessionID, Direction: dirServerToClient, ReceivedOffset: rx}, nil)
		}
		return fmt.Errorf("partial duplicate server data")
	}
	if f.Offset > rx {
		return fmt.Errorf("server data offset gap")
	}
	n, err := writeAll(rt.stdout, payload)
	rt.mu.Lock()
	rt.s2cRx += uint64(n)
	rx = rt.s2cRx
	rt.mu.Unlock()
	_ = conn.send(frameHeader{Type: "ack", SessionID: rt.sessionID, Direction: dirServerToClient, ReceivedOffset: rx}, nil)
	return err
}

func (rt *clientRuntime) handleServerFin(conn *clientConn, f frameHeader) error {
	if f.Direction != dirServerToClient {
		return fmt.Errorf("bad fin direction")
	}
	rt.mu.Lock()
	rx := rt.s2cRx
	rt.mu.Unlock()
	if f.Offset != rx {
		return fmt.Errorf("server fin offset gap")
	}
	closeWrite(rt.stdout)
	_ = conn.send(frameHeader{Type: "ack", SessionID: rt.sessionID, Direction: dirServerToClient, ReceivedOffset: rx}, nil)
	return nil
}
