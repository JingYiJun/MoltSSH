package tunnel

import (
	"context"
	"net"
	"testing"
)

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
