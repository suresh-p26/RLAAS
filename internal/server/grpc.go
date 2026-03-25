package server

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// GRPCServer contains address and server instance for the gRPC transport layer.
type GRPCServer struct {
	Addr   string
	Server *grpc.Server
}

// GRPCOption configures gRPC server construction.
type GRPCOption func(*grpcConfig)

type grpcConfig struct {
	tlsConfig    *tls.Config
	maxRecvBytes int
	maxSendBytes int
}

// WithGRPCTLS attaches TLS credentials to the gRPC server.
func WithGRPCTLS(tc *tls.Config) GRPCOption {
	return func(c *grpcConfig) { c.tlsConfig = tc }
}

// WithGRPCMaxBytes limits inbound and outbound message sizes.
func WithGRPCMaxBytes(recv, send int) GRPCOption {
	return func(c *grpcConfig) {
		c.maxRecvBytes = recv
		c.maxSendBytes = send
	}
}

// NewGRPCServer constructs a production-hardened gRPC server.
// If an existing *grpc.Server is passed it is used as-is for backwards
// compatibility; otherwise a new server is built with keepalive, recovery
// interceptor, TLS, and message size limits.
func NewGRPCServer(addr string, srv *grpc.Server, opts ...GRPCOption) *GRPCServer {
	if srv != nil {
		return &GRPCServer{Addr: addr, Server: srv}
	}

	gc := &grpcConfig{
		maxRecvBytes: 4 << 20,
		maxSendBytes: 4 << 20,
	}
	for _, o := range opts {
		o(gc)
	}

	serverOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(gc.maxRecvBytes),
		grpc.MaxSendMsgSize(gc.maxSendBytes),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 10 * time.Second,
			Time:                  1 * time.Minute,
			Timeout:               20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.ChainUnaryInterceptor(grpcRecoveryUnary()),
		grpc.ChainStreamInterceptor(grpcRecoveryStream()),
	}

	if gc.tlsConfig != nil {
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(gc.tlsConfig)))
	}

	return &GRPCServer{Addr: addr, Server: grpc.NewServer(serverOpts...)}
}

// ListenAndServe starts the gRPC server.
func (s *GRPCServer) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	slog.Info("grpc server listening", "addr", s.Addr)
	return s.Server.Serve(ln)
}

// GracefulStop stops the gRPC server gracefully, finishing in-flight RPCs.
func (s *GRPCServer) GracefulStop() {
	if s.Server != nil {
		s.Server.GracefulStop()
	}
}

// grpcRecoveryUnary returns a unary interceptor that recovers panics.
func grpcRecoveryUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("grpc panic recovered",
					"error", r,
					"method", info.FullMethod,
					"stack", string(debug.Stack()),
				)
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// grpcRecoveryStream returns a stream interceptor that recovers panics.
func grpcRecoveryStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("grpc stream panic recovered",
					"error", r,
					"method", info.FullMethod,
					"stack", string(debug.Stack()),
				)
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()
		return handler(srv, ss)
	}
}
