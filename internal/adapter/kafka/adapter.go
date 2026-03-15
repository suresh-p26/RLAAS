// Package kafka provides an RLAAS adapter for Apache Kafka consumer/producer
// pipelines. It enforces per-topic, per-consumer-group rate limits on event
// streams, letting enterprises control event throughput without modifying
// application code.
//
// Use case: A Kafka consumer interceptor that throttles event processing
// to protect downstream systems from burst traffic.
//
// Integration pattern:
//
//	adapter := kafka.NewAdapter(rlaasClient, "fidelity", 4, true)
//	kept := adapter.FilterMessages(ctx, messages)
package kafka

import (
	"context"
	"time"

	"rlaas/pkg/provider"
)

// Message represents a Kafka message for rate limit evaluation.
type Message struct {
	Topic         string            `json:"topic"`
	Partition     int               `json:"partition"`
	Offset        int64             `json:"offset"`
	Key           string            `json:"key"`
	Headers       map[string]string `json:"headers"`
	Timestamp     time.Time         `json:"timestamp"`
	ConsumerGroup string            `json:"consumer_group"`
	Service       string            `json:"service"` // producing or consuming service
}

// Adapter wraps the RLAAS engine for Kafka event streams.
type Adapter struct {
	bp    provider.BatchProcessor
	orgID string
}

// NewAdapter creates a Kafka provider adapter.
func NewAdapter(eval provider.Evaluator, orgID string, workers int, failOpen bool) *Adapter {
	return &Adapter{
		bp:    provider.BatchProcessor{Eval: eval, Workers: workers, FailOpen: failOpen},
		orgID: orgID,
	}
}

// Name returns "kafka".
func (a *Adapter) Name() string { return "kafka" }

// SignalKinds returns event.
func (a *Adapter) SignalKinds() []provider.SignalKind {
	return []provider.SignalKind{provider.SignalEvent}
}

// ProcessBatch evaluates a batch of generic TelemetryRecords.
func (a *Adapter) ProcessBatch(ctx context.Context, records []provider.TelemetryRecord) ([]provider.Decision, error) {
	decisions := a.bp.Process(ctx, records)
	return decisions, nil
}

// FilterMessages converts Kafka messages → TelemetryRecords, evaluates,
// and returns only the messages that should be processed (not rate-limited).
func (a *Adapter) FilterMessages(ctx context.Context, messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	records := make([]provider.TelemetryRecord, len(messages))
	for i, m := range messages {
		tags := make(map[string]string, len(m.Headers)+2)
		for k, v := range m.Headers {
			tags[k] = v
		}
		tags["kafka.key"] = m.Key
		if m.ConsumerGroup != "" {
			tags["kafka.consumer_group"] = m.ConsumerGroup
		}
		records[i] = provider.TelemetryRecord{
			Signal:    provider.SignalEvent,
			OrgID:     a.orgID,
			Service:   m.Service,
			Operation: m.Topic,
			Tags:      tags,
		}
	}
	decisions := a.bp.Process(ctx, records)
	out := make([]Message, 0, len(messages))
	for i, d := range decisions {
		if d.Allowed {
			out = append(out, messages[i])
		}
	}
	return out
}

// compile-time interface check
var _ provider.Adapter = (*Adapter)(nil)
