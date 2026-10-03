// Package auth owns sessions, CSRF, passkeys (WebAuthn), step-up and enrollment
// (ARCHITECTURE §17.2, §10.3, §13). It implements core.Passkeys and (P1) core.StepUp.
//
// Owner: P0-W01 → P1-W07.
//
// Sessions are opaque cookie tokens stored only as sha256 (sessions.id_hash).
// Every privilege change (login, enrollment, join) deletes the old row and
// issues a new token with a new CSRF seed. A session holds at most one pending
// WebAuthn ceremony (2 minutes, single use, cleared on any finish attempt).
package auth

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the auth module.
type Service struct {
	d *core.Deps

	rpOnce sync.Once
	wa     *webauthn.WebAuthn
	waErr  error
}

var (
	_ core.Passkeys = (*Service)(nil)
	_ core.StepUp   = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// JobPurge deletes expired sessions (hourly).
const JobPurge = "sessions.purge"

// Init registers the purge job and the AccountDeleted clean-up.
func (s *Service) Init(ctx context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error { return s.purgeExpired(ctx) })
		s.d.Jobs.Every(JobPurge, time.Hour)
	}
	if s.d.Bus != nil {
		base := context.WithoutCancel(ctx)
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.forget(base, e.ID) })
	}
	return nil
}

// forget deletes a deleted account's sessions and credentials (plan §4.7:
// every owner deletes its own rows on AccountDeleted).
func (s *Service) forget(ctx context.Context, user core.UserID) {
	evs, err := endSessions(ctx, s.d.DB.Q(), "user_id = $1", int64(user))
	if err == nil {
		s.publishEnded(evs)
	}
	_, _ = s.d.DB.Q().Exec(ctx, "DELETE FROM webauthn_credentials WHERE user_id = $1", int64(user))
}

// PasskeyView is one of the viewer's passkeys.
type PasskeyView struct {
	ID         gate.Ref   `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Current    bool       `json:"current"`
}

// BeginView starts a ceremony. Token, when set, is a new session cookie the
// handler must send (it never appears in the JSON).
type BeginView struct {
	Token   string          `json:"-"`
	Options json.RawMessage `json:"options"`
	CSRF    string          `json:"csrf,omitempty"`
}

// EnrollView is the enrollment ceremony start.
type EnrollView struct {
	Token            string          `json:"-"`
	Options          json.RawMessage `json:"options"`
	CSRF             string          `json:"csrf,omitempty"`
	Purpose          string          `json:"purpose"` // "genesis" | "recovery"
	ExistingPasskeys []PasskeyView   `json:"existing_passkeys,omitempty"`
}

// signedIn returns the signed-in viewer, also while a P1 step-up is pending
// (the step-up ceremony itself must remain reachable).
func signedIn(ctx context.Context) (core.Viewer, error) {
	v, ok := core.ViewerFrom(ctx)
	if !ok || v.Kind != core.SessUser || v.ID == 0 {
		return core.Viewer{}, gate.ErrNotFound
	}
	return v, nil
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
