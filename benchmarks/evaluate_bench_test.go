package benchmarks

import (
	"context"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/rlaas"
)

type staticPolicyStore struct {
	policy model.Policy
}

func (s staticPolicyStore) LoadPolicies(_ context.Context, _ string) ([]model.Policy, error) {
	return []model.Policy{s.policy}, nil
}

func (s staticPolicyStore) GetPolicyByID(_ context.Context, policyID string) (*model.Policy, error) {
	if s.policy.PolicyID != policyID {
		return nil, context.Canceled
	}
	p := s.policy
	return &p, nil
}

func (s *staticPolicyStore) UpsertPolicy(_ context.Context, p model.Policy) error {
	s.policy = p
	return nil
}

func (s staticPolicyStore) DeletePolicy(_ context.Context, _ string) error {
	return nil
}

func (s staticPolicyStore) ListPolicies(_ context.Context, _ map[string]string) ([]model.Policy, error) {
	return []model.Policy{s.policy}, nil
}

func BenchmarkEvaluateFixedWindow(b *testing.B) {
	ps := &staticPolicyStore{policy: model.Policy{
		PolicyID: "bench-policy",
		Name:     "bench",
		Enabled:  true,
		Priority: 100,
		Scope:    model.PolicyScope{SignalType: "http", Operation: "charge"},
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoFixedWindow,
			Limit:  1_000_000_000,
			Window: "1s",
		},
		Action:          model.ActionDeny,
		FailureMode:     model.FailOpen,
		EnforcementMode: model.EnforceMode,
		RolloutPercent:  100,
	}}

	client := rlaas.New(rlaas.Options{
		PolicyStore:  ps,
		CounterStore: memory.NewSharded(128),
		CacheTTL:     time.Minute,
		KeyPrefix:    "bench",
	})

	req := model.RequestContext{
		RequestID:  "req-1",
		OrgID:      "acme",
		TenantID:   "retail",
		SignalType: "http",
		Operation:  "charge",
		Endpoint:   "/v1/charge",
		Method:     "POST",
		Timestamp:  time.Now(),
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = client.Evaluate(ctx, req)
		}
	})
}
