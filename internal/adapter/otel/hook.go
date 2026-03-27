package otel

import (
	"context"

	"github.com/rlaas-io/rlaas/pkg/model"
)

// Evaluator is the minimal contract needed by OTEL hooks.
type Evaluator interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
}

// Hook adapts telemetry records into standard request contexts.
type Hook struct {
	Eval Evaluator
}

// AllowLog evaluates one log record and returns both bool and full decision.
func (h Hook) AllowLog(ctx context.Context, orgID, service, severity string, tags map[string]string) (bool, model.Decision, error) {
	req := model.RequestContext{OrgID: orgID, Service: service, Severity: severity, SignalType: "log", Operation: "otel_log", Tags: tags}
	d, err := h.Eval.Evaluate(ctx, req)
	return d.Allowed, d, err
}

// AllowSpan evaluates one span record and returns both bool and full decision.
func (h Hook) AllowSpan(ctx context.Context, orgID, service, span string, tags map[string]string) (bool, model.Decision, error) {
	req := model.RequestContext{OrgID: orgID, Service: service, SpanName: span, SignalType: "span", Operation: "otel_span", Tags: tags}
	d, err := h.Eval.Evaluate(ctx, req)
	return d.Allowed, d, err
}
