package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type testServerStream struct {
	ctx context.Context
}

func (s *testServerStream) SetHeader(_ metadata.MD) error  { return nil }
func (s *testServerStream) SendHeader(_ metadata.MD) error { return nil }
func (s *testServerStream) SetTrailer(_ metadata.MD)       {}
func (s *testServerStream) Context() context.Context       { return s.ctx }
func (s *testServerStream) SendMsg(_ interface{}) error    { return nil }
func (s *testServerStream) RecvMsg(_ interface{}) error    { return nil }

func TestGRPCRecoveryUnary_PanicAndSuccess(t *testing.T) {
	interceptor := grpcRecoveryUnary()
	info := &grpc.UnaryServerInfo{FullMethod: "/svc.Test/Do"}

	_, err := interceptor(context.Background(), "req", info, func(context.Context, interface{}) (interface{}, error) {
		panic("boom")
	})
	assert.Equal(t, codes.Internal, status.Code(err), "expected internal code on panic")

	resp, err := interceptor(context.Background(), "req", info, func(context.Context, interface{}) (interface{}, error) {
		return "ok", nil
	})
	require.NoError(t, err, "expected success path")
	assert.Equal(t, "ok", resp)
}

func TestGRPCRecoveryStream_PanicAndSuccess(t *testing.T) {
	interceptor := grpcRecoveryStream()
	info := &grpc.StreamServerInfo{FullMethod: "/svc.Test/Stream", IsClientStream: true, IsServerStream: true}
	ss := &testServerStream{ctx: context.Background()}

	err := interceptor(nil, ss, info, func(interface{}, grpc.ServerStream) error {
		panic("boom")
	})
	assert.Equal(t, codes.Internal, status.Code(err), "expected internal code on panic")

	err = interceptor(nil, ss, info, func(interface{}, grpc.ServerStream) error {
		return errors.New("stream error")
	})
	require.Error(t, err)
	assert.NotEqual(t, codes.Internal, status.Code(err), "expected passthrough non-panic error")
}

func TestPanicRecovery_HandlesPanic(t *testing.T) {
	h := PanicRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("http panic")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "expected 500 from panic recovery")
}
