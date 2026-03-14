package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"rlaas/internal/model"
	"rlaas/internal/store/counter/memory"
	filestore "rlaas/internal/store/policy/file"
	"rlaas/pkg/rlaas"
	"testing"
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
