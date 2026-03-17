package common

import (
	"github.com/rlaas-io/rlaas/pkg/model"
	"testing"
	"time"
)

func TestWindowDurationVariants(t *testing.T) {
	if WindowDuration(model.AlgorithmConfig{}) != time.Minute {
		t.Fatalf("default should be 1m")
	}
	if WindowDuration(model.AlgorithmConfig{Window: "2s"}) != 2*time.Second {
		t.Fatalf("duration parse failed")
	}
	if WindowDuration(model.AlgorithmConfig{Window: "day"}) != 24*time.Hour {
		t.Fatalf("day alias failed")
	}
	if WindowDuration(model.AlgorithmConfig{Window: "week"}) != 7*24*time.Hour {
		t.Fatalf("week alias failed")
	}
	if WindowDuration(model.AlgorithmConfig{Window: "month"}) != 30*24*time.Hour {
		t.Fatalf("month alias failed")
	}
	if WindowDuration(model.AlgorithmConfig{Window: "bad_value"}) != time.Minute {
		t.Fatalf("unknown value should fallback to minute")
	}
}

func TestCostPriority(t *testing.T) {
	if Cost(model.RequestContext{Quantity: 3}, model.AlgorithmConfig{CostPerRequest: 2}) != 2 {
		t.Fatalf("cost per request should take precedence")
	}
	if Cost(model.RequestContext{Quantity: 3}, model.AlgorithmConfig{}) != 3 {
		t.Fatalf("quantity should be used")
	}
	if Cost(model.RequestContext{}, model.AlgorithmConfig{}) != 1 {
		t.Fatalf("default cost should be 1")
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
		if d.Allowed != tc.expectAllowed || d.ShadowMode != tc.expectShadow {
			t.Fatalf("unexpected decision for action %s", tc.action)
		}
	}
}
