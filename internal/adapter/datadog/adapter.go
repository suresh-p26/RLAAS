// Package datadog provides an RLAAS adapter for the Datadog Agent pipeline.
// It converts Datadog-native log and metric records into provider.TelemetryRecords,
// evaluates them against the RLAAS policy engine, and filters denied signals.
//
// Use case: A Datadog Agent custom check or log processor that enforces
// per-service rate limits before shipping to Datadog intake.
//
// Integration pattern:
//
//	adapter := datadog.NewAdapter(rlaasClient, 4, true)
//	kept := adapter.FilterLogs(ctx, logs)
package datadog

import (
	"context"

	"rlaas/internal/model"
	"rlaas/internal/provider"
)

// LogEntry represents a Datadog-style log record.
type LogEntry struct {
	Hostname string            `json:"hostname"`
	Service  string            `json:"ddsource"`
	Source   string            `json:"source"`
	Tags     []string          `json:"ddtags"`
	Status   string            `json:"status"` // info, warn, error, critical
	Message  string            `json:"message"`
	Attrs    map[string]string `json:"attributes,omitempty"`
}

// MetricSample represents a Datadog-style metric submission.
type MetricSample struct {
	Metric    string            `json:"metric"`
	Host      string            `json:"host"`
	Service   string            `json:"service"`
	Type      string            `json:"type"` // gauge, count, rate, histogram, distribution
	Tags      []string          `json:"tags"`
	Namespace string            `json:"namespace,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// Adapter wraps the RLAAS engine for Datadog-specific signal types.
type Adapter struct {
	bp    provider.BatchProcessor
	orgID string
}

// NewAdapter creates a Datadog provider adapter.
// orgID is injected into every evaluation as the owning organization.
func NewAdapter(eval provider.Evaluator, orgID string, workers int, failOpen bool) *Adapter {
	return &Adapter{
		bp:    provider.BatchProcessor{Eval: eval, Workers: workers, FailOpen: failOpen},
		orgID: orgID,
	}
}

// Name returns "datadog".
func (a *Adapter) Name() string { return "datadog" }

// SignalKinds returns log and metric.
func (a *Adapter) SignalKinds() []provider.SignalKind {
	return []provider.SignalKind{provider.SignalLog, provider.SignalMetric}
}

// ProcessBatch evaluates a batch of provider.TelemetryRecords.
func (a *Adapter) ProcessBatch(ctx context.Context, records []provider.TelemetryRecord) ([]provider.Decision, error) {
	decisions := a.bp.Process(ctx, records)
	return decisions, nil
}

// FilterLogs converts Datadog logs → TelemetryRecords, evaluates, and returns kept logs.
func (a *Adapter) FilterLogs(ctx context.Context, logs []LogEntry) []LogEntry {
	if len(logs) == 0 {
		return nil
	}
	records := make([]provider.TelemetryRecord, len(logs))
	for i, l := range logs {
		records[i] = provider.TelemetryRecord{
			Signal:   provider.SignalLog,
			OrgID:    a.orgID,
			Service:  l.Service,
			Severity: l.Status,
			Tags:     mergeTagSlice(l.Tags, l.Attrs),
		}
	}
	decisions := a.bp.Process(ctx, records)
	out := make([]LogEntry, 0, len(logs))
	for i, d := range decisions {
		if d.Allowed {
			out = append(out, logs[i])
		}
	}
	return out
}

// FilterMetrics converts Datadog metrics → TelemetryRecords, evaluates, and returns kept metrics.
func (a *Adapter) FilterMetrics(ctx context.Context, metrics []MetricSample) []MetricSample {
	if len(metrics) == 0 {
		return nil
	}
	records := make([]provider.TelemetryRecord, len(metrics))
	for i, m := range metrics {
		tags := mergeTagSlice(m.Tags, m.Attrs)
		tags["metric_type"] = m.Type
		tags["metric_name"] = m.Metric
		records[i] = provider.TelemetryRecord{
			Signal:    provider.SignalMetric,
			OrgID:     a.orgID,
			Service:   m.Service,
			Operation: m.Metric,
			Tags:      tags,
		}
	}
	decisions := a.bp.Process(ctx, records)
	out := make([]MetricSample, 0, len(metrics))
	for i, d := range decisions {
		if d.Allowed {
			out = append(out, metrics[i])
		}
	}
	return out
}

// mergeTagSlice converts Datadog "key:value" tag slices + map attrs into a single map.
func mergeTagSlice(tags []string, attrs map[string]string) map[string]string {
	m := make(map[string]string, len(tags)+len(attrs))
	for _, t := range tags {
		for i := 0; i < len(t); i++ {
			if t[i] == ':' {
				m[t[:i]] = t[i+1:]
				break
			}
		}
	}
	for k, v := range attrs {
		m[k] = v
	}
	return m
}

// compile-time interface checks
var _ provider.Adapter = (*Adapter)(nil)
var _ = (*Adapter)(nil).FilterLogs
var _ = (*Adapter)(nil).FilterMetrics

// ensure model import is referenced
var _ model.ActionType
