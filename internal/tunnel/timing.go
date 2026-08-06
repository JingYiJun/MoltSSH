package tunnel

import (
	"fmt"
	"time"
)

type DialPhase string

const (
	DialPhaseDNS              DialPhase = "dns"
	DialPhaseTCP              DialPhase = "tcp"
	DialPhaseTLS              DialPhase = "tls"
	DialPhaseWebSocketUpgrade DialPhase = "websocket_upgrade"
	DialPhaseMoltSSHHello     DialPhase = "moltssh_hello"
)

type DialTimings struct {
	DNS              time.Duration
	TCP              time.Duration
	TLS              time.Duration
	WebSocketUpgrade time.Duration
	MoltSSHHello     time.Duration
	ProbeRTT         time.Duration
	Total            time.Duration
}

type DialError struct {
	Phase DialPhase
	Err   error
}

func (e *DialError) Error() string {
	return fmt.Sprintf("%s: %v", e.Phase, e.Err)
}

func (e *DialError) Unwrap() error {
	return e.Err
}
