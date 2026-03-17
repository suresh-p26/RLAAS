package common

import (
	"github.com/rlaas-io/rlaas/pkg/model"
	"time"
)

// WindowDuration parses duration style windows and common period aliases.
func WindowDuration(cfg model.AlgorithmConfig) time.Duration {
	if cfg.Window == "" {
		return time.Minute
	}
	d, err := time.ParseDuration(cfg.Window)
	if err == nil {
		return d
	}
	switch cfg.Window {
	case "day":
		return 24 * time.Hour
	case "week":
		return 7 * 24 * time.Hour
	case "month":
		return 30 * 24 * time.Hour
	default:
		return time.Minute
	}
}

// Cost returns request cost using policy cost override or request quantity.
func Cost(req model.RequestContext, cfg model.AlgorithmConfig) int64 {
	if cfg.CostPerRequest > 0 {
		return cfg.CostPerRequest
	}
	if req.Quantity > 0 {
		return req.Quantity
	}
	return 1
}

// OverLimitDecision builds a decision when a limit is exceeded.
func OverLimitDecision(policy model.Policy, retryAfter time.Duration, remaining int64, reason string) model.Decision {
	d := model.Decision{
		Allowed:    false,
		Action:     policy.Action,
		Reason:     reason,
		Remaining:  remaining,
		RetryAfter: retryAfter,
		ResetAt:    time.Now().Add(retryAfter),
	}
	switch policy.Action {
	case model.ActionDelay:
		d.Allowed = true
		d.DelayFor = retryAfter
	case model.ActionSample:
		d.SampleRate = 0
	case model.ActionDowngrade:
		d.Allowed = true
	case model.ActionShadowOnly:
		d.Allowed = true
		d.ShadowMode = true
	}
	return d
}
