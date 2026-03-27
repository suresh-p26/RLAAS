package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestNewGRPCServer(t *testing.T) {
	s := NewGRPCServer(":9090", grpc.NewServer())
	require.NotNil(t, s)
	require.NotNil(t, s.Server)
	assert.Equal(t, ":9090", s.Addr)
}

func TestGRPCServerListenAndServeInvalidAddress(t *testing.T) {
	s := NewGRPCServer(":-1", grpc.NewServer())
	err := s.ListenAndServe()
	require.Error(t, err, "expected listen error")
}

func TestGRPCServerListenAndServeAndStop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen failed")
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
	if err != nil {
		assert.True(t, strings.Contains(err.Error(), "use of closed network connection"), "unexpected serve error: %v", err)
	}
}
