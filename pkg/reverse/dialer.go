package reverse

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	ErrMissingPeerID = errors.New("peer_id is required")
	errMissingTarget = errors.New("target is required")
)

// Dialer creates gRPC connections over yamux streams
type Dialer struct {
	registry    *registry.SessionManager
	dialTimeout time.Duration
}

// NewDialer creates a new dialer
func NewDialer(reg *registry.SessionManager) *Dialer {
	return &Dialer{
		registry:    reg,
		dialTimeout: 10 * time.Second,
	}
}

// WithDialTimeout sets the dial timeout
func (d *Dialer) WithDialTimeout(timeout time.Duration) *Dialer {
	d.dialTimeout = timeout
	return d
}

// DialPeer creates a gRPC connection to a peer over yamux
func (d *Dialer) DialPeer(ctx context.Context, peerID registry.PeerID) (*grpc.ClientConn, error) {
	if peerID == "" {
		return nil, ErrMissingPeerID
	}

	// Build dial options
	opts := []grpc.DialOption{
		// Use custom dialer that opens yamux stream
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return d.DialContext(ctx, peerID)
		}),
		// No TLS - yamux stream is already over TLS
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Block until connected
		grpc.WithBlock(),
	}

	// Create connection
	// Use "passthrough:///" as target since we're using custom dialer
	ctx, cancel := context.WithTimeout(ctx, d.dialTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///"+string(peerID), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	return conn, nil
}

// DialContext opens a yamux stream to a peer
func (d *Dialer) DialContext(ctx context.Context, peerID registry.PeerID) (net.Conn, error) {
	// Open yamux stream
	stream, err := d.registry.OpenStream(ctx, peerID)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}

	// Yamux stream implements net.Conn interface
	return stream, nil
}

func parseTarget(target string) (registry.PeerID, error) {
	if target == "" {
		return "", errMissingTarget
	}
	// Remove "passthrough:///" prefix if present
	if len(target) > 15 && target[:15] == "passthrough:///" {
		target = target[15:]
	}
	return registry.PeerID(target), nil
}
