package tunnel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/logging"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func init() {
	logging.Init("error")
}

// TestSessionRegistration tests basic session registration
func TestSessionRegistration(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)

	peerID := registry.PeerID("test-node-001")
	handshake := &control.Handshake{
		NodeID:       string(peerID),
		Version:      "v1.0.0",
		Features:     []string{"heartbeat"},
		Metadata:     map[string]string{"test": "true"},
		TimestampSec: time.Now().Unix(),
		Token:        "test-token",
	}

	// Register session
	session, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)
	require.NotNil(t, session)

	// Verify session exists
	retrieved, exists := reg.Get(peerID)
	assert.True(t, exists)
	assert.Equal(t, peerID, retrieved.PeerID)
	assert.Equal(t, handshake.Version, retrieved.Handshake.Version)
	assert.True(t, retrieved.Connected)
}

// TestSessionHeartbeat tests heartbeat timestamp updates
func TestSessionHeartbeat(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)

	peerID := registry.PeerID("test-node-hb")
	handshake := &control.Handshake{
		NodeID:       string(peerID),
		Version:      "v1.0.0",
		TimestampSec: time.Now().Unix(),
	}

	// Register session
	session, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)

	initialHeartbeat := session.LastHeartbeat

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Update heartbeat
	ctx := context.Background()
	err = reg.Heartbeat(ctx, peerID)
	require.NoError(t, err)

	// Verify heartbeat was updated
	retrieved, _ := reg.Get(peerID)
	assert.True(t, retrieved.LastHeartbeat.After(initialHeartbeat),
		"heartbeat timestamp should be updated")
}

// TestSessionMultipleHeartbeats tests multiple heartbeat updates
func TestSessionMultipleHeartbeats(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)

	peerID := registry.PeerID("test-node-multi")
	handshake := &control.Handshake{
		NodeID:       string(peerID),
		Version:      "v1.0.0",
		TimestampSec: time.Now().Unix(),
	}

	session, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)

	ctx := context.Background()
	previousHeartbeat := session.LastHeartbeat

	// Send multiple heartbeats
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)

		err = reg.Heartbeat(ctx, peerID)
		require.NoError(t, err)

		retrieved, _ := reg.Get(peerID)
		assert.True(t, retrieved.LastHeartbeat.After(previousHeartbeat))
		previousHeartbeat = retrieved.LastHeartbeat
	}

	// Verify session is still active
	retrieved, exists := reg.Get(peerID)
	assert.True(t, exists)
	assert.True(t, retrieved.Connected)
}

// TestSessionCleanup tests stale session cleanup
func TestSessionCleanup(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)

	// Register a session
	peerID := registry.PeerID("test-node-stale")
	handshake := &control.Handshake{
		NodeID:       string(peerID),
		Version:      "v1.0.0",
		TimestampSec: time.Now().Unix(),
	}

	_, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)

	// Mark as disconnected
	reg.MarkDisconnected(peerID)

	// Cleanup with zero max age (should remove disconnected sessions)
	removed := reg.Cleanup(0)
	assert.Equal(t, 1, removed)

	// Verify session was removed
	_, exists := reg.Get(peerID)
	assert.False(t, exists)
}

// TestSessionStats tests statistics gathering
func TestSessionStats(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)

	// Register multiple sessions
	for i := 0; i < 3; i++ {
		peerID := registry.PeerID(fmt.Sprintf("test-node-%d", i))
		handshake := &control.Handshake{
			NodeID:       string(peerID),
			Version:      "v1.0.0",
			TimestampSec: time.Now().Unix(),
		}
		_, err := reg.RegisterSession(peerID, handshake, nil)
		require.NoError(t, err)
	}

	// Mark one as disconnected
	reg.MarkDisconnected(registry.PeerID("test-node-1"))

	// Get stats
	stats := reg.Stats()
	assert.Equal(t, 3, stats.TotalSessions)
	assert.Equal(t, 2, stats.ActiveSessions)
	assert.Equal(t, 1, stats.InactiveSessions)
}

// TestHandshakeValidation tests handshake message validation
func TestHandshakeValidation(t *testing.T) {
	tests := []struct {
		name      string
		handshake *control.Handshake
		expectErr bool
	}{
		{
			name: "valid handshake",
			handshake: &control.Handshake{
				NodeID:       "test-node",
				Version:      "v1.0.0",
				TimestampSec: time.Now().Unix(),
			},
			expectErr: false,
		},
		{
			name: "missing node_id",
			handshake: &control.Handshake{
				Version:      "v1.0.0",
				TimestampSec: time.Now().Unix(),
			},
			expectErr: true,
		},
		{
			name: "missing version",
			handshake: &control.Handshake{
				NodeID:       "test-node",
				TimestampSec: time.Now().Unix(),
			},
			expectErr: true,
		},
		{
			name: "expired timestamp",
			handshake: &control.Handshake{
				NodeID:       "test-node",
				Version:      "v1.0.0",
				TimestampSec: time.Now().Unix() - 400, // 400 seconds ago
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.handshake.ValidateBasics()
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestHeartbeatValidation tests heartbeat message validation
func TestHeartbeatValidation(t *testing.T) {
	tests := []struct {
		name      string
		heartbeat *control.Heartbeat
		expectErr bool
	}{
		{
			name: "valid heartbeat",
			heartbeat: &control.Heartbeat{
				NodeID:           "test-node",
				TimestampUnixSec: time.Now().Unix(),
				Sequence:         1,
			},
			expectErr: false,
		},
		{
			name: "missing node_id",
			heartbeat: &control.Heartbeat{
				TimestampUnixSec: time.Now().Unix(),
				Sequence:         1,
			},
			expectErr: true,
		},
		{
			name: "future timestamp",
			heartbeat: &control.Heartbeat{
				NodeID:           "test-node",
				TimestampUnixSec: time.Now().Unix() + 100,
				Sequence:         1,
			},
			expectErr: true,
		},
		{
			name: "expired timestamp",
			heartbeat: &control.Heartbeat{
				NodeID:           "test-node",
				TimestampUnixSec: time.Now().Unix() - 200,
				Sequence:         1,
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.heartbeat.Validate()
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
