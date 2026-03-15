// Package envoy provides an RLAAS adapter for Envoy proxy's external
// rate limiting and ext_authz integration. This lets RLAAS serve as the
// rate limit decision backend for Envoy service mesh deployments.
//
// Envoy integration modes:
//
//  1. Rate Limit Service (rls): Envoy sends rate limit descriptors via gRPC
//     to the RLAAS server, which evaluates them against policies and returns
//     OVER_LIMIT or OK.
//
//  2. ext_authz: Envoy sends HTTP request metadata to RLAAS as an external
//     authorization check, which doubles as a rate limit gate.
//
// This adapter converts Envoy's descriptor/request model into RLAAS
// TelemetryRecords so the same policy engine handles both telemetry
// and traffic rate limiting.
//
// Integration pattern:
//
//	adapter := envoy.NewAdapter(rlaasClient, "fidelity", 4, true)
//	response := adapter.CheckRateLimit(ctx, descriptors)
package envoy

import (
	"context"
	"strings"

	"rlaas/pkg/provider"
)

// Descriptor represents an Envoy rate limit descriptor entry.
// In Envoy config, descriptors are key-value pairs collected by filters.
type Descriptor struct {
	Entries []DescriptorEntry `json:"entries"`
}

// DescriptorEntry is a single key-value pair in a descriptor set.
type DescriptorEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// RateLimitResponse is the result for a full rate limit check.
type RateLimitResponse struct {
	OverallCode RateLimitCode      `json:"overall_code"`
	Statuses    []DescriptorStatus `json:"statuses"`
}

// DescriptorStatus holds the result for one descriptor.
type DescriptorStatus struct {
	Code           RateLimitCode `json:"code"`
	CurrentLimit   *RateLimit    `json:"current_limit,omitempty"`
	LimitRemaining int64         `json:"limit_remaining"`
}

// RateLimit describes the active limit for display.
type RateLimit struct {
	RequestsPerUnit int64  `json:"requests_per_unit"`
	Unit            string `json:"unit"` // second, minute, hour, day
}

// RateLimitCode mirrors Envoy's rate limit response codes.
type RateLimitCode int

const (
	RateLimitOK        RateLimitCode = 1
	RateLimitOverLimit RateLimitCode = 2
	RateLimitUnknown   RateLimitCode = 3
)

// AuthRequest represents an Envoy ext_authz HTTP request check.
type AuthRequest struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	SourceIP string            `json:"source_ip"`
	Service  string            `json:"service"`
}

// AuthResponse is the ext_authz decision.
type AuthResponse struct {
	Allowed    bool              `json:"allowed"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Reason     string            `json:"reason"`
}

// Adapter wraps the RLAAS engine for Envoy rate limiting and ext_authz.
type Adapter struct {
	bp    provider.BatchProcessor
	orgID string
}

// NewAdapter creates an Envoy provider adapter.
func NewAdapter(eval provider.Evaluator, orgID string, workers int, failOpen bool) *Adapter {
	return &Adapter{
		bp:    provider.BatchProcessor{Eval: eval, Workers: workers, FailOpen: failOpen},
		orgID: orgID,
	}
}

// Name returns "envoy".
func (a *Adapter) Name() string { return "envoy" }

// SignalKinds returns http.
func (a *Adapter) SignalKinds() []provider.SignalKind {
	return []provider.SignalKind{provider.SignalHTTP}
}

// ProcessBatch evaluates a batch of generic TelemetryRecords.
func (a *Adapter) ProcessBatch(ctx context.Context, records []provider.TelemetryRecord) ([]provider.Decision, error) {
	decisions := a.bp.Process(ctx, records)
	return decisions, nil
}

// CheckRateLimit evaluates Envoy rate limit descriptors and returns a response
// compatible with Envoy's rate limit service protocol.
func (a *Adapter) CheckRateLimit(ctx context.Context, domain string, descriptors []Descriptor) RateLimitResponse {
	if len(descriptors) == 0 {
		return RateLimitResponse{OverallCode: RateLimitOK}
	}

	records := make([]provider.TelemetryRecord, len(descriptors))
	for i, desc := range descriptors {
		tags := make(map[string]string, len(desc.Entries))
		var service, endpoint, method string
		for _, e := range desc.Entries {
			tags[e.Key] = e.Value
			switch strings.ToLower(e.Key) {
			case "service", "destination_cluster":
				service = e.Value
			case "path", "request_path":
				endpoint = e.Value
			case "method", "request_method":
				method = e.Value
			}
		}
		records[i] = provider.TelemetryRecord{
			Signal:   provider.SignalHTTP,
			OrgID:    a.orgID,
			Service:  service,
			Endpoint: endpoint,
			Method:   method,
			Tags:     tags,
		}
	}

	decisions := a.bp.Process(ctx, records)
	response := RateLimitResponse{OverallCode: RateLimitOK}
	response.Statuses = make([]DescriptorStatus, len(decisions))

	for i, d := range decisions {
		if d.Allowed {
			response.Statuses[i] = DescriptorStatus{
				Code:           RateLimitOK,
				LimitRemaining: d.Raw.Remaining,
			}
		} else {
			response.OverallCode = RateLimitOverLimit
			response.Statuses[i] = DescriptorStatus{
				Code:           RateLimitOverLimit,
				LimitRemaining: 0,
			}
		}
	}

	return response
}

// CheckAuth evaluates an Envoy ext_authz request against RLAAS policies.
func (a *Adapter) CheckAuth(ctx context.Context, req AuthRequest) AuthResponse {
	records := []provider.TelemetryRecord{{
		Signal:   provider.SignalHTTP,
		OrgID:    a.orgID,
		Service:  req.Service,
		Endpoint: req.Path,
		Method:   req.Method,
		Tags:     req.Headers,
	}}

	decisions := a.bp.Process(ctx, records)
	if len(decisions) == 0 || decisions[0].Allowed {
		return AuthResponse{Allowed: true, StatusCode: 200, Reason: "allowed"}
	}

	return AuthResponse{
		Allowed:    false,
		StatusCode: 429,
		Headers:    map[string]string{"x-rlaas-reason": decisions[0].Reason},
		Reason:     decisions[0].Reason,
	}
}

// compile-time interface check
var _ provider.Adapter = (*Adapter)(nil)
