package common

import (
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestWindowStart_Month(t *testing.T) {
	// 2024-03-15 14:30:00 → should return 2024-03-01 00:00:00
	now := time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC)
	cfg := model.AlgorithmConfig{Window: "month"}
	got := WindowStart(now, cfg)
	want := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("WindowStart(month): got %v, want %v", got, want)
	}
}

func TestWindowStart_Week(t *testing.T) {
	// 2024-03-14 is Thursday → Monday is 2024-03-11
	now := time.Date(2024, 3, 14, 10, 0, 0, 0, time.UTC)
	cfg := model.AlgorithmConfig{Window: "week"}
	got := WindowStart(now, cfg)
	want := time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("WindowStart(week): got %v, want %v", got, want)
	}
}

func TestWindowStart_Default(t *testing.T) {
	now := time.Date(2024, 3, 15, 14, 30, 45, 0, time.UTC)
	cfg := model.AlgorithmConfig{Window: "1h"}
	got := WindowStart(now, cfg)
	want := time.Date(2024, 3, 15, 14, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("WindowStart(1h): got %v, want %v", got, want)
	}
}

func TestWindowEnd_Month(t *testing.T) {
	now := time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC)
	cfg := model.AlgorithmConfig{Window: "month"}
	got := WindowEnd(now, cfg)
	want := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("WindowEnd(month): got %v, want %v", got, want)
	}
}

func TestWindowEnd_Week(t *testing.T) {
	now := time.Date(2024, 3, 14, 10, 0, 0, 0, time.UTC)
	cfg := model.AlgorithmConfig{Window: "week"}
	got := WindowEnd(now, cfg)
	// Monday 2024-03-11 + 7 days = 2024-03-18
	want := time.Date(2024, 3, 18, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("WindowEnd(week): got %v, want %v", got, want)
	}
}

func TestWindowEnd_Default(t *testing.T) {
	now := time.Date(2024, 3, 15, 14, 30, 45, 0, time.UTC)
	cfg := model.AlgorithmConfig{Window: "1h"}
	got := WindowEnd(now, cfg)
	want := time.Date(2024, 3, 15, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("WindowEnd(1h): got %v, want %v", got, want)
	}
}
