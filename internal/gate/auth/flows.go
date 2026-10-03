package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Enrollment token purposes (enrollment_tokens.purpose).
const (
	purposeGenesis  uint8 = 0
	purposeRecovery uint8 = 1
)

// ---- login (security #11) ----

// LoginBegin starts a discoverable passkey login (conditional mediation) in a
// NEW guest session that holds the challenge; the caller's previous session, if
// any, ends.
func (s *Service) LoginBegin(ctx context.Context) (BeginView, error) {
	wa, err := s.rp()
	if err != nil {
		return BeginView{}, err
	}
	assertion, data, err := wa.BeginDiscoverableMediatedLogin(protocol.MediationConditional,
		webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return BeginView{}, err
	}
	cer, err := marshalCeremony(ceremony{Purpose: purposeLogin, Data: *data, Exp: s.d.Clock.Now().Add(CeremonyTTL)})
	if err != nil {
		return BeginView{}, err
	}
	opts, err := json.Marshal(assertion)
	if err != nil {
		return BeginView{}, err
	}
	var (
		tok   string
		ended []core.SessionEnded
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if ended, err = endCaller(ctx, tx); err != nil {
			return err
		}
		tok, err = s.issue(ctx, tx, core.SessGuest, 0, time.Time{}, nil, cer)
		return err
	})
	if err != nil {
		return BeginView{}, err
	}
	s.publishEnded(ended)
	sv, err := s.sessionFor(ctx, tok)
	if err != nil {
		return BeginView{}, err
	}
	return BeginView{Token: tok, Options: opts, CSRF: sv.CSRF}, nil
}

// LoginFinish verifies the login assertion. The ceremony is cleared first (any
// outcome); on success the guest session is deleted and a NEW user token is
// issued (no fixation). A bad assertion, an unknown credential and an account
// that is suspended, deleted or pending enrollment all give the same uniform 404.
func (s *Service) LoginFinish(ctx context.Context, body []byte) (string, gate.SessionView, error) {
	si, ok := sessFrom(ctx)
	if !ok || si.kind != core.SessGuest {
		return "", gate.SessionView{}, gate.ErrNotFound
	}
	c, err := s.takeCeremony(ctx, si.idHash, purposeLogin)
	if err != nil {
		return "", gate.SessionView{}, gate.ErrNotFound
	}
	wa, err := s.rp()
	if err != nil {
		return "", gate.SessionView{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return "", gate.SessionView{}, gate.ErrNotFound
	}
	var owner account
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		var uid int64
		if err := s.d.DB.Q().QueryRow(ctx, "SELECT user_id FROM webauthn_credentials WHERE id = $1", rawID).Scan(&uid); err != nil {
			return nil, errCeremony
		}
		a, err := s.loadAccount(ctx, s.d.DB.Q(), core.UserID(uid), false) //nolint:gosec // users.id < 2^32 (DB CHECK)
		if err != nil || !bytes.Equal(a.webauthnID, userHandle) {
			return nil, errCeremony
		}
		owner = a
		return s.accountUser(ctx, s.d.DB.Q(), a.id)
	}
	_, cred, err := wa.ValidatePasskeyLogin(handler, c.Data, parsed)
	if err != nil || s.checkAssertion(parsed, cred) != nil || owner.status != core.StatusActive {
		return "", gate.SessionView{}, gate.ErrNotFound
	}
	use := assertionResult{user: owner.id, credID: cred.ID, signCount: cred.Authenticator.SignCount,
		backup: parsed.Response.AuthenticatorData.Flags.HasBackupState(), parsed: parsed}
	var tok string
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// Credential row locked from here to commit (see recordUse).
		if err := s.recordUse(ctx, tx, use); err != nil {
			if errors.Is(err, errCredentialGone) {
				return gate.ErrNotFound
			}
			return err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM sessions WHERE id_hash = $1 AND kind = $2", si.idHash[:], int16(core.SessGuest))
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return gate.ErrNotFound
		}
		tok, err = s.issue(ctx, tx, core.SessUser, owner.id, s.d.Clock.Now(), cred.ID, nil)
		return err
	})
	if err != nil {
		return "", gate.SessionView{}, err
	}
	sv, err := s.sessionFor(ctx, tok)
	return tok, sv, err
}

// ---- step-up (D27) ----

// StepUpBegin starts a UV assertion over the viewer's OWN credentials.
func (s *Service) StepUpBegin(ctx context.Context) (json.RawMessage, error) {
	v, err := signedIn(ctx)
	if err != nil {
		return nil, err
	}
	return s.BeginAssertion(ctx, v, nil, purposeStepUp)
}

// StepUpFinish verifies the step-up, sets auth_at and clears stepup_required.
func (s *Service) StepUpFinish(ctx context.Context, body []byte) error {
	v, err := signedIn(ctx)
	if err != nil {
		return err
	}
	// One tx: the credential row stays locked until auth_at is set, so a passkey
	// removed concurrently never leaves a freshly stepped-up session behind.
	_, err = s.verifyAssertion(ctx, v, purposeStepUp, body, func(use assertionResult) error {
		return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			if err := s.recordUse(ctx, tx, use); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE sessions SET auth_at = $2, stepup_required = false WHERE id_hash = $1",
				v.Session[:], s.d.Clock.Now())
			return err
		})
	})
	return err
}

// ---- passkeys ----

// Passkeys lists the viewer's passkeys, oldest first.
func (s *Service) Passkeys(ctx context.Context) ([]PasskeyView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentials(ctx, s.d.DB.Q(), v.ID, false)
	if err != nil {
		return nil, err
	}
	return s.views(v.ID, creds, v.Credential), nil
}

func (s *Service) views(user core.UserID, creds []storedCred, current []byte) []PasskeyView {
	out := make([]PasskeyView, 0, len(creds))
	for _, c := range creds {
		out = append(out, PasskeyView{ID: s.d.Refs.Cred(user, c.id), Name: c.name, CreatedAt: c.createdAt,
			LastUsedAt: c.lastUsedAt, Current: len(current) > 0 && bytes.Equal(c.id, current)})
	}
	return out
}

// AddPasskeyBegin starts adding a passkey: fresh step-up required;
// excludeCredentials lists the viewer's credentials.
func (s *Service) AddPasskeyBegin(ctx context.Context) (json.RawMessage, error) {
	v, err := core.RequireFresh(ctx, s.d.Clock.Now())
	if err != nil {
		return nil, err
	}
	a, err := s.loadAccount(ctx, s.d.DB.Q(), v.ID, false)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentials(ctx, s.d.DB.Q(), v.ID, false)
	if err != nil {
		return nil, err
	}
	return s.beginRegistration(ctx, nil, v.Session, purposeAdd, v.ID, pendingUser(a, creds))
}

// cleanName validates a passkey label: trimmed, at most MaxPasskeyName code
// points, printable. Problem passkey_name_invalid.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxPasskeyName {
		return "", gate.NewProblem(gate.StatusUnprocessable, "passkey_name_invalid", "name")
	}
	for _, r := range name {
		if !unicode.IsGraphic(r) {
			return "", gate.NewProblem(gate.StatusUnprocessable, "passkey_name_invalid", "name")
		}
	}
	return name, nil
}

// AddPasskeyFinish registers the new passkey (fresh step-up required), signs
// its PasskeyChange record and notifies friends (friend_security).
func (s *Service) AddPasskeyFinish(ctx context.Context, body []byte, name string) error {
	v, err := core.RequireFresh(ctx, s.d.Clock.Now())
	if err != nil {
		return err
	}
	if name, err = cleanName(name); err != nil {
		return err
	}
	cred, c, err := s.finishRegistration(ctx, v.Session, purposeAdd, body)
	if errors.Is(err, errCeremony) || (err == nil && c.User != int64(v.ID)) {
		return rejected()
	}
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.bindCredential(ctx, tx, v.ID, cred, name); err != nil {
			if errors.Is(err, gate.ErrNotFound) {
				return rejected()
			}
			return err
		}
		return s.notifyFriends(ctx, tx, v.ID, reasonPasskeyAdded)
	})
}

// RemovePasskey removes one of the viewer's passkeys (fresh step-up required),
// never the last one (Problem last_passkey), and ends every session that
// credential authenticated.
func (s *Service) RemovePasskey(ctx context.Context, id gate.Ref) error {
	v, err := core.RequireFresh(ctx, s.d.Clock.Now())
	if err != nil {
		return err
	}
	credID, ok := s.d.Refs.OpenCred(v.ID, id)
	if !ok {
		return gate.ErrNotFound
	}
	var ended []core.SessionEnded
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		creds, err := s.credentials(ctx, tx, v.ID, true)
		if err != nil {
			return err
		}
		var target *storedCred
		for i := range creds {
			if bytes.Equal(creds[i].id, credID) {
				target = &creds[i]
			}
		}
		if target == nil {
			return gate.ErrNotFound
		}
		if len(creds) < 2 {
			return gate.NewProblem(gate.StatusConflict, "last_passkey")
		}
		if _, err := tx.Exec(ctx, "DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2", credID, int64(v.ID)); err != nil {
			return err
		}
		if err := s.signChange(ctx, tx, v.ID, opRemove, target.id, target.publicKey, target.alg, s.d.Clock.Now()); err != nil {
			return err
		}
		if ended, err = endSessions(ctx, tx, sqlEndCredential, int64(v.ID), credID); err != nil {
			return err
		}
		return s.notifyFriends(ctx, tx, v.ID, reasonPasskeyRemoved)
	})
	if err != nil {
		return err
	}
	s.publishEnded(ended)
	return nil
}

// ---- enrollment (§10.3, §10.8; security #13) ----

// tokenRow is a usable enrollment token.
type tokenRow struct {
	hash    [32]byte
	user    core.UserID
	purpose uint8
	bound   []byte
}

// usableToken loads an unused, uncancelled enrollment token inside its window.
func (s *Service) usableToken(ctx context.Context, q store.Q, where string, arg any, lock bool) (tokenRow, error) {
	tail := ` AND used_at IS NULL AND cancelled_at IS NULL AND usable_from <= $2 AND expires_at > $2`
	if lock {
		tail = ` AND used_at IS NULL AND cancelled_at IS NULL AND usable_from <= $2 AND expires_at > $2 FOR UPDATE`
	}
	sql := `SELECT token_hash, user_id, purpose, bound_session FROM enrollment_tokens
		WHERE ` + where + tail
	var (
		r       tokenRow
		h       []byte
		uid     int64
		purpose int16
	)
	if err := q.QueryRow(ctx, sql, arg, s.d.Clock.Now()).Scan(&h, &uid, &purpose, &r.bound); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r, gate.ErrNotFound
		}
		return r, err
	}
	copy(r.hash[:], h)
	r.user, r.purpose = core.UserID(uid), uint8(purpose) //nolint:gosec // users.id < 2^32; purpose is 0..1
	return r, nil
}

// accountReady reports whether the account may enroll for the token's purpose:
// genesis accounts are pending enrollment, recovering accounts are active.
func accountReady(a account, purpose uint8) bool {
	switch purpose {
	case purposeGenesis:
		return a.status == core.StatusPendingEnrollment
	case purposeRecovery:
		return a.status == core.StatusActive
	}
	return false
}

func purposeName(p uint8) string {
	if p == purposeRecovery {
		return "recovery"
	}
	return "genesis"
}

// EnrollBegin redeems the enrollment token from the URL fragment (POSTed by the
// SPA, never in a URL) and binds it to an enrollment session: the caller's own
// session when it is already bound to this token, otherwise a NEW enrollment
// session (the token must not be bound to another live session). It returns the
// registration options and, for recovery, the existing passkeys.
func (s *Service) EnrollBegin(ctx context.Context, token string) (EnrollView, error) {
	if !wellFormed(token) {
		return EnrollView{}, gate.ErrNotFound
	}
	h := hashToken(token)
	var (
		view  EnrollView
		ended []core.SessionEnded
	)
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		t, err := s.usableToken(ctx, tx, "token_hash = $1", h[:], true)
		if err != nil {
			return err
		}
		a, err := s.loadAccount(ctx, tx, t.user, false)
		if err != nil {
			return err
		}
		if !accountReady(a, t.purpose) {
			return gate.ErrNotFound
		}
		sess, err := s.bindEnrollment(ctx, tx, t, &view, &ended)
		if err != nil {
			return err
		}
		creds, err := s.credentials(ctx, tx, t.user, false)
		if err != nil {
			return err
		}
		if view.Options, err = s.beginRegistration(ctx, tx, sess, purposeEnroll, t.user, pendingUser(a, creds)); err != nil {
			return err
		}
		view.Purpose = purposeName(t.purpose)
		if t.purpose == purposeGenesis {
			view.TOSVersion, view.MinAge = s.d.Cfg.TOSVersion, s.d.Cfg.MinAge
		}
		if t.purpose == purposeRecovery {
			view.ExistingPasskeys = s.views(t.user, creds, nil)
		}
		return nil
	})
	if err != nil {
		return EnrollView{}, err
	}
	s.publishEnded(ended)
	if view.Token != "" {
		sv, err := s.sessionFor(ctx, view.Token)
		if err != nil {
			return EnrollView{}, err
		}
		view.CSRF = sv.CSRF
	} else if sv, err := s.Session(ctx); err == nil {
		view.CSRF = sv.CSRF
	}
	return view, nil
}

// bindEnrollment picks the enrollment session for token t (see EnrollBegin).
func (s *Service) bindEnrollment(ctx context.Context, tx pgx.Tx, t tokenRow, view *EnrollView,
	ended *[]core.SessionEnded) ([32]byte, error) {
	si, ok := sessFrom(ctx)
	if ok && si.kind == core.SessEnrollment && t.bound != nil && bytes.Equal(t.bound, si.idHash[:]) {
		return si.idHash, nil
	}
	if t.bound != nil {
		var live bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM sessions WHERE id_hash = $1 AND expires_at > $2)",
			t.bound, s.d.Clock.Now()).Scan(&live); err != nil {
			return [32]byte{}, err
		}
		if live {
			return [32]byte{}, gate.ErrNotFound
		}
	}
	evs, err := endCaller(ctx, tx)
	if err != nil {
		return [32]byte{}, err
	}
	*ended = evs
	tok, err := s.issue(ctx, tx, core.SessEnrollment, t.user, time.Time{}, nil, nil)
	if err != nil {
		return [32]byte{}, err
	}
	sess := hashToken(tok)
	if _, err := tx.Exec(ctx, "UPDATE enrollment_tokens SET bound_session = $2 WHERE token_hash = $1",
		t.hash[:], sess[:]); err != nil {
		return [32]byte{}, err
	}
	view.Token = tok
	return sess, nil
}

// EnrollFinish registers the enrollment passkey from the bound session, marks
// the token used atomically and returns a NEW user session. Genesis records the
// terms version and the age attestation (Problems tos_required, age_required);
// recovery ends every session of the account, optionally removes the old
// passkeys and notifies friends.
func (s *Service) EnrollFinish(ctx context.Context, body []byte, removeOld bool, tosVersion int, ageAttested bool) (string, gate.SessionView, error) {
	si, ok := sessFrom(ctx)
	if !ok || si.kind != core.SessEnrollment {
		return "", gate.SessionView{}, gate.ErrNotFound
	}
	t, err := s.usableToken(ctx, s.d.DB.Q(), "bound_session = $1", si.idHash[:], false)
	if err != nil {
		return "", gate.SessionView{}, err
	}
	if t.purpose == purposeGenesis {
		if tosVersion != s.d.Cfg.TOSVersion {
			return "", gate.SessionView{}, gate.NewProblem(gate.StatusUnprocessable, "tos_required", "tos_version")
		}
		if !ageAttested {
			return "", gate.SessionView{}, gate.NewProblem(gate.StatusUnprocessable, "age_required", "age_attested")
		}
	}
	cred, c, err := s.finishRegistration(ctx, si.idHash, purposeEnroll, body)
	if err != nil || c.User != int64(t.user) {
		if err != nil && !errors.Is(err, errCeremony) {
			return "", gate.SessionView{}, err
		}
		return "", gate.SessionView{}, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var (
		tok   string
		ended []core.SessionEnded
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE enrollment_tokens SET used_at = $3 WHERE token_hash = $1 AND bound_session = $2
			AND used_at IS NULL AND cancelled_at IS NULL AND usable_from <= $3 AND expires_at > $3`,
			t.hash[:], si.idHash[:], now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return gate.ErrNotFound
		}
		a, err := s.loadAccount(ctx, tx, t.user, true)
		if err != nil {
			return err
		}
		if !accountReady(a, t.purpose) {
			return gate.ErrNotFound
		}
		switch t.purpose {
		case purposeGenesis:
			if err := s.activateGenesis(ctx, tx, t.user, tosVersion, now); err != nil {
				return err
			}
		case purposeRecovery:
			// Lock (or remove) the old credentials BEFORE ending the sessions: a
			// login racing with an old passkey holds its credential row lock
			// (recordUse) until it commits, so either it commits first and its
			// new session is visible to the DELETE below, or it waits and then
			// finds the credential gone (removeOld) or logs in after recovery.
			if removeOld {
				if err := s.removeAll(ctx, tx, t.user, now); err != nil {
					return err
				}
			} else if _, err := s.credentials(ctx, tx, t.user, true); err != nil {
				return err
			}
			if ended, err = endSessions(ctx, tx, sqlEndUser, int64(t.user)); err != nil {
				return err
			}
		}
		if err := s.bindCredential(ctx, tx, t.user, cred, ""); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE id_hash = $1", si.idHash[:]); err != nil {
			return err
		}
		if tok, err = s.issue(ctx, tx, core.SessUser, t.user, now, cred.ID, nil); err != nil {
			return err
		}
		if t.purpose == purposeRecovery {
			return s.notifyFriends(ctx, tx, t.user, reasonRecovered)
		}
		return nil
	})
	if err != nil {
		return "", gate.SessionView{}, err
	}
	s.publishEnded(ended)
	s.d.Bus.Publish(core.UserChanged{ID: t.user})
	sv, err := s.sessionFor(ctx, tok)
	return tok, sv, err
}

// removeAll deletes every credential of user, signing a remove record for each.
func (s *Service) removeAll(ctx context.Context, tx store.Q, user core.UserID, at time.Time) error {
	creds, err := s.credentials(ctx, tx, user, true)
	if err != nil {
		return err
	}
	for _, c := range creds {
		if _, err := tx.Exec(ctx, "DELETE FROM webauthn_credentials WHERE id = $1", c.id); err != nil {
			return err
		}
		if err := s.signChange(ctx, tx, user, opRemove, c.id, c.publicKey, c.alg, at); err != nil {
			return err
		}
	}
	return nil
}
