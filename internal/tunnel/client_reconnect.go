package tunnel

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

func (rt *clientRuntime) reconnectLoop(ctx context.Context) {
	runReconnectLoop(ctx, rt.cfg.Resume.Timeout, reconnectDependencies{
		now: time.Now,
		newTimer: func(delay time.Duration) reconnectTimer {
			return systemReconnectTimer{timer: time.NewTimer(delay)}
		},
		random: rand.Float64,
		state: func() reconnectState {
			rt.mu.Lock()
			defer rt.mu.Unlock()
			return reconnectState{active: rt.active != nil, session: rt.sessionID != ""}
		},
		attempt: func(attemptCtx context.Context) error {
			return rt.connectAny(attemptCtx, true)
		},
		finish: rt.finish,
		done:   rt.done,
		wake:   rt.reconnectSignal,
	})
}

type reconnectTimer interface {
	C() <-chan time.Time
	Stop()
}

type systemReconnectTimer struct {
	timer *time.Timer
}

func (t systemReconnectTimer) C() <-chan time.Time { return t.timer.C }

func (t systemReconnectTimer) Stop() { t.timer.Stop() }

type reconnectState struct {
	active  bool
	session bool
}

type reconnectDependencies struct {
	now      func() time.Time
	newTimer func(time.Duration) reconnectTimer
	random   func() float64
	state    func() reconnectState
	attempt  func(context.Context) error
	finish   func(error)
	done     <-chan struct{}
	wake     <-chan struct{}
}

func runReconnectLoop(ctx context.Context, timeout time.Duration, deps reconnectDependencies) {
	policy := retryPolicy{}
	deadline := time.Time{}
	attempt := 0
	for {
		select {
		case <-deps.done:
			return
		case <-ctx.Done():
			return
		default:
		}
		state := deps.state()
		if state.active || !state.session {
			deadline = time.Time{}
			attempt = 0
			if !waitForReconnectSignal(ctx, deps) {
				return
			}
			continue
		}
		if deadline.IsZero() {
			deadline = deps.now().Add(timeout)
		}
		decision := policy.next(attempt, deadline.Sub(deps.now()), deps.random())
		if decision.Exhausted {
			deps.finish(fmt.Errorf("resume timeout"))
			return
		}
		if decision.Delay > 0 {
			timer := deps.newTimer(decision.Delay)
			if !waitForReconnectTimer(ctx, deps, timer) {
				return
			}
		}
		if !deps.now().Before(deadline) {
			deps.finish(fmt.Errorf("resume timeout"))
			return
		}
		attemptCtx, cancel := context.WithDeadline(ctx, deadline)
		err := deps.attempt(attemptCtx)
		cancel()
		if err == nil {
			deadline = time.Time{}
			attempt = 0
			continue
		}
		attempt++
	}
}

func waitForReconnectSignal(ctx context.Context, deps reconnectDependencies) bool {
	select {
	case <-deps.done:
		return false
	case <-ctx.Done():
		return false
	case <-deps.wake:
		return true
	}
}

func waitForReconnectTimer(ctx context.Context, deps reconnectDependencies, timer reconnectTimer) bool {
	select {
	case <-deps.done:
		timer.Stop()
		return false
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-deps.wake:
		timer.Stop()
		return true
	case <-timer.C():
		return true
	}
}
