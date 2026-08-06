package tunnel

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type serverConn struct {
	ws      *websocket.Conn
	session *serverSession
	epoch   uint64
	sendMu  sync.Mutex
}

func (c *serverConn) send(h frameHeader, payload []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if h.SessionID == "" && h.Type != "ping" && h.Type != "pong" && h.Type != "error" {
		h.SessionID = c.session.id
	}
	if h.Epoch == 0 && h.Type != "ping" && h.Type != "pong" && h.Type != "error" {
		h.Epoch = c.epoch
	}
	return writeFrame(c.ws, h, payload)
}

type serverSession struct {
	server *wsServer
	id     string
	target net.Conn

	mu    sync.Mutex
	cond  *sync.Cond
	epoch uint64

	active        *serverConn
	disconnected  time.Time
	disconnectTTL *time.Timer
	closed        bool

	c2sRx      uint64
	c2sFin     bool
	s2cNext    uint64
	s2cAck     uint64
	s2cBuf     []byte
	s2cBufFrom uint64
	s2cFin     bool
}

func newServerSession(server *wsServer, id string, target net.Conn) *serverSession {
	s := &serverSession{server: server, id: id, target: target}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *serverSession) resume(ws *websocket.Conn, f frameHeader) (*serverConn, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("session is closed")
	}
	if !s.disconnected.IsZero() && time.Since(s.disconnected) > s.server.cfg.Resume.Timeout {
		s.mu.Unlock()
		s.closeTerminal("resume_timeout", "resume timeout")
		return nil, fmt.Errorf("resume timeout")
	}
	if f.ClientToServerRx > s.c2sRx {
		s.mu.Unlock()
		return nil, fmt.Errorf("client_to_server_rx is ahead of server")
	}
	if err := s.advanceS2CAckLocked(f.ServerToClientRx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.epoch++
	old := s.active
	conn := &serverConn{ws: ws, session: s, epoch: s.epoch}
	s.active = conn
	s.disconnected = time.Time{}
	if s.disconnectTTL != nil {
		s.disconnectTTL.Stop()
		s.disconnectTTL = nil
	}
	accept := frameHeader{
		Type:             "accept",
		SessionID:        s.id,
		Epoch:            s.epoch,
		ClientToServerRx: s.c2sRx,
		ServerToClientRx: s.s2cAck,
	}
	replayFrom := s.s2cBufFrom
	replay := append([]byte(nil), s.s2cBuf...)
	fin := s.s2cFin
	finAt := s.s2cNext
	s.mu.Unlock()

	if old != nil {
		_ = old.ws.Close()
	}
	log.Printf("server resume path=ws session=%s epoch=%d", s.id, conn.epoch)
	if err := conn.send(accept, nil); err != nil {
		s.disconnect(conn)
		return nil, err
	}
	if err := sendDataChunks(conn, dirServerToClient, replayFrom, replay); err != nil {
		s.disconnect(conn)
		return nil, err
	}
	if fin {
		_ = conn.send(frameHeader{Type: "fin", Direction: dirServerToClient, Offset: finAt}, nil)
	}
	return conn, nil
}

func (s *serverSession) run(conn *serverConn) {
	for {
		f, payload, err := readFrame(conn.ws)
		if err != nil {
			if isFrameError(err) {
				_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: err.Error()}, nil)
			}
			s.disconnect(conn)
			return
		}
		if f.Type == "ping" {
			_ = conn.send(frameHeader{Type: "pong", Nonce: f.Nonce, SentAtUnixNano: f.SentAtUnixNano}, nil)
			continue
		}
		if !s.isActive(conn, f) {
			continue
		}
		switch f.Type {
		case "data":
			if err := s.handleClientData(conn, f, payload); err != nil {
				_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: err.Error()}, nil)
				s.disconnect(conn)
				return
			}
		case "ack":
			if f.Direction != dirServerToClient {
				_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: "bad ack direction"}, nil)
				s.disconnect(conn)
				return
			}
			s.mu.Lock()
			err := s.advanceS2CAckLocked(f.ReceivedOffset)
			s.cond.Broadcast()
			s.mu.Unlock()
			if err != nil {
				_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: err.Error()}, nil)
				s.disconnect(conn)
				return
			}
		case "fin":
			if err := s.handleClientFin(conn, f); err != nil {
				_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: err.Error()}, nil)
				s.disconnect(conn)
				return
			}
		case "close":
			s.closeTerminal("normal", "client closed")
			return
		case "error":
			s.disconnect(conn)
			return
		default:
			_ = conn.send(frameHeader{Type: "error", Code: "protocol_error", Message: "unknown frame type"}, nil)
			s.disconnect(conn)
			return
		}
	}
}

func (s *serverSession) isActive(conn *serverConn, f frameHeader) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.active == conn && f.SessionID == s.id && f.Epoch == s.epoch
}
