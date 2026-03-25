package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected internal code on panic, got %v", err)
	}

	resp, err := interceptor(context.Background(), "req", info, func(context.Context, interface{}) (interface{}, error) {
		return "ok", nil
	})
	if err != nil || resp != "ok" {
		t.Fatalf("expected success path, resp=%v err=%v", resp, err)
	}
}

func TestGRPCRecoveryStream_PanicAndSuccess(t *testing.T) {
	interceptor := grpcRecoveryStream()
	info := &grpc.StreamServerInfo{FullMethod: "/svc.Test/Stream", IsClientStream: true, IsServerStream: true}
	ss := &testServerStream{ctx: context.Background()}

	err := interceptor(nil, ss, info, func(interface{}, grpc.ServerStream) error {
		panic("boom")
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected internal code on panic, got %v", err)
	}

	err = interceptor(nil, ss, info, func(interface{}, grpc.ServerStream) error {
		return errors.New("stream error")
	})
	if err == nil || status.Code(err) == codes.Internal {
		t.Fatalf("expected passthrough non-panic error, got %v", err)
	}
}

func TestPanicRecovery_HandlesPanic(t *testing.T) {
	h := PanicRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("http panic")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from panic recovery, got %d", rec.Code)
	}
}
