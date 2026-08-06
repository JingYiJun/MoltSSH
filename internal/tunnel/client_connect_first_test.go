package tunnel

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestConnectAny_WithoutLKGActivatesFastPathBeforeSlowProbeCompletes(t *testing.T) {
	// Given
	slowPing := make(chan struct{})
	releaseSlow := make(chan struct{})
	fastHello := make(chan struct{})
	slowServer := probeServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "ping"); err != nil {
			t.Error(err)
			return
		}
		close(slowPing)
		<-releaseSlow
	})
	fastServer := probeServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
			t.Error(err)
			return
		}
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		close(fastHello)
		_ = writeFrame(ws, frameHeader{Type: "accept", SessionID: "fast", Epoch: 1}, nil)
	})
	cfg := lkgConnectConfig(t, []PathConfig{
		connectPath("slow-high-priority", slowServer.URL, 100),
		connectPath("fast", fastServer.URL, 1),
	})
	cfg.Probe.Timeout = 5 * time.Second
	rt := newClientRuntime(cfg, strings.NewReader(""), io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- rt.connectAny(ctx, false) }()
	t.Cleanup(func() {
		cancel()
		close(releaseSlow)
		rt.finish(nil)
	})
	requireSignal(t, slowPing, "slow path probe")

	// When
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connect waited for the unfinished slow probe")
	}

	// Then
	requireSignal(t, fastHello, "fast path hello")
	if got := rt.lastKnownGoodPath(); got == nil || got.Name != "fast" {
		t.Fatalf("last known good = %+v, want fast", got)
	}
}
