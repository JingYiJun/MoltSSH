package tunnel

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/net/websocket"
)

type sessionRequest struct {
	path             PathConfig
	name             string
	resume           bool
	sessionID        string
	clientToServerRx uint64
	serverToClientRx uint64
}

func startSession(ctx context.Context, ws *websocket.Conn, request sessionRequest) (_ *clientConn, accept frameHeader, timings DialTimings, err error) {
	started := time.Now()
	defer func() {
		timings.MoltSSHHello = time.Since(started)
		if err != nil {
			_ = ws.Close()
		}
	}()
	stopContextClose := context.AfterFunc(ctx, func() { _ = ws.Close() })
	defer stopContextClose()
	setContextDeadline(ctx, ws)

	if err := ctx.Err(); err != nil {
		return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, err)
	}
	hello := frameHeader{
		Type:             "hello",
		Version:          1,
		Name:             request.name,
		Resume:           request.resume,
		SessionID:        request.sessionID,
		ClientToServerRx: request.clientToServerRx,
		ServerToClientRx: request.serverToClientRx,
	}
	if err := writeFrame(ws, hello, nil); err != nil {
		return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, contextError(ctx, err))
	}
	for {
		f, _, err := readFrame(ws)
		if err != nil {
			return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, contextError(ctx, err))
		}
		switch f.Type {
		case "accept":
			if err := ctx.Err(); err != nil {
				return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, err)
			}
			if !stopContextClose() {
				return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, context.Canceled)
			}
			if err := ws.SetDeadline(time.Time{}); err != nil {
				return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, fmt.Errorf("clear connection deadline: %w", err))
			}
			conn := &clientConn{ws: ws, path: request.path, epoch: f.Epoch}
			return conn, f, timings, nil
		case "ping":
			if err := writeFrame(ws, frameHeader{Type: "pong", Nonce: f.Nonce, SentAtUnixNano: f.SentAtUnixNano}, nil); err != nil {
				return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, contextError(ctx, err))
			}
		case "error":
			return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, fmt.Errorf("%s: %s", f.Code, f.Message))
		default:
			return nil, frameHeader{}, timings, phaseError(DialPhaseMoltSSHHello, fmt.Errorf("unexpected frame %q before accept", f.Type))
		}
	}
}
