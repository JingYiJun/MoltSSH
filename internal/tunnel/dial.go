package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"time"

	"golang.org/x/net/websocket"
)

const maxConcurrentTCPDials = 4

type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dialDependencies struct {
	resolver  ipResolver
	dialTCP   func(context.Context, string, string) (net.Conn, error)
	tlsConfig func(string) *tls.Config
	now       func() time.Time
}

func defaultDialDependencies() dialDependencies {
	dialer := &net.Dialer{}
	return dialDependencies{
		resolver: net.DefaultResolver,
		dialTCP:  dialer.DialContext,
		tlsConfig: func(serverName string) *tls.Config {
			return &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
		},
		now: time.Now,
	}
}

func openWebSocket(ctx context.Context, endpoint string) (*websocket.Conn, DialTimings, error) {
	return openWebSocketWith(ctx, endpoint, defaultDialDependencies())
}

func openWebSocketWith(ctx context.Context, endpoint string, deps dialDependencies) (_ *websocket.Conn, timings DialTimings, err error) {
	started := deps.now()
	defer func() { timings.Total = deps.now().Sub(started) }()

	cfg, err := websocket.NewConfig(endpoint, "http://moltssh.local/")
	if err != nil || cfg.Location == nil {
		return nil, timings, phaseError(DialPhaseWebSocketUpgrade, errors.New("invalid websocket endpoint"))
	}
	cfg.Protocol = []string{wsProtocol}
	host := cfg.Location.Hostname()
	port, err := endpointPort(cfg.Location)
	if err != nil {
		return nil, timings, phaseError(DialPhaseWebSocketUpgrade, err)
	}

	addresses, err := deps.resolveEndpoint(ctx, host, &timings)
	if err != nil {
		return nil, timings, phaseError(DialPhaseDNS, err)
	}

	phaseStarted := deps.now()
	raw, err := deps.dialFirstTCP(ctx, port, addresses)
	timings.TCP = deps.now().Sub(phaseStarted)
	if err != nil {
		return nil, timings, phaseError(DialPhaseTCP, err)
	}
	owned := true
	defer func() {
		if owned {
			_ = raw.Close()
		}
	}()
	setContextDeadline(ctx, raw)

	if cfg.Location.Scheme == "wss" {
		phaseStarted = deps.now()
		tlsCfg := deps.tlsConfig(host).Clone()
		tlsCfg.ServerName = host
		tlsConn := tls.Client(raw, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			timings.TLS = deps.now().Sub(phaseStarted)
			return nil, timings, phaseError(DialPhaseTLS, contextError(ctx, err))
		}
		timings.TLS = deps.now().Sub(phaseStarted)
		raw = tlsConn
	}

	phaseStarted = deps.now()
	ws, err := upgradeWebSocket(ctx, cfg, raw)
	timings.WebSocketUpgrade = deps.now().Sub(phaseStarted)
	if err != nil {
		return nil, timings, phaseError(DialPhaseWebSocketUpgrade, err)
	}
	owned = false
	if err := ws.SetDeadline(time.Time{}); err != nil {
		_ = ws.Close()
		return nil, timings, phaseError(DialPhaseWebSocketUpgrade, fmt.Errorf("clear connection deadline: %w", err))
	}
	return ws, timings, nil
}

func (deps dialDependencies) resolveEndpoint(ctx context.Context, host string, timings *DialTimings) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address}, nil
	}
	started := deps.now()
	addresses, err := deps.resolver.LookupNetIP(ctx, "ip", host)
	timings.DNS = deps.now().Sub(started)
	if err != nil {
		return nil, contextError(ctx, err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("DNS returned no addresses")
	}
	return addresses, nil
}

func endpointPort(location *url.URL) (string, error) {
	if port := location.Port(); port != "" {
		return port, nil
	}
	switch location.Scheme {
	case "ws":
		return "80", nil
	case "wss":
		return "443", nil
	default:
		return "", errors.New("unsupported websocket scheme")
	}
}

type tcpDialResult struct {
	conn net.Conn
	err  error
}

func (deps dialDependencies) dialFirstTCP(ctx context.Context, port string, addresses []netip.Addr) (net.Conn, error) {
	count := min(len(addresses), maxConcurrentTCPDials)
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan tcpDialResult, count)
	for _, address := range addresses[:count] {
		address := net.JoinHostPort(address.String(), port)
		go func() {
			conn, err := deps.dialTCP(dialCtx, "tcp", address)
			results <- tcpDialResult{conn: conn, err: err}
		}()
	}

	var winner net.Conn
	var failures []error
	for range count {
		result := <-results
		if result.err == nil && result.conn != nil {
			if winner == nil {
				winner = result.conn
				cancel()
			} else {
				_ = result.conn.Close()
			}
			continue
		}
		if result.conn != nil {
			_ = result.conn.Close()
		}
		if result.err != nil {
			failures = append(failures, result.err)
		} else {
			failures = append(failures, errors.New("TCP dial returned no connection"))
		}
	}
	if winner != nil {
		return winner, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.Join(failures...)
}

func upgradeWebSocket(ctx context.Context, cfg *websocket.Config, raw net.Conn) (*websocket.Conn, error) {
	type result struct {
		ws  *websocket.Conn
		err error
	}
	done := make(chan result, 1)
	go func() {
		ws, err := websocket.NewClient(cfg, raw)
		done <- result{ws: ws, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = raw.Close()
		<-done
		return nil, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			if result.ws != nil {
				_ = result.ws.Close()
			}
			return nil, err
		}
		return result.ws, result.err
	}
}

func setContextDeadline(ctx context.Context, conn net.Conn) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
}

func contextError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

func phaseError(phase DialPhase, err error) error {
	return &DialError{Phase: phase, Err: err}
}

func dialWS(ctx context.Context, endpoint string) (*websocket.Conn, error) {
	ws, _, err := openWebSocket(ctx, endpoint)
	return ws, err
}
