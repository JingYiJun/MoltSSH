package tunnel

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestProbeCommand_ReportsConcurrentResultsInConfigOrder(t *testing.T) {
	// Given
	fastArrived := make(chan struct{})
	closed := make(chan string, 2)
	unexpectedFrame := make(chan frameHeader, 2)
	observation := probeCommandObservation{closed: closed, unexpected: unexpectedFrame}
	slow := newProbeCommandServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			return
		}
		<-fastArrived
		if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
			return
		}
		observation.observe(ws, "slow")
	})
	fast := newProbeCommandServer(t, func(ws *websocket.Conn) {
		ping, _, err := readFrame(ws)
		if err != nil {
			return
		}
		close(fastArrived)
		if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: ping.Nonce}, nil); err != nil {
			return
		}
		observation.observe(ws, "fast")
	})
	cfg := &Config{
		Probe: ProbeConfig{Timeout: 200 * time.Millisecond},
		Paths: []PathConfig{
			{Name: "slow", Endpoint: probeCommandEndpoint(slow.URL, ""), Enabled: true},
			{Name: "disabled", Endpoint: probeCommandEndpoint(fast.URL, ""), Enabled: false},
			{Name: "fast", Endpoint: probeCommandEndpoint(fast.URL, ""), Enabled: true},
		},
		sourceIdentity: filepath.Join(t.TempDir(), "probe.toml"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var stdout bytes.Buffer

	// When
	err := Probe(ctx, cfg, &stdout)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q, want one per enabled path", lines)
	}
	if !strings.HasPrefix(lines[0], "path=slow status=ok") || !strings.HasPrefix(lines[1], "path=fast status=ok") {
		t.Fatalf("output order/status = %q, want slow then fast successes", lines)
	}
	for _, line := range lines {
		for _, field := range []string{
			" dns=", " tcp=", " tls=", " websocket_upgrade=", " probe_rtt=", " total=", " failed_phase=",
		} {
			if !strings.Contains(line, field) {
				t.Fatalf("line %q lacks field %q", line, field)
			}
		}
	}
	for range 2 {
		select {
		case <-closed:
		case frame := <-unexpectedFrame:
			t.Fatalf("probe sent post-ping frame %q", frame.Type)
		case <-ctx.Done():
			t.Fatal("probe connection was not closed")
		}
	}
	store, storeErr := pathStateStoreForConfig(cfg)
	if storeErr != nil || store == nil {
		t.Fatalf("path state store = (%+v, %v)", store, storeErr)
	}
	if _, statErr := os.Stat(store.path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("probe persisted path state: %v", statErr)
	}
}

func TestProbeCommand_RedactsFailureEndpointAndError(t *testing.T) {
	// Given
	const secret = "query-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Error(w, "upgrade rejected for token="+request.URL.Query().Get("token"), http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	cfg := &Config{
		Probe: ProbeConfig{Timeout: time.Second},
		Paths: []PathConfig{{
			Name: "relay", Endpoint: probeCommandEndpoint(server.URL, "?token="+secret+"&region=west"), Enabled: true,
		}},
	}
	var stdout bytes.Buffer
	var logs bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	// When
	err := Probe(context.Background(), cfg, &stdout)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	combined := stdout.String() + logs.String()
	if !strings.HasPrefix(stdout.String(), "path=relay status=fail") {
		t.Fatalf("output = %q, want failure prefix", stdout.String())
	}
	for _, field := range []string{
		" dns=", " tcp=", " tls=", " websocket_upgrade=", " probe_rtt=", " total=", " failed_phase=websocket_upgrade", " endpoint=", " error=",
	} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("output %q lacks field %q", stdout.String(), field)
		}
	}
	if strings.Contains(combined, secret) {
		t.Fatalf("probe output leaked query secret: %q", combined)
	}
	if !strings.Contains(combined, "token=redacted") {
		t.Fatalf("probe output did not retain redacted endpoint shape: %q", combined)
	}
	leakyError := phaseError(
		DialPhaseWebSocketUpgrade,
		errors.New("upgrade rejected endpoint="+cfg.Paths[0].Endpoint+" token="+secret),
	)
	leakyRecord := formatProbeRecord(probeCandidate{
		Path: cfg.Paths[0], Err: leakyError, FailedPhase: DialPhaseWebSocketUpgrade,
	})
	if strings.Contains(leakyRecord, secret) {
		t.Fatalf("formatted probe error leaked query secret: %q", leakyRecord)
	}
}

func newProbeCommandServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(websocket.Server{
		Handshake: func(config *websocket.Config, _ *http.Request) error {
			config.Protocol = []string{wsProtocol}
			return nil
		},
		Handler: handler,
	})
	t.Cleanup(server.Close)
	return server
}

type probeCommandObservation struct {
	closed     chan<- string
	unexpected chan<- frameHeader
}

func (o probeCommandObservation) observe(ws *websocket.Conn, name string) {
	frame, _, err := readFrame(ws)
	if err != nil {
		o.closed <- name
		return
	}
	o.unexpected <- frame
}

func probeCommandEndpoint(serverURL, suffix string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/moltssh" + suffix
}
