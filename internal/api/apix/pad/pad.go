// Package pad is the P1 timing-padding middleware (fidelity #8, security #19):
// it rounds the total handler time UP to the next point of the route's grid
// for success, Problem and 404 alike, so found and not-found are indistinguishable
// by latency. apix.Route installs it on every route For reports.
//
// Owner: P1-W07. Steward stub (contract request p1-w07-1): For derives the set
// from the path parameters only and Wrap passes through.
package pad

import (
	"net/http"
	"strings"
	"time"
)

// Buckets (plan §5.1).
const (
	DefaultBucket = 150 * time.Millisecond
	DeepBucket    = 600 * time.Millisecond
)

// Params are the path parameters that make a route padded.
var Params = []string{"{ref}", "{hs}", "{id}", "{snapshot}"}

// Grid is the set of response times a padded route can take.
type Grid struct {
	Bucket time.Duration
	Slack  time.Duration
}

// Default is the grid of ordinary padded routes.
var Default = Grid{Bucket: DefaultBucket}

// For reports whether method+pattern is padded and on which grid.
func For(method, pattern string) (Grid, bool) {
	for _, p := range Params {
		if strings.Contains(pattern, p) {
			return Default, true
		}
	}
	return Grid{}, false
}

// Wrap pads the route named route to grid g (stub: pass-through).
func Wrap(route string, g Grid) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// Middleware pads handler time to bucket (stub: pass-through).
func Middleware(bucket time.Duration) func(http.Handler) http.Handler {
	return Wrap("", Grid{Bucket: bucket})
}
