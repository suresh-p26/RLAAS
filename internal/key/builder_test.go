package key

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestBuildDeterministic(t *testing.T) {
	b := New("rlaas")
	p := model.Policy{Scope: model.PolicyScope{OrgID: "acme", TenantID: "retail", Service: "payments", Endpoint: "/v1/charge", Method: "POST", Tags: map[string]string{"env": "prod"}}}
	r := model.RequestContext{OrgID: "acme", TenantID: "retail", Service: "payments", Endpoint: "/v1/charge", Method: "POST", Tags: map[string]string{"env": "prod", "x": "y"}}

	k1, err := b.Build(p, r)
	require.NoError(t, err)
	k2, err := b.Build(p, r)
	require.NoError(t, err)
	assert.Equal(t, k1, k2, "key should be stable across calls")
}

func TestBuilderDefaultsAndSanitize(t *testing.T) {
	b := New("")
	assert.Equal(t, "rlaas", b.Prefix, "empty prefix should default to rlaas")

	sanitizeTests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty input falls back to underscore", "", "_"},
		{"special chars replaced with underscore", "a:b c", "a_b_c"},
	}
	for _, tt := range sanitizeTests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitize(tt.input))
		})
	}
}
