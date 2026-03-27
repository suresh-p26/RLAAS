package oracle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestOraclePolicyStoreScaffoldErrors(t *testing.T) {
	s := New("dsn")
	ctx := context.Background()

	tests := []struct {
		name    string
		op      func() error
		wantErr bool
	}{
		{"LoadPolicies", func() error { _, err := s.LoadPolicies(ctx, "x"); return err }, true},
		{"GetPolicyByID", func() error { _, err := s.GetPolicyByID(ctx, "p"); return err }, true},
		{"UpsertPolicy", func() error { return s.UpsertPolicy(ctx, model.Policy{}) }, true},
		{"DeletePolicy", func() error { return s.DeletePolicy(ctx, "p") }, true},
		{"ListPolicies", func() error { _, err := s.ListPolicies(ctx, nil); return err }, true},
		{"Ping", func() error { return s.Ping(ctx) }, true},
		{"Close", func() error { return s.Close() }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
