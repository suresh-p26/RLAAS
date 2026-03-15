package algorithm

import (
	"context"
	"rlaas/pkg/model"
)

// Evaluator executes one algorithm against a policy and request.
type Evaluator interface {
	Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error)
}
