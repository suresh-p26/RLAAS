package evaluator

import (
	"context"
	"fmt"
	"github.com/suresh-p26/RLAAS/internal/algorithm"
	"github.com/suresh-p26/RLAAS/internal/engine/matcher"
	"github.com/suresh-p26/RLAAS/internal/key"
	"github.com/suresh-p26/RLAAS/internal/store"
	cache "github.com/suresh-p26/RLAAS/internal/store/cache"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"hash/fnv"
	"time"
)

// Engine is the main decision contract used by SDK and service layers.
type Engine interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
	StartConcurrencyLease(ctx context.Context, req model.RequestContext) (model.Decision, func() error, error)
}

// DefaultEngine wires policy store, matcher, key builder, and algorithms.
type DefaultEngine struct {
	PolicyStore  store.PolicyStore
	CounterStore store.CounterStore
	Matcher      matcher.Matcher
	KeyBuilder   key.Builder
	PolicyCache  cache.PolicyCache
	Algorithms   map[model.AlgorithmType]algorithm.Evaluator
	Now          func() time.Time
}

// Evaluate resolves policy, runs algorithm, and returns the final decision.
func (e *DefaultEngine) Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error) {
	policy, keyStr, err := e.pickPolicy(ctx, req)
	if err != nil {
		return allowDecision("policy resolution failed: " + err.Error()), nil
	}
	if policy == nil {
		return allowDecision("no policy matched"), nil
	}
	alg := e.Algorithms[policy.Algorithm.Type]
	if alg == nil {
		return e.handleFailure(*policy, keyStr, "algorithm not configured")
	}
	decision, err := alg.Evaluate(ctx, *policy, req, keyStr)
	if err != nil {
		return e.handleFailure(*policy, keyStr, err.Error())
	}
	decision.MatchedPolicyID = policy.PolicyID
	decision.LimitKey = keyStr
	if policy.EnforcementMode == model.ShadowMode {
		decision.Metadata = ensureMap(decision.Metadata)
		decision.Metadata["shadow_would_action"] = string(decision.Action)
		decision.ShadowMode = true
		decision.Allowed = true
		decision.Action = model.ActionShadowOnly
		decision.Reason = "shadow_mode"
	}
	return decision, nil
}

// StartConcurrencyLease is a helper path for acquire and release workflows.
func (e *DefaultEngine) StartConcurrencyLease(ctx context.Context, req model.RequestContext) (model.Decision, func() error, error) {
	policy, keyStr, err := e.pickPolicy(ctx, req)
	if err != nil {
		d := allowDecision("policy resolution failed")
		return d, func() error { return nil }, nil
	}
	if policy == nil {
		d := allowDecision("no policy matched")
		return d, func() error { return nil }, nil
	}
	if policy.Algorithm.Type != model.AlgoConcurrency {
		decision, err := e.Evaluate(ctx, req)
		return decision, func() error { return nil }, err
	}
	limit := policy.Algorithm.MaxConcurrency
	if limit <= 0 {
		limit = policy.Algorithm.Limit
	}
	if limit <= 0 {
		limit = 1
	}
	ok, current, err := e.CounterStore.AcquireLease(ctx, keyStr, limit, 2*time.Minute)
	if err != nil {
		d, _ := e.handleFailure(*policy, keyStr, err.Error())
		return d, func() error { return nil }, nil
	}
	if !ok {
		retry := 100 * time.Millisecond
		d := model.Decision{Allowed: false, Action: policy.Action, Reason: "concurrency_limit_exceeded", MatchedPolicyID: policy.PolicyID, LimitKey: keyStr, Remaining: maxInt64(limit-current, 0), RetryAfter: retry}
		if policy.EnforcementMode == model.ShadowMode {
			d.ShadowMode = true
			d.Allowed = true
			d.Action = model.ActionShadowOnly
			d.Reason = "shadow_mode"
		}
		return d, func() error { return nil }, nil
	}
	release := func() error {
		return e.CounterStore.ReleaseLease(context.Background(), keyStr)
	}
	d := model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "concurrency_lease_acquired", MatchedPolicyID: policy.PolicyID, LimitKey: keyStr, Remaining: maxInt64(limit-current, 0)}
	if policy.EnforcementMode == model.ShadowMode {
		d.ShadowMode = true
		d.Action = model.ActionShadowOnly
	}
	return d, release, nil
}

// pickPolicy loads policies, filters them, and returns the selected winner.
func (e *DefaultEngine) pickPolicy(ctx context.Context, req model.RequestContext) (*model.Policy, string, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	namespace := req.TenantID
	if namespace == "" {
		namespace = req.OrgID
	}
	policies, ok := e.PolicyCache.Get(namespace)
	if !ok {
		loaded, err := e.PolicyStore.LoadPolicies(ctx, namespace)
		if err != nil {
			return nil, "", err
		}
		policies = loaded
		e.PolicyCache.Set(namespace, policies)
	}
	candidates := make([]model.Policy, 0, len(policies))
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if p.ValidFromUnix > 0 && now.Unix() < p.ValidFromUnix {
			continue
		}
		if p.ValidToUnix > 0 && now.Unix() > p.ValidToUnix {
			continue
		}
		if p.RolloutPercent >= 0 && p.RolloutPercent < 100 && !isInRollout(p, req) {
			continue
		}
		candidates = append(candidates, p)
	}
	matched, err := e.Matcher.Match(req, candidates)
	if err != nil {
		return nil, "", err
	}
	if len(matched) == 0 {
		return nil, "", nil
	}
	winner, err := e.Matcher.SelectWinner(req, matched)
	if err != nil {
		return nil, "", err
	}
	keyStr, err := e.KeyBuilder.Build(*winner, req)
	if err != nil {
		return nil, "", err
	}
	return winner, keyStr, nil
}

// handleFailure applies fail open or fail closed behavior.
func (e *DefaultEngine) handleFailure(policy model.Policy, keyStr, reason string) (model.Decision, error) {
	if policy.FailureMode == model.FailClosed {
		return model.Decision{Allowed: false, Action: model.ActionDeny, Reason: "fail_closed: " + reason, MatchedPolicyID: policy.PolicyID, LimitKey: keyStr}, nil
	}
	return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "fail_open: " + reason, MatchedPolicyID: policy.PolicyID, LimitKey: keyStr}, nil
}

// isInRollout checks if the request falls into the rollout percentage.
func isInRollout(policy model.Policy, req model.RequestContext) bool {
	if policy.RolloutPercent <= 0 {
		return false
	}
	if policy.RolloutPercent >= 100 {
		return true
	}
	seed := req.RequestID
	if seed == "" {
		seed = fmt.Sprintf("%s:%s:%s", req.OrgID, req.TenantID, req.UserID)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(policy.PolicyID + ":" + seed))
	bucket := int(h.Sum32() % 100)
	return bucket < policy.RolloutPercent
}

// allowDecision is used for safe pass through scenarios.
func allowDecision(reason string) model.Decision {
	return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: reason}
}

// ensureMap avoids nil map writes when adding metadata.
func ensureMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return in
}

// maxInt64 returns the larger value.
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
