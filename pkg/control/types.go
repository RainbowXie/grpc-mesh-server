package control

import (
	"errors"
	"time"
)

var (
	ErrHandshakeExpired  = errors.New("handshake timestamp expired")
	ErrHeartbeatExpired  = errors.New("heartbeat too old")
	ErrHeartbeatInFuture = errors.New("heartbeat timestamp in future")
)

// Handshake represents the initial peer registration
type Handshake struct {
	NodeID       string            `json:"node_id"`
	Version      string            `json:"version"`
	Features     []string          `json:"features"`
	Metadata     map[string]string `json:"metadata"`
	TimestampSec int64             `json:"timestamp_sec"`
	Token        string            `json:"token,omitempty"`
}

// ValidateBasics performs basic validation
func (h *Handshake) ValidateBasics() error {
	if h.NodeID == "" {
		return errors.New("node_id is required")
	}
	if h.Version == "" {
		return errors.New("version is required")
	}
	
	// Check timestamp is not too old (5 minutes)
	now := time.Now().Unix()
	if h.TimestampSec > 0 && now-h.TimestampSec > 300 {
		return ErrHandshakeExpired
	}
	
	return nil
}

// Supports checks if a feature is supported
func (h *Handshake) Supports(feature string) bool {
	for _, f := range h.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// Heartbeat represents periodic keep-alive
type Heartbeat struct {
	NodeID          string                 `json:"node_id"`
	TimestampUnixSec int64                  `json:"timestamp_unix_sec"`
	Sequence        uint64                 `json:"sequence"`
	Metrics         map[string]interface{} `json:"metrics,omitempty"`
	Status          string                 `json:"status,omitempty"`
}

// EnsureTimestamp sets timestamp if not set
func (h *Heartbeat) EnsureTimestamp() {
	if h.TimestampUnixSec == 0 {
		h.TimestampUnixSec = time.Now().Unix()
	}
}

// Validate checks if heartbeat is valid
func (h *Heartbeat) Validate() error {
	if h.NodeID == "" {
		return errors.New("node_id is required")
	}
	
	now := time.Now().Unix()
	
	// Check not in future
	if h.TimestampUnixSec > now+10 {
		return ErrHeartbeatInFuture
	}
	
	// Check not too old (2 minutes)
	if now-h.TimestampUnixSec > 120 {
		return ErrHeartbeatExpired
	}
	
	return nil
}

// Age returns how old the heartbeat is
func (h *Heartbeat) Age() time.Duration {
	return time.Since(time.Unix(h.TimestampUnixSec, 0))
}

// Clone creates a copy
func (h *Heartbeat) Clone() *Heartbeat {
	metrics := make(map[string]interface{}, len(h.Metrics))
	for k, v := range h.Metrics {
		metrics[k] = v
	}
	
	return &Heartbeat{
		NodeID:          h.NodeID,
		TimestampUnixSec: h.TimestampUnixSec,
		Sequence:        h.Sequence,
		Metrics:         metrics,
		Status:          h.Status,
	}
}

// ControlMessage represents a control plane message
type ControlMessage struct {
	Type      string                 `json:"type"`
	Handshake *Handshake             `json:"handshake,omitempty"`
	Heartbeat *Heartbeat             `json:"heartbeat,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// Validate validates the message
func (m *ControlMessage) Validate() error {
	if m.Type == "" {
		return errors.New("type is required")
	}
	
	switch m.Type {
	case "handshake":
		if m.Handshake == nil {
			return errors.New("handshake is required for handshake message")
		}
		return m.Handshake.ValidateBasics()
	case "heartbeat":
		if m.Heartbeat == nil {
			return errors.New("heartbeat is required for heartbeat message")
		}
		return m.Heartbeat.Validate()
	case "custom", "update_methods":
		// Allow these without specific validation
		return nil
	default:
		return errors.New("unknown message type: " + m.Type)
	}
}

// NewHeartbeatMessage creates a heartbeat message
func NewHeartbeatMessage(nodeID string, sequence uint64) *ControlMessage {
	return &ControlMessage{
		Type: "heartbeat",
		Heartbeat: &Heartbeat{
			NodeID:          nodeID,
			TimestampUnixSec: time.Now().Unix(),
			Sequence:        sequence,
		},
	}
}
