package tunnel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
)

func TestOpenWebSocket_AddressRaceClosesConnectionReturnedWithError(t *testing.T) {
	// Given
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	closed := make(chan struct{})
	tracked := &closeTrackingConn{Conn: client, closed: closed}
	dial := func(context.Context, string, string) (net.Conn, error) {
		return tracked, errors.New("dial failed after opening socket")
	}

	// When
	_, err := (dialDependencies{dialTCP: dial}).dialFirstTCP(
		context.Background(), "443", []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	)

	// Then
	if err == nil {
		t.Fatal("dial error = nil, want failure")
	}
	select {
	case <-closed:
	default:
		t.Fatal("failed connection remained open")
	}
}

func TestOpenWebSocket_AddressRaceUsesFirstSuccessAndClosesLosers(t *testing.T) {
	// Given
	addresses := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
		netip.MustParseAddr("192.0.2.3"),
		netip.MustParseAddr("192.0.2.4"),
		netip.MustParseAddr("192.0.2.5"),
	}
	var mu sync.Mutex
	dialCount := 0
	var losers []*closeTrackingConn
	var peers []net.Conn
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		mu.Lock()
		dialCount++
		mu.Unlock()
		client, server := net.Pipe()
		mu.Lock()
		peers = append(peers, server)
		mu.Unlock()
		if address == "192.0.2.1:443" {
			return client, nil
		}
		<-ctx.Done()
		tracked := &closeTrackingConn{Conn: client, closed: make(chan struct{})}
		mu.Lock()
		losers = append(losers, tracked)
		mu.Unlock()
		return tracked, nil
	}

	// When
	winner, err := (dialDependencies{dialTCP: dial}).dialFirstTCP(context.Background(), "443", addresses)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	_ = winner.Close()
	mu.Lock()
	defer mu.Unlock()
	for _, peer := range peers {
		_ = peer.Close()
	}
	if dialCount != maxConcurrentTCPDials {
		t.Fatalf("dial count = %d, want %d", dialCount, maxConcurrentTCPDials)
	}
	if len(losers) != maxConcurrentTCPDials-1 {
		t.Fatalf("loser count = %d, want %d", len(losers), maxConcurrentTCPDials-1)
	}
	for _, loser := range losers {
		select {
		case <-loser.closed:
		default:
			t.Fatal("losing connection remained open")
		}
	}
}
