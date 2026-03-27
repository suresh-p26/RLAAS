package common

import (
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

// WindowDuration parses duration-style windows and period aliases (day/week/month).
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

// WindowStart returns the start of the current window aligned to calendar
// boundaries for "month" (1st of month) and "week" (Monday 00:00 ISO).
func WindowStart(now time.Time, cfg model.AlgorithmConfig) time.Time {
	switch cfg.Window {
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	case "week":
		wd := int(now.Weekday()+6) % 7 // Monday=0 … Sunday=6
		y, m, d := now.Date()
		return time.Date(y, m, d-wd, 0, 0, 0, 0, now.Location())
	default:
		return now.Truncate(WindowDuration(cfg))
	}
}

// WindowEnd returns the start of the next window (exclusive upper bound).
func WindowEnd(now time.Time, cfg model.AlgorithmConfig) time.Time {
	switch cfg.Window {
	case "month":
		return WindowStart(now, cfg).AddDate(0, 1, 0)
	case "week":
		return WindowStart(now, cfg).Add(7 * 24 * time.Hour)
	default:
		d := WindowDuration(cfg)
		return WindowStart(now, cfg).Add(d)
	}
}

// Cost returns the effective request cost: policy override → request quantity → 1.
func Cost(req model.RequestContext, cfg model.AlgorithmConfig) int64 {
	if cfg.CostPerRequest > 0 {
		return cfg.CostPerRequest
	}
	if req.Quantity > 0 {
		return req.Quantity
	}
	return 1
}

// OverLimitDecision builds a denied decision, adjusting for special actions
// (delay, sample, downgrade, shadow) that still allow the request through.
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
		d.SampleRate = policy.Algorithm.SampleRate
	case model.ActionDowngrade:
		d.Allowed = true
	case model.ActionShadowOnly:
		d.Allowed = true
		d.ShadowMode = true
	}
	return d
}
