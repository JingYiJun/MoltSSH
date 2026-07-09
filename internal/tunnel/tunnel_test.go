package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestParseConfigProxy(t *testing.T) {
	cfg, err := ParseConfig(sampleConfig("127.0.0.1:1", "127.0.0.1:2"), CommandProxy)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "loop" || cfg.Probe.Timeout != time.Second || len(enabledPaths(cfg.Paths)) != 1 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseConfigRejectsUnknownKey(t *testing.T) {
	_, err := ParseConfig(sampleConfig("127.0.0.1:1", "127.0.0.1:2")+"\nwat = true\n", CommandProxy)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProxyLocalLoop(t *testing.T) {
	targetLn, targetAddr := startEchoTarget(t)
	defer targetLn.Close()

	serverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverCfg := mustConfig(t, sampleConfig(serverLn.Addr().String(), targetAddr), CommandServer)
	go func() {
		_ = serveListener(ctx, serverLn, serverCfg)
	}()

	clientCfg := mustConfig(t, sampleConfig(serverLn.Addr().String(), targetAddr), CommandProxy)
	var out bytes.Buffer
	runCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := Proxy(runCtx, clientCfg, strings.NewReader("hello over ws"), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hello over ws" {
		t.Fatalf("unexpected proxy output %q", got)
	}
}

func TestServerResumeReplaysBufferedBytes(t *testing.T) {
	targetLn, targetAddr := startEchoTarget(t)
	defer targetLn.Close()

	serverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverCfg := mustConfig(t, sampleConfig(serverLn.Addr().String(), targetAddr), CommandServer)
	go func() {
		_ = serveListener(ctx, serverLn, serverCfg)
	}()

	path := PathConfig{Name: "direct", Transport: "ws", Endpoint: "ws://" + serverLn.Addr().String() + "/moltssh", Priority: 100, Enabled: true}
	ws1, err := dialWS(ctx, path.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(ws1, frameHeader{Type: "hello", Version: 1, Name: "loop"}, nil); err != nil {
		t.Fatal(err)
	}
	accept1 := readFrameType(t, ws1, "accept")
	if err := writeFrame(ws1, frameHeader{
		Type:      "data",
		SessionID: accept1.SessionID,
		Epoch:     accept1.Epoch,
		Direction: dirClientToServer,
		Offset:    0,
	}, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	_ = readFrameType(t, ws1, "ack")
	_ = ws1.Close()

	ws2, err := dialWS(ctx, path.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	if err := writeFrame(ws2, frameHeader{
		Type:             "hello",
		Version:          1,
		Name:             "loop",
		Resume:           true,
		SessionID:        accept1.SessionID,
		ClientToServerRx: 3,
		ServerToClientRx: 0,
	}, nil); err != nil {
		t.Fatal(err)
	}
	accept2 := readFrameType(t, ws2, "accept")
	if accept2.Epoch <= accept1.Epoch {
		t.Fatalf("resume did not advance epoch: first=%d second=%d", accept1.Epoch, accept2.Epoch)
	}
	f, payload := readDataFrame(t, ws2)
	if f.Offset != 0 || string(payload) != "abc" {
		t.Fatalf("unexpected replay offset=%d payload=%q", f.Offset, payload)
	}
}

func mustConfig(t *testing.T, data, command string) *Config {
	t.Helper()
	cfg, err := ParseConfig(data, command)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func startEchoTarget(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln, ln.Addr().String()
}

func readFrameType(t *testing.T, ws *websocket.Conn, typ string) frameHeader {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		f, _, err := readFrame(ws)
		if err != nil {
			t.Fatal(err)
		}
		if f.Type == typ {
			return f
		}
	}
}

func readDataFrame(t *testing.T, ws *websocket.Conn) (frameHeader, []byte) {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		f, payload, err := readFrame(ws)
		if err != nil {
			t.Fatal(err)
		}
		if f.Type == "data" {
			return f, payload
		}
	}
}

func sampleConfig(listen, connect string) string {
	return fmt.Sprintf(`schema_version = 1
name = "loop"

[server]
listen = %q
http_path = "/moltssh"
connect = %q

[resume]
timeout = "5s"
buffer_bytes = 1048576

[probe]
interval = "100ms"
timeout = "1s"
switch_cooldown = "200ms"
active_failure_threshold = 2
candidate_success_threshold = 1
better_rtt_min_delta = "1ms"
better_rtt_ratio = 0.25

[[paths]]
name = "direct"
transport = "ws"
endpoint = "ws://%s/moltssh"
priority = 100
enabled = true
`, listen, connect, listen)
}
