package otel

import (
"context"
"errors"
"testing"

"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"

"github.com/rlaas-io/rlaas/pkg/model"
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
require.NoError(t, err, "expected allowed log")
assert.True(t, ok, "expected ok for allowed log")
assert.True(t, d.Allowed, "expected decision allowed")

ok, d, err = h.AllowSpan(context.Background(), "o", "s", "span1", nil)
require.NoError(t, err, "expected allowed span")
assert.True(t, ok, "expected ok for allowed span")
assert.True(t, d.Allowed, "expected decision allowed for span")

h2 := Hook{Eval: hookEvalStub{err: errors.New("x")}}
_, _, err = h2.AllowLog(context.Background(), "o", "s", "warn", nil)
require.Error(t, err, "expected error")
}