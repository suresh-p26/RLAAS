package common

import (
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
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

// WindowStart returns the start of the current window for the given time.
// For "month" and "week" it aligns to calendar boundaries (1st of month,
// Monday 00:00 respectively). For all other durations it uses Truncate.
func WindowStart(now time.Time, cfg model.AlgorithmConfig) time.Time {
	switch cfg.Window {
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	case "week":
		// ISO week: Monday is the start.
		wd := int(now.Weekday()+6) % 7 // Monday=0 ... Sunday=6
		y, m, d := now.Date()
		return time.Date(y, m, d-wd, 0, 0, 0, 0, now.Location())
	default:
		return now.Truncate(WindowDuration(cfg))
	}
}

// WindowEnd returns the end of the current window (start of next window).
func WindowEnd(now time.Time, cfg model.AlgorithmConfig) time.Time {
	switch cfg.Window {
	case "month":
		start := WindowStart(now, cfg)
		return start.AddDate(0, 1, 0)
	case "week":
		return WindowStart(now, cfg).Add(7 * 24 * time.Hour)
	default:
		d := WindowDuration(cfg)
		return WindowStart(now, cfg).Add(d)
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
		d.SampleRate = policy.Algorithm.SampleRate
	case model.ActionDowngrade:
		d.Allowed = true
	case model.ActionShadowOnly:
		d.Allowed = true
		d.ShadowMode = true
	}
	return d
}
