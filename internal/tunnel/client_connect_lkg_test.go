package tunnel

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestConnectAny_PrefersPersistedLKGAndProbesOthersInBackground(t *testing.T) {
	// Given
	lkgHello := make(chan struct{})
	alternativePing := make(chan struct{})
	releaseAlternative := make(chan struct{})
	alternativeClosed := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseAlternative:
		default:
			close(releaseAlternative)
		}
	})
	lkgServer := probeServer(t, func(ws *websocket.Conn) {
		if _, err := readSessionFrameType(ws, "hello"); err != nil {
			t.Error(err)
			return
		}
		close(lkgHello)
		<-alternativePing
		_ = writeFrame(ws, frameHeader{Type: "accept", SessionID: "warm", Epoch: 1}, nil)
	})
	alternativeServer := probeServer(t, func(ws *websocket.Conn) {
		_, _, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		close(alternativePing)
		<-releaseAlternative
		_, _, _ = readFrame(ws)
		close(alternativeClosed)
	})
	cfg := lkgConnectConfig(t, []PathConfig{
		connectPath("warm", lkgServer.URL, 1),
		connectPath("alternative", alternativeServer.URL, 100),
	})
	saveTestLKG(t, cfg, "warm")
	var logs bytes.Buffer
	rt := newClientRuntimeWithLogger(cfg, clientStreams{stdin: strings.NewReader(""), stdout: io.Discard}, log.New(&logs, "", 0))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// When
	err := rt.connectAny(ctx, false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	close(releaseAlternative)
	defer rt.finish(nil)
	requireSignal(t, lkgHello, "persisted LKG hello")
	requireSignal(t, alternativeClosed, "background candidate close")
	if got := rt.lastKnownGoodPath(); got == nil || got.Name != "warm" {
		t.Fatalf("last known good = %+v, want warm", got)
	}
	line := singleDialLog(t, logs.String())
	if !strings.Contains(line, "path=warm") || !strings.Contains(line, "probe_rtt=0s") {
		t.Fatalf("dial log = %q, want direct warm LKG", line)
	}
}

func TestConnectAny_FallsBackFromFailedLKG(t *testing.T) {
	// Given
	failed := rejectingSessionServer(t, "rejected")
	healthy := acceptingProbeSessionServer(t, "healthy")
	cfg := lkgConnectConfig(t, []PathConfig{
		connectPath("failed", failed.URL, 100), connectPath("healthy", healthy.URL, 1),
	})
	saveTestLKG(t, cfg, "failed")
	var logs bytes.Buffer
	rt := newClientRuntimeWithLogger(cfg, clientStreams{stdin: strings.NewReader(""), stdout: io.Discard}, log.New(&logs, "", 0))

	// When
	err := rt.connectAny(context.Background(), false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer rt.finish(nil)
	if path, loadErr := LoadLastKnownGoodPath(cfg); loadErr != nil || path == nil || path.Name != "healthy" {
		t.Fatalf("persisted path/error = %+v/%v, want healthy", path, loadErr)
	}
	if strings.Count(logs.String(), "event=proxy_dial") != 2 {
		t.Fatalf("logs = %q, want failed LKG and promoted success", logs.String())
	}
}

func TestConnectAny_IgnoresDisabledLKG(t *testing.T) {
	// Given
	healthy := acceptingProbeSessionServer(t, "healthy")
	cfg := lkgConnectConfig(t, []PathConfig{
		{Name: "disabled", Transport: "ws", Endpoint: "ws://127.0.0.1:1/moltssh", Enabled: false},
		connectPath("healthy", healthy.URL, 1),
	})
	writeTestPathState(t, cfg, `{"version":1,"path":"disabled"}`)
	rt := newClientRuntime(cfg, strings.NewReader(""), io.Discard)

	// When
	err := rt.connectAny(context.Background(), false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer rt.finish(nil)
	if got := rt.lastKnownGoodPath(); got == nil || got.Name != "healthy" {
		t.Fatalf("last known good = %+v, want healthy", got)
	}
}

func TestConnectAny_KeepsLKGOnFailedAttempt(t *testing.T) {
	// Given
	failed := rejectingSessionServer(t, "not-accepted")
	cfg := lkgConnectConfig(t, []PathConfig{connectPath("failed", failed.URL, 1)})
	saveTestLKG(t, cfg, "failed")
	rt := newClientRuntime(cfg, strings.NewReader(""), io.Discard)

	// When
	err := rt.connectAny(context.Background(), false)

	// Then
	if err == nil {
		t.Fatal("connect error = nil, want rejection")
	}
	path, loadErr := LoadLastKnownGoodPath(cfg)
	if loadErr != nil || path == nil || path.Name != "failed" {
		t.Fatalf("persisted path/error = %+v/%v, want unchanged failed", path, loadErr)
	}
}

func TestConnectAny_FallsBackFromStaleLKG(t *testing.T) {
	// Given
	healthy := acceptingProbeSessionServer(t, "healthy")
	cfg := lkgConnectConfig(t, []PathConfig{connectPath("healthy", healthy.URL, 1)})
	writeTestPathState(t, cfg, `{"version":1,"path":"removed"}`)
	rt := newClientRuntime(cfg, strings.NewReader(""), io.Discard)

	// When
	err := rt.connectAny(context.Background(), false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer rt.finish(nil)
	if path, loadErr := LoadLastKnownGoodPath(cfg); loadErr != nil || path == nil || path.Name != "healthy" {
		t.Fatalf("persisted path/error = %+v/%v, want healthy", path, loadErr)
	}
}

func TestConnectAny_ContinuesWhenPathStateIsUnwritable(t *testing.T) {
	// Given
	cacheFile := filepath.Join(t.TempDir(), "cache-file")
	if err := os.WriteFile(cacheFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", cacheFile)
	healthy := acceptingProbeSessionServer(t, "healthy")
	cfg := &Config{
		Name: "loop", Paths: []PathConfig{connectPath("healthy", healthy.URL, 1)},
		sourceIdentity: filepath.Join(t.TempDir(), "client.toml"),
		Probe:          ProbeConfig{Timeout: time.Second}, Resume: ResumeConfig{BufferBytes: 1024},
	}
	var logs bytes.Buffer
	rt := newClientRuntimeWithLogger(cfg, clientStreams{stdin: strings.NewReader(""), stdout: io.Discard}, log.New(&logs, "", 0))

	// When
	err := rt.connectAny(context.Background(), false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer rt.finish(nil)
	if !strings.Contains(logs.String(), "event=path_state_load status=fail") ||
		!strings.Contains(logs.String(), "event=path_state_save status=fail") {
		t.Fatalf("logs = %q, want nonfatal load and save warnings", logs.String())
	}
}

func TestConnectAny_DialsFailedProbeOnceAsLastFallback(t *testing.T) {
	// Given
	var upgrades atomic.Int32
	server := probeServer(t, func(ws *websocket.Conn) {
		attempt := upgrades.Add(1)
		frame, _, err := readFrame(ws)
		if err != nil {
			t.Error(err)
			return
		}
		if attempt == 1 {
			if frame.Type != "ping" {
				t.Errorf("first frame = %q, want probe ping", frame.Type)
			}
			return
		}
		if frame.Type != "hello" {
			t.Errorf("fallback frame = %q, want direct hello", frame.Type)
			return
		}
		_ = writeFrame(ws, frameHeader{Type: "accept", SessionID: "fallback", Epoch: 1}, nil)
	})
	cfg := lkgConnectConfig(t, []PathConfig{connectPath("fallback", server.URL, 1)})
	rt := newClientRuntime(cfg, strings.NewReader(""), io.Discard)

	// When
	err := rt.connectAny(context.Background(), false)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer rt.finish(nil)
	if got := upgrades.Load(); got != 2 {
		t.Fatalf("websocket upgrades = %d, want one failed probe and one direct fallback", got)
	}
}

func lkgConnectConfig(t *testing.T, paths []PathConfig) *Config {
	t.Helper()
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	return &Config{
		Name: "loop", Paths: paths, sourceIdentity: filepath.Join(cacheRoot, "client.toml"),
		Probe: ProbeConfig{Timeout: time.Second}, Resume: ResumeConfig{BufferBytes: 1024},
	}
}

func connectPath(name, serverURL string, priority int) PathConfig {
	return PathConfig{
		Name: name, Transport: "ws", Endpoint: "ws" + strings.TrimPrefix(serverURL, "http") + "/moltssh", Priority: priority, Enabled: true,
	}
}

func saveTestLKG(t *testing.T, cfg *Config, name string) {
	t.Helper()
	if err := SaveLastKnownGoodPath(cfg, name); err != nil {
		t.Fatal(err)
	}
}

func writeTestPathState(t *testing.T, cfg *Config, contents string) {
	t.Helper()
	store, err := pathStateStoreForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
