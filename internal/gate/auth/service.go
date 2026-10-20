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
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Service is the auth module.
type Service struct {
	d *core.Deps

	rpOnce sync.Once
	wa     *webauthn.WebAuthn
	waErr  error

	// testHook, when set (tests only), runs at the named points of a flow.
	testHook func(point string)
	// pending tracks the post-commit SessionEnded publishers of RevokeAll.
	pending sync.WaitGroup
}

// Test hook points.
const (
	hookBeforeUse = "before-use" // recordUse, before the credential UPDATE
	hookAfterUse  = "after-use"  // recordUse, credential row locked
)

func (s *Service) hook(point string) {
	if s.testHook != nil {
		s.testHook(point)
	}
}

var (
	_ core.Passkeys = (*Service)(nil)
	_ core.StepUp   = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// JobPurge deletes expired sessions (hourly).
const JobPurge = "sessions.purge"

// Init registers the purge job, the co-sign purposes of passkey changes and
// the AccountDeleted clean-up.
func (s *Service) Init(ctx context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error { return s.purgeExpired(ctx) })
		s.d.Jobs.Every(JobPurge, time.Hour)
	}
	s.registerPurposes()
	if s.d.Bus != nil {
		base := context.WithoutCancel(ctx)
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.forget(base, e.ID) })
	}
	return nil
}

// forget deletes a deleted account's sessions and credentials (plan §4.7:
// every owner deletes its own rows on AccountDeleted).
func (s *Service) forget(ctx context.Context, user core.UserID) {
	evs, err := endSessions(ctx, s.d.DB.Q(), sqlEndUser, int64(user))
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
	// Genesis only: the terms version to accept and the minimum age to attest.
	TOSVersion int `json:"tos_version,omitempty"`
	MinAge     int `json:"min_age,omitempty"`
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

// Repeated step-ups (§17.2(5)): RepeatMin step-ups of one account within
// RepeatWindow put a stepup-repeat item on the moderation queue (it merges
// into the account's open item of that source).
const (
	RepeatMin    = 3
	RepeatWindow = 7 * 24 * time.Hour
)

// Require marks session (of user) as needing a passkey step-up for reason (a
// code, e.g. the automation signal kinds that crossed the threshold): every
// request of that session then answers step_up_required until the person
// verifies with a passkey (StepUpFinish). It records the step-up (stepups);
// the RepeatMin-th within RepeatWindow goes to moderators. A session that
// already awaits a step-up, or does not exist, is left as it is. Signals never
// do more than this: no ban, no sign-out.
func (s *Service) Require(ctx context.Context, user core.UserID, session [32]byte, reason string) error {
	now := s.d.Clock.Now()
	var set bool
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sessions SET stepup_required = true
			WHERE id_hash = $1 AND user_id = $2 AND kind = 1 AND NOT stepup_required AND expires_at > $3`,
			session[:], int64(user), now)
		if err != nil || tag.RowsAffected() != 1 {
			return err
		}
		set = true
		if _, err := tx.Exec(ctx, `INSERT INTO stepups (user_id, session_hash, required_at, reason) VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`, int64(user), session[:], now, reason); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM stepups WHERE user_id = $1 AND required_at > $2",
			int64(user), now.Add(-RepeatWindow)).Scan(&n); err != nil {
			return err
		}
		if n < RepeatMin || s.d.ModQueue == nil {
			return nil
		}
		return s.d.ModQueue.Enqueue(ctx, tx, core.ModQueueItem{Source: core.ModSrcStepUpRepeat, Priority: 3,
			SubjectUser: user, Summary: map[string]int{"stepups": n}})
	})
	if err != nil || !set {
		return err
	}
	s.d.Bus.Publish(core.SessionStepUp{User: user, Session: session})
	return nil
}
