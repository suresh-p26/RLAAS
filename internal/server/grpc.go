package server

import (
	"log/slog"
	"net"

	"google.golang.org/grpc"
)

// GRPCServer contains address and server instance for the gRPC transport layer.
type GRPCServer struct {
	Addr   string
	Server *grpc.Server
}

// NewGRPCServer constructs a GRPCServer.
func NewGRPCServer(addr string, srv *grpc.Server) *GRPCServer {
	return &GRPCServer{Addr: addr, Server: srv}
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
