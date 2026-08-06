package tunnel

import (
	"errors"
	"testing"
	"time"
)

func TestHeartbeatTracker_CompletesMatchingPong(t *testing.T) {
	// Given
	tracker := newHeartbeatTracker()
	sentAt := time.Unix(10, 0)
	now := sentAt.Add(12 * time.Millisecond)
	done, err := tracker.begin("nonce-1", sentAt)
	if err != nil {
		t.Fatalf("begin heartbeat: %v", err)
	}

	// When
	matched := tracker.resolvePong("nonce-1", now)

	// Then
	if !matched {
		t.Fatal("matching pong was not accepted")
	}
	result := <-done
	if result.RTT != 12*time.Millisecond || result.TimedOut || result.Closed {
		t.Fatalf("unexpected heartbeat result: %+v", result)
	}
}

func TestHeartbeatTracker_IgnoresStalePong(t *testing.T) {
	// Given
	tracker := newHeartbeatTracker()
	sentAt := time.Unix(20, 0)
	done, err := tracker.begin("current", sentAt)
	if err != nil {
		t.Fatalf("begin heartbeat: %v", err)
	}

	// When
	matched := tracker.resolvePong("stale", sentAt.Add(time.Millisecond))

	// Then
	if matched {
		t.Fatal("stale pong was accepted")
	}
	select {
	case result := <-done:
		t.Fatalf("stale pong completed heartbeat: %+v", result)
	default:
	}
	if !tracker.resolvePong("current", sentAt.Add(2*time.Millisecond)) {
		t.Fatal("current pong was not accepted after stale pong")
	}
	result := <-done
	if result.RTT != 2*time.Millisecond {
		t.Fatalf("unexpected RTT after stale pong: %s", result.RTT)
	}
}

func TestHeartbeatTracker_RejectsOverlappingStart(t *testing.T) {
	// Given
	tracker := newHeartbeatTracker()
	first, err := tracker.begin("first", time.Unix(30, 0))
	if err != nil {
		t.Fatalf("begin first heartbeat: %v", err)
	}

	// When
	second, err := tracker.begin("second", time.Unix(31, 0))

	// Then
	if second != nil || !errors.Is(err, errHeartbeatPending) {
		t.Fatalf("overlapping begin result = channel %v, error %v", second, err)
	}
	tracker.timeout("first")
	result := <-first
	if !result.TimedOut || result.Closed {
		t.Fatalf("first heartbeat was not timed out: %+v", result)
	}
}

func TestHeartbeatTracker_TimeoutClearsPending(t *testing.T) {
	// Given
	tracker := newHeartbeatTracker()
	first, err := tracker.begin("expired", time.Unix(40, 0))
	if err != nil {
		t.Fatalf("begin expired heartbeat: %v", err)
	}

	// When
	if !tracker.timeout("expired") {
		t.Fatal("timeout did not match pending heartbeat")
	}

	// Then
	result := <-first
	if !result.TimedOut || result.RTT != 0 || result.Closed {
		t.Fatalf("unexpected timeout result: %+v", result)
	}
	second, err := tracker.begin("next", time.Unix(41, 0))
	if err != nil || second == nil {
		t.Fatalf("begin after timeout = channel %v, error %v", second, err)
	}
}

func TestHeartbeatTracker_CloseUnblocksPending(t *testing.T) {
	// Given
	tracker := newHeartbeatTracker()
	done, err := tracker.begin("open", time.Unix(50, 0))
	if err != nil {
		t.Fatalf("begin heartbeat: %v", err)
	}

	// When
	tracker.close()

	// Then
	result := <-done
	if !result.Closed || result.TimedOut || result.RTT != 0 {
		t.Fatalf("unexpected close result: %+v", result)
	}
	if _, err := tracker.begin("after-close", time.Unix(51, 0)); !errors.Is(err, errHeartbeatClosed) {
		t.Fatalf("begin after close error = %v", err)
	}
}
