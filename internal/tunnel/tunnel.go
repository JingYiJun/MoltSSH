package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

const proto = "moltssh/1"

func Proxy(ctx context.Context, addr string, insecure bool, stdin io.Reader, stdout io.Writer) error {
	conn, err := quic.DialAddr(ctx, addr, clientTLS(insecure), quicConfig())
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "")

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(stream, "CONNECT\n\n"); err != nil {
		return err
	}
	return copyBoth(stream, stdin, stdout)
}

func Serve(ctx context.Context, listen, connect string) error {
	ln, err := quic.ListenAddr(listen, serverTLS(), quicConfig())
	if err != nil {
		return err
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			return err
		}
		go serveConn(ctx, conn, connect)
	}
}

func Probe(ctx context.Context, addr string) error {
	c, err := quic.DialAddr(ctx, addr, clientTLS(true), quicConfig())
	if err != nil {
		return err
	}
	return c.CloseWithError(0, "")
}

func serveConn(ctx context.Context, conn quic.Connection, connect string) {
	defer conn.CloseWithError(0, "")
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go serveStream(stream, connect)
	}
}

func serveStream(stream quic.Stream, connect string) {
	defer stream.Close()
	streamReader, err := readConnect(stream)
	if err != nil {
		stream.CancelRead(1)
		stream.CancelWrite(1)
		return
	}

	tcp, err := net.Dial("tcp", connect)
	if err != nil {
		stream.CancelRead(2)
		stream.CancelWrite(2)
		return
	}
	defer tcp.Close()
	_ = copyBothFrom(stream, streamReader, tcp, tcp)
}

func readConnect(r io.Reader) (*bufio.Reader, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(line) != "CONNECT" {
		return nil, fmt.Errorf("bad connect header")
	}
	for {
		line, err = br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			return br, nil
		}
	}
}

func copyBoth(stream quic.Stream, in io.Reader, out io.Writer) error {
	return copyBothFrom(stream, stream, in, out)
}

func copyBothFrom(stream quic.Stream, streamReader io.Reader, in io.Reader, out io.Writer) error {
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error
	setErr := func(err error) {
		if err != nil {
			errOnce.Do(func() { firstErr = err })
		}
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := io.Copy(stream, in)
		setErr(err)
		_ = stream.Close()
	}()
	go func() {
		defer wg.Done()
		_, err := io.Copy(out, streamReader)
		setErr(err)
		closeWrite(out)
	}()
	wg.Wait()
	return firstErr
}

func closeWrite(w io.Writer) {
	if c, ok := w.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	}
}

func clientTLS(insecure bool) *tls.Config {
	// ponytail: M0 defaults to ad-hoc self-signed TLS; add cert pinning/mTLS in M2.
	return &tls.Config{ServerName: "moltssh", NextProtos: []string{proto}, InsecureSkipVerify: insecure}
}

func serverTLS() *tls.Config {
	cert, err := selfSignedCert()
	if err != nil {
		panic(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{proto}}
}

func quicConfig() *quic.Config {
	return &quic.Config{MaxIdleTimeout: 30 * time.Second, KeepAlivePeriod: 10 * time.Second}
}

func selfSignedCert() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "moltssh"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"moltssh"},
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}
