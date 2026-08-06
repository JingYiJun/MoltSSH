package tunnel

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestStartSession_AcceptsOnExistingWebSocket(t *testing.T) {
	// Given
	pingReceived := make(chan struct{})
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "accept", SessionID: "session-1", Epoch: 7}, nil); err != nil {
			t.Error(err)
			return
		}
		if _, err := readSessionFrameType(ws, "ping"); err != nil {
			t.Error(err)
			return
		}
		close(pingReceived)
	})
	ws := dialSessionTestWebSocket(t, srv)
	defer ws.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// When
	conn, accept, timings, err := startSession(ctx, ws, sessionRequest{path: PathConfig{Name: "direct"}, name: "loop"})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if conn.ws != ws {
		t.Fatal("start session replaced the caller-owned websocket")
	}
	if accept.SessionID != "session-1" || accept.Epoch != 7 {
		t.Fatalf("accept = %+v, want session-1 epoch 7", accept)
	}
	if timings.MoltSSHHello <= 0 {
		t.Fatalf("hello timing = %s, want positive duration", timings.MoltSSHHello)
	}
	<-ctx.Done()
	if err := writeFrame(ws, frameHeader{Type: "ping"}, nil); err != nil {
		t.Fatalf("write after successful handshake deadline = %v, want nil", err)
	}
	select {
	case <-pingReceived:
	case <-time.After(time.Second):
		t.Fatal("server did not receive post-handshake ping")
	}
}

func TestStartSession_RespondsToServerPingBeforeAccept(t *testing.T) {
	// Given
	pong := make(chan frameHeader, 1)
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "ping", Nonce: "nonce", SentAtUnixNano: 42}, nil); err != nil {
			t.Error(err)
			return
		}
		got, err := readSessionFrameType(ws, "pong")
		if err != nil {
			t.Error(err)
			return
		}
		pong <- got
		if err := writeFrame(ws, frameHeader{Type: "accept", SessionID: "session-1", Epoch: 1}, nil); err != nil {
			t.Error(err)
		}
	})
	ws := dialSessionTestWebSocket(t, srv)
	defer ws.Close()

	// When
	_, _, _, err := startSession(context.Background(), ws, sessionRequest{name: "loop"})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-pong:
		if got.Nonce != "nonce" || got.SentAtUnixNano != 42 {
			t.Fatalf("pong = %+v, want echoed ping fields", got)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive pong")
	}
}

func TestStartSession_ReturnsServerErrorAndClosesWebSocket(t *testing.T) {
	// Given
	closed := make(chan error, 1)
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "error", Code: "rejected", Message: "no session"}, nil); err != nil {
			t.Error(err)
			return
		}
		_, _, err := readFrame(ws)
		closed <- err
	})
	ws := dialSessionTestWebSocket(t, srv)

	// When
	_, _, _, err := startSession(context.Background(), ws, sessionRequest{name: "loop"})

	// Then
	requireHelloPhase(t, err)
	if !strings.Contains(err.Error(), "rejected: no session") {
		t.Fatalf("error = %q, want server error", err)
	}
	requireSessionSocketClosed(t, closed)
}

func TestStartSession_ClosesOnUnexpectedFrame(t *testing.T) {
	// Given
	closed := make(chan error, 1)
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "ack"}, nil); err != nil {
			t.Error(err)
			return
		}
		_, _, err := readFrame(ws)
		closed <- err
	})
	ws := dialSessionTestWebSocket(t, srv)

	// When
	_, _, _, err := startSession(context.Background(), ws, sessionRequest{name: "loop"})

	// Then
	requireHelloPhase(t, err)
	if !strings.Contains(err.Error(), `unexpected frame "ack" before accept`) {
		t.Fatalf("error = %q, want unexpected-frame error", err)
	}
	requireSessionSocketClosed(t, closed)
}

func TestStartSession_ReturnsCancellationWhileWaitingForAccept(t *testing.T) {
	// Given
	helloReceived := make(chan struct{})
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		close(helloReceived)
		_, _, _ = readFrame(ws)
	})
	ws := dialSessionTestWebSocket(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)

	// When
	go func() {
		_, _, _, err := startSession(ctx, ws, sessionRequest{name: "loop"})
		done <- err
	}()
	select {
	case <-helloReceived:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("server did not receive hello")
	}

	// Then
	select {
	case err := <-done:
		requireHelloPhase(t, err)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("start session did not return after cancellation")
	}
}

func newSessionTestServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(websocket.Server{Handler: handler})
	t.Cleanup(srv.Close)
	return srv
}

func dialSessionTestWebSocket(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	ws, _, err := openWebSocket(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func readSessionFrameType(ws *websocket.Conn, want string) (frameHeader, error) {
	if err := ws.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return frameHeader{}, err
	}
	for {
		frame, _, err := readFrame(ws)
		if err != nil {
			return frameHeader{}, err
		}
		if frame.Type == want {
			return frame, nil
		}
	}
}

func requireHelloPhase(t *testing.T, err error) {
	t.Helper()
	var dialErr *DialError
	if !errors.As(err, &dialErr) || dialErr.Phase != DialPhaseMoltSSHHello {
		t.Fatalf("error = %v, want DialError phase %q", err, DialPhaseMoltSSHHello)
	}
}

func requireSessionSocketClosed(t *testing.T, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("server websocket remained readable after client handshake failure")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe client websocket close")
	}
}
