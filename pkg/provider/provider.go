// Package provider defines the Service Provider Interface (SPI) that all
// telemetry provider adapters must implement. This abstraction lets RLAAS
// rate-limit signals from any observability pipeline — OpenTelemetry,
// Datadog, Fluent Bit, Envoy, Kafka, or custom sources — through one
// unified policy engine.
//
// Architecture:
//
//	┌──────────────────┐         ┌──────────────────────┐
//	│ Provider Adapter  │────────→│ model.RequestContext  │
//	│ (OTEL, DD, etc.) │ Adapt() │         ↓             │
//	└──────────────────┘         │ Engine.Evaluate()     │
//	                             │         ↓             │
//	                             │ model.Decision        │
//	                             └──────────────────────┘
//
// To add a new provider, implement [Adapter] and register it with [Registry].
package provider

import (
	"context"
	"github.com/rlaas-io/rlaas/pkg/model"
	"sync"
)

// SignalKind classifies the type of telemetry signal being rate-limited.
type SignalKind string

const (
	SignalLog    SignalKind = "log"
	SignalSpan   SignalKind = "span"
	SignalMetric SignalKind = "metric"
	SignalEvent  SignalKind = "event"
	SignalHTTP   SignalKind = "http"
)

// TelemetryRecord is a provider-agnostic representation of one telemetry
// signal. Each provider adapter converts its native record format into
// this struct so the core engine can evaluate it uniformly.
type TelemetryRecord struct {
	// Signal classifies the record (log, span, metric, event, http).
	Signal SignalKind `json:"signal"`

	// OrgID identifies the organization that owns this telemetry.
	OrgID string `json:"org_id"`

	// Service is the originating service name.
	Service string `json:"service"`

	// Operation further identifies the action (e.g. span name, endpoint, log channel).
	Operation string `json:"operation"`

	// Severity (INFO, WARN, ERROR, etc.) — optional, mainly for logs.
	Severity string `json:"severity,omitempty"`

	// Environment (prod, staging, dev).
	Environment string `json:"environment,omitempty"`

	// Region identifies the data-center or cloud region.
	Region string `json:"region,omitempty"`

	// Endpoint is the specific path or URL for HTTP signals.
	Endpoint string `json:"endpoint,omitempty"`

	// Method is the HTTP method for HTTP signals.
	Method string `json:"method,omitempty"`

	// Tags holds arbitrary key-value metadata from the provider.
	Tags map[string]string `json:"tags,omitempty"`
}

// ToRequestContext converts a provider-agnostic TelemetryRecord into the
// engine's canonical RequestContext for policy evaluation.
func (r TelemetryRecord) ToRequestContext() model.RequestContext {
	return model.RequestContext{
		OrgID:       r.OrgID,
		Service:     r.Service,
		SignalType:  string(r.Signal),
		Operation:   r.Operation,
		Severity:    r.Severity,
		Environment: r.Environment,
		Region:      r.Region,
		Endpoint:    r.Endpoint,
		Method:      r.Method,
		Tags:        r.Tags,
	}
}

// Decision wraps the core Decision with the original record for batch
// processing convenience, so callers can correlate results.
type Decision struct {
	Allowed bool             `json:"allowed"`
	Action  model.ActionType `json:"action"`
	Reason  string           `json:"reason"`
	Record  TelemetryRecord  `json:"record"`
	Raw     model.Decision   `json:"raw"`
}

// Evaluator is the minimal engine contract that adapters depend on.
// Both the embedded SDK client and the HTTP/gRPC service implement this.
type Evaluator interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
}

// Adapter is the interface every telemetry provider must implement.
// It converts native records into TelemetryRecords and processes batches.
type Adapter interface {
	// Name returns the provider identifier (e.g. "otel", "datadog", "fluentbit").
	Name() string

	// SignalKinds returns which signal types this adapter handles.
	SignalKinds() []SignalKind

	// ProcessBatch evaluates a batch of provider-agnostic records and returns
	// only the records that should be kept (not rate-limited).
	ProcessBatch(ctx context.Context, records []TelemetryRecord) ([]Decision, error)
}

// BatchProcessor is a default implementation of batch processing that any
// adapter can embed. It evaluates records concurrently with bounded workers
// and supports fail-open / fail-closed semantics.
type BatchProcessor struct {
	Eval     Evaluator
	Workers  int
	FailOpen bool
}

// Process evaluates a batch of TelemetryRecords concurrently and returns
// a Decision for each input record.
func (bp *BatchProcessor) Process(ctx context.Context, records []TelemetryRecord) []Decision {
	if len(records) == 0 {
		return nil
	}
	workers := bp.Workers
	if workers <= 0 {
		workers = 1
	}

	results := make([]Decision, len(records))
	jobs := make(chan int, len(records))

	for i := range records {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				rec := records[idx]
				reqCtx := rec.ToRequestContext()
				d, err := bp.Eval.Evaluate(ctx, reqCtx)
				if err != nil {
					if bp.FailOpen {
						results[idx] = Decision{Allowed: true, Action: model.ActionAllow, Reason: "fail_open: " + err.Error(), Record: rec, Raw: d}
					} else {
						results[idx] = Decision{Allowed: false, Action: model.ActionDeny, Reason: "fail_closed: " + err.Error(), Record: rec, Raw: d}
					}
					continue
				}
				results[idx] = Decision{Allowed: d.Allowed, Action: d.Action, Reason: d.Reason, Record: rec, Raw: d}
			}
		}()
	}
	wg.Wait()

	return results
}

// FilterAllowed is a convenience that returns only the records whose evaluation was allowed.
func FilterAllowed(decisions []Decision) []TelemetryRecord {
	out := make([]TelemetryRecord, 0, len(decisions))
	for _, d := range decisions {
		if d.Allowed {
			out = append(out, d.Record)
		}
	}
	return out
}
