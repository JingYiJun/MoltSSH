package tunnel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestProbeBatch_ProbesPathsConcurrently(t *testing.T) {
	// Given
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := probeServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			return
		}
		arrived <- struct{}{}
		<-release
		_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil)
	})
	paths := probePaths(server.URL, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan []probeCandidate, 1)
	go func() {
		candidates, _ := probeBatch(ctx, paths, 500*time.Millisecond)
		done <- candidates
	}()

	// When
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			close(release)
			candidates := <-done
			t.Fatalf("two probes did not reach the barrier concurrently: %+v", candidates)
		}
	}
	close(release)
	candidates := <-done

	// Then
	defer closeProbeCandidates(candidates)
	if len(candidates) != 2 || candidates[0].Err != nil || candidates[1].Err != nil {
		t.Fatalf("candidates = %+v, want two successes", candidates)
	}
	if candidates[0].open == nil || candidates[1].open == nil {
		t.Fatal("successful probes must retain their connections")
	}
}

func TestProbeBatch_CapsConcurrencyAtEight(t *testing.T) {
	// Given
	var mu sync.Mutex
	active, maximum := 0, 0
	eightArrived := make(chan struct{})
	release := make(chan struct{})
	server := probeServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			return
		}
		mu.Lock()
		active++
		maximum = max(maximum, active)
		if active == 8 {
			close(eightArrived)
		}
		mu.Unlock()
		<-release
		_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil)
		mu.Lock()
		active--
		mu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan []probeCandidate, 1)
	go func() {
		candidates, _ := probeBatch(ctx, probePaths(server.URL, 10), 500*time.Millisecond)
		done <- candidates
	}()

	// When
	select {
	case <-eightArrived:
	case <-ctx.Done():
		t.Fatal("eight workers did not start")
	}
	close(release)
	candidates := <-done

	// Then
	defer closeProbeCandidates(candidates)
	mu.Lock()
	gotMaximum := maximum
	mu.Unlock()
	if gotMaximum != 8 {
		t.Fatalf("maximum concurrency = %d, want 8", gotMaximum)
	}
}

func TestProbeBatch_CancellationClosesAllConnections(t *testing.T) {
	// Given
	arrived := make(chan struct{}, 3)
	closed := make(chan struct{}, 3)
	server := probeServer(t, func(ws *websocket.Conn) {
		if _, _, err := readFrame(ws); err != nil {
			return
		}
		arrived <- struct{}{}
		if _, _, err := readFrame(ws); err != nil {
			closed <- struct{}{}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		candidates, err := probeBatch(ctx, probePaths(server.URL, 3), time.Second)
		closeProbeCandidates(candidates)
		done <- err
	}()
	for range 3 {
		<-arrived
	}

	// When
	cancel()
	err := <-done

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	for range 3 {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("server did not observe every probe connection close")
		}
	}
}

func TestProbeCandidate_MatchesPongClearsDeadlineAndTransfersOwnership(t *testing.T) {
	// Given
	server := probeServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			return
		}
		_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: "stale"}, nil)
		_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil)
	})
	var tracked *probeDeadlineTrackingConn
	request := probeRequest{
		Path:    probePaths(server.URL, 1)[0],
		Timeout: time.Second,
		Open: func(ctx context.Context, endpoint string) (*websocket.Conn, DialTimings, error) {
			deps := defaultDialDependencies()
			deps.dialTCP = func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				tracked = &probeDeadlineTrackingConn{Conn: conn}
				return tracked, nil
			}
			return openWebSocketWith(ctx, endpoint, deps)
		},
	}

	// When
	candidate := probePathCandidate(context.Background(), request)
	owned := candidate.transfer()
	closeErr := candidate.close()

	// Then
	if candidate.Err != nil || owned == nil || candidate.open != nil {
		t.Fatalf("candidate = %+v, want transferred successful connection", candidate)
	}
	if closeErr != nil || tracked == nil || !tracked.deadline.IsZero() {
		t.Fatalf("close/deadline = %v/%v, want nil and cleared", closeErr, tracked)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeCandidate_TimeoutClosesConnectionAndReportsPhase(t *testing.T) {
	// Given
	closed := make(chan struct{}, 1)
	server := probeServer(t, func(ws *websocket.Conn) {
		if _, _, err := readFrame(ws); err != nil {
			return
		}
		if _, _, err := readFrame(ws); err != nil {
			closed <- struct{}{}
		}
	})

	// When
	candidate := probePathCandidate(context.Background(), probeRequest{
		Path: probePaths(server.URL, 1)[0], Timeout: 20 * time.Millisecond, Open: openWebSocket,
	})

	// Then
	if candidate.open != nil || !errors.Is(candidate.Err, context.DeadlineExceeded) || candidate.FailedPhase != DialPhaseProbe {
		t.Fatalf("candidate = %+v, want closed probe timeout", candidate)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server did not observe timed-out connection close")
	}
}

func TestProbeRanking_OrdersSuccessThenRTTThenPriorityThenDeclaration(t *testing.T) {
	// Given
	failure := errors.New("failed")
	candidates := []probeCandidate{
		{Path: PathConfig{Name: "failed-high", Priority: 100}, Err: failure, order: 0},
		{Path: PathConfig{Name: "slow", Priority: 100}, RTT: 20 * time.Millisecond, order: 1},
		{Path: PathConfig{Name: "first", Priority: 5}, RTT: 10 * time.Millisecond, order: 2},
		{Path: PathConfig{Name: "priority", Priority: 10}, RTT: 10 * time.Millisecond, order: 3},
		{Path: PathConfig{Name: "stable", Priority: 10}, RTT: 10 * time.Millisecond, order: 4},
	}

	// When
	rankProbeCandidates(candidates)

	// Then
	want := []string{"priority", "stable", "first", "slow", "failed-high"}
	for i, name := range want {
		if candidates[i].Path.Name != name {
			t.Fatalf("rank %d = %q, want %q", i, candidates[i].Path.Name, name)
		}
	}
}

func probeServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(websocket.Server{
		Handshake: func(config *websocket.Config, _ *http.Request) error {
			config.Protocol = []string{wsProtocol}
			return nil
		},
		Handler: func(ws *websocket.Conn) { handler(ws) },
	})
	t.Cleanup(server.Close)
	return server
}

func probePaths(serverURL string, count int) []PathConfig {
	paths := make([]PathConfig, count)
	for i := range count {
		paths[i] = PathConfig{Name: string(rune('a' + i)), Endpoint: "ws" + serverURL[4:], Enabled: true}
	}
	return paths
}

type probeDeadlineTrackingConn struct {
	net.Conn
	deadline time.Time
}

func (c *probeDeadlineTrackingConn) SetDeadline(deadline time.Time) error {
	c.deadline = deadline
	return c.Conn.SetDeadline(deadline)
}
