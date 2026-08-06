package tunnel

import (
	"fmt"
	"time"
)

func (rt *clientRuntime) receiveLoop(conn *clientConn) {
	for {
		f, payload, err := readFrame(conn.ws)
		if err != nil {
			rt.dropActive(conn)
			return
		}
		rt.mu.Lock()
		active := rt.active == conn && f.Epoch == rt.epoch && (f.SessionID == "" || f.SessionID == rt.sessionID)
		rt.mu.Unlock()
		if !active && f.Type != "ping" && f.Type != "pong" {
			continue
		}
		switch f.Type {
		case "data":
			if err := rt.handleServerData(conn, f, payload); err != nil {
				_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: err.Error()}, nil)
				rt.finish(err)
				return
			}
		case "ack":
			if f.Direction != dirClientToServer {
				rt.finish(fmt.Errorf("bad ack direction"))
				return
			}
			rt.mu.Lock()
			err := rt.advanceC2SAckLocked(f.ReceivedOffset)
			rt.cond.Broadcast()
			rt.mu.Unlock()
			if err != nil {
				rt.finish(err)
				return
			}
		case "fin":
			if err := rt.handleServerFin(conn, f); err != nil {
				rt.finish(err)
				return
			}
		case "close":
			if f.Code == "normal" || f.Code == "target_closed" {
				rt.finish(nil)
			} else {
				rt.finish(fmt.Errorf("%s: %s", f.Code, f.Message))
			}
			return
		case "error":
			rt.finish(fmt.Errorf("%s: %s", f.Code, f.Message))
			return
		case "ping":
			_ = conn.send(frameHeader{Type: "pong", Nonce: f.Nonce, SentAtUnixNano: f.SentAtUnixNano}, nil)
		case "pong":
			conn.heartbeat.resolvePong(f.Nonce, time.Now())
		default:
			rt.finish(fmt.Errorf("unknown frame type %q", f.Type))
			return
		}
	}
}
