package grpcadapter

import (
	"context"
	"fmt"
	"sync"
	"time"

	rlaasv1 "rlaas/api/proto"
	"rlaas/pkg/model"
)

// LeaseServiceEvaluator is the engine contract needed by the gRPC decision service.
type LeaseServiceEvaluator interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
	StartConcurrencyLease(ctx context.Context, req model.RequestContext) (model.Decision, func() error, error)
}

// RateLimitService exposes CheckLimit, Acquire, and Release RPCs.
type RateLimitService struct {
	rlaasv1.UnimplementedRateLimitServiceServer
	eval   LeaseServiceEvaluator
	leases *grpcLeaseRegistry
}

type grpcLeaseRegistry struct {
	mu    sync.Mutex
	items map[string]func() error
}

func newGRPCLeaseRegistry() *grpcLeaseRegistry {
	return &grpcLeaseRegistry{items: map[string]func() error{}}
}

func (r *grpcLeaseRegistry) put(fn func() error) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := fmt.Sprintf("grpc-lease-%d", time.Now().UnixNano())
	r.items[id] = fn
	return id
}

func (r *grpcLeaseRegistry) pop(id string) (func() error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn, ok := r.items[id]
	if ok {
		delete(r.items, id)
	}
	return fn, ok
}

// NewRateLimitService constructs a new gRPC decision service.
func NewRateLimitService(eval LeaseServiceEvaluator) *RateLimitService {
	return &RateLimitService{eval: eval, leases: newGRPCLeaseRegistry()}
}

// CheckLimit evaluates one request context and returns the policy decision.
func (s *RateLimitService) CheckLimit(ctx context.Context, in *rlaasv1.CheckLimitRequest) (*rlaasv1.CheckLimitResponse, error) {
	decision, err := s.eval.Evaluate(ctx, model.RequestContext{
		RequestID:  in.GetRequestId(),
		OrgID:      in.GetOrgId(),
		TenantID:   in.GetTenantId(),
		SignalType: nonEmpty(in.GetSignalType(), "grpc"),
		Operation:  in.GetOperation(),
		Endpoint:   in.GetEndpoint(),
		Method:     nonEmpty(in.GetMethod(), "UNARY"),
		UserID:     in.GetUserId(),
		APIKey:     in.GetApiKey(),
	})
	if err != nil {
		return nil, err
	}
	return &rlaasv1.CheckLimitResponse{
		Allowed:      decision.Allowed,
		Action:       string(decision.Action),
		Reason:       decision.Reason,
		Remaining:    decision.Remaining,
		RetryAfterMs: decision.RetryAfter.Milliseconds(),
	}, nil
}

// Acquire starts a concurrency lease and returns a lease id when allowed.
func (s *RateLimitService) Acquire(ctx context.Context, in *rlaasv1.AcquireRequest) (*rlaasv1.AcquireResponse, error) {
	decision, release, err := s.eval.StartConcurrencyLease(ctx, model.RequestContext{
		RequestID:  in.GetRequestId(),
		OrgID:      in.GetOrgId(),
		TenantID:   in.GetTenantId(),
		SignalType: "grpc",
		Operation:  in.GetOperation(),
		Endpoint:   in.GetOperation(),
		Method:     "UNARY",
	})
	if err != nil {
		return nil, err
	}
	resp := &rlaasv1.AcquireResponse{Allowed: decision.Allowed, Reason: decision.Reason}
	if decision.Allowed && release != nil {
		resp.LeaseId = s.leases.put(release)
	}
	return resp, nil
}

// Release frees a previously acquired lease id.
func (s *RateLimitService) Release(_ context.Context, in *rlaasv1.ReleaseRequest) (*rlaasv1.ReleaseResponse, error) {
	release, ok := s.leases.pop(in.GetLeaseId())
	if !ok {
		return &rlaasv1.ReleaseResponse{Released: false}, nil
	}
	if err := release(); err != nil {
		return &rlaasv1.ReleaseResponse{Released: false}, nil
	}
	return &rlaasv1.ReleaseResponse{Released: true}, nil
}

func nonEmpty(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
