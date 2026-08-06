package tunnel

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestActiveHeartbeat_UsesReceiveLoopPong(t *testing.T) {
	var connections atomic.Int32
	serverPong := make(chan frameHeader, 1)
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		connections.Add(1)
		ping, err := readSessionFrameType(ws, "ping")
		if err != nil {
			t.Error(err)
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "ping", Nonce: "server-ping"}, nil); err != nil {
			t.Error(err)
			return
		}
		pong, err := readSessionFrameType(ws, "pong")
		if err != nil {
			t.Error(err)
			return
		}
		serverPong <- pong
		if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
			t.Error(err)
		}
	})
	ws := dialSessionTestWebSocket(t, srv)
	conn := &clientConn{ws: ws, path: PathConfig{Name: "direct"}}
	rt := newClientRuntime(&Config{}, nil, io.Discard)
	rt.active = conn
	go rt.receiveLoop(conn)

	result := rt.activeHeartbeat(context.Background(), conn, time.Second)

	if result.Err != nil || result.TimedOut || result.Closed || result.RTT <= 0 {
		t.Fatalf("heartbeat result = %+v, want positive RTT", result)
	}
	if connections.Load() != 1 {
		t.Fatalf("websocket connections = %d, want 1", connections.Load())
	}
	select {
	case pong := <-serverPong:
		if pong.Nonce != "server-ping" {
			t.Fatalf("server pong nonce = %q, want server-ping", pong.Nonce)
		}
	case <-time.After(time.Second):
		t.Fatal("receive loop did not answer server ping")
	}
}

func TestActiveHeartbeat_IgnoresPongFromReplacedConnection(t *testing.T) {
	serverConns := make(chan *websocket.Conn, 2)
	release := make(chan struct{})
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		serverConns <- ws
		<-release
	})
	t.Cleanup(func() { close(release) })

	oldConn := &clientConn{ws: dialSessionTestWebSocket(t, srv), path: PathConfig{Name: "old"}}
	oldServer := <-serverConns
	newConn := &clientConn{ws: dialSessionTestWebSocket(t, srv), path: PathConfig{Name: "new"}}
	newServer := <-serverConns
	t.Cleanup(func() {
		_ = oldConn.close()
		_ = newConn.close()
	})
	rt := newClientRuntime(&Config{}, nil, io.Discard)
	rt.active = newConn
	go rt.receiveLoop(oldConn)
	go rt.receiveLoop(newConn)
	result := make(chan activeHeartbeatResult, 1)
	go func() {
		result <- rt.activeHeartbeat(context.Background(), newConn, time.Second)
	}()
	ping, err := readSessionFrameType(newServer, "ping")
	if err != nil {
		t.Fatal(err)
	}

	if err := writeFrame(oldServer, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		t.Fatalf("old connection pong completed new heartbeat: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
	if err := writeFrame(newServer, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.Err != nil || got.TimedOut || got.Closed || got.RTT <= 0 {
			t.Fatalf("new connection heartbeat result = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("new connection pong did not complete heartbeat")
	}
}

func TestActiveHeartbeat_SendFailureDropsOnlyMatchingConnection(t *testing.T) {
	serverConns := make(chan *websocket.Conn, 2)
	release := make(chan struct{})
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		serverConns <- ws
		<-release
	})
	t.Cleanup(func() { close(release) })

	failed := &clientConn{ws: dialSessionTestWebSocket(t, srv)}
	<-serverConns
	replacement := &clientConn{ws: dialSessionTestWebSocket(t, srv)}
	<-serverConns
	t.Cleanup(func() { _ = replacement.close() })
	rt := newClientRuntime(&Config{}, nil, io.Discard)
	rt.active = failed
	_ = failed.ws.Close()

	result := rt.activeHeartbeat(context.Background(), failed, time.Second)
	if result.Err == nil || !result.Closed {
		t.Fatalf("send failure result = %+v, want closed error", result)
	}
	if rt.isActive(failed) {
		t.Fatal("failed connection remained active")
	}

	rt.mu.Lock()
	rt.active = replacement
	rt.mu.Unlock()
	rt.dropActive(failed)
	if !rt.isActive(replacement) {
		t.Fatal("dropping stale connection removed replacement")
	}
}

func TestActiveHeartbeat_TimeoutLeavesThresholdDecisionToSwitchLoop(t *testing.T) {
	pingReceived := make(chan struct{})
	release := make(chan struct{})
	srv := newSessionTestServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "ping"); err != nil {
			t.Error(err)
			return
		}
		close(pingReceived)
		<-release
	})
	t.Cleanup(func() { close(release) })
	conn := &clientConn{ws: dialSessionTestWebSocket(t, srv)}
	t.Cleanup(func() { _ = conn.close() })
	rt := newClientRuntime(&Config{}, nil, io.Discard)
	rt.active = conn
	go rt.receiveLoop(conn)

	result := rt.activeHeartbeat(context.Background(), conn, 20*time.Millisecond)
	if !result.TimedOut || result.Err != nil || result.Closed {
		t.Fatalf("timeout result = %+v, want timeout", result)
	}
	if !rt.isActive(conn) {
		t.Fatal("one timeout dropped active before threshold")
	}
	select {
	case <-pingReceived:
	case <-time.After(time.Second):
		t.Fatal("server did not receive heartbeat ping")
	}
}
