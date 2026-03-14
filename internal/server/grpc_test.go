package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestNewGRPCServer(t *testing.T) {
	s := NewGRPCServer(":9090", grpc.NewServer())
	if s == nil || s.Server == nil || s.Addr != ":9090" {
		t.Fatalf("expected grpc server initialized")
	}
}

func TestGRPCServerListenAndServeInvalidAddress(t *testing.T) {
	s := NewGRPCServer(":-1", grpc.NewServer())
	if err := s.ListenAndServe(); err == nil {
		t.Fatalf("expected listen error")
	}
}

func TestGRPCServerListenAndServeAndStop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	grpcSrv := grpc.NewServer()
	s := NewGRPCServer(addr, grpcSrv)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe()
	}()

	time.Sleep(50 * time.Millisecond)
	grpcSrv.Stop()

	err = <-errCh
	if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
		t.Fatalf("unexpected serve error: %v", err)
	}
}
