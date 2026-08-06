package tunnel

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

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
