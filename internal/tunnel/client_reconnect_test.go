package tunnel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type reconnectTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *reconnectTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *reconnectTestClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type reconnectTestTimer struct {
	c       chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newReconnectTestTimer() *reconnectTestTimer {
	return &reconnectTestTimer{c: make(chan time.Time, 1), stopped: make(chan struct{})}
}

func (t *reconnectTestTimer) C() <-chan time.Time { return t.c }

func (t *reconnectTestTimer) Stop() { t.once.Do(func() { close(t.stopped) }) }

func TestReconnectLoop_attemptsImmediately_thenWaitsForFullJitter(t *testing.T) {
	// Given
	clock := &reconnectTestClock{now: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timers := make(chan struct {
		delay time.Duration
		timer *reconnectTestTimer
	}, 1)
	attempts := make(chan context.Context, 2)
	done := make(chan struct{})
	state := reconnectState{session: true}
	deps := reconnectDependencies{
		now:    clock.Now,
		random: func() float64 { return 0.75 },
		newTimer: func(delay time.Duration) reconnectTimer {
			timer := newReconnectTestTimer()
			timers <- struct {
				delay time.Duration
				timer *reconnectTestTimer
			}{delay: delay, timer: timer}
			return timer
		},
		state: func() reconnectState { return state },
		attempt: func(attemptCtx context.Context) error {
			attempts <- attemptCtx
			return errors.New("dial failed")
		},
		finish: func(error) { t.Fatal("resume must not time out") },
		done:   done,
		wake:   make(chan struct{}),
	}
	finished := make(chan struct{})
	go func() {
		runReconnectLoop(ctx, time.Second, deps)
		close(finished)
	}()

	// When
	firstAttempt := <-attempts
	timer := <-timers
	timer.timer.c <- clock.Now().Add(timer.delay)
	secondAttempt := <-attempts
	cancel()
	<-finished

	// Then
	deadline := clock.Now().Add(time.Second)
	if got, ok := firstAttempt.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatalf("first attempt deadline = %v, %t, want %v", got, ok, deadline)
	}
	if got, ok := secondAttempt.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatalf("second attempt deadline = %v, %t, want %v", got, ok, deadline)
	}
	if timer.delay != 150*time.Millisecond {
		t.Fatalf("jitter delay = %s, want 150ms", timer.delay)
	}
	select {
	case <-timer.timer.stopped:
		t.Fatal("fired timer must not be stopped again")
	default:
	}
}

func TestReconnectLoop_finishesWhenAbsoluteResumeDeadlineExpires(t *testing.T) {
	// Given
	clock := &reconnectTestClock{now: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timers := make(chan *reconnectTestTimer, 1)
	finished := make(chan error, 1)
	deps := reconnectDependencies{
		now:    clock.Now,
		random: func() float64 { return 1 },
		newTimer: func(time.Duration) reconnectTimer {
			timer := newReconnectTestTimer()
			timers <- timer
			return timer
		},
		state:   func() reconnectState { return reconnectState{session: true} },
		attempt: func(context.Context) error { return errors.New("dial failed") },
		finish:  func(err error) { finished <- err },
		done:    make(chan struct{}),
		wake:    make(chan struct{}),
	}
	loopDone := make(chan struct{})
	go func() {
		runReconnectLoop(ctx, 100*time.Millisecond, deps)
		close(loopDone)
	}()

	// When
	timer := <-timers
	clock.Set(clock.Now().Add(100 * time.Millisecond))
	timer.c <- clock.Now()
	err := <-finished
	<-loopDone

	// Then
	if err == nil || err.Error() != "resume timeout" {
		t.Fatalf("finish error = %v, want resume timeout", err)
	}
}

func TestReconnectLoop_resetsAfterSuccessfulReconnectAndNewDisconnect(t *testing.T) {
	// Given
	clock := &reconnectTestClock{now: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	state := reconnectState{session: true}
	wake := make(chan struct{}, 1)
	attempts := make(chan context.Context, 2)
	unexpected := make(chan string, 1)
	call := 0
	deps := reconnectDependencies{
		now:    clock.Now,
		random: func() float64 { return 0.5 },
		newTimer: func(time.Duration) reconnectTimer {
			unexpected <- "successful attempts must not schedule a retry"
			return newReconnectTestTimer()
		},
		state: func() reconnectState {
			mu.Lock()
			defer mu.Unlock()
			return state
		},
		attempt: func(attemptCtx context.Context) error {
			mu.Lock()
			call++
			current := call
			if current == 1 {
				state.active = true
			}
			mu.Unlock()
			attempts <- attemptCtx
			if current == 1 {
				return nil
			}
			<-attemptCtx.Done()
			return attemptCtx.Err()
		},
		finish: func(error) { unexpected <- "resume must not time out" },
		done:   make(chan struct{}),
		wake:   wake,
	}
	loopDone := make(chan struct{})
	go func() {
		runReconnectLoop(ctx, time.Second, deps)
		close(loopDone)
	}()

	// When
	<-attempts
	reconnectStarted := clock.Now().Add(time.Second)
	clock.Set(reconnectStarted)
	mu.Lock()
	state.active = false
	mu.Unlock()
	wake <- struct{}{}
	secondAttempt := <-attempts
	if got, ok := secondAttempt.Deadline(); !ok || !got.Equal(reconnectStarted.Add(time.Second)) {
		t.Fatalf("second attempt deadline = %v, %t, want %v", got, ok, reconnectStarted.Add(time.Second))
	}
	cancel()
	<-loopDone

	// Then
	mu.Lock()
	gotCalls := call
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("attempt count = %d, want 2", gotCalls)
	}
	select {
	case message := <-unexpected:
		t.Fatal(message)
	default:
	}
}

func TestReconnectLoop_stopsPendingRetryTimerWhenContextCancels(t *testing.T) {
	// Given
	clock := &reconnectTestClock{now: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	timers := make(chan *reconnectTestTimer, 1)
	deps := reconnectDependencies{
		now:    clock.Now,
		random: func() float64 { return 0.5 },
		newTimer: func(time.Duration) reconnectTimer {
			timer := newReconnectTestTimer()
			timers <- timer
			return timer
		},
		state:   func() reconnectState { return reconnectState{session: true} },
		attempt: func(context.Context) error { return errors.New("dial failed") },
		finish:  func(error) { t.Fatal("resume must not time out") },
		done:    make(chan struct{}),
		wake:    make(chan struct{}),
	}
	loopDone := make(chan struct{})
	go func() {
		runReconnectLoop(ctx, time.Second, deps)
		close(loopDone)
	}()

	// When
	timer := <-timers
	cancel()
	<-loopDone

	// Then
	select {
	case <-timer.stopped:
	default:
		t.Fatal("pending retry timer was not stopped on context cancellation")
	}
}
