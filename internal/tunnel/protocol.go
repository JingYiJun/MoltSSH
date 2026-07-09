package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"golang.org/x/net/websocket"
)

const (
	wsProtocol      = "moltssh.v1"
	maxHeaderBytes  = 65536
	maxPayloadBytes = 1048576

	dirClientToServer = "client_to_server"
	dirServerToClient = "server_to_client"
)

type frameHeader struct {
	Type             string `json:"type"`
	Version          int    `json:"version,omitempty"`
	Name             string `json:"name,omitempty"`
	Resume           bool   `json:"resume,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	Epoch            uint64 `json:"epoch,omitempty"`
	Direction        string `json:"direction,omitempty"`
	Offset           uint64 `json:"offset,omitempty"`
	ReceivedOffset   uint64 `json:"received_offset,omitempty"`
	ClientToServerRx uint64 `json:"client_to_server_rx,omitempty"`
	ServerToClientRx uint64 `json:"server_to_client_rx,omitempty"`
	Nonce            string `json:"nonce,omitempty"`
	SentAtUnixNano   int64  `json:"sent_at_unix_nano,omitempty"`
	Code             string `json:"code,omitempty"`
	Message          string `json:"message,omitempty"`
}

func writeFrame(ws *websocket.Conn, h frameHeader, payload []byte) error {
	if h.Type == "" {
		return fmt.Errorf("frame type is required")
	}
	if h.Type != "data" && len(payload) != 0 {
		return fmt.Errorf("%s frame must not carry payload", h.Type)
	}
	if h.Type == "data" && len(payload) > maxPayloadBytes {
		return fmt.Errorf("data payload too large")
	}
	header, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if len(header) > maxHeaderBytes {
		return fmt.Errorf("frame header too large")
	}
	msg := make([]byte, 8+len(header)+len(payload))
	binary.BigEndian.PutUint32(msg[0:4], uint32(len(header)))
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(payload)))
	copy(msg[8:], header)
	copy(msg[8+len(header):], payload)
	return websocket.Message.Send(ws, msg)
}

func readFrame(ws *websocket.Conn) (frameHeader, []byte, error) {
	var msg []byte
	if err := websocket.Message.Receive(ws, &msg); err != nil {
		return frameHeader{}, nil, err
	}
	if len(msg) < 8 {
		return frameHeader{}, nil, fmt.Errorf("truncated frame envelope")
	}
	headerLen := binary.BigEndian.Uint32(msg[0:4])
	payloadLen := binary.BigEndian.Uint32(msg[4:8])
	if headerLen > maxHeaderBytes {
		return frameHeader{}, nil, fmt.Errorf("frame header too large")
	}
	if payloadLen > maxPayloadBytes {
		return frameHeader{}, nil, fmt.Errorf("frame payload too large")
	}
	want := 8 + int(headerLen) + int(payloadLen)
	if len(msg) != want {
		return frameHeader{}, nil, fmt.Errorf("truncated frame body")
	}
	var h frameHeader
	if err := json.Unmarshal(msg[8:8+headerLen], &h); err != nil {
		return frameHeader{}, nil, fmt.Errorf("bad frame header: %w", err)
	}
	if h.Type == "" {
		return frameHeader{}, nil, fmt.Errorf("frame type is required")
	}
	payload := msg[8+headerLen:]
	if h.Type != "data" && len(payload) != 0 {
		return frameHeader{}, nil, fmt.Errorf("%s frame must not carry payload", h.Type)
	}
	return h, payload, nil
}

func sendError(ws *websocket.Conn, code, message string) {
	_ = writeFrame(ws, frameHeader{Type: "error", Code: code, Message: message}, nil)
}
