package server

import (
	"net/http"
)

// MaxBodyBytes wraps a handler to limit the size of request bodies.
// Requests exceeding the limit receive 413 Request Entity Too Large.
func MaxBodyBytes(maxBytes int64, next http.Handler) http.Handler {
	if maxBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}
