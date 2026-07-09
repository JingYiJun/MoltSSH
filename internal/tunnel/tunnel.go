package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const dataChunkBytes = 32 * 1024

func Serve(ctx context.Context, cfg *Config) error {
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return err
	}
	return serveListener(ctx, ln, cfg)
}

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

func Proxy(ctx context.Context, cfg *Config, stdin io.Reader, stdout io.Writer) error {
	rt := newClientRuntime(cfg, stdin, stdout)
	go func() {
		<-ctx.Done()
		rt.finish(ctx.Err())
	}()
	if err := rt.connectAny(ctx, false); err != nil {
		return err
	}
	go rt.stdinLoop(ctx)
	go rt.reconnectLoop(ctx)
	go rt.switchLoop(ctx)
	return rt.wait()
}

func Probe(ctx context.Context, cfg *Config, stdout io.Writer) error {
	for _, p := range enabledPaths(cfg.Paths) {
		rtt, err := probePath(ctx, p, cfg.Probe.Timeout)
		if err != nil {
			log.Printf("probe path=%s status=fail rtt= error=%s", p.Name, err)
			fmt.Fprintf(stdout, "path=%s status=fail rtt= endpoint=%s error=%s\n", p.Name, redactEndpoint(p.Endpoint), err)
			continue
		}
		log.Printf("probe path=%s status=ok rtt=%s error=", p.Name, rtt)
		fmt.Fprintf(stdout, "path=%s status=ok rtt=%s endpoint=%s error=\n", p.Name, rtt, redactEndpoint(p.Endpoint))
	}
	return nil
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

func (s *serverSession) handleClientData(conn *serverConn, f frameHeader, payload []byte) error {
	if f.Direction != dirClientToServer {
		return fmt.Errorf("bad data direction")
	}
	s.mu.Lock()
	rx := s.c2sRx
	s.mu.Unlock()
	if f.Offset < rx {
		if f.Offset+uint64(len(payload)) <= rx {
			return conn.send(frameHeader{Type: "ack", Direction: dirClientToServer, ReceivedOffset: rx}, nil)
		}
		return fmt.Errorf("partial duplicate client data")
	}
	if f.Offset > rx {
		return fmt.Errorf("client data offset gap")
	}
	n, err := writeAll(s.target, payload)
	s.mu.Lock()
	s.c2sRx += uint64(n)
	rx = s.c2sRx
	s.mu.Unlock()
	_ = conn.send(frameHeader{Type: "ack", Direction: dirClientToServer, ReceivedOffset: rx}, nil)
	if err != nil {
		s.closeTerminal("target_closed", err.Error())
		return nil
	}
	return nil
}

func (s *serverSession) handleClientFin(conn *serverConn, f frameHeader) error {
	if f.Direction != dirClientToServer {
		return fmt.Errorf("bad fin direction")
	}
	s.mu.Lock()
	if f.Offset < s.c2sRx || (f.Offset == s.c2sRx && s.c2sFin) {
		rx := s.c2sRx
		s.mu.Unlock()
		return conn.send(frameHeader{Type: "ack", Direction: dirClientToServer, ReceivedOffset: rx}, nil)
	}
	if f.Offset != s.c2sRx {
		s.mu.Unlock()
		return fmt.Errorf("client fin offset gap")
	}
	s.c2sFin = true
	rx := s.c2sRx
	s.mu.Unlock()
	closeWrite(s.target)
	return conn.send(frameHeader{Type: "ack", Direction: dirClientToServer, ReceivedOffset: rx}, nil)
}

func (s *serverSession) readTarget() {
	buf := make([]byte, dataChunkBytes)
	for {
		s.mu.Lock()
		for !s.closed && len(s.s2cBuf) >= s.server.cfg.Resume.BufferBytes {
			s.cond.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		n, err := s.target.Read(buf)
		if n > 0 {
			payload := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			offset := s.s2cNext
			s.s2cNext += uint64(n)
			s.s2cBuf = append(s.s2cBuf, payload...)
			active := s.active
			s.mu.Unlock()
			if active != nil {
				if err := active.send(frameHeader{Type: "data", Direction: dirServerToClient, Offset: offset}, payload); err != nil {
					s.disconnect(active)
				}
			}
		}
		if err != nil {
			s.mu.Lock()
			s.s2cFin = true
			active := s.active
			offset := s.s2cNext
			s.mu.Unlock()
			if active != nil {
				_ = active.send(frameHeader{Type: "fin", Direction: dirServerToClient, Offset: offset}, nil)
			}
			s.closeTerminal("target_closed", "target closed")
			return
		}
	}
}

func (s *serverSession) disconnect(conn *serverConn) {
	s.mu.Lock()
	if s.closed || s.active != conn {
		s.mu.Unlock()
		return
	}
	s.active = nil
	s.disconnected = time.Now()
	epoch := s.epoch
	if s.disconnectTTL != nil {
		s.disconnectTTL.Stop()
	}
	s.disconnectTTL = time.AfterFunc(s.server.cfg.Resume.Timeout, func() {
		s.closeTerminal("resume_timeout", "resume timeout")
	})
	s.cond.Broadcast()
	s.mu.Unlock()
	log.Printf("server disconnect path=ws session=%s epoch=%d", s.id, epoch)
}

func (s *serverSession) closeTerminal(code, message string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	active := s.active
	s.active = nil
	if s.disconnectTTL != nil {
		s.disconnectTTL.Stop()
	}
	epoch := s.epoch
	s.cond.Broadcast()
	s.mu.Unlock()

	log.Printf("server close path=ws session=%s epoch=%d code=%s", s.id, epoch, code)
	if active != nil {
		_ = active.send(frameHeader{Type: "close", Code: code, Message: message}, nil)
		_ = active.ws.Close()
	}
	_ = s.target.Close()
	s.server.mu.Lock()
	delete(s.server.sessions, s.id)
	s.server.mu.Unlock()
}

func (s *serverSession) advanceS2CAckLocked(offset uint64) error {
	if offset < s.s2cAck {
		return nil
	}
	if offset > s.s2cNext {
		return fmt.Errorf("server_to_client ack is ahead of sender")
	}
	drop := offset - s.s2cBufFrom
	if drop > uint64(len(s.s2cBuf)) {
		return fmt.Errorf("server_to_client ack outside buffer")
	}
	s.s2cBuf = s.s2cBuf[drop:]
	s.s2cBufFrom = offset
	s.s2cAck = offset
	return nil
}

type clientConn struct {
	ws     *websocket.Conn
	path   PathConfig
	epoch  uint64
	sendMu sync.Mutex
}

func (c *clientConn) send(h frameHeader, payload []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if h.Epoch == 0 && h.Type != "ping" && h.Type != "pong" && h.Type != "error" {
		h.Epoch = c.epoch
	}
	return writeFrame(c.ws, h, payload)
}

type clientRuntime struct {
	cfg    *Config
	stdin  io.Reader
	stdout io.Writer

	mu       sync.Mutex
	cond     *sync.Cond
	switchMu sync.Mutex

	active    *clientConn
	sessionID string
	epoch     uint64

	c2sNext    uint64
	c2sAck     uint64
	c2sBuf     []byte
	c2sBufFrom uint64
	c2sFin     bool
	c2sFinAt   uint64
	s2cRx      uint64

	lastSwitch time.Time
	done       chan struct{}
	doneErr    error
	doneOnce   sync.Once
}

type probeStat struct {
	success int
	fail    int
	rtt     time.Duration
}

func newClientRuntime(cfg *Config, stdin io.Reader, stdout io.Writer) *clientRuntime {
	rt := &clientRuntime{cfg: cfg, stdin: stdin, stdout: stdout, done: make(chan struct{})}
	rt.cond = sync.NewCond(&rt.mu)
	return rt
}

func (rt *clientRuntime) connectAny(ctx context.Context, resume bool) error {
	var last error
	for _, p := range rt.rankedPaths(ctx) {
		if err := rt.activate(ctx, p, resume); err != nil {
			last = err
			continue
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("no enabled path")
	}
	return last
}

func (rt *clientRuntime) rankedPaths(ctx context.Context) []PathConfig {
	type result struct {
		path PathConfig
		rtt  time.Duration
		ok   bool
	}
	var results []result
	for _, p := range enabledPaths(rt.cfg.Paths) {
		rtt, err := probePath(ctx, p, rt.cfg.Probe.Timeout)
		results = append(results, result{path: p, rtt: rtt, ok: err == nil})
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.ok != b.ok {
			return a.ok
		}
		if a.ok && b.ok && a.rtt != b.rtt {
			return a.rtt < b.rtt
		}
		return a.path.Priority > b.path.Priority
	})
	paths := make([]PathConfig, 0, len(results))
	for _, r := range results {
		paths = append(paths, r.path)
	}
	return paths
}

func (rt *clientRuntime) activate(ctx context.Context, path PathConfig, resume bool) error {
	rt.switchMu.Lock()
	defer rt.switchMu.Unlock()

	sessionID, c2sAck, s2cRx := rt.helloOffsets()
	conn, accept, err := dialSession(ctx, path, rt.cfg.Name, resume, sessionID, c2sAck, s2cRx)
	if err != nil {
		return err
	}

	rt.mu.Lock()
	if resume && accept.SessionID != rt.sessionID {
		rt.mu.Unlock()
		_ = conn.ws.Close()
		return fmt.Errorf("server accepted different session")
	}
	if accept.ClientToServerRx > rt.c2sNext {
		rt.mu.Unlock()
		_ = conn.ws.Close()
		return fmt.Errorf("server ack is ahead of client")
	}
	if err := rt.advanceC2SAckLocked(accept.ClientToServerRx); err != nil {
		rt.mu.Unlock()
		_ = conn.ws.Close()
		return err
	}
	old := rt.active
	rt.active = conn
	rt.sessionID = accept.SessionID
	rt.epoch = accept.Epoch
	rt.lastSwitch = time.Now()
	rt.cond.Broadcast()
	rt.mu.Unlock()

	log.Printf("proxy active path=%s session=%s epoch=%d", path.Name, accept.SessionID, accept.Epoch)
	if old != nil {
		_ = old.ws.Close()
	}
	go rt.receiveLoop(conn)
	return rt.replayClientBytes(conn)
}

func (rt *clientRuntime) helloOffsets() (string, uint64, uint64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.sessionID, rt.c2sAck, rt.s2cRx
}

func dialSession(ctx context.Context, path PathConfig, name string, resume bool, sessionID string, c2sAck, s2cRx uint64) (*clientConn, frameHeader, error) {
	ws, err := dialWS(ctx, path.Endpoint)
	if err != nil {
		return nil, frameHeader{}, err
	}
	hello := frameHeader{
		Type:             "hello",
		Version:          1,
		Name:             name,
		Resume:           resume,
		SessionID:        sessionID,
		ClientToServerRx: c2sAck,
		ServerToClientRx: s2cRx,
	}
	if err := writeFrame(ws, hello, nil); err != nil {
		_ = ws.Close()
		return nil, frameHeader{}, err
	}
	for {
		f, _, err := readFrame(ws)
		if err != nil {
			_ = ws.Close()
			return nil, frameHeader{}, err
		}
		switch f.Type {
		case "accept":
			conn := &clientConn{ws: ws, path: path, epoch: f.Epoch}
			return conn, f, nil
		case "ping":
			_ = writeFrame(ws, frameHeader{Type: "pong", Nonce: f.Nonce, SentAtUnixNano: f.SentAtUnixNano}, nil)
		case "error":
			_ = ws.Close()
			return nil, frameHeader{}, fmt.Errorf("%s: %s", f.Code, f.Message)
		default:
			_ = ws.Close()
			return nil, frameHeader{}, fmt.Errorf("unexpected frame %q before accept", f.Type)
		}
	}
}

func dialWS(ctx context.Context, endpoint string) (*websocket.Conn, error) {
	cfg, err := websocket.NewConfig(endpoint, "http://moltssh.local/")
	if err != nil {
		return nil, err
	}
	cfg.Protocol = []string{wsProtocol}
	return cfg.DialContext(ctx)
}

func (rt *clientRuntime) replayClientBytes(conn *clientConn) error {
	rt.mu.Lock()
	offset := rt.c2sBufFrom
	buf := append([]byte(nil), rt.c2sBuf...)
	fin := rt.c2sFin
	finAt := rt.c2sFinAt
	sessionID := rt.sessionID
	rt.mu.Unlock()

	for len(buf) > 0 {
		n := min(len(buf), maxPayloadBytes)
		if err := conn.send(frameHeader{Type: "data", SessionID: sessionID, Direction: dirClientToServer, Offset: offset}, buf[:n]); err != nil {
			rt.dropActive(conn)
			return err
		}
		offset += uint64(n)
		buf = buf[n:]
	}
	if fin {
		if err := conn.send(frameHeader{Type: "fin", SessionID: sessionID, Direction: dirClientToServer, Offset: finAt}, nil); err != nil {
			rt.dropActive(conn)
			return err
		}
	}
	return nil
}

func (rt *clientRuntime) stdinLoop(ctx context.Context) {
	buf := make([]byte, dataChunkBytes)
	for {
		conn, err := rt.waitActive(ctx)
		if err != nil {
			rt.finish(err)
			return
		}
		if err := rt.waitBuffer(ctx); err != nil {
			rt.finish(err)
			return
		}
		n, err := rt.stdin.Read(buf)
		if n > 0 {
			payload := append([]byte(nil), buf[:n]...)
			offset, sessionID := rt.appendClientBytes(payload)
			if sendErr := conn.send(frameHeader{Type: "data", SessionID: sessionID, Direction: dirClientToServer, Offset: offset}, payload); sendErr != nil {
				rt.dropActive(conn)
			}
		}
		if errors.Is(err, io.EOF) {
			rt.markClientFin()
			rt.sendClientFin()
			return
		}
		if err != nil {
			rt.finish(err)
			return
		}
	}
}

func (rt *clientRuntime) reconnectLoop(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Time{}
	for {
		select {
		case <-rt.done:
			return
		case <-ticker.C:
		}
		rt.mu.Lock()
		active := rt.active
		if active != nil {
			deadline = time.Time{}
			rt.mu.Unlock()
			continue
		}
		if rt.sessionID == "" {
			rt.mu.Unlock()
			continue
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(rt.cfg.Resume.Timeout)
		}
		if time.Now().After(deadline) {
			rt.mu.Unlock()
			rt.finish(fmt.Errorf("resume timeout"))
			return
		}
		rt.mu.Unlock()
		_ = rt.connectAny(ctx, true)
	}
}

func (rt *clientRuntime) switchLoop(ctx context.Context) {
	ticker := time.NewTicker(rt.cfg.Probe.Interval)
	defer ticker.Stop()
	stats := map[string]*probeStat{}
	for {
		select {
		case <-rt.done:
			return
		case <-ticker.C:
		}
		rt.mu.Lock()
		active := rt.active
		lastSwitch := rt.lastSwitch
		rt.mu.Unlock()
		if active == nil {
			continue
		}
		var best *PathConfig
		var bestRTT time.Duration
		activeStat := stats[active.path.Name]
		for _, p := range enabledPaths(rt.cfg.Paths) {
			st := stats[p.Name]
			if st == nil {
				st = &probeStat{}
				stats[p.Name] = st
			}
			rtt, err := probePath(ctx, p, rt.cfg.Probe.Timeout)
			if err != nil {
				st.fail++
				st.success = 0
				continue
			}
			st.success++
			st.fail = 0
			st.rtt = rtt
			if p.Name == active.path.Name {
				activeStat = st
				continue
			}
			if st.success < rt.cfg.Probe.CandidateSuccessThreshold {
				continue
			}
			if best == nil || rtt < bestRTT || (rtt == bestRTT && p.Priority > best.Priority) {
				cp := p
				best = &cp
				bestRTT = rtt
			}
		}
		if best == nil {
			continue
		}
		activeFailed := activeStat != nil && activeStat.fail >= rt.cfg.Probe.ActiveFailureThreshold
		if activeFailed || rt.shouldLatencySwitch(active.path, activeStat, *best, bestRTT, lastSwitch) {
			_ = rt.activate(ctx, *best, true)
		}
	}
}

func (rt *clientRuntime) shouldLatencySwitch(active PathConfig, activeStat *probeStat, candidate PathConfig, candidateRTT time.Duration, lastSwitch time.Time) bool {
	if time.Since(lastSwitch) < rt.cfg.Probe.SwitchCooldown {
		return false
	}
	if activeStat == nil || activeStat.rtt <= 0 {
		return candidate.Priority > active.Priority
	}
	delta := activeStat.rtt - candidateRTT
	if delta >= rt.cfg.Probe.BetterRTTMinDelta && float64(delta)/float64(activeStat.rtt) >= rt.cfg.Probe.BetterRTTRatio {
		return true
	}
	return absDuration(delta) <= rt.cfg.Probe.BetterRTTMinDelta && candidate.Priority > active.Priority
}

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
		default:
			rt.finish(fmt.Errorf("unknown frame type %q", f.Type))
			return
		}
	}
}

func (rt *clientRuntime) handleServerData(conn *clientConn, f frameHeader, payload []byte) error {
	if f.Direction != dirServerToClient {
		return fmt.Errorf("bad data direction")
	}
	rt.mu.Lock()
	rx := rt.s2cRx
	rt.mu.Unlock()
	if f.Offset < rx {
		if f.Offset+uint64(len(payload)) <= rx {
			return conn.send(frameHeader{Type: "ack", SessionID: rt.sessionID, Direction: dirServerToClient, ReceivedOffset: rx}, nil)
		}
		return fmt.Errorf("partial duplicate server data")
	}
	if f.Offset > rx {
		return fmt.Errorf("server data offset gap")
	}
	n, err := writeAll(rt.stdout, payload)
	rt.mu.Lock()
	rt.s2cRx += uint64(n)
	rx = rt.s2cRx
	rt.mu.Unlock()
	_ = conn.send(frameHeader{Type: "ack", SessionID: rt.sessionID, Direction: dirServerToClient, ReceivedOffset: rx}, nil)
	return err
}

func (rt *clientRuntime) handleServerFin(conn *clientConn, f frameHeader) error {
	if f.Direction != dirServerToClient {
		return fmt.Errorf("bad fin direction")
	}
	rt.mu.Lock()
	rx := rt.s2cRx
	rt.mu.Unlock()
	if f.Offset != rx {
		return fmt.Errorf("server fin offset gap")
	}
	closeWrite(rt.stdout)
	_ = conn.send(frameHeader{Type: "ack", SessionID: rt.sessionID, Direction: dirServerToClient, ReceivedOffset: rx}, nil)
	return nil
}

func (rt *clientRuntime) appendClientBytes(payload []byte) (uint64, string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	offset := rt.c2sNext
	rt.c2sNext += uint64(len(payload))
	rt.c2sBuf = append(rt.c2sBuf, payload...)
	return offset, rt.sessionID
}

func (rt *clientRuntime) markClientFin() {
	rt.mu.Lock()
	rt.c2sFin = true
	rt.c2sFinAt = rt.c2sNext
	rt.mu.Unlock()
}

func (rt *clientRuntime) sendClientFin() {
	for {
		conn, err := rt.waitActive(context.Background())
		if err != nil {
			return
		}
		rt.mu.Lock()
		sessionID := rt.sessionID
		offset := rt.c2sFinAt
		rt.mu.Unlock()
		if err := conn.send(frameHeader{Type: "fin", SessionID: sessionID, Direction: dirClientToServer, Offset: offset}, nil); err != nil {
			rt.dropActive(conn)
			continue
		}
		return
	}
}

func (rt *clientRuntime) advanceC2SAckLocked(offset uint64) error {
	if offset < rt.c2sAck {
		return nil
	}
	if offset > rt.c2sNext {
		return fmt.Errorf("client_to_server ack is ahead of sender")
	}
	drop := offset - rt.c2sBufFrom
	if drop > uint64(len(rt.c2sBuf)) {
		return fmt.Errorf("client_to_server ack outside buffer")
	}
	rt.c2sBuf = rt.c2sBuf[drop:]
	rt.c2sBufFrom = offset
	rt.c2sAck = offset
	return nil
}

func (rt *clientRuntime) waitActive(ctx context.Context) (*clientConn, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for rt.active == nil {
		select {
		case <-rt.done:
			if rt.doneErr != nil {
				return nil, rt.doneErr
			}
			return nil, io.EOF
		default:
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rt.cond.Wait()
	}
	return rt.active, nil
}

func (rt *clientRuntime) waitBuffer(ctx context.Context) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for len(rt.c2sBuf) >= rt.cfg.Resume.BufferBytes {
		select {
		case <-rt.done:
			if rt.doneErr != nil {
				return rt.doneErr
			}
			return io.EOF
		default:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rt.cond.Wait()
	}
	return nil
}

func (rt *clientRuntime) dropActive(conn *clientConn) {
	rt.mu.Lock()
	if rt.active == conn {
		rt.active = nil
		rt.cond.Broadcast()
	}
	rt.mu.Unlock()
	_ = conn.ws.Close()
}

func (rt *clientRuntime) finish(err error) {
	rt.doneOnce.Do(func() {
		rt.mu.Lock()
		active := rt.active
		rt.active = nil
		rt.doneErr = err
		rt.cond.Broadcast()
		rt.mu.Unlock()
		if active != nil {
			_ = active.ws.Close()
		}
		close(rt.done)
	})
}

func (rt *clientRuntime) wait() error {
	<-rt.done
	return rt.doneErr
}

func sendDataChunks(conn *serverConn, direction string, offset uint64, data []byte) error {
	for len(data) > 0 {
		n := min(len(data), maxPayloadBytes)
		if err := conn.send(frameHeader{Type: "data", Direction: direction, Offset: offset}, data[:n]); err != nil {
			return err
		}
		offset += uint64(n)
		data = data[n:]
	}
	return nil
}

func probePath(ctx context.Context, path PathConfig, timeout time.Duration) (time.Duration, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ws, err := dialWS(probeCtx, path.Endpoint)
	if err != nil {
		return 0, err
	}
	defer ws.Close()
	if deadline, ok := probeCtx.Deadline(); ok {
		_ = ws.SetDeadline(deadline)
	}
	nonce := newNonce()
	start := time.Now()
	if err := writeFrame(ws, frameHeader{Type: "ping", Nonce: nonce, SentAtUnixNano: start.UnixNano()}, nil); err != nil {
		return 0, err
	}
	for {
		f, _, err := readFrame(ws)
		if err != nil {
			return 0, err
		}
		if f.Type == "pong" && f.Nonce == nonce {
			return time.Since(start), nil
		}
	}
}

func newSessionID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func newNonce() string {
	id, err := newSessionID()
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return id
}

func headerHasProtocol(header, proto string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == proto {
			return true
		}
	}
	return false
}

func writeAll(w io.Writer, p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n, err := w.Write(p)
		written += n
		p = p[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func closeWrite(v any) {
	if c, ok := v.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	}
}

func redactEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if u.User != nil {
		u.User = url.User("redacted")
	}
	q := u.Query()
	for k := range q {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "key") || strings.Contains(lower, "pass") ||
			strings.Contains(lower, "auth") {
			q.Set(k, "redacted")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
