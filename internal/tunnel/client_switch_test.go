package tunnel

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestPathSwitch_HeartbeatFailurePromotesProbedCandidateWithoutRedial(t *testing.T) {
	var upgrades atomic.Int32
	stream := make(chan string, 1)
	acknowledged := make(chan struct{}, 1)
	server := probeServer(t, func(ws *websocket.Conn) {
		switch upgrades.Add(1) {
		case 1:
			handleUnhealthyActiveSession(t, ws)
		case 2:
			handleHealthyResumeCandidate(t, ws, acknowledged)
		default:
			t.Errorf("unexpected websocket upgrade %d", upgrades.Load())
		}
	})

	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/moltssh"
	cfg := &Config{
		Name: "switch-loop",
		Paths: []PathConfig{
			{Name: "active", Transport: "ws", Endpoint: endpoint, Priority: 100, Enabled: true},
			{Name: "candidate", Transport: "ws", Endpoint: endpoint, Priority: 50, Enabled: true},
		},
		sourceIdentity: filepath.Join(cacheRoot, "client.toml"),
		Resume:         ResumeConfig{Timeout: time.Second, BufferBytes: 1024},
		Probe: ProbeConfig{
			Timeout:                   20 * time.Millisecond,
			ActiveFailureThreshold:    1,
			CandidateSuccessThreshold: 1,
			SwitchCooldown:            time.Hour,
			BetterRTTMinDelta:         time.Millisecond,
			BetterRTTRatio:            0.25,
		},
	}
	rt := newClientRuntimeWithLogger(cfg, clientStreams{
		stdin: strings.NewReader(""), stdout: channelWriter{writes: stream},
	}, log.New(io.Discard, "", 0))
	if err := rt.activate(context.Background(), cfg.Paths[0], false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.finish(nil) })

	stats := map[string]*probeStat{}
	if err := rt.runSwitchCycle(context.Background(), stats); err != nil {
		t.Fatal(err)
	}

	if got := upgrades.Load(); got != 2 {
		t.Fatalf("websocket upgrades = %d, want initial active plus one promoted candidate", got)
	}
	rt.mu.Lock()
	active := rt.active
	sessionID := rt.sessionID
	epoch := rt.epoch
	rt.mu.Unlock()
	if active == nil || active.path.Name != "candidate" || sessionID != "switch-session" || epoch != 2 {
		t.Fatalf("active/session/epoch = %+v/%q/%d, want candidate/switch-session/2", active, sessionID, epoch)
	}
	select {
	case got := <-stream:
		if got != "byte-perfect" {
			t.Fatalf("stream = %q, want byte-perfect", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resumed stream")
	}
	select {
	case <-acknowledged:
	case <-time.After(time.Second):
		t.Fatal("server did not receive resumed stream ACK")
	}
	path, err := LoadLastKnownGoodPath(cfg)
	if err != nil || path == nil || path.Name != "candidate" {
		t.Fatalf("last-known-good path/error = %+v/%v, want candidate", path, err)
	}
}

func handleUnhealthyActiveSession(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	hello, _, err := readFrame(ws)
	if err != nil {
		t.Error(err)
		return
	}
	if hello.Type != "hello" || hello.Resume {
		t.Errorf("initial frame = %+v, want new-session hello", hello)
		return
	}
	if err := writeFrame(ws, frameHeader{Type: "accept", SessionID: "switch-session", Epoch: 1}, nil); err != nil {
		t.Error(err)
		return
	}
	ping, _, err := readFrame(ws)
	if err != nil {
		return
	}
	if ping.Type != "ping" {
		t.Errorf("active frame = %q, want heartbeat ping", ping.Type)
		return
	}
	_, _, _ = readFrame(ws)
}

func handleHealthyResumeCandidate(t *testing.T, ws *websocket.Conn, acknowledged chan<- struct{}) {
	t.Helper()
	ping, _, err := readFrame(ws)
	if err != nil {
		t.Error(err)
		return
	}
	if ping.Type != "ping" {
		t.Errorf("candidate first frame = %q, want probe ping", ping.Type)
		return
	}
	if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
		t.Error(err)
		return
	}
	hello, _, err := readFrame(ws)
	if err != nil {
		t.Error(err)
		return
	}
	if hello.Type != "hello" || !hello.Resume || hello.SessionID != "switch-session" {
		t.Errorf("candidate hello = %+v, want switch-session resume", hello)
		return
	}
	if err := writeFrame(ws, frameHeader{
		Type: "accept", SessionID: "switch-session", Epoch: 2,
	}, nil); err != nil {
		t.Error(err)
		return
	}
	if err := writeFrame(ws, frameHeader{
		Type: "data", SessionID: "switch-session", Epoch: 2,
		Direction: dirServerToClient, Offset: 0,
	}, []byte("byte-perfect")); err != nil {
		t.Error(err)
		return
	}
	ack, _, err := readFrame(ws)
	if err != nil {
		t.Error(err)
		return
	}
	if ack.Type != "ack" || ack.ReceivedOffset != uint64(len("byte-perfect")) {
		t.Errorf("resume ACK = %+v", ack)
		return
	}
	acknowledged <- struct{}{}
	_, _, _ = readFrame(ws)
}

type channelWriter struct {
	writes chan<- string
}

func (w channelWriter) Write(payload []byte) (int, error) {
	w.writes <- string(payload)
	return len(payload), nil
}

func TestPathSwitch_HealthyActiveHeartbeatAddsNoActiveUpgrade(t *testing.T) {
	var upgrades atomic.Int32
	server := probeServer(t, func(ws *websocket.Conn) {
		switch upgrades.Add(1) {
		case 1:
			hello, _, err := readFrame(ws)
			if err != nil || hello.Type != "hello" {
				t.Errorf("initial hello/error = %+v/%v", hello, err)
				return
			}
			if err := writeFrame(ws, frameHeader{Type: "accept", SessionID: "healthy", Epoch: 1}, nil); err != nil {
				t.Error(err)
				return
			}
			ping, _, err := readFrame(ws)
			if err != nil || ping.Type != "ping" {
				t.Errorf("heartbeat/error = %+v/%v", ping, err)
				return
			}
			_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil)
			_, _, _ = readFrame(ws)
		case 2:
			ping, _, err := readFrame(ws)
			if err != nil || ping.Type != "ping" {
				t.Errorf("candidate probe/error = %+v/%v", ping, err)
				return
			}
			_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil)
			_, _, _ = readFrame(ws)
		default:
			t.Errorf("unexpected websocket upgrade %d", upgrades.Load())
		}
	})
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/moltssh"
	cfg := &Config{
		Name: "healthy-heartbeat",
		Paths: []PathConfig{
			{Name: "active", Endpoint: endpoint, Priority: 100, Enabled: true},
			{Name: "candidate", Endpoint: endpoint, Priority: 50, Enabled: true},
		},
		Resume: ResumeConfig{Timeout: time.Second, BufferBytes: 1024},
		Probe: ProbeConfig{
			Timeout: 100 * time.Millisecond, ActiveFailureThreshold: 2,
			CandidateSuccessThreshold: 2, SwitchCooldown: time.Hour,
		},
	}
	rt := newClientRuntimeWithLogger(cfg, clientStreams{
		stdin: strings.NewReader(""), stdout: io.Discard,
	}, log.New(io.Discard, "", 0))
	if err := rt.activate(context.Background(), cfg.Paths[0], false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.finish(nil) })

	if err := rt.runSwitchCycle(context.Background(), map[string]*probeStat{}); err != nil {
		t.Fatal(err)
	}
	if got := upgrades.Load(); got != 2 {
		t.Fatalf("websocket upgrades = %d, want initial active plus inactive probe only", got)
	}
	rt.mu.Lock()
	active := rt.active
	epoch := rt.epoch
	rt.mu.Unlock()
	if active == nil || active.path.Name != "active" || epoch != 1 {
		t.Fatalf("active/epoch = %+v/%d, want unchanged active/1", active, epoch)
	}
}
