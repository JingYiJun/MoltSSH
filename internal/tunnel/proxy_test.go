package tunnel

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

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
