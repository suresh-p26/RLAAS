package key

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"sort"
	"strings"
)

// Builder generates deterministic counter keys from policy scope and request values.
type Builder interface {
	Build(policy model.Policy, req model.RequestContext) (string, error)
}

// DefaultBuilder is the standard key builder used by the engine.
type DefaultBuilder struct {
	Prefix string
}

// New creates a key builder with an optional namespace prefix.
func New(prefix string) *DefaultBuilder {
	if prefix == "" {
		prefix = "rlaas"
	}
	return &DefaultBuilder{Prefix: prefix}
}

// Build includes only scope fields defined by the winning policy.
func (b *DefaultBuilder) Build(policy model.Policy, req model.RequestContext) (string, error) {
	parts := []string{b.Prefix}
	appendIfScoped := func(name, scope, value string) {
		if scope != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", name, sanitize(value)))
		}
	}
	scope := policy.Scope
	appendIfScoped("org", scope.OrgID, req.OrgID)
	appendIfScoped("tenant", scope.TenantID, req.TenantID)
	appendIfScoped("app", scope.Application, req.Application)
	appendIfScoped("service", scope.Service, req.Service)
	appendIfScoped("env", scope.Environment, req.Environment)
	appendIfScoped("signal", scope.SignalType, req.SignalType)
	appendIfScoped("operation", scope.Operation, req.Operation)
	appendIfScoped("endpoint", scope.Endpoint, req.Endpoint)
	appendIfScoped("method", scope.Method, req.Method)
	appendIfScoped("user", scope.UserID, req.UserID)
	appendIfScoped("api_key", scope.APIKey, req.APIKey)
	appendIfScoped("client", scope.ClientID, req.ClientID)
	appendIfScoped("source_ip", scope.SourceIP, req.SourceIP)
	appendIfScoped("region", scope.Region, req.Region)
	appendIfScoped("resource", scope.Resource, req.Resource)
	appendIfScoped("severity", scope.Severity, req.Severity)
	appendIfScoped("span", scope.SpanName, req.SpanName)
	appendIfScoped("topic", scope.Topic, req.Topic)
	appendIfScoped("consumer_group", scope.ConsumerGroup, req.ConsumerGroup)
	appendIfScoped("job_type", scope.JobType, req.JobType)
	if len(scope.Tags) > 0 {
		parts = append(parts, "tags="+hashTags(req.Tags, scope.Tags))
	}
	return strings.Join(parts, ":"), nil
}

// hashTags creates a short stable representation for matched tags.
func hashTags(reqTags, required map[string]string) string {
	keys := make([]string, 0, len(required))
	for k := range required {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b := strings.Builder{}
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(reqTags[k])
		b.WriteByte(';')
	}
	h := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(h[:8])
}

// sanitize keeps key segments safe for logs and Redis usage.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ":", "_")
	if s == "" {
		return "_"
	}
	return s
}
