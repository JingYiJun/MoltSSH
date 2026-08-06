package tunnel

import (
	"io"
	"log"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type clientConn struct {
	ws        *websocket.Conn
	path      PathConfig
	epoch     uint64
	sendMu    sync.Mutex
	heartbeat heartbeatTracker
	closeOnce sync.Once
	closeErr  error
}

func (c *clientConn) send(h frameHeader, payload []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if h.Epoch == 0 && h.Type != "ping" && h.Type != "pong" && h.Type != "error" {
		h.Epoch = c.epoch
	}
	return writeFrame(c.ws, h, payload)
}

func (c *clientConn) close() error {
	c.closeOnce.Do(func() {
		c.heartbeat.close()
		c.closeErr = c.ws.Close()
	})
	return c.closeErr
}

type clientRuntime struct {
	cfg    *Config
	stdin  io.Reader
	stdout io.Writer
	logger *log.Logger

	mu       sync.Mutex
	cond     *sync.Cond
	switchMu sync.Mutex

	active        *clientConn
	sessionID     string
	epoch         uint64
	lastKnownGood *PathConfig

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
	finished   bool
	doneOnce   sync.Once

	reconnectSignal chan struct{}
}

type probeStat struct {
	success int
	fail    int
	rtt     time.Duration
}

type clientStreams struct {
	stdin  io.Reader
	stdout io.Writer
}

func newClientRuntime(cfg *Config, stdin io.Reader, stdout io.Writer) *clientRuntime {
	return newClientRuntimeWithLogger(cfg, clientStreams{stdin: stdin, stdout: stdout}, log.Default())
}

func newClientRuntimeWithLogger(cfg *Config, streams clientStreams, logger *log.Logger) *clientRuntime {
	rt := &clientRuntime{
		cfg:             cfg,
		stdin:           streams.stdin,
		stdout:          streams.stdout,
		logger:          logger,
		done:            make(chan struct{}),
		reconnectSignal: make(chan struct{}, 1),
	}
	rt.cond = sync.NewCond(&rt.mu)
	path, err := LoadLastKnownGoodPath(cfg)
	if err != nil {
		rt.warnPathState("load", err)
	} else if path != nil {
		rt.lastKnownGood = path
	}
	return rt
}
