package server

import (
	"context"
	"net/http"
	"regexp"
)

// traceKey is the unexported context key for TraceContext values.
type traceKey struct{}

// TraceContext holds W3C Trace Context identifiers extracted from a
// traceparent header (https://www.w3.org/TR/trace-context/).
type TraceContext struct {
	TraceID string // 32 lower-case hex chars
	SpanID  string // 16 lower-case hex chars
	Flags   string // 2 lower-case hex chars (sampled, etc.)
}

// traceparentRE matches the canonical traceparent format:
//
//	{version}-{traceId}-{parentId}-{flags}
var traceparentRE = regexp.MustCompile(
	`^[0-9a-f]{2}-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`,
)

// TraceContextMiddleware reads the incoming W3C traceparent header, parses
// the trace and span IDs, and stores them in the request context so that
// downstream handlers and log lines can correlate entries across services.
//
// The traceparent header is echoed in the response so callers can verify
// propagation end-to-end. An absent or malformed header is silently ignored
// (no-op); this mirrors the W3C specification's MUST NOT reject behaviour.
func TraceContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc := parseTraceparent(r.Header.Get("traceparent"))
		ctx := context.WithValue(r.Context(), traceKey{}, tc)
		if tc.TraceID != "" {
			// Echo so downstream proxies and clients can correlate.
			w.Header().Set("traceparent", r.Header.Get("traceparent"))
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TraceFromContext returns the TraceContext stored by TraceContextMiddleware,
// or an empty TraceContext if none is present.
func TraceFromContext(ctx context.Context) TraceContext {
	if tc, ok := ctx.Value(traceKey{}).(TraceContext); ok {
		return tc
	}
	return TraceContext{}
}

func parseTraceparent(header string) TraceContext {
	m := traceparentRE.FindStringSubmatch(header)
	if m == nil {
		return TraceContext{}
	}
	return TraceContext{TraceID: m[1], SpanID: m[2], Flags: m[3]}
}
