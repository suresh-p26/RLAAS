package otel

import (
	"context"
	"errors"
	"github.com/rlaas-io/rlaas/pkg/model"
	"testing"
)

type hookEvalStub struct {
	decision model.Decision
	err      error
}

func (h hookEvalStub) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return h.decision, h.err
}

func TestHookAllowLogAndSpan(t *testing.T) {
	h := Hook{Eval: hookEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}}
	ok, d, err := h.AllowLog(context.Background(), "o", "s", "info", map[string]string{"k": "v"})
	if err != nil || !ok || !d.Allowed {
		t.Fatalf("expected allowed log")
	}
	ok, d, err = h.AllowSpan(context.Background(), "o", "s", "span1", nil)
	if err != nil || !ok || !d.Allowed {
		t.Fatalf("expected allowed span")
	}

	h2 := Hook{Eval: hookEvalStub{err: errors.New("x")}}
	_, _, err = h2.AllowLog(context.Background(), "o", "s", "warn", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
}
