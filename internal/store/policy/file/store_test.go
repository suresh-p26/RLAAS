package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestFileStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)
	p := model.Policy{PolicyID: "p1", Scope: model.PolicyScope{OrgID: "acme"}, Enabled: true}
	err := s.UpsertPolicy(context.Background(), p)
	require.NoError(t, err, "upsert failed")

	all, err := s.LoadPolicies(context.Background(), "acme")
	require.NoError(t, err)
	assert.Len(t, all, 1)

	one, err := s.GetPolicyByID(context.Background(), "p1")
	require.NoError(t, err)
	assert.Equal(t, "p1", one.PolicyID)

	all2, err := s.ListPolicies(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, all2, 1)

	err = s.DeletePolicy(context.Background(), "p1")
	require.NoError(t, err, "delete failed")

	_, err = s.GetPolicyByID(context.Background(), "p1")
	require.Error(t, err, "expected missing policy")
}

func TestFileStoreDirectArrayFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	_ = os.WriteFile(path, []byte(`[{"policy_id":"p2","enabled":true}]`), 0644)
	s := New(path)
	all, err := s.LoadPolicies(context.Background(), "")
	require.NoError(t, err, "expected direct array fallback")
	require.Len(t, all, 1)
	assert.Equal(t, "p2", all[0].PolicyID)
}

func TestFileStoreEmptyAndInvalidFiles(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantErr   bool
		wantEmpty bool
	}{
		{"empty file returns empty list", "", false, true},
		{"invalid JSON returns error", "not-json", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "policies.json")
			_ = os.WriteFile(path, []byte(tt.content), 0644)
			s := New(path)
			all, err := s.LoadPolicies(context.Background(), "")
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Empty(t, all)
			}
		})
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
	require.NoError(t, err)
	assert.Equal(t, "second", one.Name, "expected update existing policy")
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
	require.NoError(t, err)
	assert.Len(t, audits, 2, "expected 2 audit entries")
	for _, a := range audits {
		assert.Equal(t, "p1", a.PolicyID)
	}

	// Non-existent policy returns empty
	audits2, err := s.ListPolicyAudit(context.Background(), "nope")
	require.NoError(t, err)
	assert.Empty(t, audits2, "expected empty audit for non-existent policy")
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
	require.NoError(t, err)
	assert.Len(t, versions, 3, "expected 3 versions")
	assert.Equal(t, int64(1), versions[0].Version)
	assert.Equal(t, int64(3), versions[2].Version)
	assert.Equal(t, "v3", versions[2].Snapshot.Name)

	// Non-existent policy returns nil
	versions2, err := s.ListPolicyVersions(context.Background(), "nope")
	require.NoError(t, err)
	assert.Empty(t, versions2, "expected empty versions for non-existent policy")
}

func TestFileStoreLoadPoliciesWithTenantFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)
	ctx := context.Background()

	_ = s.UpsertPolicy(ctx, model.Policy{PolicyID: "p1", Scope: model.PolicyScope{OrgID: "acme"}})
	_ = s.UpsertPolicy(ctx, model.Policy{PolicyID: "p2", Scope: model.PolicyScope{TenantID: "retail"}})
	_ = s.UpsertPolicy(ctx, model.Policy{PolicyID: "p3", Scope: model.PolicyScope{OrgID: "other"}})
	_ = s.UpsertPolicy(ctx, model.Policy{PolicyID: "p4", Scope: model.PolicyScope{}}) // wildcard

	tests := []struct {
		name      string
		filter    string
		wantIDs   []string
		absentIDs []string
		wantLen   int
	}{
		{
			name:      "org filter includes match and wildcard",
			filter:    "acme",
			wantIDs:   []string{"p1", "p4"},
			absentIDs: []string{"p3"},
		},
		{
			name:    "tenant filter includes match and wildcard",
			filter:  "retail",
			wantIDs: []string{"p2", "p4"},
		},
		{
			name:    "empty filter returns all policies",
			filter:  "",
			wantLen: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policies, err := s.LoadPolicies(ctx, tt.filter)
			require.NoError(t, err)
			if tt.wantLen > 0 {
				assert.Len(t, policies, tt.wantLen)
			}
			found := map[string]bool{}
			for _, p := range policies {
				found[p.PolicyID] = true
			}
			for _, id := range tt.wantIDs {
				assert.True(t, found[id], "expected %s in results for filter %q", id, tt.filter)
			}
			for _, id := range tt.absentIDs {
				assert.False(t, found[id], "expected %s absent from results for filter %q", id, tt.filter)
			}
		})
	}
}

func TestFileStoreDeleteAddsAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	s := New(path)

	_ = s.UpsertPolicy(context.Background(), model.Policy{PolicyID: "p1", Name: "test"})
	_ = s.DeletePolicy(context.Background(), "p1")

	audits, _ := s.ListPolicyAudit(context.Background(), "p1")
	require.Len(t, audits, 2, "expected 2 audit entries (upsert + delete)")
	lastAudit := audits[len(audits)-1]
	assert.Equal(t, "delete", lastAudit.ActionType)
}

func TestFileStoreNonExistentPath(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "nonexistent", "policies.json"))
	all, err := s.LoadPolicies(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, all, "expected empty for non-existent path")
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
	assert.Len(t, all, 10, "expected 10 policies")
}
