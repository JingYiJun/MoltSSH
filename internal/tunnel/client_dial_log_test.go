package tunnel

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

func TestDialLog_PromotedCandidateIncludesProbeAndHello(t *testing.T) {
	// Given
	server := acceptingProbeSessionServer(t, "promoted")
	cfg := lkgConnectConfig(t, []PathConfig{connectPath("promoted", server.URL, 1)})
	var logs bytes.Buffer
	rt := newClientRuntimeWithLogger(cfg, clientStreams{stdin: strings.NewReader(""), stdout: io.Discard}, log.New(&logs, "", 0))

	// When
	err := rt.connectAny(context.Background(), false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer rt.finish(nil)
	line := singleDialLog(t, logs.String())
	for _, field := range []string{
		"event=proxy_dial", "path=promoted", "status=ok", "failed_phase=", "dns=", "tcp=", "tls=", "websocket_upgrade=", "moltssh_hello=", "probe_rtt=", "total=", "error=",
	} {
		if !strings.Contains(line, field) {
			t.Fatalf("dial log = %q, missing %q", line, field)
		}
	}
	if strings.Contains(line, server.URL) || strings.Contains(line, "probe_rtt=0s") {
		t.Fatalf("dial log = %q, want promoted timing without endpoint", line)
	}
}

func TestDialLog_TLSFailure(t *testing.T) {
	// Given
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(plain.Close)
	secret := "tls-query-secret"
	path := PathConfig{
		Name: "secure", Transport: "ws", Endpoint: "wss" + strings.TrimPrefix(plain.URL, "http") + "/moltssh?token=" + secret, Enabled: true,
	}
	cfg := lkgConnectConfig(t, []PathConfig{path})
	var logs bytes.Buffer
	rt := newClientRuntimeWithLogger(cfg, clientStreams{stdin: strings.NewReader(""), stdout: io.Discard}, log.New(&logs, "", 0))

	// When
	err := rt.connectAny(context.Background(), false)

	// Then
	if err == nil {
		t.Fatal("connect error = nil, want TLS failure")
	}
	line := singleDialLog(t, logs.String())
	if !strings.Contains(line, "status=fail") || !strings.Contains(line, "failed_phase=tls") {
		t.Fatalf("dial log = %q, want TLS failure phase", line)
	}
	if strings.Contains(line, path.Endpoint) || strings.Contains(line, secret) {
		t.Fatalf("dial log leaked endpoint secret: %q", line)
	}
}

func TestDialLog_HelloFailure(t *testing.T) {
	// Given
	secret := "hello-query-secret"
	var endpoint string
	server := probeServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil)
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		_ = writeFrame(ws, frameHeader{Type: "error", Code: "rejected", Message: endpoint + " " + secret}, nil)
	})
	endpoint = "ws" + strings.TrimPrefix(server.URL, "http") + "/moltssh?token=" + secret
	cfg := lkgConnectConfig(t, []PathConfig{{Name: "hello", Transport: "ws", Endpoint: endpoint, Enabled: true}})
	var logs bytes.Buffer
	rt := newClientRuntimeWithLogger(cfg, clientStreams{stdin: strings.NewReader(""), stdout: io.Discard}, log.New(&logs, "", 0))

	// When
	err := rt.connectAny(context.Background(), false)

	// Then
	if err == nil {
		t.Fatal("connect error = nil, want hello rejection")
	}
	line := singleDialLog(t, logs.String())
	if !strings.Contains(line, "status=fail") || !strings.Contains(line, "failed_phase=moltssh_hello") {
		t.Fatalf("dial log = %q, want hello failure phase", line)
	}
	if strings.Contains(line, endpoint) || strings.Contains(line, secret) {
		t.Fatalf("dial log leaked endpoint secret: %q", line)
	}
}

func acceptingProbeSessionServer(t *testing.T, sessionID string) *httptest.Server {
	t.Helper()
	return probeServer(t, func(ws *websocket.Conn) {
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
		_ = writeFrame(ws, frameHeader{Type: "accept", SessionID: sessionID, Epoch: 1}, nil)
	})
}

func rejectingSessionServer(t *testing.T, message string) *httptest.Server {
	t.Helper()
	return probeServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		_ = writeFrame(ws, frameHeader{Type: "error", Code: "rejected", Message: message}, nil)
	})
}

func singleDialLog(t *testing.T, logs string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	dialLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "event=proxy_dial") {
			dialLines = append(dialLines, line)
		}
	}
	if len(dialLines) != 1 {
		t.Fatalf("dial log count = %d, want 1; logs=%q", len(dialLines), logs)
	}
	return dialLines[0]
}
