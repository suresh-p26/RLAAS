package evaluator

import (
	"context"
	"errors"
	"rlaas/internal/algorithm"
	"rlaas/internal/engine/matcher"
	"rlaas/internal/key"
	"rlaas/internal/store"
	cache "rlaas/internal/store/cache"
	"rlaas/pkg/model"
	"testing"
	"time"
)

type matcherErrStub struct {
	matchErr  error
	selectErr error
}

func (m matcherErrStub) Match(model.RequestContext, []model.Policy) ([]model.Policy, error) {
	if m.matchErr != nil {
		return nil, m.matchErr
	}
	return []model.Policy{{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionDeny}}, nil
}

func (m matcherErrStub) SelectWinner(model.RequestContext, []model.Policy) (*model.Policy, error) {
	if m.selectErr != nil {
		return nil, m.selectErr
	}
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionDeny}
	return &p, nil
}

type keyErrBuilder struct{}

func (keyErrBuilder) Build(model.Policy, model.RequestContext) (string, error) {
	return "", errors.New("key error")
}

type passthroughAlgo struct{}

func (passthroughAlgo) Evaluate(context.Context, model.Policy, model.RequestContext, string) (model.Decision, error) {
	return model.Decision{Allowed: true, Action: model.ActionAllow}, nil
}

type leaseStore struct {
	store.CounterStore
	ok  bool
	cur int64
	err error
}

func (l leaseStore) AcquireLease(context.Context, string, int64, time.Duration) (bool, int64, error) {
	return l.ok, l.cur, l.err
}
func (l leaseStore) ReleaseLease(context.Context, string) error { return nil }

func TestEvaluateReturnsAllowOnMatchOrSelectOrKeyErrors(t *testing.T) {
	basePolicies := []model.Policy{{PolicyID: "p0", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionDeny}}
	ps := policyStoreStub{policies: basePolicies}

	e1 := &DefaultEngine{PolicyStore: ps, CounterStore: leaseStore{}, Matcher: matcherErrStub{matchErr: errors.New("match")}, KeyBuilder: key.New("rlaas"), PolicyCache: cache.NewMemoryPolicyCache(time.Second), Algorithms: map[model.AlgorithmType]algorithm.Evaluator{model.AlgoFixedWindow: passthroughAlgo{}}}
	d, _ := e1.Evaluate(context.Background(), model.RequestContext{})
	if !d.Allowed {
		t.Fatalf("expected allow on match error")
	}

	e2 := &DefaultEngine{PolicyStore: ps, CounterStore: leaseStore{}, Matcher: matcherErrStub{selectErr: errors.New("select")}, KeyBuilder: key.New("rlaas"), PolicyCache: cache.NewMemoryPolicyCache(time.Second), Algorithms: map[model.AlgorithmType]algorithm.Evaluator{model.AlgoFixedWindow: passthroughAlgo{}}}
	d, _ = e2.Evaluate(context.Background(), model.RequestContext{})
	if !d.Allowed {
		t.Fatalf("expected allow on select error")
	}

	e3 := &DefaultEngine{PolicyStore: ps, CounterStore: leaseStore{}, Matcher: matcherErrStub{}, KeyBuilder: keyErrBuilder{}, PolicyCache: cache.NewMemoryPolicyCache(time.Second), Algorithms: map[model.AlgorithmType]algorithm.Evaluator{model.AlgoFixedWindow: passthroughAlgo{}}}
	d, _ = e3.Evaluate(context.Background(), model.RequestContext{})
	if !d.Allowed {
		t.Fatalf("expected allow on key build error")
	}
}

func TestStartConcurrencyLeaseShadowVariantsAndPickPolicyFilters(t *testing.T) {
	now := time.Unix(1000, 0)
	policies := []model.Policy{
		{PolicyID: "disabled", Enabled: false, RolloutPercent: 100, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}},
		{PolicyID: "future", Enabled: true, RolloutPercent: 100, ValidFromUnix: now.Unix() + 100, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}},
		{PolicyID: "past", Enabled: true, RolloutPercent: 100, ValidToUnix: now.Unix() - 100, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}},
		{PolicyID: "rollout0", Enabled: true, RolloutPercent: 0, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}},
		{PolicyID: "active", Enabled: true, RolloutPercent: 100, EnforcementMode: model.ShadowMode, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}, Action: model.ActionDeny},
	}

	e := &DefaultEngine{PolicyStore: policyStoreStub{policies: policies}, CounterStore: leaseStore{ok: false, cur: 1}, Matcher: matcher.New(), KeyBuilder: key.New("rlaas"), PolicyCache: cache.NewMemoryPolicyCache(time.Second), Algorithms: map[model.AlgorithmType]algorithm.Evaluator{model.AlgoConcurrency: passthroughAlgo{}}, Now: func() time.Time { return now }}
	d, _, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{RequestID: "r1"})
	if !d.Allowed || d.Action != model.ActionShadowOnly {
		t.Fatalf("expected shadow deny path")
	}

	e.CounterStore = leaseStore{ok: true, cur: 1}
	d, release, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{RequestID: "r2"})
	if !d.Allowed || d.Action != model.ActionShadowOnly || release == nil {
		t.Fatalf("expected shadow allow path")
	}
}
