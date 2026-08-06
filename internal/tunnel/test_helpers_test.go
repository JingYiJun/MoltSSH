package tunnel

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

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
