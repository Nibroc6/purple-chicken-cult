package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc"
)

// Device Bound Session Credentials (ARCHITECTURE §17.2(2), plan P1-W07,
// security #15). With PCC_DBSC=1 every unbound user session is offered a
// binding (Sec-Session-Registration on /api/session and on the answers of
// login and enrollment). A browser with a device key registers it: the session
// becomes bound, long-lived (30 d / 14 d idle like Phase 0) and from then on
// needs, on every request, the 10-minute bound cookie that only a refresh
// signed by the device key renews. Its long-lived cookie is accepted at the
// refresh endpoint and nowhere else, and a bound session never goes back to
// unbound. Challenges are single use (cleared when consumed, whatever the
// outcome) and live 5 minutes; every JWT names the exact endpoint URL (aud).
// Browsers without DBSC keep the short session of ShortTTL.

// ChallengeTTL bounds a registration offer or a refresh challenge.
const ChallengeTTL = 5 * time.Minute

type refreshKey struct{}

// dbscPath reports whether path is one of the DBSC endpoints.
func dbscPath(path string) bool { return path == dbsc.RegisterPath || path == dbsc.RefreshPath }

// DBSCView is the answer of registration and refresh: the new bound cookie
// (Token, never in JSON) and the session configuration document; or, for a
// refresh without a proof, the challenge header to send with a 401.
type DBSCView struct {
	Token     string          `json:"-"`
	Challenge string          `json:"-"`
	Config    json.RawMessage `json:"-"`
}

// sessionID is the DBSC session identifier of a session (not its hash).
func (s *Service) sessionID(idHash [32]byte) string {
	m := hmac.New(sha256.New, s.d.Keys.CSRF)
	m.Write([]byte("pcc-dbsc-sid"))
	m.Write(idHash[:])
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12])
}

func (s *Service) config(idHash [32]byte) (json.RawMessage, error) {
	return json.Marshal(dbsc.SessionConfig(s.d.Cfg.Origin, s.sessionID(idHash), s.secure()))
}

func newChallenge() ([]byte, error) {
	c := make([]byte, 32)
	_, err := rand.Read(c)
	return c, err
}

// DBSCEnabled reports whether device-bound sessions are offered.
func (s *Service) DBSCEnabled() bool { return s.d.Cfg != nil && s.d.Cfg.DBSCEnabled() }

// DBSCOffer returns the Sec-Session-Registration header value offering the
// user session identified by token (the caller's own when token is empty) a
// device binding, or "" when there is nothing to offer (DBSC off, no unbound
// user session). A pending offer younger than ChallengeTTL is repeated, so a
// browser that is registering never races a newer challenge.
func (s *Service) DBSCOffer(ctx context.Context, token string) string {
	if !s.DBSCEnabled() {
		return ""
	}
	var idHash [32]byte
	if token != "" {
		idHash = hashToken(token)
	} else {
		si, ok := sessFrom(ctx)
		if !ok || si.kind != core.SessUser || si.bound {
			return ""
		}
		idHash = si.idHash
	}
	fresh, err := newChallenge()
	if err != nil {
		return ""
	}
	now := s.d.Clock.Now()
	var c []byte
	err = s.d.DB.Q().QueryRow(ctx, `UPDATE sessions SET
		dbsc_challenge = CASE WHEN dbsc_challenge IS NOT NULL AND dbsc_challenge_at > $3 THEN dbsc_challenge ELSE $2 END,
		dbsc_challenge_at = CASE WHEN dbsc_challenge IS NOT NULL AND dbsc_challenge_at > $3 THEN dbsc_challenge_at ELSE $4 END
		WHERE id_hash = $1 AND kind = 1 AND NOT dbsc_bound AND expires_at > $4 RETURNING dbsc_challenge`,
		idHash[:], fresh, now.Add(-ChallengeTTL), now).Scan(&c)
	if err != nil {
		return ""
	}
	return dbsc.RegistrationHeader(c)
}

// DBSCRegister binds the caller's user session to the device key of the
// registration JWT (header Sec-Session-Response) and returns the first bound
// cookie. Any failure — DBSC off, no unbound user session, no live offer, a
// bad proof — is the uniform not-found and burns the offer.
func (s *Service) DBSCRegister(ctx context.Context, jwt []byte) (DBSCView, error) {
	if !s.DBSCEnabled() {
		return DBSCView{}, gate.ErrNotFound
	}
	si, ok := sessFrom(ctx)
	if !ok || si.kind != core.SessUser || si.bound {
		return DBSCView{}, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var (
		challenge []byte
		created   time.Time
		authAt    *time.Time
	)
	// Consume the offer first: one attempt per challenge.
	err := s.d.DB.Q().QueryRow(ctx, `WITH old AS (
			SELECT id_hash, dbsc_challenge, dbsc_challenge_at FROM sessions
			WHERE id_hash = $1 AND kind = 1 AND NOT dbsc_bound AND dbsc_challenge IS NOT NULL FOR UPDATE)
		UPDATE sessions s SET dbsc_challenge = NULL, dbsc_challenge_at = NULL FROM old
		WHERE s.id_hash = old.id_hash AND old.dbsc_challenge_at > $2
		RETURNING old.dbsc_challenge, s.created_at, s.auth_at`, si.idHash[:], now.Add(-ChallengeTTL)).
		Scan(&challenge, &created, &authAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.burnOffer(ctx, si.idHash)
			return DBSCView{}, gate.ErrNotFound
		}
		return DBSCView{}, err
	}
	jwk, err := dbsc.ParseRegistrationJWT(jwt, challenge, s.d.Cfg.Origin+dbsc.RegisterPath)
	if err != nil {
		return DBSCView{}, gate.ErrNotFound
	}
	raw, err := json.Marshal(jwk)
	if err != nil {
		return DBSCView{}, err
	}
	tok, bh, err := newToken()
	if err != nil {
		return DBSCView{}, err
	}
	var at time.Time
	if authAt != nil {
		at = *authAt
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE sessions SET dbsc_bound = true, dbsc_public_jwk = $2, dbsc_refreshed_at = $3,
		bound_token_hash = $4, bound_expires_at = $5, short_lived = false, expires_at = $6
		WHERE id_hash = $1 AND NOT dbsc_bound AND expires_at > $3`,
		si.idHash[:], raw, now, bh[:], now.Add(dbsc.BoundTTL), userExpiry(now, created, at, false))
	if err != nil {
		return DBSCView{}, err
	}
	if tag.RowsAffected() != 1 {
		return DBSCView{}, gate.ErrNotFound
	}
	cfg, err := s.config(si.idHash)
	if err != nil {
		return DBSCView{}, err
	}
	return DBSCView{Token: tok, Config: cfg}, nil
}

// burnOffer clears a stale offer (an expired challenge is consumed as well).
func (s *Service) burnOffer(ctx context.Context, idHash [32]byte) {
	_, _ = s.d.DB.Q().Exec(ctx, "UPDATE sessions SET dbsc_challenge = NULL, dbsc_challenge_at = NULL WHERE id_hash = $1 AND NOT dbsc_bound",
		idHash[:])
}

// DBSCRefresh renews the bound cookie of the bound session whose long-lived
// cookie the request carries (the only request that cookie alone may make).
// Without a proof it issues a single-use challenge (View.Challenge, answered
// with 401); with one it consumes the challenge, verifies the JWT against the
// registered device key and returns a new bound cookie. Every failure is the
// uniform not-found, and a challenge is never accepted twice.
func (s *Service) DBSCRefresh(ctx context.Context, jwt []byte) (DBSCView, error) {
	if !s.DBSCEnabled() {
		return DBSCView{}, gate.ErrNotFound
	}
	idHash, ok := ctx.Value(refreshKey{}).([32]byte)
	if !ok {
		return DBSCView{}, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	if len(strings.TrimSpace(string(jwt))) == 0 {
		c, err := newChallenge()
		if err != nil {
			return DBSCView{}, err
		}
		tag, err := s.d.DB.Q().Exec(ctx, `UPDATE sessions SET dbsc_challenge = $2, dbsc_challenge_at = $3
			WHERE id_hash = $1 AND dbsc_bound AND expires_at > $3`, idHash[:], c, now)
		if err != nil {
			return DBSCView{}, err
		}
		if tag.RowsAffected() != 1 {
			return DBSCView{}, gate.ErrNotFound
		}
		return DBSCView{Challenge: dbsc.ChallengeHeader(c, s.sessionID(idHash))}, nil
	}
	var (
		challenge, raw []byte
		created        time.Time
		authAt         *time.Time
	)
	err := s.d.DB.Q().QueryRow(ctx, `WITH old AS (
			SELECT id_hash, dbsc_challenge, dbsc_challenge_at FROM sessions
			WHERE id_hash = $1 AND dbsc_bound AND dbsc_challenge IS NOT NULL FOR UPDATE)
		UPDATE sessions s SET dbsc_challenge = NULL, dbsc_challenge_at = NULL FROM old
		WHERE s.id_hash = old.id_hash AND old.dbsc_challenge_at > $2
		RETURNING old.dbsc_challenge, s.dbsc_public_jwk, s.created_at, s.auth_at`, idHash[:], now.Add(-ChallengeTTL)).
		Scan(&challenge, &raw, &created, &authAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = s.d.DB.Q().Exec(ctx, "UPDATE sessions SET dbsc_challenge = NULL, dbsc_challenge_at = NULL WHERE id_hash = $1",
				idHash[:])
			return DBSCView{}, gate.ErrNotFound
		}
		return DBSCView{}, err
	}
	var jwk dbsc.PublicJWK
	if json.Unmarshal(raw, &jwk) != nil {
		return DBSCView{}, gate.ErrNotFound
	}
	if dbsc.VerifyRefreshJWT(jwk, jwt, challenge, s.d.Cfg.Origin+dbsc.RefreshPath, s.sessionID(idHash)) != nil {
		return DBSCView{}, gate.ErrNotFound
	}
	tok, bh, err := newToken()
	if err != nil {
		return DBSCView{}, err
	}
	var at time.Time
	if authAt != nil {
		at = *authAt
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE sessions SET bound_token_hash = $2, bound_expires_at = $3, dbsc_refreshed_at = $4,
		last_seen_at = $4, expires_at = $5 WHERE id_hash = $1 AND dbsc_bound AND expires_at > $4`,
		idHash[:], bh[:], now.Add(dbsc.BoundTTL), now, userExpiry(now, created, at, false))
	if err != nil {
		return DBSCView{}, err
	}
	if tag.RowsAffected() != 1 {
		return DBSCView{}, gate.ErrNotFound
	}
	cfg, err := s.config(idHash)
	if err != nil {
		return DBSCView{}, err
	}
	return DBSCView{Token: tok, Config: cfg}, nil
}
