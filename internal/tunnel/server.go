package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"golang.org/x/net/websocket"
)

func serveListener(ctx context.Context, ln net.Listener, cfg *Config) error {
	ws := &wsServer{cfg: cfg, sessions: map[string]*serverSession{}}
	mux := http.NewServeMux()
	mux.Handle(cfg.Server.HTTPPath, websocket.Server{
		Config: websocket.Config{Protocol: []string{wsProtocol}},
		Handshake: func(c *websocket.Config, r *http.Request) error {
			if r.URL.Path != cfg.Server.HTTPPath {
				return fmt.Errorf("bad websocket path")
			}
			if !headerHasProtocol(r.Header.Get("Sec-WebSocket-Protocol"), wsProtocol) {
				return fmt.Errorf("missing websocket subprotocol %s", wsProtocol)
			}
			c.Protocol = []string{wsProtocol}
			return nil
		},
		Handler: ws.handle,
	})
	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type wsServer struct {
	cfg *Config
	mu  sync.Mutex

	sessions map[string]*serverSession
}

func (s *wsServer) handle(ws *websocket.Conn) {
	for {
		f, _, err := readFrame(ws)
		if err != nil {
			if isFrameError(err) {
				sendError(ws, "protocol_error", err.Error())
			}
			return
		}
		switch f.Type {
		case "ping":
			_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: f.Nonce, SentAtUnixNano: f.SentAtUnixNano}, nil)
		case "hello":
			sess, conn, err := s.accept(ws, f)
			if err != nil {
				sendError(ws, "protocol_error", err.Error())
				return
			}
			sess.run(conn)
			return
		default:
			sendError(ws, "protocol_error", "expected hello or ping")
			return
		}
	}
}

func (s *wsServer) accept(ws *websocket.Conn, f frameHeader) (*serverSession, *serverConn, error) {
	if f.Version != 1 {
		return nil, nil, fmt.Errorf("unsupported version")
	}
	if f.Name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}
	if !f.Resume {
		target, err := net.Dial("tcp", s.cfg.Server.Connect)
		if err != nil {
			_ = writeFrame(ws, frameHeader{Type: "error", Code: "target_dial_failed", Message: err.Error()}, nil)
			return nil, nil, err
		}
		id, err := newSessionID()
		if err != nil {
			_ = target.Close()
			return nil, nil, err
		}
		sess := newServerSession(s, id, target)
		conn := &serverConn{ws: ws, session: sess, epoch: 1}
		sess.active = conn
		sess.epoch = 1
		s.mu.Lock()
		s.sessions[id] = sess
		s.mu.Unlock()
		go sess.readTarget()
		log.Printf("server accept path=ws session=%s epoch=1", id)
		if err := conn.send(frameHeader{
			Type:             "accept",
			SessionID:        id,
			Epoch:            1,
			ClientToServerRx: 0,
			ServerToClientRx: 0,
		}, nil); err != nil {
			sess.closeTerminal("protocol_error", err.Error())
			return nil, nil, err
		}
		return sess, conn, nil
	}

	if f.SessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required for resume")
	}
	s.mu.Lock()
	sess := s.sessions[f.SessionID]
	s.mu.Unlock()
	if sess == nil {
		return nil, nil, fmt.Errorf("unknown session")
	}
	conn, err := sess.resume(ws, f)
	if err != nil {
		return nil, nil, err
	}
	return sess, conn, nil
}
