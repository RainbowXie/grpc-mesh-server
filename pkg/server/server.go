package server

import (
	"context"
	"fmt"
	"net"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/config"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/logging"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/metrics"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/reverse"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/tunnel"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// Server is the main gRPC-Mesh server
type Server struct {
	cfg *config.Config

	registry       *registry.SessionManager
	tunnelServer   *tunnel.Server
	grpcServer     *grpc.Server
	metricsServer  *metrics.Exporter
	reverseGateway *reverse.Gateway

	logger *zap.Logger
}

// New creates a new server
func New(cfg *config.Config) (*Server, error) {
	if err := logging.Init(cfg.Logging.Level); err != nil {
		return nil, fmt.Errorf("failed to init logging: %w", err)
	}

	logger := logging.L()
	reg := registry.New(logger)
	authPolicy := control.NewAuthPolicy(cfg.Auth.Enabled, cfg.Auth.AllowedTokens)

	tunnelSrv, err := tunnel.New(
		cfg.Listener.Address,
		cfg.Listener.CertFile,
		cfg.Listener.KeyFile,
		cfg.Listener.CAFile,
		authPolicy,
		reg,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel server: %w", err)
	}

	gateway := reverse.NewGateway(reg, logger)
	grpcSrv := grpc.NewServer()
	metricsSrv := metrics.NewExporter(cfg.Server.MetricsAddress, logger)

	return &Server{
		cfg:            cfg,
		registry:       reg,
		tunnelServer:   tunnelSrv,
		grpcServer:     grpcSrv,
		metricsServer:  metricsSrv,
		reverseGateway: gateway,
		logger:         logger,
	}, nil
}

// Start starts all server components
func (s *Server) Start() error {
	s.logger.Info("starting gRPC-Mesh server")

	if err := s.metricsServer.Start(); err != nil {
		return fmt.Errorf("failed to start metrics: %w", err)
	}

	if err := s.tunnelServer.Start(); err != nil {
		return fmt.Errorf("failed to start tunnel: %w", err)
	}

	lis, err := net.Listen("tcp", s.cfg.Server.GRPCAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.cfg.Server.GRPCAddress, err)
	}

	go func() {
		s.logger.Info("gRPC server listening", zap.String("addr", s.cfg.Server.GRPCAddress))
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("gRPC server error", zap.Error(err))
		}
	}()

	s.logger.Info("server started successfully")
	return nil
}

// Stop stops all server components
func (s *Server) Stop() {
	s.logger.Info("stopping server")

	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	if s.tunnelServer != nil {
		s.tunnelServer.Stop()
	}

	if s.metricsServer != nil {
		s.metricsServer.Stop()
	}

	s.logger.Info("server stopped")
}

// ReverseGateway returns the reverse gateway
func (s *Server) ReverseGateway() *reverse.Gateway {
	return s.reverseGateway
}

// Registry returns the session registry
func (s *Server) Registry() *registry.SessionManager {
	return s.registry
}

// InvokePlaneService implements the gRPC InvokePlane service
type InvokePlaneService struct {
	rpc.UnimplementedInvokePlaneServer
	gateway *reverse.Gateway
	logger  *zap.Logger
}

// Invoke implements unary invocation
func (s *InvokePlaneService) Invoke(
	ctx context.Context,
	req *rpc.InvokeRequest,
) (*rpc.InvokeResponse, error) {
	peerID := registry.PeerID(req.PeerId)
	return s.gateway.Invoke(ctx, peerID, req)
}
