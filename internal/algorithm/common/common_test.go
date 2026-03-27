package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestWindowDurationVariants(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   time.Duration
	}{
		{"default empty", "", time.Minute},
		{"parse 2s", "2s", 2 * time.Second},
		{"day alias", "day", 24 * time.Hour},
		{"week alias", "week", 7 * 24 * time.Hour},
		{"month alias", "month", 30 * 24 * time.Hour},
		{"bad value fallback", "bad_value", time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WindowDuration(model.AlgorithmConfig{Window: tt.window})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCostPriority(t *testing.T) {
	tests := []struct {
		name string
		req  model.RequestContext
		cfg  model.AlgorithmConfig
		want int64
	}{
		{"cost per request takes precedence over quantity", model.RequestContext{Quantity: 3}, model.AlgorithmConfig{CostPerRequest: 2}, 2},
		{"quantity used when no cost per request", model.RequestContext{Quantity: 3}, model.AlgorithmConfig{}, 3},
		{"default cost is 1", model.RequestContext{}, model.AlgorithmConfig{}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Cost(tt.req, tt.cfg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOverLimitDecisionActionBehavior(t *testing.T) {
	for _, tc := range []struct {
		action        model.ActionType
		expectAllowed bool
		expectShadow  bool
	}{
		{action: model.ActionDeny, expectAllowed: false},
		{action: model.ActionDelay, expectAllowed: true},
		{action: model.ActionSample, expectAllowed: false},
		{action: model.ActionDowngrade, expectAllowed: true},
		{action: model.ActionShadowOnly, expectAllowed: true, expectShadow: true},
	} {
		d := OverLimitDecision(model.Policy{Action: tc.action}, time.Second, 0, "x")
		assert.Equal(t, tc.expectAllowed, d.Allowed, "unexpected allowed for action %s", tc.action)
		assert.Equal(t, tc.expectShadow, d.ShadowMode, "unexpected shadow for action %s", tc.action)
	}
}
