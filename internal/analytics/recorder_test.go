package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecorderAggregatesEventsAndTags(t *testing.T) {
	r := NewRecorder()
	r.Record(context.Background(), "policy_create", map[string]string{"status": "ok", "actor": "admin"})
	r.Record(context.Background(), "policy_create", map[string]string{"status": "ok"})
	r.Record(context.Background(), "policy_delete", map[string]string{"status": "ok"})

	events := r.Snapshot()
	if events["policy_create"] != 2 || events["policy_delete"] != 1 {
		t.Fatalf("unexpected event counts: %+v", events)
	}
	tags := r.SnapshotTags()
	if tags["status=ok"] != 3 || tags["actor=admin"] != 1 {
		t.Fatalf("unexpected tag counts: %+v", tags)
	}
	if r.Total() != 3 {
		t.Fatalf("expected total 3")
	}
}

func TestSummaryHandler(t *testing.T) {
	r := NewRecorder()
	r.Record(context.Background(), "a_event", map[string]string{"team": "alpha"})
	r.Record(context.Background(), "b_event", map[string]string{"team": "beta"})

	h := SummaryHandler(r)

	badMethod := httptest.NewRecorder()
	h.ServeHTTP(badMethod, httptest.NewRequest(http.MethodPost, "/rlaas/v1/analytics/summary", nil))
	if badMethod.Code != http.StatusNotFound {
		t.Fatalf("expected not found for unsupported method")
	}

	badTop := httptest.NewRecorder()
	h.ServeHTTP(badTop, httptest.NewRequest(http.MethodGet, "/rlaas/v1/analytics/summary?top=x", nil))
	if badTop.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for invalid top")
	}

	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/rlaas/v1/analytics/summary?top=1", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("expected summary success")
	}
	body := ok.Body.String()
	if !strings.Contains(body, "total_events") || !strings.Contains(body, "by_event") || !strings.Contains(body, "by_tag") {
		t.Fatalf("expected aggregated summary payload")
	}
}
