package evaluator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/internal/algorithm"
	"github.com/rlaas-io/rlaas/internal/engine/matcher"
	"github.com/rlaas-io/rlaas/internal/key"
	"github.com/rlaas-io/rlaas/internal/store"
	cache "github.com/rlaas-io/rlaas/internal/store/cache"
	"github.com/rlaas-io/rlaas/pkg/model"
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
func (policyStoreStub) Ping(context.Context) error { return nil }
func (policyStoreStub) Close() error               { return nil }

type counterStoreStub struct {
	store.CounterStore
	leaseOK  bool
	leaseCur int64
	leaseErr error
	leaseKey string
	leaseTTL time.Duration
	relKey   string
}

func (c *counterStoreStub) AcquireLease(_ context.Context, key string, _ int64, ttl time.Duration) (bool, int64, error) {
	c.leaseKey = key
	c.leaseTTL = ttl
	return c.leaseOK, c.leaseCur, c.leaseErr
}
func (c *counterStoreStub) ReleaseLease(_ context.Context, key string) error {
	c.relKey = key
	return nil
}

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
		CounterStore: &counterStoreStub{leaseOK: true, leaseCur: 1},
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
	require.NoError(t, err)
	assert.True(t, d.Allowed, "expected allow with no policy")

	e.PolicyStore = policyStoreStub{err: errors.New("boom")}
	_, _ = e.Evaluate(context.Background(), model.RequestContext{})
}

func TestEvaluateAlgorithmAndShadowAndFailure(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionDeny, FailureMode: model.FailClosed}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}})
	d, err := e.Evaluate(context.Background(), model.RequestContext{})
	require.NoError(t, err)
	assert.Equal(t, "p1", d.MatchedPolicyID, "expected match")

	p.EnforcementMode = model.ShadowMode
	e = newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}})
	d, _ = e.Evaluate(context.Background(), model.RequestContext{})
	assert.True(t, d.Allowed, "expected shadow allow")
	assert.Equal(t, model.ActionShadowOnly, d.Action)

	e = newTestEngine([]model.Policy{p}, algoStub{err: errors.New("x")})
	d, _ = e.Evaluate(context.Background(), model.RequestContext{})
	assert.False(t, d.Allowed, "expected fail closed deny")
}

func TestStartConcurrencyLeasePaths(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}, Action: model.ActionDeny}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	e.CounterStore = &counterStoreStub{leaseOK: false, leaseCur: 1}
	d, _, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	assert.False(t, d.Allowed, "expected denied lease")

	leaseStore := &counterStoreStub{leaseOK: true, leaseCur: 1}
	e.CounterStore = leaseStore
	d, release, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	assert.True(t, d.Allowed, "expected allowed lease")
	require.NotNil(t, release)
	_ = release()
	assert.NotEmpty(t, leaseStore.leaseKey)
	assert.Equal(t, leaseStore.leaseKey, leaseStore.relKey, "acquire/release should use same lease key")
	assert.Equal(t, ":concurrency", leaseStore.leaseKey[len(leaseStore.leaseKey)-12:], "expected concurrency suffix on lease key")
}

func TestStartConcurrencyLeasePolicyErrorAndNoPolicy(t *testing.T) {
	eErr := newTestEngine(nil, algoStub{})
	eErr.PolicyStore = policyStoreStub{err: errors.New("load fail")}
	d, release, err := eErr.StartConcurrencyLease(context.Background(), model.RequestContext{})
	require.NoError(t, err)
	assert.True(t, d.Allowed, "expected allow on policy resolution failure")
	require.NotNil(t, release)

	eNo := newTestEngine(nil, algoStub{})
	d, release, err = eNo.StartConcurrencyLease(context.Background(), model.RequestContext{})
	require.NoError(t, err)
	assert.True(t, d.Allowed, "expected allow when no policy matches")
	require.NotNil(t, release)
}

func TestHelperFunctions(t *testing.T) {
	assert.True(t, isInRollout(model.Policy{PolicyID: "p", RolloutPercent: 100}, model.RequestContext{}), "100% rollout should include")
	assert.False(t, isInRollout(model.Policy{PolicyID: "p", RolloutPercent: 0}, model.RequestContext{}), "0% rollout should exclude")
	assert.Equal(t, model.ActionAllow, allowDecision("x").Action, "allowDecision should allow")
	require.NotNil(t, ensureMap(nil), "ensureMap should allocate")
	m := map[string]string{"k": "v"}
	assert.Equal(t, "v", ensureMap(m)["k"], "ensureMap should keep existing map")
	assert.Equal(t, int64(2), maxInt64(1, 2))
	assert.Equal(t, int64(3), maxInt64(3, 2))

	seenTrue, seenFalse := false, false
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
	assert.True(t, seenTrue && seenFalse, "expected rollout to produce both true and false for 50%")
}

func TestStartConcurrencyLeaseNonConcurrencyPolicy(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionAllow}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	d, release, err := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	require.NoError(t, err)
	assert.True(t, d.Allowed, "expected evaluate fallback for non-concurrency policy")
	require.NotNil(t, release)
}

func TestHandleFailureModes(t *testing.T) {
	tests := []struct {
		name        string
		failureMode model.FailureMode
		wantAllowed bool
	}{
		{"fail closed denies", model.FailClosed, false},
		{"fail open allows", model.FailOpen, true},
	}
	e := newTestEngine(nil, algoStub{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := e.handleFailure(model.Policy{PolicyID: "p", FailureMode: tt.failureMode}, "k", "x")
			assert.Equal(t, tt.wantAllowed, d.Allowed)
		})
	}
}

func TestEvaluateNoAlgorithmConfigured(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: "unknown_algo"}, Action: model.ActionDeny, FailureMode: model.FailOpen}
	e := newTestEngine([]model.Policy{p}, algoStub{})
	d, err := e.Evaluate(context.Background(), model.RequestContext{})
	require.NoError(t, err)
	assert.True(t, d.Allowed, "expected fail-open allow when algorithm is missing")
}

func TestStartConcurrencyLeaseBackendErrorUsesFailureMode(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1}, Action: model.ActionDeny, FailureMode: model.FailClosed}
	e := newTestEngine([]model.Policy{p}, algoStub{})
	e.CounterStore = &counterStoreStub{leaseErr: errors.New("backend down")}
	d, _, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	assert.False(t, d.Allowed, "expected fail-closed deny on lease backend error")
}

func TestPickPolicy_DefaultZeroRolloutDoesNotDisablePolicy(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow}, Action: model.ActionDeny}
	e := newTestEngine([]model.Policy{p}, algoStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	d, err := e.Evaluate(context.Background(), model.RequestContext{})
	require.NoError(t, err)
	assert.Equal(t, "p1", d.MatchedPolicyID, "expected policy with zero rollout_percent to be treated as default-enabled")
}

func TestStartConcurrencyLease_UsesPolicyLeaseTTL(t *testing.T) {
	p := model.Policy{PolicyID: "p1", Enabled: true, RolloutPercent: 100, Scope: model.PolicyScope{}, Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: 1, LeaseTTL: 300}, Action: model.ActionDeny}
	e := newTestEngine([]model.Policy{p}, algoStub{})
	leaseStore := &counterStoreStub{leaseOK: true, leaseCur: 1}
	e.CounterStore = leaseStore
	_, release, _ := e.StartConcurrencyLease(context.Background(), model.RequestContext{})
	_ = release()
	assert.Equal(t, 300*time.Second, leaseStore.leaseTTL, "expected custom lease TTL")
}
