package benchmarks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	httpadapter "github.com/suresh-p26/RLAAS/internal/adapter/http"
	"github.com/suresh-p26/RLAAS/internal/store/counter/memory"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"github.com/suresh-p26/RLAAS/pkg/rlaas"
)

type benchmarkPolicyStore struct {
	mu       sync.RWMutex
	policies map[string]model.Policy
	audits   map[string][]model.PolicyAuditEntry
	versions map[string][]model.PolicyVersion
}

func newBenchmarkPolicyStore(seed ...model.Policy) *benchmarkPolicyStore {
	ps := &benchmarkPolicyStore{
		policies: map[string]model.Policy{},
		audits:   map[string][]model.PolicyAuditEntry{},
		versions: map[string][]model.PolicyVersion{},
	}
	for _, p := range seed {
		_ = ps.UpsertPolicy(context.Background(), p)
	}
	return ps
}

func (s *benchmarkPolicyStore) LoadPolicies(_ context.Context, tenantOrOrg string) ([]model.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Policy, 0, len(s.policies))
	for _, p := range s.policies {
		if tenantOrOrg == "" || p.Scope.TenantID == tenantOrOrg || p.Scope.OrgID == tenantOrOrg || (p.Scope.TenantID == "" && p.Scope.OrgID == "") {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PolicyID < out[j].PolicyID })
	return out, nil
}

func (s *benchmarkPolicyStore) GetPolicyByID(_ context.Context, policyID string) (*model.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[policyID]
	if !ok {
		return nil, fmt.Errorf("policy not found")
	}
	cp := p
	return &cp, nil
}

func (s *benchmarkPolicyStore) UpsertPolicy(_ context.Context, p model.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, hadOld := s.policies[p.PolicyID]
	s.policies[p.PolicyID] = p
	version := int64(len(s.versions[p.PolicyID]) + 1)
	s.versions[p.PolicyID] = append(s.versions[p.PolicyID], model.PolicyVersion{
		PolicyID:      p.PolicyID,
		Version:       version,
		CreatedAtUnix: time.Now().Unix(),
		Snapshot:      p,
	})
	var oldRef *model.Policy
	if hadOld {
		oldCopy := old
		oldRef = &oldCopy
	}
	newCopy := p
	s.audits[p.PolicyID] = append(s.audits[p.PolicyID], model.PolicyAuditEntry{
		AuditID:       fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		PolicyID:      p.PolicyID,
		ActionType:    "upsert",
		ChangedAtUnix: time.Now().Unix(),
		OldValue:      oldRef,
		NewValue:      &newCopy,
	})
	return nil
}

func (s *benchmarkPolicyStore) DeletePolicy(_ context.Context, policyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, hadOld := s.policies[policyID]
	delete(s.policies, policyID)
	var oldRef *model.Policy
	if hadOld {
		oldCopy := old
		oldRef = &oldCopy
	}
	s.audits[policyID] = append(s.audits[policyID], model.PolicyAuditEntry{
		AuditID:       fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		PolicyID:      policyID,
		ActionType:    "delete",
		ChangedAtUnix: time.Now().Unix(),
		OldValue:      oldRef,
	})
	return nil
}

func (s *benchmarkPolicyStore) ListPolicies(_ context.Context, _ map[string]string) ([]model.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Policy, 0, len(s.policies))
	for _, p := range s.policies {
		out = append(out, p)
	}
	return out, nil
}

func (s *benchmarkPolicyStore) ListPolicyAudit(_ context.Context, policyID string) ([]model.PolicyAuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.audits[policyID]
	out := make([]model.PolicyAuditEntry, len(entries))
	copy(out, entries)
	return out, nil
}

func (s *benchmarkPolicyStore) ListPolicyVersions(_ context.Context, policyID string) ([]model.PolicyVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.versions[policyID]
	out := make([]model.PolicyVersion, len(versions))
	copy(out, versions)
	return out, nil
}

func benchmarkClient() *rlaas.Client {
	basePolicy := model.Policy{
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
	}
	return rlaas.New(rlaas.Options{
		PolicyStore:  newBenchmarkPolicyStore(basePolicy),
		CounterStore: memory.NewSharded(128),
		CacheTTL:     time.Minute,
		KeyPrefix:    "bench",
	})
}

func BenchmarkHTTPCheckHandler(b *testing.B) {
	h := httpadapter.CheckHandler(benchmarkClient())
	payload := []byte(`{"request_id":"r1","org_id":"acme","tenant_id":"retail","signal_type":"http","operation":"charge","endpoint":"/v1/charge","method":"POST"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewReader(payload))
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status: %d", rr.Code)
		}
	}
}

func BenchmarkHTTPAcquireReleaseHandlers(b *testing.B) {
	acquire, release := httpadapter.NewAcquireReleaseHandlers(benchmarkClient())
	acqPayload := []byte(`{"request_id":"a1","signal_type":"job","operation":"op1"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acqRR := httptest.NewRecorder()
		acqReq := httptest.NewRequest(http.MethodPost, "/v1/acquire", bytes.NewReader(acqPayload))
		acquire.ServeHTTP(acqRR, acqReq)
		if acqRR.Code != http.StatusOK {
			b.Fatalf("unexpected acquire status: %d", acqRR.Code)
		}
		var parsed map[string]any
		_ = json.Unmarshal(acqRR.Body.Bytes(), &parsed)
		leaseID, _ := parsed["lease_id"].(string)
		if leaseID == "" {
			b.Fatalf("missing lease id")
		}
		relPayload, _ := json.Marshal(map[string]string{"lease_id": leaseID})
		relRR := httptest.NewRecorder()
		relReq := httptest.NewRequest(http.MethodPost, "/v1/release", bytes.NewReader(relPayload))
		release.ServeHTTP(relRR, relReq)
		if relRR.Code != http.StatusOK {
			b.Fatalf("unexpected release status: %d", relRR.Code)
		}
	}
}

func BenchmarkHTTPPoliciesHandler(b *testing.B) {
	seed := model.Policy{
		PolicyID: "p1",
		Name:     "seed",
		Enabled:  true,
		Scope:    model.PolicyScope{SignalType: "http", Operation: "charge"},
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoFixedWindow,
			Limit:  100,
			Window: "1s",
		},
		Action:         model.ActionDeny,
		RolloutPercent: 100,
	}
	newPolicy := model.Policy{
		PolicyID: "p2",
		Name:     "new",
		Enabled:  true,
		Scope:    model.PolicyScope{SignalType: "http", Operation: "charge"},
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoFixedWindow,
			Limit:  100,
			Window: "1s",
		},
		Action:         model.ActionDeny,
		RolloutPercent: 100,
	}
	updatePolicy := newPolicy
	updatePolicy.Name = "updated"
	updateBody, _ := json.Marshal(updatePolicy)
	rolloutBody := []byte(`{"rollout_percent":55}`)
	rollbackBody := []byte(`{"version":1}`)
	validateBody, _ := json.Marshal(model.Policy{
		PolicyID: "p-validate",
		Name:     "validate",
		Enabled:  true,
		Scope:    model.PolicyScope{SignalType: "http", Operation: "charge"},
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoFixedWindow,
			Limit:  10,
			Window: "1s",
		},
		Action:         model.ActionDeny,
		RolloutPercent: 100,
	})

	b.Run("list", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed, newPolicy))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/policies", nil)
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("get", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed, newPolicy))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/policies/p1", nil)
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("create", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			candidate := newPolicy
			candidate.PolicyID = fmt.Sprintf("p-create-%d", i)
			payload, _ := json.Marshal(candidate)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewReader(payload))
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusCreated {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("update", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed, newPolicy))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/v1/policies/p2", bytes.NewReader(updateBody))
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("audit", func(b *testing.B) {
		store := newBenchmarkPolicyStore(seed, newPolicy)
		h := httpadapter.PoliciesHandler(store)
		for i := 0; i < 3; i++ {
			p := newPolicy
			p.Name = fmt.Sprintf("u-%d", i)
			_ = store.UpsertPolicy(context.Background(), p)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/policies/p2/audit", nil)
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("versions", func(b *testing.B) {
		store := newBenchmarkPolicyStore(seed, newPolicy)
		h := httpadapter.PoliciesHandler(store)
		for i := 0; i < 3; i++ {
			p := newPolicy
			p.Name = fmt.Sprintf("v-%d", i)
			_ = store.UpsertPolicy(context.Background(), p)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/policies/p2/versions", nil)
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("validate", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewReader(validateBody))
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("rollout", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed, newPolicy))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/policies/p2/rollout", bytes.NewReader(rolloutBody))
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("rollback", func(b *testing.B) {
		store := newBenchmarkPolicyStore(seed, newPolicy)
		_ = store.UpsertPolicy(context.Background(), updatePolicy)
		h := httpadapter.PoliciesHandler(store)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/policies/p2/rollback", bytes.NewReader(rollbackBody))
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
		}
	})
	b.Run("delete", func(b *testing.B) {
		h := httpadapter.PoliciesHandler(newBenchmarkPolicyStore(seed, newPolicy))
		createBody, _ := json.Marshal(newPolicy)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/v1/policies/p2", nil)
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent {
				b.Fatalf("unexpected status: %d", rr.Code)
			}
			createRR := httptest.NewRecorder()
			createReq := httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewReader(createBody))
			h.ServeHTTP(createRR, createReq)
		}
	})
}
