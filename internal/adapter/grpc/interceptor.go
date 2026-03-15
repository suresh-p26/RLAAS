package grpcadapter

import (
	"context"
	"github.com/suresh-p26/RLAAS/pkg/model"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Evaluator is the interface required by the gRPC interceptor.
type Evaluator interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
}

// UnaryServerInterceptor applies rate limiting before unary RPC handlers.
// gRPC status mapping:
// Internal when evaluation fails
// ResourceExhausted when request is denied by policy
func UnaryServerInterceptor(eval Evaluator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		decision, err := eval.Evaluate(ctx, model.RequestContext{SignalType: "grpc", Operation: info.FullMethod, Endpoint: info.FullMethod, Method: "UNARY"})
		if err != nil {
			return nil, status.Error(codes.Internal, "rate limiter failed")
		}
		if !decision.Allowed && (decision.Action == model.ActionDeny || decision.Action == model.ActionDrop || decision.Action == model.ActionDropLowPriority) {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}
