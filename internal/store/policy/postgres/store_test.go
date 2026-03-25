package postgres

import (
	"context"
	"testing"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestPostgresPolicyStoreScaffoldErrors(t *testing.T) {
	s := New("dsn")
	if _, err := s.LoadPolicies(context.Background(), "x"); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := s.GetPolicyByID(context.Background(), "p"); err == nil {
		t.Fatalf("expected error")
	}
	if err := s.UpsertPolicy(context.Background(), model.Policy{}); err == nil {
		t.Fatalf("expected error")
	}
	if err := s.DeletePolicy(context.Background(), "p"); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := s.ListPolicies(context.Background(), nil); err == nil {
		t.Fatalf("expected error")
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatalf("expected ping scaffold error")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("expected close to succeed: %v", err)
	}
}
