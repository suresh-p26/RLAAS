package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
)

// Recorder stores in-memory counters for phase 3 usage analytics.
type Recorder struct {
	mu         sync.RWMutex
	eventCount map[string]int64
	tagCount   map[string]int64
	total      int64
}

// NewRecorder creates an empty analytics recorder.
func NewRecorder() *Recorder {
	return &Recorder{eventCount: map[string]int64{}, tagCount: map[string]int64{}}
}

// Record increments one named event counter.
func (r *Recorder) Record(_ context.Context, event string, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventCount[event]++
	r.total++
	for k, v := range tags {
		r.tagCount[k+"="+v]++
	}
}

// Snapshot returns a copy of all counters.
func (r *Recorder) Snapshot() map[string]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int64, len(r.eventCount))
	for k, v := range r.eventCount {
		out[k] = v
	}
	return out
}

// SnapshotTags returns a copy of tag value counters.
func (r *Recorder) SnapshotTags() map[string]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int64, len(r.tagCount))
	for k, v := range r.tagCount {
		out[k] = v
	}
	return out
}

// Total returns the total number of recorded analytics events.
func (r *Recorder) Total() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.total
}

// SummaryHandler exposes counters for operations and analytics consumers.
func SummaryHandler(r *Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.NotFound(w, req)
			return
		}
		events := r.Snapshot()
		tags := r.SnapshotTags()
		type row struct {
			Event string `json:"event"`
			Count int64  `json:"count"`
		}
		eventRows := make([]row, 0, len(events))
		for k, v := range events {
			eventRows = append(eventRows, row{Event: k, Count: v})
		}
		tagRows := make([]row, 0, len(tags))
		for k, v := range tags {
			tagRows = append(tagRows, row{Event: k, Count: v})
		}
		sort.Slice(eventRows, func(i, j int) bool { return eventRows[i].Event < eventRows[j].Event })
		sort.Slice(tagRows, func(i, j int) bool { return tagRows[i].Event < tagRows[j].Event })
		limit := 0
		if raw := req.URL.Query().Get("top"); raw != "" {
			for _, ch := range raw {
				if ch < '0' || ch > '9' {
					http.Error(w, "top must be numeric", http.StatusBadRequest)
					return
				}
				limit = limit*10 + int(ch-'0')
			}
		}
		if limit > 0 {
			if len(eventRows) > limit {
				eventRows = eventRows[:limit]
			}
			if len(tagRows) > limit {
				tagRows = tagRows[:limit]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"total_events": r.Total(), "by_event": eventRows, "by_tag": tagRows})
	})
}
