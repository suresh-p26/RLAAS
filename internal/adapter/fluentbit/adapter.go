// Package fluentbit provides an RLAAS adapter for Fluent Bit / Fluentd
// log processing pipelines. It converts Fluent Bit records into
// provider.TelemetryRecords so the RLAAS engine can enforce per-service
// rate limits on log volumes before they reach storage backends.
//
// Integration pattern (Fluent Bit Go plugin):
//
//	adapter := fluentbit.NewAdapter(rlaasClient, "fidelity", 4, true)
//	kept := adapter.FilterRecords(ctx, records)
//
// This adapter handles the common Fluent Bit record format: a timestamp
// plus a map[string]string of key-value fields.
package fluentbit

import (
	"context"
	"time"

	"github.com/rlaas-io/rlaas/pkg/provider"
)

// Record represents a single Fluent Bit log record.
type Record struct {
	Timestamp time.Time         `json:"timestamp"`
	Tag       string            `json:"tag"`    // Fluent Bit routing tag (e.g. "kube.payments")
	Fields    map[string]string `json:"fields"` // key-value payload
}

// Adapter wraps the RLAAS engine for Fluent Bit log records.
type Adapter struct {
	bp    provider.BatchProcessor
	orgID string

	// ServiceField is the record field key that contains the service name.
	// Default: "service_name". Fluent Bit kubernetes filter sets "kubernetes.container_name".
	ServiceField string

	// SeverityField is the record field key that contains log severity.
	// Default: "level".
	SeverityField string

	// EnvironmentField is the field key for environment. Default: "environment".
	EnvironmentField string
}

// NewAdapter creates a Fluent Bit provider adapter.
func NewAdapter(eval provider.Evaluator, orgID string, workers int, failOpen bool) *Adapter {
	return &Adapter{
		bp:               provider.BatchProcessor{Eval: eval, Workers: workers, FailOpen: failOpen},
		orgID:            orgID,
		ServiceField:     "service_name",
		SeverityField:    "level",
		EnvironmentField: "environment",
	}
}

// Name returns "fluentbit".
func (a *Adapter) Name() string { return "fluentbit" }

// SignalKinds returns log only.
func (a *Adapter) SignalKinds() []provider.SignalKind {
	return []provider.SignalKind{provider.SignalLog}
}

// ProcessBatch evaluates a batch of generic TelemetryRecords.
func (a *Adapter) ProcessBatch(ctx context.Context, records []provider.TelemetryRecord) ([]provider.Decision, error) {
	decisions := a.bp.Process(ctx, records)
	return decisions, nil
}

// FilterRecords converts Fluent Bit records → TelemetryRecords, evaluates,
// and returns only the records that are allowed through rate limiting.
func (a *Adapter) FilterRecords(ctx context.Context, records []Record) []Record {
	if len(records) == 0 {
		return nil
	}
	telRecords := make([]provider.TelemetryRecord, len(records))
	for i, r := range records {
		service := r.Fields[a.ServiceField]
		if service == "" {
			// Fall back to tag-based service resolution.
			service = r.Tag
		}
		telRecords[i] = provider.TelemetryRecord{
			Signal:      provider.SignalLog,
			OrgID:       a.orgID,
			Service:     service,
			Severity:    r.Fields[a.SeverityField],
			Environment: r.Fields[a.EnvironmentField],
			Tags:        r.Fields,
		}
	}
	decisions := a.bp.Process(ctx, telRecords)
	out := make([]Record, 0, len(records))
	for i, d := range decisions {
		if d.Allowed {
			out = append(out, records[i])
		}
	}
	return out
}

// compile-time interface check
var _ provider.Adapter = (*Adapter)(nil)
