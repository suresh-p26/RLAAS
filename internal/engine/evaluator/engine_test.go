package evaluator

import (
	"context"
	"errors"
	"github.com/suresh-p26/RLAAS/internal/algorithm"
	"github.com/suresh-p26/RLAAS/internal/engine/matcher"
	"github.com/suresh-p26/RLAAS/internal/key"
	"github.com/suresh-p26/RLAAS/internal/store"
	cache "github.com/suresh-p26/RLAAS/internal/store/cache"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"testing"
	"time"
)

type policyStoreStub struct {
	policies []model.Policy
	err      error
}

func (p policyStoreStub) LoadPolicies(context.Context, string) ([]model.Policy, error) {
	return p.policies, p.err
}
func (p policyStoreStub) GetPolicyByID(context.Context, string) (*model.Policy, error) {
	return nil, errors.New("n/a")
}
func (p policyStoreStub) UpsertPolicy(context.Context, model.Policy) error { return errors.New("n/a") }
func (p policyStoreStub) DeletePolicy(context.Context, string) error       { return errors.New("n/a") }
func (p policyStoreStub) ListPolicies(context.Context, map[string]string) ([]model.Policy, error) {
	return nil, errors.New("n/a")
}

type counterStoreStub struct {
	store.CounterStore
	leaseOK  bool
	leaseCur int64
	leaseErr error
}

func (c counterStoreStub) AcquireLease(context.Context, string, int64, time.Duration) (bool, int64, error) {
	return c.leaseOK, c.leaseCur, c.leaseErr
}
func (c counterStoreStub) ReleaseLease(context.Context, string) error { return nil }

type algoStub struct {
	decision model.Decision
	err      error
}

func (a algoStub) Evaluate(context.Context, model.Policy, model.RequestContext, string) (model.Decision, error) {
	return a.decision, a.err
}

func newTestEngine(p []model.Policy, a algorithm.Evaluator) *DefaultEngine {
	return &DefaultEngine{
		PolicyStore:  policyStoreStub{policies: p},
		CounterStore: counterStoreStub{leaseOK: true, leaseCur: 1},
		Matcher:      matcher.New(),
		KeyBuilder:   key.New("rlaas"),
		PolicyCache:  cache.NewMemoryPolicyCache(time.Minute),
		Algorithms:   map[model.AlgorithmType]algorithm.Evaluator{model.AlgoFixedWindow: a, model.AlgoConcurrency: a},
		Now:          func() time.Time { return time.Unix(1000, 0) },
	}
}

func TestEvaluateNoPolicyAndStoreError(t *testing.T) {
	e := newTestEngine(nil, algoStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	d, err := e.Evaluate(context.Background(), model.RequestContext{})
	if err != nil || !d.Allowed {
		t.Fatalf("expected allow with no policy")
	}
	e.PolicyStore = policyStoreStub{err: errors.New("boom")}
	_, _ = e.Evaluate(context.Background(), model.RequestContext{})
}

func TestEvaluateAlgorithmAndShadowAndFailure(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionDeny, FailureMode: model.FailClosed}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}})
	d, err := e.Evaluate(context.Background(), model.RequestContext{})
	if err != nil || d.MatchedPolicyID != "p1" {
		t.Fatalf("expected match")
	}
	p.EnforcementMode = model.ShadowMode
	e = newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}})
	d, _ = e.Evaluate(context.Background(), model.RequestContext{})
	if !d.Allowed || d.Action != model.ActionShadowOnly {
		t.Fatalf("expected shadow")
	}
	e = newTestEngine([]model.Policy{p}, algoStub{err: errors.New("x")})
	d, _ = e.Evaluate(context.Background(), model.RequestContext{})
	if d.Allowed {
		t.Fatalf("expected fail closed deny")
	}
}

func TestStartConcurrencyLeasePaths(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}, Action: model.ActionDeny}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	e.CounterStore = counterStoreStub{leaseOK: false, leaseCur: 1}
	d, _, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	if d.Allowed {
		t.Fatalf("expected denied lease")
	}
	e.CounterStore = counterStoreStub{leaseOK: true, leaseCur: 1}
	d, release, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	if !d.Allowed || release == nil {
		t.Fatalf("expected allowed lease")
	}
	_ = release()
}

func TestStartConcurrencyLeasePolicyErrorAndNoPolicy(t *testing.T) {
	eErr := newTestEngine(nil, algoStub{})
	eErr.PolicyStore = policyStoreStub{err: errors.New("load fail")}
	d, release, err := eErr.StartConcurrencyLease(context.Background(), model.RequestContext{})
	if err != nil || !d.Allowed || release == nil {
		t.Fatalf("expected allow on policy resolution failure")
	}

	eNo := newTestEngine(nil, algoStub{})
	d, release, err = eNo.StartConcurrencyLease(context.Background(), model.RequestContext{})
	if err != nil || !d.Allowed || release == nil {
		t.Fatalf("expected allow when no policy matches")
	}
}

func TestHelperFunctions(t *testing.T) {
	if !isInRollout(model.Policy{PolicyID: "p", RolloutPercent: 100}, model.RequestContext{}) {
		t.Fatalf("100 percent rollout should include")
	}
	if isInRollout(model.Policy{PolicyID: "p", RolloutPercent: 0}, model.RequestContext{}) {
		t.Fatalf("0 percent rollout should exclude")
	}
	if allowDecision("x").Action != model.ActionAllow {
		t.Fatalf("allowDecision should allow")
	}
	if ensureMap(nil) == nil {
		t.Fatalf("ensureMap should allocate")
	}
	m := map[string]string{"k": "v"}
	if ensureMap(m)["k"] != "v" {
		t.Fatalf("ensureMap should keep existing map")
	}
	if maxInt64(1, 2) != 2 || maxInt64(3, 2) != 3 {
		t.Fatalf("maxInt64 failed")
	}

	seenTrue := false
	seenFalse := false
	for i := 0; i < 200; i++ {
		r := model.RequestContext{RequestID: "rid-" + string(rune(i))}
		if isInRollout(model.Policy{PolicyID: "p-mid", RolloutPercent: 50}, r) {
			seenTrue = true
		} else {
			seenFalse = true
		}
		if seenTrue && seenFalse {
			break
		}
	}
	if !seenTrue || !seenFalse {
		t.Fatalf("expected rollout to produce both true and false for 50 percent")
	}
}

func TestStartConcurrencyLeaseNonConcurrencyPolicy(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionAllow}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	d, release, err := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	if err != nil || !d.Allowed || release == nil {
		t.Fatalf("expected evaluate fallback for non-concurrency policy")
	}
}

func TestHandleFailureModes(t *testing.T) {
	e := newTestEngine(nil, algoStub{})
	d1, _ := e.handleFailure(model.Policy{PolicyID: "p", FailureMode: model.FailClosed}, "k", "x")
	if d1.Allowed {
		t.Fatalf("fail closed should deny")
	}
	d2, _ := e.handleFailure(model.Policy{PolicyID: "p", FailureMode: model.FailOpen}, "k", "x")
	if !d2.Allowed {
		t.Fatalf("fail open should allow")
	}
}

func TestEvaluateNoAlgorithmConfigured(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: "unknown_algo"}, Action: model.ActionDeny, FailureMode: model.FailOpen}
	e := newTestEngine([]model.Policy{p}, algoStub{})
	d, err := e.Evaluate(context.Background(), model.RequestContext{})
	if err != nil || !d.Allowed {
		t.Fatalf("expected fail-open allow when algorithm is missing")
	}
}

func TestStartConcurrencyLeaseBackendErrorUsesFailureMode(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}, Action: model.ActionDeny, FailureMode: model.FailClosed}
	e := newTestEngine([]model.Policy{p}, algoStub{})
	e.CounterStore = counterStoreStub{leaseErr: errors.New("backend down")}
	d, _, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	if d.Allowed {
		t.Fatalf("expected fail-closed deny on lease backend error")
	}
}
