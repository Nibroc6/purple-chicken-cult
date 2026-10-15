package gate

import (
	"errors"
	"time"
)

// The closed set of gate failures (plan §0.8). A gate service returns a view,
// ErrNotFound, a *Problem about the caller's OWN input, ErrRateLimited, ErrStepUp
// or ErrDisabled. Nothing else. Handlers never write their own 403/404.
//
// Check order inside every service: charge (done by apix) → resolve every ref
// (ErrNotFound on any failure) → only then evaluate Problems that depend on the target.
var (
	// ErrNotFound is the ONE failure for missing / invisible / foreign / unauthenticated.
	ErrNotFound = errors.New("not_found")
	// ErrRateLimited is the uniform 429, charged regardless of outcome.
	ErrRateLimited = errors.New("rate_limited")
	// ErrStepUp asks the client to run a passkey step-up (401).
	ErrStepUp = errors.New("step_up_required")
	// ErrDisabled is a fail-closed feature (media, push): 409.
	ErrDisabled = errors.New("feature_disabled")
	// ErrNotImplemented is returned ONLY by scaffold stubs until the owning work
	// package lands (501 {"error":"not_implemented"}). No finished code returns it.
	ErrNotImplemented = errors.New("not_implemented")
)

// Problem statuses, so gate packages need not import net/http (archtest A4).
const (
	StatusBadRequest    = 400
	StatusConflict      = 409
	StatusUnprocessable = 422
)

// Problem is a 400/409/422 about the caller's own input. Code matches
// ^[a-z][a-z0-9_]*$ (archtest A7).
type Problem struct {
	Code   string
	Field  string
	Status int
}

// NewProblem builds a Problem. Code must be a literal snake_case code.
func NewProblem(status int, code string, field ...string) *Problem {
	p := &Problem{Code: code, Status: status}
	if len(field) > 0 {
		p.Field = field[0]
	}
	return p
}

func (p *Problem) Error() string { return p.Code }

// RateLimitedError is ErrRateLimited with a retry hint derived only from the
// viewer's own bucket.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string        { return ErrRateLimited.Error() }
func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited } //nolint:errorlint // sentinel identity

// RateLimited returns ErrRateLimited carrying a retry hint.
func RateLimited(retry time.Duration) error { return &RateLimitedError{RetryAfter: retry} }

// DisabledError is ErrDisabled naming the feature ("media", "push").
type DisabledError struct{ Feature string }

func (e *DisabledError) Error() string        { return ErrDisabled.Error() }
func (e *DisabledError) Is(target error) bool { return target == ErrDisabled } //nolint:errorlint // sentinel identity

// Disabled returns ErrDisabled naming feature.
func Disabled(feature string) error { return &DisabledError{Feature: feature} }

// NotFoundJSON is the exact body of the uniform not-found response (§13). Every
// path that answers "not found" (apix, auth's APIGuard, unknown routes) writes
// these bytes with status 404, so the responses are byte-identical.
const NotFoundJSON = `{"error":"not_found"}` + "\n"
