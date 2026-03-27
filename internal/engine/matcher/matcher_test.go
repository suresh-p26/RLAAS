package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestSelectWinnerSpecificity(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", TenantID: "retail", Service: "payments", UserID: "u1", SignalType: "http"}
	policies := []model.Policy{
		{PolicyID: "org", Enabled: true, Priority: 10, Scope: model.PolicyScope{OrgID: "acme", SignalType: "http"}},
		{PolicyID: "user", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme", UserID: "u1", SignalType: "http"}},
	}
	matched, err := m.Match(req, policies)
	require.NoError(t, err, "match failed")
	winner, err := m.SelectWinner(req, matched)
	require.NoError(t, err, "select failed")
	assert.Equal(t, "user", winner.PolicyID)
}

func TestSelectWinnerErrorAndTieBreak(t *testing.T) {
	m := New()
	_, err := m.SelectWinner(model.RequestContext{}, nil)
	require.Error(t, err, "expected error when no policies")

	policies := []model.Policy{
		{PolicyID: "a", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme"}},
		{PolicyID: "b", Enabled: true, Priority: 1, Scope: model.PolicyScope{OrgID: "acme"}},
	}
	w, err := m.SelectWinner(model.RequestContext{}, policies)
	require.NoError(t, err)
	assert.Equal(t, "b", w.PolicyID, "expected deterministic policy id tie-break")
}

func TestMatchTagMismatch(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", Tags: map[string]string{"env": "dev"}}
	policies := []model.Policy{{PolicyID: "p", Scope: model.PolicyScope{OrgID: "acme", Tags: map[string]string{"env": "prod"}}}}
	matched, err := m.Match(req, policies)
	require.NoError(t, err)
	assert.Empty(t, matched, "expected no match due to tag mismatch")
}

func TestMatchExpression(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", SignalType: "http", Operation: "charge", Method: "POST", Tags: map[string]string{"tier": "gold"}}
	policies := []model.Policy{
		{PolicyID: "p1", Scope: model.PolicyScope{OrgID: "acme"}, Metadata: map[string]string{"match_expr": "method==POST&&tag.tier==gold"}},
		{PolicyID: "p2", Scope: model.PolicyScope{OrgID: "acme"}, Metadata: map[string]string{"match_expr": "method==GET"}},
	}
	matched, err := m.Match(req, policies)
	require.NoError(t, err)
	require.Len(t, matched, 1)
	assert.Equal(t, "p1", matched[0].PolicyID, "expected p1 to match expression")
}

func TestEvaluateExpression(t *testing.T) {
	req := model.RequestContext{OrgID: "acme", Method: "POST", Tags: map[string]string{"env": "prod"}}
	tests := []struct {
		name    string
		expr    string
		want    bool
		wantErr bool
	}{
		{"all conditions match", "org_id==acme&&method!=GET&&tag.env==prod", true, false},
		{"method condition fails", "method==GET", false, false},
		{"invalid clause returns error", "bad-clause", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateExpression(req, tt.expr)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

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
	require.NoError(t, err)
	assert.Len(t, matched, 1, "expected full scope match")

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
		badPolicies := []model.Policy{{PolicyID: "bad-" + field, Enabled: true, Scope: badScope}}
		badMatched, _ := m.Match(req, badPolicies)
		assert.Empty(t, badMatched, "expected mismatch on %s", field)
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
	require.NoError(t, err, "select failed")
	assert.Equal(t, "specific", winner.PolicyID, "expected more specific policy to win")
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
		assert.Equal(t, tc.expect, got, "resolveExprField(%q)", tc.field)
	}
}

func TestEvaluateExpressionQuotedValues(t *testing.T) {
	req := model.RequestContext{OrgID: "acme", Method: "POST"}
	ok, err := evaluateExpression(req, `org_id=="acme"&&method=='POST'`)
	require.NoError(t, err)
	assert.True(t, ok, "expected quoted expression to pass")
}

func TestMatchExpressionEmptyExpr(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme"}
	policies := []model.Policy{
		{PolicyID: "p1", Enabled: true, Scope: model.PolicyScope{OrgID: "acme"}, Metadata: map[string]string{"match_expr": "  "}},
	}
	matched, _ := m.Match(req, policies)
	assert.Len(t, matched, 1, "empty match_expr should still match")
}

func TestMatchDisabledPoliciesSkipped(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme"}
	policies := []model.Policy{
		{PolicyID: "disabled", Enabled: false, Scope: model.PolicyScope{OrgID: "acme"}},
	}
	// Disabled policies still match on scope; the engine enforces Enabled check.
	matched, _ := m.Match(req, policies)
	assert.Len(t, matched, 1, "matcher should not filter on Enabled")
}

func TestMatchWildcardScope(t *testing.T) {
	m := New()
	req := model.RequestContext{OrgID: "acme", Service: "api"}
	policies := []model.Policy{{PolicyID: "wildcard", Enabled: true, Scope: model.PolicyScope{}}}
	matched, _ := m.Match(req, policies)
	assert.Len(t, matched, 1, "empty scope should match any request")
}
