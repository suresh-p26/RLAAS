package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	filestore "github.com/rlaas-io/rlaas/internal/store/policy/file"
	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/rlaas"
)

func newEvalForControlplane(t *testing.T) *rlaas.Client {
	t.Helper()
	ps := filestore.New("../../../../examples/policies.json")
	return rlaas.New(rlaas.Options{PolicyStore: ps, CounterStore: memory.New(), KeyPrefix: "rlaas"})
}

func TestAcquireReleaseHandlers(t *testing.T) {
	eval := newEvalForControlplane(t)
	acquire, release := NewAcquireReleaseHandlers(eval)

	payload, _ := json.Marshal(model.RequestContext{SignalType: "job", Operation: "op1"})
	rr := httptest.NewRecorder()
	acquire.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/acquire", bytes.NewBuffer(payload)))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from acquire")
	}
	var ar map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &ar)
	leaseID, _ := ar["lease_id"].(string)

	rrBad := httptest.NewRecorder()
	release.ServeHTTP(rrBad, httptest.NewRequest(http.MethodPost, "/v1/release", bytes.NewBufferString(`{"lease_id":""}`)))
	if rrBad.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for empty lease")
	}

	rrNotFound := httptest.NewRecorder()
	release.ServeHTTP(rrNotFound, httptest.NewRequest(http.MethodPost, "/v1/release", bytes.NewBufferString(`{"lease_id":"unknown"}`)))
	if rrNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected not found for unknown lease")
	}

	if leaseID != "" {
		rr2 := httptest.NewRecorder()
		releasePayload, _ := json.Marshal(map[string]string{"lease_id": leaseID})
		release.ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/v1/release", bytes.NewBuffer(releasePayload)))
		if rr2.Code != http.StatusOK {
			t.Fatalf("expected 200 from release")
		}
	}
}

type policyStoreErr struct{}

func (policyStoreErr) LoadPolicies(context.Context, string) ([]model.Policy, error) {
	return nil, context.Canceled
}
func (policyStoreErr) GetPolicyByID(context.Context, string) (*model.Policy, error) {
	return nil, context.Canceled
}
func (policyStoreErr) UpsertPolicy(context.Context, model.Policy) error { return context.Canceled }
func (policyStoreErr) DeletePolicy(context.Context, string) error       { return context.Canceled }
func (policyStoreErr) ListPolicies(context.Context, map[string]string) ([]model.Policy, error) {
	return nil, context.Canceled
}
func (policyStoreErr) Ping(context.Context) error { return context.Canceled }
func (policyStoreErr) Close() error               { return nil }

func TestPoliciesHandlerCRUD(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	rrGet := httptest.NewRecorder()
	h.ServeHTTP(rrGet, httptest.NewRequest(http.MethodGet, "/v1/policies", nil))
	if rrGet.Code != http.StatusOK {
		t.Fatalf("expected 200 from list")
	}

	rrPostBad := httptest.NewRecorder()
	h.ServeHTTP(rrPostBad, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBufferString("{")))
	if rrPostBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad post")
	}

	p := model.Policy{PolicyID: "p1", Enabled: true}
	body, _ := json.Marshal(p)
	rrPost := httptest.NewRecorder()
	h.ServeHTTP(rrPost, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rrPost.Code != http.StatusCreated {
		t.Fatalf("expected 201 from create")
	}

	rrPut := httptest.NewRecorder()
	h.ServeHTTP(rrPut, httptest.NewRequest(http.MethodPut, "/v1/policies/p1", bytes.NewBuffer(body)))
	if rrPut.Code != http.StatusOK {
		t.Fatalf("expected 200 from put")
	}

	rrDelete := httptest.NewRecorder()
	h.ServeHTTP(rrDelete, httptest.NewRequest(http.MethodDelete, "/v1/policies/p1", nil))
	if rrDelete.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from delete")
	}

	rrNotFound := httptest.NewRecorder()
	h.ServeHTTP(rrNotFound, httptest.NewRequest(http.MethodPatch, "/v1/policies", nil))
	if rrNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unsupported route")
	}
}

func TestPoliciesHandlerStoreErrors(t *testing.T) {
	h := PoliciesHandler(policyStoreErr{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/policies", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from list error")
	}

	body, _ := json.Marshal(model.Policy{PolicyID: "p1"})
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rr2.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from create error")
	}

	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodDelete, "/v1/policies/p1", nil))
	if rr3.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from delete error")
	}
}

func TestPoliciesHandlerValidateAndRollback(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	invalidBody := bytes.NewBufferString(`{"name":"x","action":"deny"}`)
	rrInvalid := httptest.NewRecorder()
	h.ServeHTTP(rrInvalid, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", invalidBody))
	if rrInvalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid policy validate")
	}

	valid := model.Policy{PolicyID: "p1", Name: "safe", Enabled: true, Action: model.ActionDeny, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow, Limit: 10}}
	validBody, _ := json.Marshal(valid)
	rrValid := httptest.NewRecorder()
	h.ServeHTTP(rrValid, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewBuffer(validBody)))
	if rrValid.Code != http.StatusOK || !bytes.Contains(rrValid.Body.Bytes(), []byte(`"valid":true`)) {
		t.Fatalf("expected validate success")
	}

	rrCreate := httptest.NewRecorder()
	h.ServeHTTP(rrCreate, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(validBody)))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("expected create success")
	}

	updated := valid
	updated.Name = "changed"
	updatedBody, _ := json.Marshal(updated)
	rrUpdate := httptest.NewRecorder()
	h.ServeHTTP(rrUpdate, httptest.NewRequest(http.MethodPut, "/v1/policies/p1", bytes.NewBuffer(updatedBody)))
	if rrUpdate.Code != http.StatusOK {
		t.Fatalf("expected update success")
	}

	rrRollback := httptest.NewRecorder()
	h.ServeHTTP(rrRollback, httptest.NewRequest(http.MethodPost, "/v1/policies/p1/rollback", bytes.NewBufferString(`{"version":1}`)))
	if rrRollback.Code != http.StatusOK {
		t.Fatalf("expected rollback success, got %d", rrRollback.Code)
	}
	var restored model.Policy
	_ = json.Unmarshal(rrRollback.Body.Bytes(), &restored)
	if restored.Name != "safe" {
		t.Fatalf("expected restored version name, got %s", restored.Name)
	}
}

func TestSelectVersionForRollback(t *testing.T) {
	versions := []model.PolicyVersion{{Version: 1}, {Version: 2}, {Version: 3}}
	if v, ok := selectVersionForRollback(versions, 2); !ok || v.Version != 2 {
		t.Fatalf("expected specific version rollback")
	}
	if v, ok := selectVersionForRollback(versions, 0); !ok || v.Version != 2 {
		t.Fatalf("expected previous version rollback")
	}
	if _, ok := selectVersionForRollback([]model.PolicyVersion{{Version: 1}}, 0); ok {
		t.Fatalf("expected no rollback target for single version")
	}
}

// --- Additional coverage tests ---

func TestPoliciesHandlerGetByID(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	p := model.Policy{PolicyID: "p1", Name: "test", Enabled: true}
	body, _ := json.Marshal(p)
	rrPost := httptest.NewRecorder()
	h.ServeHTTP(rrPost, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rrPost.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rrPost.Code)
	}

	rrGet := httptest.NewRecorder()
	h.ServeHTTP(rrGet, httptest.NewRequest(http.MethodGet, "/v1/policies/p1", nil))
	if rrGet.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrGet.Code)
	}
	var got model.Policy
	_ = json.Unmarshal(rrGet.Body.Bytes(), &got)
	if got.PolicyID != "p1" {
		t.Fatalf("expected p1, got %s", got.PolicyID)
	}

	// Missing ID
	rrEmpty := httptest.NewRecorder()
	h.ServeHTTP(rrEmpty, httptest.NewRequest(http.MethodGet, "/v1/policies/", nil))
	if rrEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty id, got %d", rrEmpty.Code)
	}

	// Not found
	rrMissing := httptest.NewRecorder()
	h.ServeHTTP(rrMissing, httptest.NewRequest(http.MethodGet, "/v1/policies/unknown", nil))
	if rrMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rrMissing.Code)
	}
}

func TestPoliciesHandlerAuditAndVersions(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	p := model.Policy{PolicyID: "p1", Name: "v1", Enabled: true}
	body, _ := json.Marshal(p)
	rrCreate := httptest.NewRecorder()
	h.ServeHTTP(rrCreate, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rrCreate.Code)
	}

	p.Name = "v2"
	body2, _ := json.Marshal(p)
	rrUpdate := httptest.NewRecorder()
	h.ServeHTTP(rrUpdate, httptest.NewRequest(http.MethodPut, "/v1/policies/p1", bytes.NewBuffer(body2)))
	if rrUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrUpdate.Code)
	}

	// Audit
	rrAudit := httptest.NewRecorder()
	h.ServeHTTP(rrAudit, httptest.NewRequest(http.MethodGet, "/v1/policies/p1/audit", nil))
	if rrAudit.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrAudit.Code)
	}
	var audits []model.PolicyAuditEntry
	_ = json.Unmarshal(rrAudit.Body.Bytes(), &audits)
	if len(audits) < 2 {
		t.Fatalf("expected >=2 audit entries, got %d", len(audits))
	}

	// Versions
	rrVer := httptest.NewRecorder()
	h.ServeHTTP(rrVer, httptest.NewRequest(http.MethodGet, "/v1/policies/p1/versions", nil))
	if rrVer.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrVer.Code)
	}
	var versions []model.PolicyVersion
	_ = json.Unmarshal(rrVer.Body.Bytes(), &versions)
	if len(versions) < 2 {
		t.Fatalf("expected >=2 versions, got %d", len(versions))
	}

	// Empty id for audit
	rrAuditEmpty := httptest.NewRecorder()
	h.ServeHTTP(rrAuditEmpty, httptest.NewRequest(http.MethodGet, "/v1/policies//audit", nil))
	if rrAuditEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty audit id, got %d", rrAuditEmpty.Code)
	}

	// Empty id for versions
	rrVerEmpty := httptest.NewRecorder()
	h.ServeHTTP(rrVerEmpty, httptest.NewRequest(http.MethodGet, "/v1/policies//versions", nil))
	if rrVerEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty versions id, got %d", rrVerEmpty.Code)
	}
}

func TestPoliciesHandlerRollout(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	p := model.Policy{PolicyID: "p1", Name: "test", Enabled: true, RolloutPercent: 0}
	body, _ := json.Marshal(p)
	rrCreate := httptest.NewRecorder()
	h.ServeHTTP(rrCreate, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rrCreate.Code)
	}

	// Valid rollout
	rrRollout := httptest.NewRecorder()
	h.ServeHTTP(rrRollout, httptest.NewRequest(http.MethodPost, "/v1/policies/p1/rollout", bytes.NewBufferString(`{"rollout_percent":50}`)))
	if rrRollout.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rrRollout.Code)
	}
	var updated model.Policy
	_ = json.Unmarshal(rrRollout.Body.Bytes(), &updated)
	if updated.RolloutPercent != 50 {
		t.Fatalf("expected 50 rollout, got %d", updated.RolloutPercent)
	}

	// Invalid percent
	rrBad := httptest.NewRecorder()
	h.ServeHTTP(rrBad, httptest.NewRequest(http.MethodPost, "/v1/policies/p1/rollout", bytes.NewBufferString(`{"rollout_percent":200}`)))
	if rrBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rrBad.Code)
	}

	// Missing id
	rrEmpty := httptest.NewRecorder()
	h.ServeHTTP(rrEmpty, httptest.NewRequest(http.MethodPost, "/v1/policies//rollout", bytes.NewBufferString(`{"rollout_percent":10}`)))
	if rrEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty id, got %d", rrEmpty.Code)
	}

	// Not found
	rrNotFound := httptest.NewRecorder()
	h.ServeHTTP(rrNotFound, httptest.NewRequest(http.MethodPost, "/v1/policies/unknown/rollout", bytes.NewBufferString(`{"rollout_percent":10}`)))
	if rrNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rrNotFound.Code)
	}

	// Bad JSON
	rrBadJSON := httptest.NewRecorder()
	h.ServeHTTP(rrBadJSON, httptest.NewRequest(http.MethodPost, "/v1/policies/p1/rollout", bytes.NewBufferString("{")))
	if rrBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rrBadJSON.Code)
	}
}

func TestPoliciesHandlerWithHooksFiresEvents(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")

	var publishedTopics []string
	var recordedEvents []string
	publish := func(_ context.Context, topic string, _ map[string]string) error {
		publishedTopics = append(publishedTopics, topic)
		return nil
	}
	record := func(_ context.Context, event string, _ map[string]string) {
		recordedEvents = append(recordedEvents, event)
	}
	h := PoliciesHandlerWithHooks(store, publish, record)

	// Create
	p := model.Policy{PolicyID: "p1", Name: "test", Enabled: true, Action: model.ActionDeny, Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow, Limit: 10}}
	body, _ := json.Marshal(p)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	// List (triggers analytics)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/v1/policies", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr2.Code)
	}

	// Update
	p.Name = "updated"
	body2, _ := json.Marshal(p)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodPut, "/v1/policies/p1", bytes.NewBuffer(body2)))
	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr3.Code)
	}

	// Delete
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, httptest.NewRequest(http.MethodDelete, "/v1/policies/p1", nil))
	if rr4.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr4.Code)
	}

	// Validate
	rr5 := httptest.NewRecorder()
	h.ServeHTTP(rr5, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewBuffer(body)))
	if rr5.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr5.Code)
	}

	if len(publishedTopics) < 3 {
		t.Fatalf("expected >=3 publish events, got %d", len(publishedTopics))
	}
	if len(recordedEvents) < 4 {
		t.Fatalf("expected >=4 analytics events, got %d", len(recordedEvents))
	}
}

func TestPoliciesHandlerStoreErrorsPut(t *testing.T) {
	h := PoliciesHandler(policyStoreErr{})
	body, _ := json.Marshal(model.Policy{PolicyID: "p1"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/v1/policies/p1", bytes.NewBuffer(body)))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from put error, got %d", rr.Code)
	}
}

func TestPoliciesHandlerStoreErrorsGetByID(t *testing.T) {
	h := PoliciesHandler(policyStoreErr{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/policies/p1", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 from get error, got %d", rr.Code)
	}
}

func TestPoliciesHandlerAutoGeneratesPolicyID(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	p := model.Policy{Name: "no-id", Enabled: true}
	body, _ := json.Marshal(p)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	var created model.Policy
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.PolicyID == "" {
		t.Fatal("expected auto-generated policy id")
	}
}

func TestPoliciesHandlerDeleteMissingID(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/v1/policies/", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty delete id, got %d", rr.Code)
	}
}

func TestPoliciesHandlerPutMissingID(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)
	body, _ := json.Marshal(model.Policy{Name: "x"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/v1/policies/", bytes.NewBuffer(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPoliciesHandlerPutBadJSON(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/v1/policies/p1", bytes.NewBufferString("{")))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPoliciesHandlerValidateMissingFields(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	// missing algorithm type
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewBufferString(`{"name":"x","action":"deny","algorithm":{"limit":10}}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	// negative limit
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewBufferString(`{"name":"x","action":"deny","algorithm":{"type":"fixed_window","limit":-1}}`)))
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative limit, got %d", rr2.Code)
	}

	// bad rollout percent
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewBufferString(`{"name":"x","action":"deny","algorithm":{"type":"fixed_window","limit":10},"rollout_percent":200}`)))
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad rollout, got %d", rr3.Code)
	}

	// bad JSON
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, httptest.NewRequest(http.MethodPost, "/v1/policies/validate", bytes.NewBufferString("{")))
	if rr4.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr4.Code)
	}
}

func TestPoliciesHandlerRollbackErrors(t *testing.T) {
	store := filestore.New(t.TempDir() + "/policies.json")
	h := PoliciesHandler(store)

	// Empty id
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/policies//rollback", bytes.NewBufferString(`{"version":1}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	// Bad JSON
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/v1/policies/p1/rollback", bytes.NewBufferString("{")))
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr2.Code)
	}

	// Missing policy (no versions)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodPost, "/v1/policies/unknown/rollback", bytes.NewBufferString(`{"version":1}`)))
	if rr3.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr3.Code)
	}

	// Create a policy then try rollback with wrong version
	p := model.Policy{PolicyID: "p1", Name: "v1", Enabled: true}
	body, _ := json.Marshal(p)
	rrCreate := httptest.NewRecorder()
	h.ServeHTTP(rrCreate, httptest.NewRequest(http.MethodPost, "/v1/policies", bytes.NewBuffer(body)))

	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, httptest.NewRequest(http.MethodPost, "/v1/policies/p1/rollback", bytes.NewBufferString(`{"version":999}`)))
	if rr4.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent version, got %d", rr4.Code)
	}
}

func TestMiddlewareDropActions(t *testing.T) {
	for _, action := range []model.ActionType{model.ActionDrop, model.ActionDropLowPriority} {
		mw := NewMiddleware(evalStub{decision: model.Decision{Action: action}})
		h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 for %s, got %d", action, rr.Code)
		}
	}
}

func TestAcquireHandlerStandalone(t *testing.T) {
	eval := newEvalForControlplane(t)
	h := AcquireHandler(eval)
	payload, _ := json.Marshal(model.RequestContext{SignalType: "job", Operation: "op1"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/acquire", bytes.NewBuffer(payload)))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// Bad JSON
	rrBad := httptest.NewRecorder()
	h.ServeHTTP(rrBad, httptest.NewRequest(http.MethodPost, "/v1/acquire", bytes.NewBufferString("{")))
	if rrBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rrBad.Code)
	}
}

func TestReleaseHandlerStandalone(t *testing.T) {
	eval := newEvalForControlplane(t)
	h := ReleaseHandler(eval)

	// Bad JSON
	rrBad := httptest.NewRecorder()
	h.ServeHTTP(rrBad, httptest.NewRequest(http.MethodPost, "/v1/release", bytes.NewBufferString("{")))
	if rrBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rrBad.Code)
	}

	// Unknown lease
	rrNotFound := httptest.NewRecorder()
	h.ServeHTTP(rrNotFound, httptest.NewRequest(http.MethodPost, "/v1/release", bytes.NewBufferString(`{"lease_id":"unknown"}`)))
	if rrNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rrNotFound.Code)
	}
}
