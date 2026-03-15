package postgres

import (
	"context"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"testing"
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
}
