package matcher

import (
	"errors"
	"fmt"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"sort"
	"strings"
)

// Matcher finds policies that apply to a request and picks the final winner.
type Matcher interface {
	Match(req model.RequestContext, policies []model.Policy) ([]model.Policy, error)
	SelectWinner(req model.RequestContext, policies []model.Policy) (*model.Policy, error)
}

// DefaultMatcher uses strict field matching and deterministic tie breaking.
type DefaultMatcher struct{}

// New creates the default policy matcher.
func New() *DefaultMatcher {
	return &DefaultMatcher{}
}

// Match returns all policies whose scope matches the request.
func (m *DefaultMatcher) Match(req model.RequestContext, policies []model.Policy) ([]model.Policy, error) {
	matched := make([]model.Policy, 0, len(policies))
	for _, policy := range policies {
		if matchesPolicy(req, policy) {
			matched = append(matched, policy)
		}
	}
	return matched, nil
}

func matchesPolicy(req model.RequestContext, p model.Policy) bool {
	if !matchesScope(req, p.Scope) {
		return false
	}
	expr := ""
	if p.Metadata != nil {
		expr = strings.TrimSpace(p.Metadata["match_expr"])
	}
	if expr == "" {
		return true
	}
	ok, err := evaluateExpression(req, expr)
	return err == nil && ok
}

// SelectWinner chooses one policy using specificity, priority, and policy id.
func (m *DefaultMatcher) SelectWinner(_ model.RequestContext, policies []model.Policy) (*model.Policy, error) {
	if len(policies) == 0 {
		return nil, errors.New("no matching policy")
	}
	sort.SliceStable(policies, func(i, j int) bool {
		si, sj := specificityScore(policies[i].Scope), specificityScore(policies[j].Scope)
		if si != sj {
			return si > sj
		}
		if policies[i].Priority != policies[j].Priority {
			return policies[i].Priority > policies[j].Priority
		}
		if policies[i].PolicyID != policies[j].PolicyID {
			return policies[i].PolicyID > policies[j].PolicyID
		}
		return false
	})
	winner := policies[0]
	return &winner, nil
}

// matchesScope checks every configured scope field and required tags.
func matchesScope(req model.RequestContext, s model.PolicyScope) bool {
	if !matchString(s.OrgID, req.OrgID) || !matchString(s.TenantID, req.TenantID) || !matchString(s.Application, req.Application) || !matchString(s.Service, req.Service) || !matchString(s.Environment, req.Environment) || !matchString(s.SignalType, req.SignalType) || !matchString(s.Operation, req.Operation) || !matchString(s.Endpoint, req.Endpoint) || !matchString(s.Method, req.Method) || !matchString(s.UserID, req.UserID) || !matchString(s.APIKey, req.APIKey) || !matchString(s.ClientID, req.ClientID) || !matchString(s.SourceIP, req.SourceIP) || !matchString(s.Region, req.Region) || !matchString(s.Resource, req.Resource) || !matchString(s.Severity, req.Severity) || !matchString(s.SpanName, req.SpanName) || !matchString(s.Topic, req.Topic) || !matchString(s.ConsumerGroup, req.ConsumerGroup) || !matchString(s.JobType, req.JobType) {
		return false
	}
	for k, v := range s.Tags {
		if req.Tags[k] != v {
			return false
		}
	}
	return true
}

// matchString treats empty scope values as wildcards.
func matchString(scope, val string) bool {
	return scope == "" || scope == val
}

// specificityScore ranks policies by how many concrete scope fields they set.
func specificityScore(s model.PolicyScope) int {
	score := 0
	weights := []string{s.UserID, s.APIKey, s.ClientID, s.Endpoint, s.Method, s.Operation, s.Service, s.Application, s.TenantID, s.OrgID, s.SignalType, s.Environment, s.SourceIP, s.Region, s.Resource, s.Severity, s.SpanName, s.Topic, s.ConsumerGroup, s.JobType}
	for _, field := range weights {
		if field != "" {
			score += 10
		}
	}
	score += len(s.Tags)
	return score
}

func evaluateExpression(req model.RequestContext, expr string) (bool, error) {
	clauses := strings.Split(expr, "&&")
	for _, raw := range clauses {
		clause := strings.TrimSpace(raw)
		if clause == "" {
			continue
		}
		op := "=="
		parts := strings.SplitN(clause, "==", 2)
		if len(parts) != 2 {
			op = "!="
			parts = strings.SplitN(clause, "!=", 2)
		}
		if len(parts) != 2 {
			return false, fmt.Errorf("invalid clause: %s", clause)
		}
		left := strings.TrimSpace(parts[0])
		right := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		actual := resolveExprField(req, left)
		if op == "==" && actual != right {
			return false, nil
		}
		if op == "!=" && actual == right {
			return false, nil
		}
	}
	return true, nil
}

func resolveExprField(req model.RequestContext, field string) string {
	switch field {
	case "org_id":
		return req.OrgID
	case "tenant_id":
		return req.TenantID
	case "application":
		return req.Application
	case "service":
		return req.Service
	case "environment":
		return req.Environment
	case "signal_type":
		return req.SignalType
	case "operation":
		return req.Operation
	case "endpoint":
		return req.Endpoint
	case "method":
		return req.Method
	case "user_id":
		return req.UserID
	case "api_key":
		return req.APIKey
	case "client_id":
		return req.ClientID
	case "source_ip":
		return req.SourceIP
	case "region":
		return req.Region
	case "resource":
		return req.Resource
	case "severity":
		return req.Severity
	case "span_name":
		return req.SpanName
	case "topic":
		return req.Topic
	case "consumer_group":
		return req.ConsumerGroup
	case "job_type":
		return req.JobType
	}
	if strings.HasPrefix(field, "tag.") {
		return req.Tags[strings.TrimPrefix(field, "tag.")]
	}
	return ""
}
