// Package apix is the handler adapter (plan §5.1, §5.2): route registration with a
// mandatory charge class, strict JSON decoding, the ONE error mapping (uniform
// not-found, Problems, 429, step-up, feature_disabled) and JSON responses. No
// handler writes its own status code for not-found or forbidden.
//
// Owner: SCAFFOLD (contract steward during phases); SCAFFOLD-1 wires apix/pad.
package apix

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apix/pad"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// HandlerFunc is a handler that returns an error instead of writing one.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Class is the request charge class.
type Class = gate.Class

// Read returns a read class (feed_page, tree_view, graph_read, post_read, media_full).
func Read(name string) Class { return Class{Kind: gate.ClassRead, Name: name} }

// Write returns a write class (post, like, friend_request, report, social, account,
// invite_handshake, save, media).
func Write(name string) Class { return Class{Kind: gate.ClassWrite, Name: name} }

// Anon returns an unauthenticated-surface class (login, join, enroll, csp_report),
// charged per client network prefix.
func Anon(name string) Class { return Class{Kind: gate.ClassAnon, Name: name} }

// Free is the reviewed free class (/session, /events, /seen, /notifications/read, …).
var Free = Class{Kind: gate.ClassFree, Name: "free"}

// Charger charges requests (implemented by *limits.Service).
type Charger interface {
	ChargeRequest(ctx context.Context, c gate.Class) error
}

type chargerKey struct{}

// WithCharger installs c for every route below (router installs it once).
func WithCharger(c Charger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chargerKey{}, c)))
		})
	}
}

// RouteInfo describes a registered route (router_test, archtest A12).
type RouteInfo struct {
	Method, Pattern string
	Class           Class
	Padded          bool
}

var (
	regMu    sync.Mutex
	registry = map[string]RouteInfo{}
)

// Routes returns every route registered through Route, sorted.
func Routes() []RouteInfo {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]RouteInfo, 0, len(registry))
	for _, ri := range registry {
		out = append(out, ri)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern != out[j].Pattern {
			return out[i].Pattern < out[j].Pattern
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// PaddedParams are the path parameters that make a route timing-padded in P1.
var PaddedParams = pad.Params

// Route registers h for method + pattern (relative to /api) with charge class c.
// The charge happens before the handler and before any ref is resolved (D19).
// Never call r.Route / r.Mount / r.Group with a shared prefix.
func Route(r chi.Router, method, pattern string, c Class, h HandlerFunc) {
	if c.Kind == 0 || c.Name == "" {
		panic(errors.New("apix: route " + method + " " + pattern + " has no charge class"))
	}
	// The padded set is derived by apix/pad (plan §5.1): every route with a
	// {ref}/{hs}/{id}/{snapshot} parameter or a ref in its request, the
	// deep-tier feed/tree routes on their own grid (contract request p1-w07-1).
	grid, padded := pad.For(method, pattern)
	regMu.Lock()
	registry[method+" "+pattern] = RouteInfo{Method: method, Pattern: pattern, Class: c, Padded: padded}
	regMu.Unlock()
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if ch, ok := req.Context().Value(chargerKey{}).(Charger); ok && ch != nil {
			if err := ch.ChargeRequest(req.Context(), c); err != nil {
				Error(w, req, err)
				return
			}
		}
		if err := h(w, req); err != nil {
			Error(w, req, err)
		}
	})
	if padded {
		handler = pad.Wrap(method+" "+pattern, grid)(handler)
	}
	r.Method(method, pattern, handler)
}

// Decode strictly decodes a JSON body of at most max bytes (DisallowUnknownFields).
// Any failure is Problem bad_request (400).
func Decode[T any](r *http.Request, max int64) (T, error) {
	var v T
	if max <= 0 {
		max = 16 << 10
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, max+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	if dec.More() {
		return v, gate.NewProblem(gate.StatusBadRequest, "bad_request")
	}
	return v, nil
}

// JSON writes v with status (no-store; every API response).
func JSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if status == http.StatusNoContent || v == nil {
		return nil
	}
	return json.NewEncoder(w).Encode(v)
}

// NoContent writes 204.
func NoContent(w http.ResponseWriter) error { return JSON(w, http.StatusNoContent, nil) }

// Ref returns the chi URL parameter name as a gate.Ref.
func Ref(r *http.Request, name string) gate.Ref { return gate.Ref(chi.URLParam(r, name)) }

// NotFound writes the uniform not-found response (byte-identical everywhere).
func NotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, gate.NotFoundJSON)
}

// NoStore marks every /api response uncacheable.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type errBody struct {
	Error       string `json:"error"`
	Field       string `json:"field,omitempty"`
	RetryAfterS int    `json:"retry_after_s,omitempty"`
	Feature     string `json:"feature,omitempty"`
}

// Error maps a gate result to the one response table of plan §5.1.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	var (
		p  *gate.Problem
		rl *gate.RateLimitedError
		de *gate.DisabledError
	)
	switch {
	case errors.Is(err, gate.ErrNotFound):
		NotFound(w, r)
	case errors.As(err, &p):
		status := p.Status
		if status != gate.StatusBadRequest && status != gate.StatusConflict && status != gate.StatusUnprocessable {
			status = gate.StatusBadRequest
		}
		_ = JSON(w, status, errBody{Error: p.Code, Field: p.Field})
	case errors.Is(err, gate.ErrRateLimited):
		retry := 60
		if errors.As(err, &rl) {
			retry = int(math.Ceil(rl.RetryAfter.Seconds()))
		}
		_ = JSON(w, http.StatusTooManyRequests, errBody{Error: "rate_limited", RetryAfterS: max(retry, 1)})
	case errors.Is(err, gate.ErrStepUp):
		_ = JSON(w, http.StatusUnauthorized, errBody{Error: "step_up_required"})
	case errors.Is(err, gate.ErrDisabled):
		feature := ""
		if errors.As(err, &de) {
			feature = de.Feature
		}
		_ = JSON(w, http.StatusConflict, errBody{Error: "feature_disabled", Feature: feature})
	case errors.Is(err, gate.ErrNotImplemented):
		_ = JSON(w, http.StatusNotImplemented, errBody{Error: "not_implemented"})
	default:
		slog.Error("handler", "req_id", httpx.ReqID(r.Context()), "code", "internal", "err", err.Error())
		_ = JSON(w, http.StatusInternalServerError, errBody{Error: "internal"})
	}
}
