package tunnel

import (
	"context"
	"time"
)

type activeHeartbeatResult struct {
	RTT      time.Duration
	TimedOut bool
	Closed   bool
	Err      error
}

func (rt *clientRuntime) activeHeartbeat(ctx context.Context, conn *clientConn, timeout time.Duration) activeHeartbeatResult {
	if !rt.isActive(conn) {
		return activeHeartbeatResult{Closed: true}
	}

	nonce := newNonce()
	sentAt := time.Now()
	done, err := conn.heartbeat.begin(nonce, sentAt)
	if err != nil {
		return activeHeartbeatResult{Closed: err == errHeartbeatClosed, Err: err}
	}
	if err := conn.send(frameHeader{Type: "ping", Nonce: nonce, SentAtUnixNano: sentAt.UnixNano()}, nil); err != nil {
		rt.dropActive(conn)
		return activeHeartbeatResult{Closed: true, Err: err}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return heartbeatCycleResult(result)
	case <-timer.C:
		if conn.heartbeat.timeout(nonce) {
			return activeHeartbeatResult{TimedOut: true}
		}
		return heartbeatCycleResult(<-done)
	case <-ctx.Done():
		if conn.heartbeat.timeout(nonce) {
			return activeHeartbeatResult{Err: ctx.Err()}
		}
		return heartbeatCycleResult(<-done)
	}
}

func (rt *clientRuntime) isActive(conn *clientConn) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.active == conn
}

func heartbeatCycleResult(result heartbeatResult) activeHeartbeatResult {
	return activeHeartbeatResult{
		RTT:      result.RTT,
		TimedOut: result.TimedOut,
		Closed:   result.Closed,
	}
}
