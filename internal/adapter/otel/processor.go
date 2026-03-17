package otel

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/provider"
)

// LogRecord is a lightweight telemetry log model for processor-style filtering.
type LogRecord struct {
	OrgID    string
	Service  string
	Severity string
	Tags     map[string]string
	Body     string
}

// SpanRecord is a lightweight telemetry span model for processor-style filtering.
type SpanRecord struct {
	OrgID   string
	Service string
	Name    string
	Tags    map[string]string
}

// ProcessorStats tracks allow/drop/error counters for OTEL pipeline processing.
type ProcessorStats struct {
	Allowed int64 `json:"allowed"`
	Dropped int64 `json:"dropped"`
	Errors  int64 `json:"errors"`
}

// Processor applies RLAAS decisions to telemetry batches.
type Processor struct {
	hook     Hook
	workers  int
	failOpen bool
	allowCnt atomic.Int64
	dropCnt  atomic.Int64
	errorCnt atomic.Int64
}

// NewProcessor creates an OTEL processor with bounded workers.
func NewProcessor(eval Evaluator, workers int, failOpen bool) *Processor {
	if workers <= 0 {
		workers = 1
	}
	return &Processor{hook: Hook{Eval: eval}, workers: workers, failOpen: failOpen}
}

// ProcessLogs applies rate limiting on a log batch and returns kept records.
func (p *Processor) ProcessLogs(ctx context.Context, logs []LogRecord) []LogRecord {
	if len(logs) == 0 {
		return nil
	}
	jobs := make(chan int, len(logs))
	for i := range logs {
		jobs <- i
	}
	close(jobs)

	keep := make([]bool, len(logs))
	for i := range keep {
		keep[i] = true
	}
	var wg sync.WaitGroup
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				logRec := logs[idx]
				allowed, _, err := p.hook.AllowLog(ctx, logRec.OrgID, logRec.Service, logRec.Severity, logRec.Tags)
				if err != nil {
					p.errorCnt.Add(1)
					if !p.failOpen {
						keep[idx] = false
						p.dropCnt.Add(1)
						continue
					}
				}
				if !allowed {
					keep[idx] = false
					p.dropCnt.Add(1)
					continue
				}
				p.allowCnt.Add(1)
			}
		}()
	}
	wg.Wait()

	out := make([]LogRecord, 0, len(logs))
	for i, ok := range keep {
		if ok {
			out = append(out, logs[i])
		}
	}
	return out
}

// ProcessSpans applies rate limiting on a span batch and returns kept records.
func (p *Processor) ProcessSpans(ctx context.Context, spans []SpanRecord) []SpanRecord {
	if len(spans) == 0 {
		return nil
	}
	jobs := make(chan int, len(spans))
	for i := range spans {
		jobs <- i
	}
	close(jobs)

	keep := make([]bool, len(spans))
	for i := range keep {
		keep[i] = true
	}
	var wg sync.WaitGroup
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				s := spans[idx]
				allowed, _, err := p.hook.AllowSpan(ctx, s.OrgID, s.Service, s.Name, s.Tags)
				if err != nil {
					p.errorCnt.Add(1)
					if !p.failOpen {
						keep[idx] = false
						p.dropCnt.Add(1)
						continue
					}
				}
				if !allowed {
					keep[idx] = false
					p.dropCnt.Add(1)
					continue
				}
				p.allowCnt.Add(1)
			}
		}()
	}
	wg.Wait()

	out := make([]SpanRecord, 0, len(spans))
	for i, ok := range keep {
		if ok {
			out = append(out, spans[i])
		}
	}
	return out
}

// Stats returns aggregate processor counters.
func (p *Processor) Stats() ProcessorStats {
	return ProcessorStats{Allowed: p.allowCnt.Load(), Dropped: p.dropCnt.Load(), Errors: p.errorCnt.Load()}
}

// DecisionFilter reports whether to keep one decision based on action.
func DecisionFilter(d model.Decision) bool {
	if !d.Allowed {
		return false
	}
	switch d.Action {
	case model.ActionDrop, model.ActionDropLowPriority, model.ActionDeny:
		return false
	default:
		return true
	}
}

// --- Provider SPI implementation ---

// ProviderAdapter wraps the existing OTEL Processor as a provider.Adapter,
// so it can be registered in the multi-provider registry.
type ProviderAdapter struct {
	proc *Processor
}

// NewProviderAdapter creates a provider.Adapter backed by the existing
// OTEL Processor. This bridges the legacy OTEL-specific API with the
// flexible provider SPI.
func NewProviderAdapter(eval Evaluator, workers int, failOpen bool) *ProviderAdapter {
	return &ProviderAdapter{proc: NewProcessor(eval, workers, failOpen)}
}

// Name returns "otel".
func (a *ProviderAdapter) Name() string { return "otel" }

// SignalKinds returns log and span.
func (a *ProviderAdapter) SignalKinds() []provider.SignalKind {
	return []provider.SignalKind{provider.SignalLog, provider.SignalSpan}
}

// ProcessBatch converts provider.TelemetryRecords into OTEL-native records,
// evaluates them through the existing Processor, and returns Decisions.
func (a *ProviderAdapter) ProcessBatch(ctx context.Context, records []provider.TelemetryRecord) ([]provider.Decision, error) {
	results := make([]provider.Decision, len(records))
	var logs []logIndex
	var spans []spanIndex

	for i, rec := range records {
		switch rec.Signal {
		case provider.SignalLog:
			logs = append(logs, logIndex{idx: i, rec: LogRecord{
				OrgID: rec.OrgID, Service: rec.Service, Severity: rec.Severity, Tags: rec.Tags,
			}})
		case provider.SignalSpan:
			spans = append(spans, spanIndex{idx: i, rec: SpanRecord{
				OrgID: rec.OrgID, Service: rec.Service, Name: rec.Operation, Tags: rec.Tags,
			}})
		default:
			// Unknown signal types pass through as allowed.
			results[i] = provider.Decision{Allowed: true, Action: model.ActionAllow, Reason: "unsupported_signal", Record: rec}
		}
	}

	if len(logs) > 0 {
		input := make([]LogRecord, len(logs))
		for i, li := range logs {
			input[i] = li.rec
		}
		kept := a.proc.ProcessLogs(ctx, input)
		keptSet := make(map[int]bool)
		for idx, in := range input {
			for _, k := range kept {
				if in.OrgID == k.OrgID && in.Service == k.Service && in.Severity == k.Severity {
					keptSet[idx] = true
					break
				}
			}
		}
		for i, li := range logs {
			rec := records[li.idx]
			if keptSet[i] {
				results[li.idx] = provider.Decision{Allowed: true, Action: model.ActionAllow, Reason: "allowed", Record: rec}
			} else {
				results[li.idx] = provider.Decision{Allowed: false, Action: model.ActionDrop, Reason: "rate_limited", Record: rec}
			}
		}
	}

	if len(spans) > 0 {
		input := make([]SpanRecord, len(spans))
		for i, si := range spans {
			input[i] = si.rec
		}
		kept := a.proc.ProcessSpans(ctx, input)
		keptSet := make(map[int]bool)
		for idx, in := range input {
			for _, k := range kept {
				if in.OrgID == k.OrgID && in.Service == k.Service && in.Name == k.Name {
					keptSet[idx] = true
					break
				}
			}
		}
		for i, si := range spans {
			rec := records[si.idx]
			if keptSet[i] {
				results[si.idx] = provider.Decision{Allowed: true, Action: model.ActionAllow, Reason: "allowed", Record: rec}
			} else {
				results[si.idx] = provider.Decision{Allowed: false, Action: model.ActionDrop, Reason: "rate_limited", Record: rec}
			}
		}
	}

	return results, nil
}

type logIndex struct {
	idx int
	rec LogRecord
}

type spanIndex struct {
	idx int
	rec SpanRecord
}

// compile-time check
var _ provider.Adapter = (*ProviderAdapter)(nil)
