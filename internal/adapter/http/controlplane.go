package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"rlaas/internal/model"
	"rlaas/internal/store"
	"strings"
	"sync"
	"time"
)

// LeaseEvaluator extends evaluate with acquire and release workflow support.
type LeaseEvaluator interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
	StartConcurrencyLease(ctx context.Context, req model.RequestContext) (model.Decision, func() error, error)
}

type policyHistoryStore interface {
	ListPolicyAudit(ctx context.Context, policyID string) ([]model.PolicyAuditEntry, error)
	ListPolicyVersions(ctx context.Context, policyID string) ([]model.PolicyVersion, error)
}

type invalidationNotifier interface {
	Publish(ctx context.Context, topic string, event map[string]string) error
}

type analyticsRecorder interface {
	Record(ctx context.Context, event string, tags map[string]string)
}

type policiesHandlerConfig struct {
	notifier  invalidationNotifier
	analytics analyticsRecorder
}

type acquireResponse struct {
	Allowed bool             `json:"allowed"`
	LeaseID string           `json:"lease_id,omitempty"`
	Reason  string           `json:"reason"`
	Action  model.ActionType `json:"action"`
}

type releaseRequest struct {
	LeaseID string `json:"lease_id"`
}

type releaseResponse struct {
	Released bool   `json:"released"`
	Reason   string `json:"reason,omitempty"`
}

type leaseRegistry struct {
	mu    sync.Mutex
	items map[string]func() error
}

func newLeaseRegistry() *leaseRegistry {
	return &leaseRegistry{items: map[string]func() error{}}
}

func (r *leaseRegistry) put(fn func() error) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := fmt.Sprintf("lease-%d", time.Now().UnixNano())
	r.items[id] = fn
	return id
}

func (r *leaseRegistry) pop(id string) (func() error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn, ok := r.items[id]
	if ok {
		delete(r.items, id)
	}
	return fn, ok
}

// AcquireHandler acquires a concurrency lease and returns a lease id.
func AcquireHandler(eval LeaseEvaluator) http.Handler {
	leases := newLeaseRegistry()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req model.RequestContext
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		decision, release, err := eval.StartConcurrencyLease(r.Context(), req)
		if err != nil {
			http.Error(w, "rate limiter error", http.StatusInternalServerError)
			return
		}
		resp := acquireResponse{Allowed: decision.Allowed, Reason: decision.Reason, Action: decision.Action}
		if decision.Allowed && release != nil {
			resp.LeaseID = leases.put(release)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// ReleaseHandler releases a previously acquired lease id.
func ReleaseHandler(eval LeaseEvaluator) http.Handler {
	leases := newLeaseRegistry()
	_ = eval
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req releaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LeaseID == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		release, ok := leases.pop(req.LeaseID)
		if !ok {
			http.Error(w, "lease not found", http.StatusNotFound)
			return
		}
		if err := release(); err != nil {
			http.Error(w, "release failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releaseResponse{Released: true})
	})
}

// NewAcquireReleaseHandlers returns paired handlers that share the same lease registry.
func NewAcquireReleaseHandlers(eval LeaseEvaluator) (http.Handler, http.Handler) {
	leases := newLeaseRegistry()
	acquire := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req model.RequestContext
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		decision, release, err := eval.StartConcurrencyLease(r.Context(), req)
		if err != nil {
			http.Error(w, "rate limiter error", http.StatusInternalServerError)
			return
		}
		resp := acquireResponse{Allowed: decision.Allowed, Reason: decision.Reason, Action: decision.Action}
		if decision.Allowed && release != nil {
			resp.LeaseID = leases.put(release)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	release := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req releaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LeaseID == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		releaseFn, ok := leases.pop(req.LeaseID)
		if !ok {
			http.Error(w, "lease not found", http.StatusNotFound)
			return
		}
		if err := releaseFn(); err != nil {
			http.Error(w, "release failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releaseResponse{Released: true})
	})
	return acquire, release
}

// PoliciesHandler exposes minimal phase 2 CRUD operations for policies.
func PoliciesHandler(ps store.PolicyStore) http.Handler {
	return PoliciesHandlerWithConfig(ps, policiesHandlerConfig{})
}

// PoliciesHandlerWithHooks wires optional phase 3 invalidation and analytics hooks.
func PoliciesHandlerWithHooks(ps store.PolicyStore, publish func(context.Context, string, map[string]string) error, record func(context.Context, string, map[string]string)) http.Handler {
	cfg := policiesHandlerConfig{}
	if publish != nil {
		cfg.notifier = invalidationNotifierFunc(publish)
	}
	if record != nil {
		cfg.analytics = analyticsRecorderFunc(record)
	}
	return PoliciesHandlerWithConfig(ps, cfg)
}

type invalidationNotifierFunc func(context.Context, string, map[string]string) error

func (f invalidationNotifierFunc) Publish(ctx context.Context, topic string, event map[string]string) error {
	return f(ctx, topic, event)
}

type analyticsRecorderFunc func(context.Context, string, map[string]string)

func (f analyticsRecorderFunc) Record(ctx context.Context, event string, tags map[string]string) {
	f(ctx, event, tags)
}

// PoliciesHandlerWithConfig enables phase 3 hooks while keeping default behavior unchanged.
func PoliciesHandlerWithConfig(ps store.PolicyStore, cfg policiesHandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/v1/policies":
			list, err := ps.ListPolicies(r.Context(), map[string]string{})
			if err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_list", map[string]string{"status": "ok"})
			}
			return
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/policies/") && strings.HasSuffix(path, "/audit"):
			historyStore, ok := ps.(policyHistoryStore)
			if !ok {
				http.Error(w, "policy history not supported", http.StatusNotImplemented)
				return
			}
			policyID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/policies/"), "/audit")
			if policyID == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			entries, err := historyStore.ListPolicyAudit(r.Context(), policyID)
			if err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entries)
			return
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/policies/") && strings.HasSuffix(path, "/versions"):
			historyStore, ok := ps.(policyHistoryStore)
			if !ok {
				http.Error(w, "policy versions not supported", http.StatusNotImplemented)
				return
			}
			policyID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/policies/"), "/versions")
			if policyID == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			versions, err := historyStore.ListPolicyVersions(r.Context(), policyID)
			if err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(versions)
			return
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/policies/"):
			id := strings.TrimPrefix(path, "/v1/policies/")
			if id == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			p, err := ps.GetPolicyByID(r.Context(), id)
			if err != nil {
				http.Error(w, "policy not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(p)
			return
		case r.Method == http.MethodPost && path == "/v1/policies":
			var p model.Policy
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if p.PolicyID == "" {
				p.PolicyID = fmt.Sprintf("policy-%d", time.Now().UnixNano())
			}
			if err := ps.UpsertPolicy(r.Context(), p); err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			if cfg.notifier != nil {
				_ = cfg.notifier.Publish(r.Context(), "policy.changed", map[string]string{"policy_id": p.PolicyID, "action": "create"})
			}
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_create", map[string]string{"policy_id": p.PolicyID})
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(p)
			return
		case r.Method == http.MethodPost && path == "/v1/policies/validate":
			var p model.Policy
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if err := validatePolicy(p); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_validate", map[string]string{"status": "ok"})
			}
			return
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/policies/") && strings.HasSuffix(path, "/rollback"):
			historyStore, ok := ps.(policyHistoryStore)
			if !ok {
				http.Error(w, "policy rollback not supported", http.StatusNotImplemented)
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/policies/"), "/rollback")
			if id == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			var req struct {
				Version int64 `json:"version"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			versions, err := historyStore.ListPolicyVersions(r.Context(), id)
			if err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			if len(versions) == 0 {
				http.Error(w, "policy version not found", http.StatusNotFound)
				return
			}
			selected, ok := selectVersionForRollback(versions, req.Version)
			if !ok {
				http.Error(w, "policy version not found", http.StatusNotFound)
				return
			}
			restore := selected.Snapshot
			restore.PolicyID = id
			if err := ps.UpsertPolicy(r.Context(), restore); err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			if cfg.notifier != nil {
				_ = cfg.notifier.Publish(r.Context(), "policy.changed", map[string]string{"policy_id": id, "action": "rollback"})
			}
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_rollback", map[string]string{"policy_id": id, "version": fmt.Sprintf("%d", selected.Version)})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(restore)
			return
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/policies/") && strings.HasSuffix(path, "/rollout"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/policies/"), "/rollout")
			if id == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			var req struct {
				RolloutPercent int `json:"rollout_percent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if req.RolloutPercent < 0 || req.RolloutPercent > 100 {
				http.Error(w, "rollout_percent must be 0..100", http.StatusBadRequest)
				return
			}
			p, err := ps.GetPolicyByID(r.Context(), id)
			if err != nil {
				http.Error(w, "policy not found", http.StatusNotFound)
				return
			}
			p.RolloutPercent = req.RolloutPercent
			if err := ps.UpsertPolicy(r.Context(), *p); err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			if cfg.notifier != nil {
				_ = cfg.notifier.Publish(r.Context(), "policy.changed", map[string]string{"policy_id": p.PolicyID, "action": "rollout_update"})
			}
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_rollout_update", map[string]string{"policy_id": p.PolicyID})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(p)
			return
		case r.Method == http.MethodPut && strings.HasPrefix(path, "/v1/policies/"):
			id := strings.TrimPrefix(path, "/v1/policies/")
			if id == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			var p model.Policy
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			p.PolicyID = id
			if err := ps.UpsertPolicy(r.Context(), p); err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			if cfg.notifier != nil {
				_ = cfg.notifier.Publish(r.Context(), "policy.changed", map[string]string{"policy_id": p.PolicyID, "action": "update"})
			}
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_update", map[string]string{"policy_id": p.PolicyID})
			}
			_ = json.NewEncoder(w).Encode(p)
			return
		case r.Method == http.MethodDelete && strings.HasPrefix(path, "/v1/policies/"):
			id := strings.TrimPrefix(path, "/v1/policies/")
			if id == "" {
				http.Error(w, "missing policy id", http.StatusBadRequest)
				return
			}
			if err := ps.DeletePolicy(r.Context(), id); err != nil {
				http.Error(w, "policy store error", http.StatusInternalServerError)
				return
			}
			if cfg.notifier != nil {
				_ = cfg.notifier.Publish(r.Context(), "policy.changed", map[string]string{"policy_id": id, "action": "delete"})
			}
			if cfg.analytics != nil {
				cfg.analytics.Record(r.Context(), "policy_delete", map[string]string{"policy_id": id})
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			http.NotFound(w, r)
		}
	})
}

func validatePolicy(p model.Policy) error {
	if p.Name == "" {
		return fmt.Errorf("policy name is required")
	}
	if p.Algorithm.Type == "" {
		return fmt.Errorf("algorithm type is required")
	}
	if p.Algorithm.Limit < 0 {
		return fmt.Errorf("algorithm limit must be >= 0")
	}
	if p.Action == "" {
		return fmt.Errorf("action is required")
	}
	if p.RolloutPercent < 0 || p.RolloutPercent > 100 {
		return fmt.Errorf("rollout_percent must be 0..100")
	}
	return nil
}

func selectVersionForRollback(versions []model.PolicyVersion, requestedVersion int64) (model.PolicyVersion, bool) {
	if len(versions) == 0 {
		return model.PolicyVersion{}, false
	}
	if requestedVersion > 0 {
		for _, v := range versions {
			if v.Version == requestedVersion {
				return v, true
			}
		}
		return model.PolicyVersion{}, false
	}
	if len(versions) < 2 {
		return model.PolicyVersion{}, false
	}
	return versions[len(versions)-2], true
}
