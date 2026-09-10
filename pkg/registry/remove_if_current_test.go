package registry

import (
	"testing"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRemoveIfCurrentDoesNotDeleteReplacement(t *testing.T) {
	reg := New(zap.NewNop())
	peerID := PeerID("android-node")
	handshake := &control.Handshake{
		NodeID:       string(peerID),
		Version:      "v1.0.0",
		TimestampSec: time.Now().Unix(),
		Token:        "token",
	}

	oldSession, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)
	newSession, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)
	require.NotSame(t, oldSession, newSession)

	require.NoError(t, reg.RemoveIfCurrent(peerID, oldSession))

	current, ok := reg.Get(peerID)
	require.True(t, ok)
	require.Same(t, newSession, current)
	require.True(t, current.Connected)
}

func TestRemoveIfCurrentDeletesMatchingSession(t *testing.T) {
	reg := New(zap.NewNop())
	peerID := PeerID("android-node")
	handshake := &control.Handshake{
		NodeID:       string(peerID),
		Version:      "v1.0.0",
		TimestampSec: time.Now().Unix(),
		Token:        "token",
	}

	session, err := reg.RegisterSession(peerID, handshake, nil)
	require.NoError(t, err)
	require.NoError(t, reg.RemoveIfCurrent(peerID, session))

	_, ok := reg.Get(peerID)
	require.False(t, ok)
}
