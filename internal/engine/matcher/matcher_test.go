package matcher

import (
	"github.com/rlaas-io/rlaas/pkg/model"
	"testing"
)

func TestSelectWinnerSpecificity(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", TenantID: "retail", Service: "payments", UserID: "u1", SignalType: "http"}
	policies := []model.Policy{
		{PolicyID: "org", Enabled: true, Priority: 10, Scope: model.PolicyScope{OrgID: "acme", SignalType: "http"}},
		{PolicyID: "user", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme", UserID: "u1", SignalType: "http"}},
	}
	matched, err := m.Match(req, policies)
	if err != nil {
		t.Fatalf("match failed: %v", err)
	}
	winner, err := m.SelectWinner(req, matched)
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if winner.PolicyID != "user" {
		t.Fatalf("expected user policy, got %s", winner.PolicyID)
	}
}

func TestSelectWinnerErrorAndTieBreak(t *testing.T) {
	m := New()
	if _, err := m.SelectWinner(model.RequestContext{}, nil); err == nil {
		t.Fatalf("expected error when no policies")
	}
	policies := []model.Policy{
		{PolicyID: "a", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme"}},
		{PolicyID: "b", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme"}},
	}
	w, err := m.SelectWinner(model.RequestContext{}, policies)
	if err != nil || w.PolicyID != "b" {
		t.Fatalf("expected deterministic policy id tie-break")
	}
}

func TestMatchTagMismatch(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", Tags: map[string]string{"env": "dev"}}
	policies := []model.Policy{{PolicyID: "p", Scope: model.PolicyScope{OrgID: "acme", Tags: map[string]string{"env": "prod"}}}}
	matched, err := m.Match(req, policies)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 0 {
		t.Fatalf("expected no match due to tag mismatch")
	}
}

func TestMatchExpression(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", SignalType: "http", Operation: "charge", Method: "POST", Tags: map[string]string{"tier": "gold"}}
	policies := []model.Policy{
		{PolicyID: "p1", Scope: model.PolicyScope{OrgID: "acme"}, Metadata: map[string]string{"match_expr": "method==POST&&tag.tier==gold"}},
		{PolicyID: "p2", Scope: model.PolicyScope{OrgID: "acme"}, Metadata: map[string]string{"match_expr": "method==GET"}},
	}
	matched, err := m.Match(req, policies)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].PolicyID != "p1" {
		t.Fatalf("expected p1 to match expression")
	}
}

func TestEvaluateExpression(t *testing.T) {
	req := model.RequestContext{OrgID: "acme", Method: "POST", Tags: map[string]string{"env": "prod"}}
	ok, err := evaluateExpression(req, "org_id==acme&&method!=GET&&tag.env==prod")
	if err != nil || !ok {
		t.Fatalf("expected expression to pass")
	}
	ok, err = evaluateExpression(req, "method==GET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected expression to fail")
	}
	if _, err = evaluateExpression(req, "bad-clause"); err == nil {
		t.Fatalf("expected invalid clause error")
	}
}

// --- Additional coverage tests ---

func TestMatchesScopeAllFields(t *testing.T) {
	m := New()
	req := model.RequestContext{
		OrgID: "acme", TenantID: "retail", Application: "payments",
		Service: "api", Environment: "prod", SignalType: "http",
		Operation: "charge", Endpoint: "/charge", Method: "POST",
		UserID: "u1", APIKey: "key1", ClientID: "c1", SourceIP: "1.2.3.4",
		Region: "us-east-1", Resource: "order", Severity: "info",
		SpanName: "handler", Topic: "orders", ConsumerGroup: "cg1", JobType: "batch",
	}
	scope := model.PolicyScope{
		OrgID: "acme", TenantID: "retail", Application: "payments",
		Service: "api", Environment: "prod", SignalType: "http",
		Operation: "charge", Endpoint: "/charge", Method: "POST",
		UserID: "u1", APIKey: "key1", ClientID: "c1", SourceIP: "1.2.3.4",
		Region: "us-east-1", Resource: "order", Severity: "info",
		SpanName: "handler", Topic: "orders", ConsumerGroup: "cg1", JobType: "batch",
	}
	policies := []model.Policy{{PolicyID: "full", Enabled: true, Scope: scope}}
	matched, err := m.Match(req, policies)
	if err != nil || len(matched) != 1 {
		t.Fatalf("expected full scope match, got %d matches", len(matched))
	}

	// Mismatch on a single field should reject
	for _, field := range []string{"environment", "region", "severity", "span_name", "topic", "consumer_group", "job_type"} {
		badScope := scope
		switch field {
		case "environment":
			badScope.Environment = "staging"
		case "region":
			badScope.Region = "eu-west-1"
		case "severity":
			badScope.Severity = "error"
		case "span_name":
			badScope.SpanName = "other"
		case "topic":
			badScope.Topic = "events"
		case "consumer_group":
			badScope.ConsumerGroup = "cg2"
		case "job_type":
			badScope.JobType = "realtime"
		}
		policies := []model.Policy{{PolicyID: "bad-" + field, Enabled: true, Scope: badScope}}
		matched, _ := m.Match(req, policies)
		if len(matched) != 0 {
			t.Fatalf("expected mismatch on %s", field)
		}
	}
}

func TestSpecificityScoring(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", Service: "api", Environment: "prod", Region: "us-east-1", SignalType: "http"}
	policies := []model.Policy{
		{PolicyID: "broad", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme", SignalType: "http"}},
		{PolicyID: "specific", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme", Service: "api", Environment: "prod", Region: "us-east-1", SignalType: "http"}},
	}
	matched, _ := m.Match(req, policies)
	winner, err := m.SelectWinner(req, matched)
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if winner.PolicyID != "specific" {
		t.Fatalf("expected more specific policy to win, got %s", winner.PolicyID)
	}
}

func TestResolveExprFieldAllBranches(t *testing.T) {
	req := model.RequestContext{
		OrgID: "o", TenantID: "t", Application: "a", Service: "s",
		Environment: "e", SignalType: "st", Operation: "op", Endpoint: "ep",
		Method: "m", UserID: "u", APIKey: "ak", ClientID: "c",
		SourceIP: "ip", Region: "r", Resource: "res", Severity: "sev",
		SpanName: "sp", Topic: "top", ConsumerGroup: "cg", JobType: "jt",
		Tags: map[string]string{"env": "prod"},
	}
	tests := []struct {
		field, expect string
	}{
		{"org_id", "o"}, {"tenant_id", "t"}, {"application", "a"}, {"service", "s"},
		{"environment", "e"}, {"signal_type", "st"}, {"operation", "op"}, {"endpoint", "ep"},
		{"method", "m"}, {"user_id", "u"}, {"api_key", "ak"}, {"client_id", "c"},
		{"source_ip", "ip"}, {"region", "r"}, {"resource", "res"}, {"severity", "sev"},
		{"span_name", "sp"}, {"topic", "top"}, {"consumer_group", "cg"}, {"job_type", "jt"},
		{"tag.env", "prod"}, {"tag.missing", ""}, {"unknown_field", ""},
	}
	for _, tc := range tests {
		got := resolveExprField(req, tc.field)
		if got != tc.expect {
			t.Fatalf("resolveExprField(%q) = %q, want %q", tc.field, got, tc.expect)
		}
	}
}

func TestEvaluateExpressionQuotedValues(t *testing.T) {
	req := model.RequestContext{OrgID: "acme", Method: "POST"}
	ok, err := evaluateExpression(req, `org_id=="acme"&&method=='POST'`)
	if err != nil || !ok {
		t.Fatalf("expected quoted expression to pass")
	}
}

func TestMatchExpressionEmptyExpr(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme"}
	policies := []model.Policy{
		{PolicyID: "p1", Enabled: true, Scope: model.PolicyScope{OrgID: "acme"}, Metadata: map[string]string{"match_expr": "  "}},
	}
	matched, _ := m.Match(req, policies)
	if len(matched) != 1 {
		t.Fatal("empty match_expr should still match")
	}
}

func TestMatchDisabledPoliciesSkipped(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme"}
	policies := []model.Policy{
		{PolicyID: "disabled", Enabled: false, Scope: model.PolicyScope{OrgID: "acme"}},
	}
	// Disabled policies still match on scope; the engine enforces Enabled check.
	// Matcher only checks scope, so this should still match.
	matched, _ := m.Match(req, policies)
	if len(matched) != 1 {
		t.Fatalf("matched %d, matcher should not filter on Enabled", len(matched))
	}
}

func TestMatchWildcardScope(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", Service: "api"}
	// Empty scope = wildcard
	policies := []model.Policy{{PolicyID: "wildcard", Enabled: true, Scope: model.PolicyScope{}}}
	matched, _ := m.Match(req, policies)
	if len(matched) != 1 {
		t.Fatal("empty scope should match any request")
	}
}
