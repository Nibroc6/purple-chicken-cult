// Package auth owns sessions, CSRF, passkeys (WebAuthn), step-up and enrollment
// (ARCHITECTURE §17.2, §10.3, §13). It implements core.Passkeys and (P1) core.StepUp.
//
// Owner: P0-W01 → P1-W07. The scaffold wrote a WORKING session-lookup Middleware,
// APIGuard (fetch metadata, exact Origin, CSRF from csrf_seed), guest sessions,
// Session, Logout, IssueEnrollment, RevokeAll and SeedGenesis; every WebAuthn
// ceremony returns gate.ErrNotImplemented until W01 lands.
package auth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the auth module.
type Service struct{ d *core.Deps }

var (
	_ core.Passkeys = (*Service)(nil)
	_ core.StepUp   = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers jobs (sessions.purge).
func (s *Service) Init(ctx context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle("sessions.purge", func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.d.DB.Q().Exec(ctx, "DELETE FROM sessions WHERE expires_at < $1", s.d.Clock.Now())
			return err
		})
		s.d.Jobs.Every("sessions.purge", time.Hour)
	}
	return nil
}

// PasskeyView is one of the viewer's passkeys.
type PasskeyView struct {
	ID         gate.Ref   `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Current    bool       `json:"current"`
}

// EnrollView is the enrollment ceremony start.
type EnrollView struct {
	Options          json.RawMessage `json:"options"`
	Purpose          string          `json:"purpose"` // "genesis" | "recovery"
	ExistingPasskeys []PasskeyView   `json:"existing_passkeys,omitempty"`
}

// ---- handler-facing API (P0-W01) ----

// LoginBegin starts a discoverable passkey login in a fresh guest session.
func (s *Service) LoginBegin(ctx context.Context) (string, json.RawMessage, error) {
	return "", nil, gate.ErrNotImplemented
}

// LoginFinish verifies the assertion and returns a NEW user session token.
func (s *Service) LoginFinish(ctx context.Context, body []byte) (string, gate.MeView, error) {
	return "", gate.MeView{}, gate.ErrNotImplemented
}

// StepUpBegin starts a UV assertion over the viewer's own credentials.
func (s *Service) StepUpBegin(ctx context.Context) (json.RawMessage, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return nil, err
	}
	return nil, gate.ErrNotImplemented
}

// StepUpFinish verifies the step-up and sets auth_at.
func (s *Service) StepUpFinish(ctx context.Context, body []byte) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// Passkeys lists the viewer's passkeys.
func (s *Service) Passkeys(ctx context.Context) ([]PasskeyView, error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return nil, err
	}
	return nil, gate.ErrNotImplemented
}

// AddPasskeyBegin starts adding a passkey (RequireFresh).
func (s *Service) AddPasskeyBegin(ctx context.Context) (json.RawMessage, error) {
	if _, err := core.RequireFresh(ctx, s.d.Clock.Now()); err != nil {
		return nil, err
	}
	return nil, gate.ErrNotImplemented
}

// AddPasskeyFinish finishes adding a passkey (RequireFresh).
func (s *Service) AddPasskeyFinish(ctx context.Context, body []byte, name string) error {
	if _, err := core.RequireFresh(ctx, s.d.Clock.Now()); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// RemovePasskey removes one of the viewer's passkeys (RequireFresh; never the last).
func (s *Service) RemovePasskey(ctx context.Context, id gate.Ref) error {
	if _, err := core.RequireFresh(ctx, s.d.Clock.Now()); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// EnrollBegin binds an enrollment token (from the URL fragment) to this session.
func (s *Service) EnrollBegin(ctx context.Context, token string) (EnrollView, error) {
	return EnrollView{}, gate.ErrNotImplemented
}

// EnrollFinish registers the passkey and returns a user session token.
func (s *Service) EnrollFinish(ctx context.Context, body []byte, removeOld bool, tosVersion int, ageAttested bool) (string, error) {
	return "", gate.ErrNotImplemented
}

// ---- core.Passkeys (P0-W01) ----

// BeginRegistration starts a WebAuthn registration in session sess.
func (s *Service) BeginRegistration(ctx context.Context, sess [32]byte, u core.PendingUser) (json.RawMessage, error) {
	return nil, gate.ErrNotImplemented
}

// FinishRegistration verifies a registration response.
func (s *Service) FinishRegistration(ctx context.Context, sess [32]byte, body []byte) (core.Credential, error) {
	return core.Credential{}, gate.ErrNotImplemented
}

// BindCredential stores c for user and writes the PasskeyChange record.
func (s *Service) BindCredential(ctx context.Context, tx store.Q, user core.UserID, c core.Credential) error {
	return gate.ErrNotImplemented
}

// Upgrade turns a guest session into a user session (rotating token and seed).
func (s *Service) Upgrade(ctx context.Context, tx store.Q, sess [32]byte, user core.UserID, cred []byte) (string, error) {
	return "", gate.ErrNotImplemented
}

// BeginAssertion starts an assertion over challenge, restricted to v's credentials.
func (s *Service) BeginAssertion(ctx context.Context, v core.Viewer, challenge []byte, purpose string) (json.RawMessage, error) {
	return nil, gate.ErrNotImplemented
}

// VerifyAssertion verifies an assertion (security #9 check list).
func (s *Service) VerifyAssertion(ctx context.Context, v core.Viewer, purpose string, body []byte) (core.Assertion, error) {
	return core.Assertion{}, gate.ErrNotImplemented
}

// ---- core.StepUp (P1-W07) ----

// Require marks a session as needing a passkey step-up.
func (s *Service) Require(ctx context.Context, user core.UserID, session [32]byte, reason string) error {
	_, err := s.d.DB.Q().Exec(ctx, "UPDATE sessions SET stepup_required = true WHERE id_hash = $1 AND user_id = $2",
		session[:], int64(user))
	if err != nil {
		return err
	}
	s.d.Bus.Publish(core.SessionStepUp{User: user, Session: session})
	return nil
}
