package file

import (
	"context"
	"fmt"
	"github.com/rlaas-io/rlaas/pkg/model"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)
	p := model.Policy{PolicyID: "p1", Scope: model.PolicyScope{OrgID: "acme"}, Enabled: true}
	if err := s.UpsertPolicy(context.Background(), p); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	all, err := s.LoadPolicies(context.Background(), "acme")
	if err != nil || len(all) != 1 {
		t.Fatalf("load failed")
	}
	one, err := s.GetPolicyByID(context.Background(), "p1")
	if err != nil || one.PolicyID != "p1" {
		t.Fatalf("get failed")
	}
	all2, err := s.ListPolicies(context.Background(), nil)
	if err != nil || len(all2) != 1 {
		t.Fatalf("list failed")
	}
	if err := s.DeletePolicy(context.Background(), "p1"); err != nil {
		t.Fatalf("delete failed")
	}
	if _, err := s.GetPolicyByID(context.Background(), "p1"); err == nil {
		t.Fatalf("expected missing policy")
	}
}

func TestFileStoreDirectArrayFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	_ = os.WriteFile(path, []byte(`[{"policy_id":"p2","enabled":true}]`), 0644)
	s := New(path)
	all, err := s.LoadPolicies(context.Background(), "")
	if err != nil || len(all) != 1 || all[0].PolicyID != "p2" {
		t.Fatalf("expected direct array fallback")
	}
}

func TestFileStoreEmptyAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	_ = os.WriteFile(path, []byte(""), 0644)
	s := New(path)
	if all, err := s.LoadPolicies(context.Background(), ""); err != nil || len(all) != 0 {
		t.Fatalf("expected empty policy list")
	}
	_ = os.WriteFile(path, []byte("not-json"), 0644)
	if _, err := s.LoadPolicies(context.Background(), ""); err == nil {
		t.Fatalf("expected invalid json error")
	}
}

func TestFileStoreUpsertUpdateExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)
	p := model.Policy{PolicyID: "p1", Name: "first", Enabled: true}
	_ = s.UpsertPolicy(context.Background(), p)
	p.Name = "second"
	_ = s.UpsertPolicy(context.Background(), p)
	one, err := s.GetPolicyByID(context.Background(), "p1")
	if err != nil || one.Name != "second" {
		t.Fatalf("expected update existing policy")
	}
}

// --- Additional coverage tests ---

func TestFileStoreListPolicyAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)
	p := model.Policy{PolicyID: "p1", Name: "v1", Enabled: true}
	_ = s.UpsertPolicy(context.Background(), p)
	p.Name = "v2"
	_ = s.UpsertPolicy(context.Background(), p)

	audits, err := s.ListPolicyAudit(context.Background(), "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(audits) != 2 {
		t.Fatalf("expected 2 audit entries, got %d", len(audits))
	}
	for _, a := range audits {
		if a.PolicyID != "p1" {
			t.Fatalf("expected audit for p1, got %s", a.PolicyID)
		}
	}

	// Non-existent policy returns empty
	audits2, err := s.ListPolicyAudit(context.Background(), "nope")
	if err != nil || len(audits2) != 0 {
		t.Fatalf("expected empty audit for non-existent policy")
	}
}

func TestFileStoreListPolicyVersions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)
	p := model.Policy{PolicyID: "p1", Name: "v1", Enabled: true}
	_ = s.UpsertPolicy(context.Background(), p)
	p.Name = "v2"
	_ = s.UpsertPolicy(context.Background(), p)
	p.Name = "v3"
	_ = s.UpsertPolicy(context.Background(), p)

	versions, err := s.ListPolicyVersions(context.Background(), "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	if versions[0].Version != 1 || versions[2].Version != 3 {
		t.Fatal("versions should be sequential")
	}
	if versions[2].Snapshot.Name != "v3" {
		t.Fatalf("expected v3 snapshot, got %s", versions[2].Snapshot.Name)
	}

	// Non-existent policy returns nil
	versions2, err := s.ListPolicyVersions(context.Background(), "nope")
	if err != nil || len(versions2) != 0 {
		t.Fatalf("expected empty versions for non-existent policy")
	}
}

func TestFileStoreLoadPoliciesWithTenantFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)

	_ = s.UpsertPolicy(context.Background(), model.Policy{PolicyID: "p1", Scope: model.PolicyScope{OrgID: "acme"}})
	_ = s.UpsertPolicy(context.Background(), model.Policy{PolicyID: "p2", Scope: model.PolicyScope{TenantID: "retail"}})
	_ = s.UpsertPolicy(context.Background(), model.Policy{PolicyID: "p3", Scope: model.PolicyScope{OrgID: "other"}})
	_ = s.UpsertPolicy(context.Background(), model.Policy{PolicyID: "p4", Scope: model.PolicyScope{}}) // no org/tenant

	// Filter by org
	acme, err := s.LoadPolicies(context.Background(), "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should include p1 (org=acme) and p4 (no org/tenant = wildcard)
	found := map[string]bool{}
	for _, p := range acme {
		found[p.PolicyID] = true
	}
	if !found["p1"] || !found["p4"] {
		t.Fatalf("expected p1 and p4, got %v", found)
	}
	if found["p3"] {
		t.Fatalf("p3 (org=other) should not appear for acme filter")
	}

	// Filter by tenant
	retail, _ := s.LoadPolicies(context.Background(), "retail")
	foundRetail := map[string]bool{}
	for _, p := range retail {
		foundRetail[p.PolicyID] = true
	}
	if !foundRetail["p2"] || !foundRetail["p4"] {
		t.Fatalf("expected p2 and p4 for retail, got %v", foundRetail)
	}

	// Empty filter returns all
	all, _ := s.LoadPolicies(context.Background(), "")
	if len(all) != 4 {
		t.Fatalf("expected 4 policies, got %d", len(all))
	}
}

func TestFileStoreDeleteAddsAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)

	_ = s.UpsertPolicy(context.Background(), model.Policy{PolicyID: "p1", Name: "test"})
	_ = s.DeletePolicy(context.Background(), "p1")

	audits, _ := s.ListPolicyAudit(context.Background(), "p1")
	if len(audits) != 2 {
		t.Fatalf("expected 2 audit entries (upsert + delete), got %d", len(audits))
	}
	lastAudit := audits[len(audits)-1]
	if lastAudit.ActionType != "delete" {
		t.Fatalf("expected delete audit, got %s", lastAudit.ActionType)
	}
}

func TestFileStoreNonExistentPath(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "nonexistent", "policies.json"))
	all, err := s.LoadPolicies(context.Background(), "")
	if err != nil || len(all) != 0 {
		t.Fatalf("expected empty for non-existent path")
	}
}

func TestFileStoreConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			p := model.Policy{PolicyID: fmt.Sprintf("p%d", id), Enabled: true}
			_ = s.UpsertPolicy(context.Background(), p)
			_, _ = s.ListPolicies(context.Background(), nil)
			_, _ = s.GetPolicyByID(context.Background(), p.PolicyID)
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	all, _ := s.ListPolicies(context.Background(), nil)
	if len(all) != 10 {
		t.Fatalf("expected 10, got %d", len(all))
	}
}
