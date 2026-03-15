package grpcadapter

import (
	"context"
	"errors"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type grpcEvalStub struct {
	decision model.Decision
	err      error
}

func (g grpcEvalStub) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return g.decision, g.err
}

func TestUnaryServerInterceptorMappings(t *testing.T) {
	interceptor := UnaryServerInterceptor(grpcEvalStub{err: errors.New("boom")})
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/x.y/z"}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal got %v", status.Code(err))
	}

	interceptor2 := UnaryServerInterceptor(grpcEvalStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}})
	_, err = interceptor2(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/x.y/z"}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted got %v", status.Code(err))
	}

	interceptor3 := UnaryServerInterceptor(grpcEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	resp, err := interceptor3(context.Background(), "in", &grpc.UnaryServerInfo{FullMethod: "/x.y/z"}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if err != nil || resp != "ok" {
		t.Fatalf("expected handler response")
	}
}
