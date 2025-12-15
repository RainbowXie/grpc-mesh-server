package reverse

import (
	"context"
	"fmt"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Gateway handles reverse RPC invocations
type Gateway struct {
	registry *registry.SessionManager
	dialer   *Dialer
	timeout  time.Duration
	logger   *zap.Logger
}

// NewGateway creates a new reverse gateway
func NewGateway(reg *registry.SessionManager, logger *zap.Logger) *Gateway {
	return &Gateway{
		registry: reg,
		dialer:   NewDialer(reg),
		timeout:  30 * time.Second,
		logger:   logger,
	}
}

// WithInvokeTimeout sets the default invoke timeout
func (g *Gateway) WithInvokeTimeout(timeout time.Duration) *Gateway {
	g.timeout = timeout
	return g
}

// Invoke performs a unary RPC invocation
func (g *Gateway) Invoke(
	ctx context.Context,
	peerID registry.PeerID,
	req *rpc.InvokeRequest,
) (*rpc.InvokeResponse, error) {
	start := time.Now()

	// Apply timeout
	ctx, cancel := g.ensureDeadline(ctx, req.TimeoutMs)
	defer cancel()

	g.logger.Debug("invoking peer",
		zap.String("peer_id", string(peerID)),
		zap.String("method", req.Method))

	// Dial the peer
	conn, err := g.dialer.DialPeer(ctx, peerID)
	if err != nil {
		return &rpc.InvokeResponse{
			PeerId:  req.PeerId,
			Method:  req.Method,
			Success: false,
			Error: &rpc.ErrorDetail{
				Code:    "DIAL_FAILED",
				Message: fmt.Sprintf("failed to dial peer: %v", err),
			},
			ElapsedMs: uint64(time.Since(start).Milliseconds()),
		}, nil
	}
	defer conn.Close()

	// Create client
	client := rpc.NewInvokePlaneClient(conn)

	// Invoke
	resp, err := client.Invoke(ctx, req)
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			st = status.New(codes.Unknown, err.Error())
		}

		return &rpc.InvokeResponse{
			PeerId:  req.PeerId,
			Method:  req.Method,
			Success: false,
			Error: &rpc.ErrorDetail{
				Code:    st.Code().String(),
				Message: st.Message(),
			},
			ElapsedMs: uint64(time.Since(start).Milliseconds()),
		}, nil
	}

	g.logger.Debug("invoke completed",
		zap.String("peer_id", string(peerID)),
		zap.String("method", req.Method),
		zap.Bool("success", resp.Success),
		zap.Duration("elapsed", time.Since(start)))

	return resp, nil
}

// InvokeStream performs a streaming RPC invocation
func (g *Gateway) InvokeStream(
	ctx context.Context,
	peerID registry.PeerID,
) (rpc.InvokePlane_InvokeStreamClient, func() error, error) {
	conn, err := g.dialer.DialPeer(ctx, peerID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial peer: %w", err)
	}

	client := rpc.NewInvokePlaneClient(conn)
	stream, err := client.InvokeStream(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to create stream: %w", err)
	}

	cleanup := func() error {
		stream.CloseSend()
		return conn.Close()
	}

	return stream, cleanup, nil
}

func (g *Gateway) ensureDeadline(ctx context.Context, timeoutMs uint32) (context.Context, context.CancelFunc) {
	if timeoutMs > 0 {
		timeout := time.Duration(timeoutMs) * time.Millisecond
		return context.WithTimeout(ctx, timeout)
	}

	if g.timeout > 0 {
		return context.WithTimeout(ctx, g.timeout)
	}

	return context.WithCancel(ctx)
}
