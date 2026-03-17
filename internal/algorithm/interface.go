package algorithm

import (
	"context"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// Evaluator executes one algorithm against a policy and request.
type Evaluator interface {
	Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error)
}
