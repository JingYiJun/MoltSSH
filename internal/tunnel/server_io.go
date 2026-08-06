package tunnel

import (
	"fmt"
	"log"
	"time"
)

const dataChunkBytes = 32 * 1024

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
