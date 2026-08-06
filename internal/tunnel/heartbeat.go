package tunnel

import (
	"errors"
	"sync"
	"time"
)

var (
	errHeartbeatPending = errors.New("heartbeat already pending")
	errHeartbeatClosed  = errors.New("heartbeat tracker closed")
)

type heartbeatResult struct {
	RTT      time.Duration
	TimedOut bool
	Closed   bool
}

type heartbeatPending struct {
	nonce  string
	sentAt time.Time
	done   chan heartbeatResult
}

type heartbeatTracker struct {
	mu      sync.Mutex
	pending *heartbeatPending
	closed  bool
}

func newHeartbeatTracker() *heartbeatTracker {
	return &heartbeatTracker{}
}

func (t *heartbeatTracker) begin(nonce string, sentAt time.Time) (<-chan heartbeatResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errHeartbeatClosed
	}
	if t.pending != nil {
		return nil, errHeartbeatPending
	}
	done := make(chan heartbeatResult, 1)
	t.pending = &heartbeatPending{nonce: nonce, sentAt: sentAt, done: done}
	return done, nil
}

func (t *heartbeatTracker) resolvePong(nonce string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil || t.pending.nonce != nonce {
		return false
	}
	pending := t.pending
	t.pending = nil
	rtt := now.Sub(pending.sentAt)
	if rtt < 0 {
		rtt = 0
	}
	pending.done <- heartbeatResult{RTT: rtt}
	return true
}

func (t *heartbeatTracker) timeout(nonce string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil || t.pending.nonce != nonce {
		return false
	}
	done := t.pending.done
	t.pending = nil
	done <- heartbeatResult{TimedOut: true}
	return true
}

func (t *heartbeatTracker) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	if t.pending == nil {
		return
	}
	done := t.pending.done
	t.pending = nil
	done <- heartbeatResult{Closed: true}
}
