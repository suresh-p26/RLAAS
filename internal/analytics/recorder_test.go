package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecorderAggregatesEventsAndTags(t *testing.T) {
	r := NewRecorder()
	r.Record(context.Background(), "policy_create", map[string]string{"status": "ok", "actor": "admin"})
	r.Record(context.Background(), "policy_create", map[string]string{"status": "ok"})
	r.Record(context.Background(), "policy_delete", map[string]string{"status": "ok"})

	events := r.Snapshot()
	assert.Equal(t, int64(2), events["policy_create"], "policy_create count")
	assert.Equal(t, int64(1), events["policy_delete"], "policy_delete count")

	tags := r.SnapshotTags()
	assert.Equal(t, int64(3), tags["status=ok"])
	assert.Equal(t, int64(1), tags["actor=admin"])
	assert.Equal(t, int64(3), r.Total())
}

func TestSummaryHandler(t *testing.T) {
	r := NewRecorder()
	r.Record(context.Background(), "a_event", map[string]string{"team": "alpha"})
	r.Record(context.Background(), "b_event", map[string]string{"team": "beta"})
	h := SummaryHandler(r)

	tests := []struct {
		name     string
		method   string
		url      string
		wantCode int
		wantBody string
	}{
		{"POST method not allowed", http.MethodPost, "/rlaas/v1/analytics/summary", http.StatusNotFound, ""},
		{"invalid top query param", http.MethodGet, "/rlaas/v1/analytics/summary?top=x", http.StatusBadRequest, ""},
		{"valid request returns summary", http.MethodGet, "/rlaas/v1/analytics/summary?top=1", http.StatusOK, "total_events"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.url, nil))
			if tt.wantCode == http.StatusOK {
				require.Equal(t, tt.wantCode, rec.Code)
			} else {
				assert.Equal(t, tt.wantCode, rec.Code)
			}
			if tt.wantBody != "" {
				body := rec.Body.String()
				assert.True(t, strings.Contains(body, "total_events") && strings.Contains(body, "by_event") && strings.Contains(body, "by_tag"), "expected aggregated summary payload")
			}
		})
	}
}
