package postgres

import (
	"context"
	"errors"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// Store is a placeholder for postgres policy persistence.
type Store struct {
	DSN string
}

// New creates a postgres policy store scaffold.
func New(dsn string) *Store {
	return &Store{DSN: dsn}
}

func (s *Store) LoadPolicies(_ context.Context, _ string) ([]model.Policy, error) {
	return nil, errors.New("postgres policy store scaffold: implement SQL persistence")
}

func (s *Store) GetPolicyByID(_ context.Context, _ string) (*model.Policy, error) {
	return nil, errors.New("postgres policy store scaffold: implement SQL persistence")
}

func (s *Store) UpsertPolicy(_ context.Context, _ model.Policy) error {
	return errors.New("postgres policy store scaffold: implement SQL persistence")
}

func (s *Store) DeletePolicy(_ context.Context, _ string) error {
	return errors.New("postgres policy store scaffold: implement SQL persistence")
}

func (s *Store) ListPolicies(_ context.Context, _ map[string]string) ([]model.Policy, error) {
	return nil, errors.New("postgres policy store scaffold: implement SQL persistence")
}
