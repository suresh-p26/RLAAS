package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

// payload is the JSON envelope persisted to disk, containing policies,
// audit entries, and version snapshots.
type payload struct {
	Policies []model.Policy                   `json:"policies"`
	Audits   []model.PolicyAuditEntry         `json:"audits,omitempty"`
	Versions map[string][]model.PolicyVersion `json:"versions,omitempty"`
}

// Store is a json file backed policy store for local development.
type Store struct {
	path string
	mu   sync.RWMutex
}

// New creates a file policy store for the given path.
func New(path string) *Store {
	return &Store{path: path}
}

// LoadPolicies returns policies for the tenant or org namespace.
func (s *Store) LoadPolicies(_ context.Context, tenantOrOrg string) ([]model.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pl, err := s.readPayload()
	if err != nil {
		return nil, err
	}
	all := pl.Policies
	if tenantOrOrg == "" {
		return all, nil
	}
	out := make([]model.Policy, 0)
	for _, p := range all {
		if p.Scope.TenantID == tenantOrOrg || p.Scope.OrgID == tenantOrOrg || (p.Scope.TenantID == "" && p.Scope.OrgID == "") {
			out = append(out, p)
		}
	}
	return out, nil
}

// GetPolicyByID returns one policy by id.
func (s *Store) GetPolicyByID(_ context.Context, policyID string) (*model.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pl, err := s.readPayload()
	if err != nil {
		return nil, err
	}
	all := pl.Policies
	for _, p := range all {
		if p.PolicyID == policyID {
			cp := p
			return &cp, nil
		}
	}
	return nil, errors.New("policy not found")
}

// UpsertPolicy inserts or updates one policy.
func (s *Store) UpsertPolicy(_ context.Context, p model.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pl, err := s.readPayload()
	if err != nil {
		return err
	}
	all := pl.Policies
	updated := false
	var oldPolicy *model.Policy
	for i := range all {
		if all[i].PolicyID == p.PolicyID {
			copyPolicy := all[i]
			oldPolicy = &copyPolicy
			all[i] = p
			updated = true
			break
		}
	}
	if !updated {
		all = append(all, p)
	}
	pl.Policies = all
	if pl.Versions == nil {
		pl.Versions = map[string][]model.PolicyVersion{}
	}
	version := int64(1)
	if vv := pl.Versions[p.PolicyID]; len(vv) > 0 {
		version = vv[len(vv)-1].Version + 1
	}
	pl.Versions[p.PolicyID] = append(pl.Versions[p.PolicyID], model.PolicyVersion{PolicyID: p.PolicyID, Version: version, CreatedAtUnix: time.Now().Unix(), Snapshot: p})
	pl.Audits = append(pl.Audits, model.PolicyAuditEntry{AuditID: fmt.Sprintf("audit-%d", time.Now().UnixNano()), PolicyID: p.PolicyID, ActionType: "upsert", ChangedAtUnix: time.Now().Unix(), OldValue: oldPolicy, NewValue: &p})
	return s.writePayload(pl)
}

// DeletePolicy removes one policy by id.
func (s *Store) DeletePolicy(_ context.Context, policyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pl, err := s.readPayload()
	if err != nil {
		return err
	}
	all := pl.Policies
	out := make([]model.Policy, 0, len(all))
	var oldPolicy *model.Policy
	for _, p := range all {
		if p.PolicyID != policyID {
			out = append(out, p)
		} else {
			cp := p
			oldPolicy = &cp
		}
	}
	pl.Policies = out
	pl.Audits = append(pl.Audits, model.PolicyAuditEntry{AuditID: fmt.Sprintf("audit-%d", time.Now().UnixNano()), PolicyID: policyID, ActionType: "delete", ChangedAtUnix: time.Now().Unix(), OldValue: oldPolicy, NewValue: nil})
	return s.writePayload(pl)
}

// ListPolicies returns all policies in this file store.
func (s *Store) ListPolicies(_ context.Context, _ map[string]string) ([]model.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pl, err := s.readPayload()
	if err != nil {
		return nil, err
	}
	return pl.Policies, nil
}

// ListPolicyAudit returns change history entries for one policy.
func (s *Store) ListPolicyAudit(_ context.Context, policyID string) ([]model.PolicyAuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pl, err := s.readPayload()
	if err != nil {
		return nil, err
	}
	out := make([]model.PolicyAuditEntry, 0)
	for _, a := range pl.Audits {
		if a.PolicyID == policyID {
			out = append(out, a)
		}
	}
	return out, nil
}

// ListPolicyVersions returns saved versions for one policy.
func (s *Store) ListPolicyVersions(_ context.Context, policyID string) ([]model.PolicyVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pl, err := s.readPayload()
	if err != nil {
		return nil, err
	}
	return pl.Versions[policyID], nil
}

// readPayload deserializes the file contents into a payload struct.
// Supports both the envelope format and a bare policy array for
// backwards compatibility.
func (s *Store) readPayload() (payload, error) {
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return payload{Policies: []model.Policy{}, Audits: []model.PolicyAuditEntry{}, Versions: map[string][]model.PolicyVersion{}}, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return payload{}, err
	}
	if len(raw) == 0 {
		return payload{Policies: []model.Policy{}, Audits: []model.PolicyAuditEntry{}, Versions: map[string][]model.PolicyVersion{}}, nil
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err == nil {
		if p.Versions == nil {
			p.Versions = map[string][]model.PolicyVersion{}
		}
		return p, nil
	}
	var direct []model.Policy
	if err := json.Unmarshal(raw, &direct); err != nil {
		return payload{}, err
	}
	return payload{Policies: direct, Audits: []model.PolicyAuditEntry{}, Versions: map[string][]model.PolicyVersion{}}, nil
}

// writePayload atomically persists the payload by writing to a temporary
// file and renaming, preventing corruption on crash.
func (s *Store) writePayload(in payload) error {
	if in.Versions == nil {
		in.Versions = map[string][]model.PolicyVersion{}
	}
	out, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write: write to temp file, then rename to prevent corruption
	// if the process crashes mid-write.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Ping verifies the policy file is readable.
func (s *Store) Ping(_ context.Context) error {
	_, err := os.Stat(s.path)
	return err
}

// Close is a no-op for file-backed stores.
func (s *Store) Close() error { return nil }
