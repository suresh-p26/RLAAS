package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestWindowStart(t *testing.T) {
	tests := []struct {
		name   string
		now    time.Time
		window string
		want   time.Time
	}{
		{"month", time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC), "month", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
		{"week", time.Date(2024, 3, 14, 10, 0, 0, 0, time.UTC), "week", time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC)},
		{"1h", time.Date(2024, 3, 15, 14, 30, 45, 0, time.UTC), "1h", time.Date(2024, 3, 15, 14, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := model.AlgorithmConfig{Window: tt.window}
			got := WindowStart(tt.now, cfg)
			assert.True(t, got.Equal(tt.want), "got %v, want %v", got, tt.want)
		})
	}
}

func TestWindowEnd(t *testing.T) {
	tests := []struct {
		name   string
		now    time.Time
		window string
		want   time.Time
	}{
		{"month", time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC), "month", time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)},
		{"week", time.Date(2024, 3, 14, 10, 0, 0, 0, time.UTC), "week", time.Date(2024, 3, 18, 0, 0, 0, 0, time.UTC)},
		{"1h", time.Date(2024, 3, 15, 14, 30, 45, 0, time.UTC), "1h", time.Date(2024, 3, 15, 15, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := model.AlgorithmConfig{Window: tt.window}
			got := WindowEnd(tt.now, cfg)
			assert.True(t, got.Equal(tt.want), "got %v, want %v", got, tt.want)
		})
	}
}
