package server

import (
	"context"
	"fmt"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/reverse"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InvokeProxy handles external invoke requests and routes them to internal nodes
type InvokeProxy struct {
	rpc.UnimplementedInvokePlaneServiceServer
	sessionManager *registry.SessionManager
	gateway        *reverse.Gateway
	acl            ACLPolicy
	limiter        RateLimiter
	logger         *zap.Logger
}

// NewInvokeProxy creates a new invoke proxy
func NewInvokeProxy(
	sessionManager *registry.SessionManager,
	gateway *reverse.Gateway,
	acl ACLPolicy,
	limiter RateLimiter,
	logger *zap.Logger,
) *InvokeProxy {
	if acl == nil {
		acl = NewPermissiveACL()
	}
	if limiter == nil {
		limiter = NewNoOpLimiter()
	}

	return &InvokeProxy{
		sessionManager: sessionManager,
		gateway:        gateway,
		acl:            acl,
		limiter:        limiter,
		logger:         logger,
	}
}

// Invoke executes a unary invoke request
func (p *InvokeProxy) Invoke(
	ctx context.Context,
	req *rpc.InvokeRequest,
) (*rpc.InvokeResponse, error) {
	startTime := time.Now()

	// Validate request
	if req.PeerId == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_id is required")
	}
	if req.Method == "" {
		return nil, status.Error(codes.InvalidArgument, "method is required")
	}

	peerID := registry.PeerID(req.PeerId)

	// Check ACL
	if !p.acl.Check(string(peerID), req.Method) {
		p.logger.Warn("ACL denied",
			zap.String("peer_id", string(peerID)),
			zap.String("method", req.Method))
		return nil, status.Error(codes.PermissionDenied, "access denied by ACL policy")
	}

	// Check rate limit
	if !p.limiter.Allow(ctx, string(peerID), req.Method) {
		p.logger.Warn("rate limit exceeded",
			zap.String("peer_id", string(peerID)),
			zap.String("method", req.Method))
		return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
	}
	defer p.limiter.Release(ctx, string(peerID), req.Method)

	// Check if peer is online
	_, exists := p.sessionManager.Get(peerID)
	if !exists {
		p.logger.Warn("peer not found",
			zap.String("peer_id", string(peerID)))
		return nil, status.Error(codes.NotFound, "peer not found or offline")
	}

	// Set timeout if specified
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	// Log the request
	p.logger.Info("invoking method",
		zap.String("peer_id", string(peerID)),
		zap.String("method", req.Method),
		zap.String("correlation_id", req.CorrelationId),
		zap.Uint32("timeout_ms", req.TimeoutMs))

	// Execute the invoke via gateway
	resp, err := p.gateway.Invoke(ctx, peerID, req)
	if err != nil {
		p.logger.Error("invoke failed",
			zap.String("peer_id", string(peerID)),
			zap.String("method", req.Method),
			zap.Error(err),
			zap.Duration("elapsed", time.Since(startTime)))
		return nil, status.Errorf(codes.Unavailable, "invoke failed: %v", err)
	}

	// Update response with timing
	elapsed := time.Since(startTime)
	if resp.ElapsedMs == 0 {
		resp.ElapsedMs = uint64(elapsed.Milliseconds())
	}

	p.logger.Info("invoke succeeded",
		zap.String("peer_id", string(peerID)),
		zap.String("method", req.Method),
		zap.Bool("success", resp.Success),
		zap.Duration("elapsed", elapsed))

	// Update session heartbeat on successful call
	_ = p.sessionManager.Heartbeat(ctx, peerID)

	return resp, nil
}

// InvokeStream handles streaming invoke requests
func (p *InvokeProxy) InvokeStream(
	stream rpc.InvokePlaneService_InvokeStreamServer,
) error {
	ctx := stream.Context()

	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}

		// Convert InvokeStreamRequest to InvokeRequest
		invokeReq := &rpc.InvokeRequest{
			PeerId:        req.PeerId,
			Method:        req.Method,
			Payload:       req.Payload,
			CorrelationId: req.CorrelationId,
			TimeoutMs:     req.TimeoutMs,
		}

		// Process each request
		resp, err := p.Invoke(ctx, invokeReq)

		var streamResp *rpc.InvokeStreamResponse

		if err != nil {
			// Convert error to response
			st, _ := status.FromError(err)
			streamResp = &rpc.InvokeStreamResponse{
				PeerId:        req.PeerId,
				Method:        req.Method,
				Success:       false,
				CorrelationId: req.CorrelationId,
				Error: &rpc.ErrorDetail{
					Code:    st.Code().String(),
					Message: st.Message(),
				},
			}
		} else {
			// Convert InvokeResponse to InvokeStreamResponse
			streamResp = &rpc.InvokeStreamResponse{
				PeerId:        resp.PeerId,
				Method:        resp.Method,
				Result:        resp.Result,
				Success:       resp.Success,
				Error:         resp.Error,
				CorrelationId: resp.CorrelationId,
				ElapsedMs:     resp.ElapsedMs,
			}
		}

		// Send response
		if err := stream.Send(streamResp); err != nil {
			return err
		}
	}
}

// ValidateRequest validates an invoke request
func (p *InvokeProxy) ValidateRequest(req *rpc.InvokeRequest) error {
	if req == nil {
		return fmt.Errorf("request is nil")
	}
	if req.PeerId == "" {
		return fmt.Errorf("peer_id is required")
	}
	if req.Method == "" {
		return fmt.Errorf("method is required")
	}
	return nil
}
