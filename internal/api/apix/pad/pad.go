// Package pad is the P1 timing-padding middleware (fidelity #8, security #19):
// it rounds the total handler time UP to the next multiple of the route's bucket
// for success, Problem and 404 alike, so found and not-found are indistinguishable
// by latency. apix.Route installs it on every route with a {ref}/{hs}/{id}/{snapshot}
// parameter.
//
// Owner: P1-W07. Scaffold stub: pass-through (Phase 0 has no timing padding).
package pad

import (
	"net/http"
	"time"
)

// Buckets (plan §5.1).
const (
	DefaultBucket = 150 * time.Millisecond
	DeepBucket    = 600 * time.Millisecond
)

// Middleware pads handler time to bucket (scaffold: pass-through).
func Middleware(bucket time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}
