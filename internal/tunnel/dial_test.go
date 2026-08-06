package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

func TestOpenWebSocket_ReportsDNSFailurePhase(t *testing.T) {
	// Given
	want := errors.New("lookup failed")
	deps := defaultDialDependencies()
	deps.resolver = resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return nil, want
	})

	// When
	_, _, err := openWebSocketWith(context.Background(), "ws://example.test/moltssh?token=secret", deps)

	// Then
	requireDialPhase(t, err, DialPhaseDNS)
	if !errors.Is(err, want) || strings.Contains(err.Error(), "token=secret") {
		t.Fatalf("error = %q, want wrapped cause without endpoint query", err)
	}
}

func TestOpenWebSocket_ReportsTCPFailurePhase(t *testing.T) {
	// Given
	want := errors.New("connect failed")
	deps := defaultDialDependencies()
	deps.resolver = staticResolver(netip.MustParseAddr("192.0.2.1"))
	deps.dialTCP = func(context.Context, string, string) (net.Conn, error) { return nil, want }

	// When
	_, timings, err := openWebSocketWith(context.Background(), "ws://example.test/moltssh", deps)

	// Then
	requireDialPhase(t, err, DialPhaseTCP)
	if !errors.Is(err, want) || timings.TCP < 0 {
		t.Fatalf("error/timing = %v/%v, want wrapped cause and TCP timing", err, timings.TCP)
	}
}

func TestOpenWebSocket_ReportsTLSFailurePhase(t *testing.T) {
	// Given
	server, client := net.Pipe()
	closed := make(chan struct{})
	tracked := &closeTrackingConn{Conn: client, closed: closed}
	t.Cleanup(func() { _ = server.Close() })
	deps := defaultDialDependencies()
	deps.resolver = staticResolver(netip.MustParseAddr("192.0.2.1"))
	deps.dialTCP = oneConnDialer(tracked)
	go func() {
		buf := make([]byte, 4096)
		_, _ = server.Read(buf)
		_, _ = server.Write([]byte("not tls"))
		_ = server.Close()
	}()

	// When
	_, _, err := openWebSocketWith(context.Background(), "wss://example.test/moltssh", deps)

	// Then
	requireDialPhase(t, err, DialPhaseTLS)
	select {
	case <-closed:
	default:
		t.Fatal("raw connection remained open after TLS failure")
	}
}

func TestOpenWebSocket_ReportsUpgradeFailurePhase(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no upgrade", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	// When
	_, _, err := openWebSocket(context.Background(), wsURL(t, srv.URL, "127.0.0.1"))

	// Then
	requireDialPhase(t, err, DialPhaseWebSocketUpgrade)
}

func TestOpenWebSocket_WSUsesLiteralWithoutDNSAndClearsDeadline(t *testing.T) {
	// Given
	srv := newWebSocketServer(t, false, nil)
	deps := defaultDialDependencies()
	var tracked *deadlineTrackingConn
	deps.dialTCP = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		tracked = &deadlineTrackingConn{Conn: conn}
		return tracked, nil
	}

	// When
	ws, timings, err := openWebSocketWith(context.Background(), wsURL(t, srv.URL, "127.0.0.1"), deps)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if timings.DNS != 0 || timings.TLS != 0 {
		t.Fatalf("timings = %+v, want zero DNS and TLS", timings)
	}
	if tracked == nil || !tracked.deadline.IsZero() {
		t.Fatalf("raw deadline = %v, want cleared", tracked.deadline)
	}
}

func TestOpenWebSocket_WSSVerifiesCAAndPreservesSNI(t *testing.T) {
	// Given
	var mu sync.Mutex
	gotSNI := ""
	srv := newWebSocketServer(t, true, func(info *tls.ClientHelloInfo) {
		mu.Lock()
		gotSNI = info.ServerName
		mu.Unlock()
	})
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	deps := defaultDialDependencies()
	deps.resolver = staticResolver(netip.MustParseAddr("127.0.0.1"))
	deps.tlsConfig = func(serverName string) *tls.Config {
		return &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12}
	}

	// When
	ws, timings, err := openWebSocketWith(context.Background(), wsURL(t, srv.URL, "example.com"), deps)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	mu.Lock()
	sni := gotSNI
	mu.Unlock()
	if sni != "example.com" || timings.TLS < 0 {
		t.Fatalf("SNI/timing = %q/%v, want example.com and TLS timing", sni, timings.TLS)
	}
}

func TestOpenWebSocket_CancelsUpgradeAndClosesConnection(t *testing.T) {
	// Given
	server, client := net.Pipe()
	closed := make(chan struct{})
	tracked := &closeTrackingConn{Conn: client, closed: closed}
	deps := defaultDialDependencies()
	deps.resolver = staticResolver(netip.MustParseAddr("192.0.2.1"))
	deps.dialTCP = oneConnDialer(tracked)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := openWebSocketWith(ctx, "ws://example.test/moltssh", deps)
		done <- err
	}()
	buf := make([]byte, 1)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}

	// When
	cancel()

	// Then
	select {
	case err := <-done:
		requireDialPhase(t, err, DialPhaseWebSocketUpgrade)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("upgrade did not stop after cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("raw connection was not closed")
	}
	_ = server.Close()
}

type closeTrackingConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

type deadlineTrackingConn struct {
	net.Conn
	deadline time.Time
}

func (c *deadlineTrackingConn) SetDeadline(deadline time.Time) error {
	c.deadline = deadline
	return c.Conn.SetDeadline(deadline)
}

func (c *closeTrackingConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func staticResolver(addrs ...netip.Addr) resolverFunc {
	return func(context.Context, string, string) ([]netip.Addr, error) { return addrs, nil }
}

func oneConnDialer(conn net.Conn) func(context.Context, string, string) (net.Conn, error) {
	var once sync.Once
	return func(context.Context, string, string) (net.Conn, error) {
		var got net.Conn
		once.Do(func() { got = conn })
		if got == nil {
			return nil, errors.New("unexpected extra dial")
		}
		return got, nil
	}
}

func requireDialPhase(t *testing.T, err error, want DialPhase) {
	t.Helper()
	var dialErr *DialError
	if !errors.As(err, &dialErr) || dialErr.Phase != want {
		t.Fatalf("error = %v, want DialError phase %q", err, want)
	}
}

func wsURL(t *testing.T, rawURL, host string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		_, port, _ := net.SplitHostPort(u.Host)
		u.Host = net.JoinHostPort(host, port)
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	u.Path = "/moltssh"
	return u.String()
}

func newWebSocketServer(t *testing.T, secure bool, observe func(*tls.ClientHelloInfo)) *httptest.Server {
	t.Helper()
	handler := websocket.Server{Handshake: func(cfg *websocket.Config, _ *http.Request) error {
		cfg.Protocol = []string{wsProtocol}
		return nil
	}, Handler: func(ws *websocket.Conn) {
		defer ws.Close()
		var value string
		_ = websocket.Message.Receive(ws, &value)
	}}
	srv := httptest.NewUnstartedServer(handler)
	if secure {
		if observe != nil {
			base := srv.TLS
			if base == nil {
				base = &tls.Config{}
			}
			base.GetConfigForClient = func(info *tls.ClientHelloInfo) (*tls.Config, error) { observe(info); return nil, nil }
			srv.TLS = base
		}
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	return srv
}
