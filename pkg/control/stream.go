package control

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	defaultMaxFrame = 1 << 20 // 1 MiB
	frameHeaderSize = 4       // 4-byte length prefix
)

// ControlStream handles control messages over a yamux stream
type ControlStream struct {
	stream   *yamux.Stream
	maxFrame uint32
	deadline time.Duration
}

// NewControlStream creates a new control stream
func NewControlStream(stream *yamux.Stream) *ControlStream {
	return &ControlStream{
		stream:   stream,
		maxFrame: defaultMaxFrame,
		deadline: 30 * time.Second,
	}
}

// Close closes the stream
func (cs *ControlStream) Close() error {
	return cs.stream.Close()
}

// SendFrame sends a length-prefixed frame
func (cs *ControlStream) SendFrame(data []byte) error {
	if len(data) > int(cs.maxFrame) {
		return fmt.Errorf("frame size %d exceeds max %d", len(data), cs.maxFrame)
	}

	if err := cs.applyDeadline(); err != nil {
		return err
	}

	// Write 4-byte length prefix (big-endian)
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header, uint32(len(data)))

	if _, err := cs.stream.Write(header); err != nil {
		return fmt.Errorf("failed to write frame header: %w", err)
	}

	// Write payload
	if _, err := cs.stream.Write(data); err != nil {
		return fmt.Errorf("failed to write frame payload: %w", err)
	}

	return nil
}

// ReceiveFrame reads a length-prefixed frame
func (cs *ControlStream) ReceiveFrame() ([]byte, error) {
	if err := cs.applyDeadline(); err != nil {
		return nil, err
	}

	// Read 4-byte length prefix
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(cs.stream, header); err != nil {
		return nil, fmt.Errorf("failed to read frame header: %w", err)
	}

	length := binary.BigEndian.Uint32(header)
	if length > cs.maxFrame {
		return nil, fmt.Errorf("frame length %d exceeds limit %d", length, cs.maxFrame)
	}

	// Read payload
	payload := make([]byte, length)
	if _, err := io.ReadFull(cs.stream, payload); err != nil {
		return nil, fmt.Errorf("failed to read frame payload: %w", err)
	}

	return payload, nil
}

// SendJSON sends a JSON message
func (cs *ControlStream) SendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return cs.SendFrame(data)
}

// ReceiveJSON receives a JSON message
func (cs *ControlStream) ReceiveJSON(v interface{}) error {
	data, err := cs.ReceiveFrame()
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return nil
}

// SendHandshake sends a handshake message
func (cs *ControlStream) SendHandshake(h *Handshake) error {
	msg := &ControlMessage{
		Type:      "handshake",
		Handshake: h,
	}
	return cs.SendJSON(msg)
}

// ReceiveHandshake receives and validates a handshake message
// It accepts direct Handshake JSON (from Rust nodes) for compatibility
func (cs *ControlStream) ReceiveHandshake() (*Handshake, error) {
	var handshake Handshake
	if err := cs.ReceiveJSON(&handshake); err != nil {
		return nil, err
	}

	if err := handshake.ValidateBasics(); err != nil {
		return nil, err
	}

	return &handshake, nil
}

// SendHeartbeat sends a heartbeat message
func (cs *ControlStream) SendHeartbeat(h *Heartbeat) error {
	msg := &ControlMessage{
		Type:      "heartbeat",
		Heartbeat: h,
	}
	return cs.SendJSON(msg)
}

// ReceiveHeartbeat receives a heartbeat message
func (cs *ControlStream) ReceiveHeartbeat() (*Heartbeat, error) {
	var msg ControlMessage
	if err := cs.ReceiveJSON(&msg); err != nil {
		return nil, err
	}

	if msg.Type != "heartbeat" {
		return nil, fmt.Errorf("expected heartbeat, got %s", msg.Type)
	}

	if msg.Heartbeat == nil {
		return nil, fmt.Errorf("heartbeat payload is nil")
	}

	return msg.Heartbeat, nil
}

// SendControlMessage sends a generic control message
func (cs *ControlStream) SendControlMessage(msg *ControlMessage) error {
	return cs.SendJSON(msg)
}

// ReceiveControlMessage receives a generic control message
func (cs *ControlStream) ReceiveControlMessage() (*ControlMessage, error) {
	var msg ControlMessage
	if err := cs.ReceiveJSON(&msg); err != nil {
		return nil, err
	}

	if err := msg.Validate(); err != nil {
		return nil, err
	}

	return &msg, nil
}

func (cs *ControlStream) applyDeadline() error {
	if cs.deadline > 0 {
		return cs.stream.SetDeadline(time.Now().Add(cs.deadline))
	}
	return nil
}

// AcceptControlStream accepts a control stream from a yamux session
func AcceptControlStream(ctx context.Context, session *yamux.Session) (*ControlStream, error) {
	stream, err := session.AcceptStream()
	if err != nil {
		return nil, fmt.Errorf("failed to accept stream: %w", err)
	}

	return NewControlStream(stream), nil
}

// ParseHandshakeFrame parses a handshake from raw frame data
func ParseHandshakeFrame(data []byte) (*Handshake, error) {
	var msg ControlMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	if msg.Type != "handshake" || msg.Handshake == nil {
		return nil, fmt.Errorf("not a handshake message")
	}

	return msg.Handshake, nil
}

func ioReadFull(r io.Reader, buf []byte) (int, error) {
	return io.ReadFull(r, buf)
}

func normalizeFeatures(features []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(features))

	for _, f := range features {
		if !seen[f] {
			seen[f] = true
			result = append(result, f)
		}
	}

	return result
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}
