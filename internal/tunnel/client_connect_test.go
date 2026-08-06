package tunnel

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestActivateCandidate_PromotesProbeConnectionWithoutRedial(t *testing.T) {
	// Given
	var upgrades atomic.Int32
	frames := make(chan string, 3)
	release := make(chan struct{})
	server := probeServer(t, func(ws *websocket.Conn) {
		upgrades.Add(1)
		ping, _, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		frames <- ping.Type
		if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
			t.Error(err)
			return
		}
		hello, _, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		frames <- hello.Type
		if err := writeFrame(ws, frameHeader{Type: "accept", SessionID: "session-1", Epoch: 7}, nil); err != nil {
			t.Error(err)
			return
		}
		data, payload, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		if data.Type != "data" || string(payload) != "buffered" {
			t.Errorf("data frame = %+v %q, want buffered data", data, payload)
			return
		}
		frames <- data.Type
		<-release
	})
	path := probePaths(server.URL, 1)[0]
	candidate := probePathCandidate(context.Background(), probeRequest{
		Path: path, Timeout: time.Second, Open: openWebSocket,
	})
	if candidate.Err != nil {
		t.Fatal(candidate.Err)
	}
	probeTimings := candidate.DialTimings
	rt := newClientRuntime(&Config{Name: "loop"}, strings.NewReader(""), io.Discard)
	rt.appendClientBytes([]byte("buffered"))

	// When
	timings, err := rt.activateCandidate(context.Background(), &candidate, false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		rt.finish(nil)
	}()
	for _, want := range []string{"ping", "hello", "data"} {
		select {
		case got := <-frames:
			if got != want {
				t.Fatalf("frame = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("server did not receive %q", want)
		}
	}
	if got := upgrades.Load(); got != 1 {
		t.Fatalf("websocket upgrades = %d, want 1", got)
	}
	if candidate.open != nil {
		t.Fatal("candidate retained ownership after promotion")
	}
	rt.mu.Lock()
	active := rt.active
	sessionID := rt.sessionID
	rt.mu.Unlock()
	if active == nil || active.ws == nil || active.path.Name != path.Name || sessionID != "session-1" {
		t.Fatalf("active/session = %+v/%q, want promoted path and session-1", active, sessionID)
	}
	if timings.DNS != probeTimings.DNS || timings.TCP != probeTimings.TCP ||
		timings.TLS != probeTimings.TLS || timings.WebSocketUpgrade != probeTimings.WebSocketUpgrade ||
		timings.ProbeRTT != probeTimings.ProbeRTT {
		t.Fatalf("timings = %+v, want preserved probe timings %+v", timings, probeTimings)
	}
	if timings.MoltSSHHello <= 0 || timings.Total < probeTimings.Total+timings.MoltSSHHello {
		t.Fatalf("timings = %+v, want merged hello and total", timings)
	}
}

func TestActivateCandidate_RejectsHelloClosesConnectionWithoutActiveCommit(t *testing.T) {
	// Given
	var upgrades atomic.Int32
	closed := make(chan error, 1)
	server := probeServer(t, func(ws *websocket.Conn) {
		upgrades.Add(1)
		ping, _, err := readFrame(ws)
		if err != nil {
			closed <- err
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
			closed <- err
			return
		}
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			closed <- err
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "error", Code: "rejected", Message: "no session"}, nil); err != nil {
			closed <- err
			return
		}
		_, _, err = readFrame(ws)
		closed <- err
	})
	path := probePaths(server.URL, 1)[0]
	candidate := probePathCandidate(context.Background(), probeRequest{
		Path: path, Timeout: time.Second, Open: openWebSocket,
	})
	if candidate.Err != nil {
		t.Fatal(candidate.Err)
	}
	rt := newClientRuntime(&Config{Name: "loop"}, strings.NewReader(""), io.Discard)

	// When
	timings, err := rt.activateCandidate(context.Background(), &candidate, false)

	// Then
	requireHelloPhase(t, err)
	if !strings.Contains(err.Error(), "rejected: no session") {
		t.Fatalf("error = %q, want server rejection", err)
	}
	if timings.MoltSSHHello <= 0 || candidate.open != nil {
		t.Fatalf("timings/candidate = %+v/%+v, want hello timing and transferred ownership", timings, candidate)
	}
	if got := upgrades.Load(); got != 1 {
		t.Fatalf("websocket upgrades = %d, want 1", got)
	}
	rt.mu.Lock()
	active := rt.active
	rt.mu.Unlock()
	if active != nil {
		t.Fatalf("active = %+v, want nil after rejected hello", active)
	}
	select {
	case closeErr := <-closed:
		if closeErr == nil || errors.Is(closeErr, context.DeadlineExceeded) {
			t.Fatalf("server close error = %v, want peer closure", closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe rejected candidate close")
	}
}

func TestCommitActivation_RejectsConnectionAfterRuntimeFinished(t *testing.T) {
	closed := make(chan error, 1)
	server := newSessionTestServer(t, func(ws *websocket.Conn) {
		_, _, err := readFrame(ws)
		closed <- err
	})
	ws := dialSessionTestWebSocket(t, server)
	rt := newClientRuntime(&Config{Name: "finished"}, strings.NewReader(""), io.Discard)
	rt.finish(nil)

	err := rt.commitActivation(activationCommit{
		conn: &clientConn{ws: ws, path: PathConfig{Name: "late"}},
		accept: frameHeader{
			Type: "accept", SessionID: "late-session", Epoch: 1,
		},
		path: PathConfig{Name: "late"},
	})

	if !errors.Is(err, errClientRuntimeClosed) {
		t.Fatalf("commit error = %v, want runtime closed", err)
	}
	rt.mu.Lock()
	active := rt.active
	rt.mu.Unlock()
	if active != nil {
		t.Fatalf("active = %+v, want nil after terminal finish", active)
	}
	select {
	case closeErr := <-closed:
		if closeErr == nil {
			t.Fatal("late websocket remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe late websocket close")
	}
}
