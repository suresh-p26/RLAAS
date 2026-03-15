package algorithm

import (
	"context"
	"github.com/suresh-p26/RLAAS/pkg/model"
)

// Evaluator executes one algorithm against a policy and request.
type Evaluator interface {
	Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error)
}
