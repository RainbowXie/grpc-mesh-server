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

// Gateway handles reverse RPC invocations to connected mesh nodes.
//
// The Gateway provides a high-level interface for invoking RPCs on remote nodes
// through their established Yamux sessions. It handles:
//   - Connection management via the session registry
//   - Stream creation and lifecycle
//   - Timeout enforcement
//   - Error handling and response marshaling
//
// The Gateway uses a Dialer to establish gRPC connections over Yamux streams,
// enabling standard gRPC semantics (unary and streaming RPCs) over the mesh.
//
// Example usage:
//
//	gateway := reverse.NewGateway(registry, logger)
//	gateway = gateway.WithInvokeTimeout(30 * time.Second)
//
//	resp, err := gateway.Invoke(ctx, registry.PeerID("node-123"), &rpc.InvokeRequest{
//	    PeerId:  "node-123",
//	    Method:  "calculator.Add",
//	    Payload: []byte(`{"a":5,"b":3}`),
//	})
type Gateway struct {
	registry *registry.SessionManager
	dialer   *Dialer
	timeout  time.Duration
	logger   *zap.Logger
}

// NewGateway creates a new reverse gateway for invoking RPCs on mesh nodes.
//
// The gateway is initialized with a default invoke timeout of 30 seconds, which
// can be customized using WithInvokeTimeout().
//
// Parameters:
//   - reg: Session registry for looking up and managing node connections
//   - logger: Zap logger for structured logging of invocations
//
// Returns:
//   - A new Gateway instance ready to handle RPC invocations
//
// Example:
//
//	gateway := reverse.NewGateway(registry, logger)
func NewGateway(reg *registry.SessionManager, logger *zap.Logger) *Gateway {
	return &Gateway{
		registry: reg,
		dialer:   NewDialer(reg),
		timeout:  30 * time.Second,
		logger:   logger,
	}
}

// WithInvokeTimeout sets the default timeout for RPC invocations.
//
// This timeout is used when an InvokeRequest does not specify its own timeout
// (TimeoutMs = 0). The timeout applies to the entire invocation including:
//   - Dialing the peer
//   - Establishing the gRPC connection
//   - Executing the RPC
//
// Parameters:
//   - timeout: Maximum duration for RPC invocations (e.g., 30*time.Second)
//
// Returns:
//   - The Gateway instance for method chaining
//
// Example:
//
//	gateway := reverse.NewGateway(registry, logger).
//	    WithInvokeTimeout(60 * time.Second)
func (g *Gateway) WithInvokeTimeout(timeout time.Duration) *Gateway {
	g.timeout = timeout
	return g
}

// Invoke performs a unary RPC invocation on a remote mesh node.
//
// This method:
//  1. Validates the target peer exists in the registry
//  2. Dials the peer over its Yamux session
//  3. Creates a gRPC client for the InvokePlane service
//  4. Executes the RPC with timeout enforcement
//  5. Returns the response with timing information
//
// Errors during dialing or invocation are returned as structured error details
// in the InvokeResponse rather than as gRPC status errors. This ensures
// consistent error handling across the mesh.
//
// Parameters:
//   - ctx: Context for cancellation and tracing
//   - peerID: Unique identifier of the target mesh node
//   - req: InvokeRequest containing method, payload, and optional timeout
//
// Returns:
//   - InvokeResponse with result or error details and elapsed time
//   - nil error (errors are encoded in the response)
//
// The context deadline is determined by (in order of precedence):
//  1. Request's TimeoutMs field (if > 0)
//  2. Gateway's default timeout (if set)
//  3. Context's existing deadline
//
// Example:
//
//	ctx := context.Background()
//	resp, err := gateway.Invoke(ctx, registry.PeerID("node-123"), &rpc.InvokeRequest{
//	    PeerId:    "node-123",
//	    Method:    "service.Method",
//	    Payload:   []byte(`{"key":"value"}`),
//	    TimeoutMs: 5000, // 5 seconds
//	})
//	if err != nil {
//	    return err
//	}
//	if !resp.Success {
//	    log.Printf("RPC failed: %s", resp.Error.Message)
//	}
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
	client := rpc.NewInvokePlaneServiceClient(conn)

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

// InvokeStream establishes a bidirectional streaming RPC connection to a mesh node.
//
// This method creates a persistent stream that can be used for multiple
// request/response exchanges. The stream remains open until:
//   - The cleanup function is called
//   - The context is cancelled
//   - Either side closes the stream
//
// Parameters:
//   - ctx: Context for stream lifecycle and cancellation
//   - peerID: Unique identifier of the target mesh node
//
// Returns:
//   - Stream client for bidirectional communication
//   - Cleanup function to close the stream and connection
//   - Error if dialing or stream creation fails
//
// The cleanup function should be called when done with the stream:
//
//	stream, cleanup, err := gateway.InvokeStream(ctx, peerID)
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
//
//	// Use stream for bidirectional communication
//	for {
//	    req := &rpc.InvokeRequest{...}
//	    if err := stream.Send(req); err != nil {
//	        return err
//	    }
//	    resp, err := stream.Recv()
//	    if err != nil {
//	        return err
//	    }
//	    // Process response...
//	}
func (g *Gateway) InvokeStream(
	ctx context.Context,
	peerID registry.PeerID,
) (rpc.InvokePlaneService_InvokeStreamClient, func() error, error) {
	conn, err := g.dialer.DialPeer(ctx, peerID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial peer: %w", err)
	}

	client := rpc.NewInvokePlaneServiceClient(conn)
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
