package key

import (
	"github.com/suresh-p26/RLAAS/pkg/model"
	"testing"
)

func TestBuildDeterministic(t *testing.T) {
	b := New("rlaas")
	p := model.Policy{Scope: model.PolicyScope{OrgID: "acme", TenantID: "retail", Service: "payments", Endpoint: "/v1/charge", Method: "POST", Tags: map[string]string{"env": "prod"}}}
	r := model.RequestContext{OrgID: "acme", TenantID: "retail", Service: "payments", Endpoint: "/v1/charge", Method: "POST", Tags: map[string]string{"env": "prod", "x": "y"}}
	k1, err := b.Build(p, r)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := b.Build(p, r)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Fatalf("expected stable key, got %s and %s", k1, k2)
	}
}

func TestBuilderDefaultsAndSanitize(t *testing.T) {
	b := New("")
	if b.Prefix != "rlaas" {
		t.Fatalf("expected default prefix")
	}
	if sanitize("") != "_" {
		t.Fatalf("expected empty sanitize fallback")
	}
	if sanitize("a:b c") != "a_b_c" {
		t.Fatalf("unexpected sanitize output")
	}
}
