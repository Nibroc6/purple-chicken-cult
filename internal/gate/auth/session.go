package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Session lifetimes (plan §4.7 auth, Phase 0).
const (
	UserAbsolute   = 30 * 24 * time.Hour
	UserIdle       = 14 * 24 * time.Hour
	GuestTTL       = 30 * time.Minute
	EnrollmentTTL  = 30 * time.Minute
	EnrollTokenTTL = 24 * time.Hour
)

// sessInfo is the loaded session, kept in ctx for APIGuard (CSRF) and Session.
type sessInfo struct {
	idHash [32]byte
	seed   []byte
	kind   core.SessionKind
}

type sessKey struct{}

func sessFrom(ctx context.Context) (sessInfo, bool) {
	s, ok := ctx.Value(sessKey{}).(sessInfo)
	return s, ok
}

func newToken() (string, [32]byte, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", [32]byte{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	return tok, sha256.Sum256([]byte(tok)), nil
}

func hashToken(tok string) [32]byte { return sha256.Sum256([]byte(tok)) }

func (s *Service) secure() bool { return strings.HasPrefix(s.d.Cfg.Origin, "https://") }

// csrfToken = base64url(HMAC(CSRFKey, id_hash ‖ csrf_seed)[:16]).
func (s *Service) csrfToken(idHash [32]byte, seed []byte) string {
	m := hmac.New(sha256.New, s.d.Keys.CSRF)
	m.Write(idHash[:])
	m.Write(seed)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// Middleware runs on ALL routes: it loads the session cookie, puts the viewer in
// the context (core.WithViewer) and slides the expiry. It never rejects a request:
// origin and CSRF checks are APIGuard's job (/api/* only).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(httpx.CookieName(s.secure()))
		if err != nil || c.Value == "" || len(c.Value) > 128 {
			next.ServeHTTP(w, r)
			return
		}
		ctx, ok := s.load(r.Context(), hashToken(c.Value))
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) load(ctx context.Context, idHash [32]byte) (context.Context, bool) {
	now := s.d.Clock.Now()
	var (
		kind                int16
		userID              *int64
		seed, cred          []byte
		authAt              *time.Time
		stepUp              bool
		createdAt, lastSeen time.Time
		role, status        *int16
	)
	err := s.d.DB.Q().QueryRow(ctx, `SELECT s.kind, s.user_id, s.csrf_seed, s.credential_id, s.auth_at, s.stepup_required,
		s.created_at, s.last_seen_at, u.role, u.status
		FROM sessions s LEFT JOIN users u ON u.id = s.user_id
		WHERE s.id_hash = $1 AND s.expires_at > $2`, idHash[:], now).
		Scan(&kind, &userID, &seed, &cred, &authAt, &stepUp, &createdAt, &lastSeen, &role, &status)
	if err != nil {
		return ctx, false
	}
	v := core.Viewer{Session: idHash, Kind: core.SessionKind(kind), StepUp: stepUp, Credential: cred}
	if userID != nil {
		v.ID = core.UserID(*userID)
	}
	if authAt != nil {
		v.AuthAt = *authAt
	}
	if role != nil {
		v.Role = core.Role(*role)
	}
	if v.Kind == core.SessUser && (status == nil || uint8(*status) != core.StatusActive) {
		return ctx, false // suspended/deleted accounts have no live sessions
	}
	if v.Kind == core.SessUser && now.Sub(lastSeen) > time.Minute {
		exp := now.Add(UserIdle)
		if abs := createdAt.Add(UserAbsolute); abs.Before(exp) {
			exp = abs
		}
		_, _ = s.d.DB.Q().Exec(ctx, "UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE id_hash = $1",
			idHash[:], now, exp)
	}
	ctx = context.WithValue(ctx, sessKey{}, sessInfo{idHash: idHash, seed: seed, kind: v.Kind})
	return core.WithViewer(ctx, v), true
}

// APIGuard protects /api/* (§17.2(1), plan §5.1): Sec-Fetch-Site must be
// same-origin or absent; unsafe methods also need Origin == cfg.Origin exactly
// (authenticated or not) and, when a session exists, a valid X-PCC-CSRF.
// Any failure is the uniform 404.
func (s *Service) APIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			writeNotFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Origin") != s.d.Cfg.Origin {
				writeNotFound(w)
				return
			}
			if si, ok := sessFrom(r.Context()); ok {
				want := s.csrfToken(si.idHash, si.seed)
				if !hmac.Equal([]byte(r.Header.Get("X-PCC-CSRF")), []byte(want)) {
					writeNotFound(w)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(gate.NotFoundJSON))
}

// IssueSession creates a session of kind for user (0 for guests) and returns its
// cookie token. Used by guest creation, enrollment, login (W01) and test hooks.
func (s *Service) IssueSession(ctx context.Context, q store.Q, kind core.SessionKind, user core.UserID, authAt time.Time) (string, error) {
	tok, idHash, err := newToken()
	if err != nil {
		return "", err
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return "", err
	}
	now := s.d.Clock.Now()
	ttl := GuestTTL
	switch kind {
	case core.SessUser:
		ttl = UserIdle
	case core.SessEnrollment:
		ttl = EnrollmentTTL
	}
	var uid *int64
	if user != 0 {
		u := int64(user)
		uid = &u
	}
	var at *time.Time
	if !authAt.IsZero() {
		at = &authAt
	}
	if q == nil {
		q = s.d.DB.Q()
	}
	_, err = q.Exec(ctx, `INSERT INTO sessions (id_hash, kind, user_id, csrf_seed, created_at, last_seen_at, expires_at, auth_at)
		VALUES ($1, $2, $3, $4, $5, $5, $6, $7)`, idHash[:], int16(kind), uid, seed, now, now.Add(ttl), at)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// MaxAge returns the cookie Max-Age for a session kind.
func MaxAge(kind string) int {
	switch kind {
	case "user":
		return int(UserIdle / time.Second)
	case "enrollment":
		return int(EnrollmentTTL / time.Second)
	}
	return int(GuestTTL / time.Second)
}

// SecureCookies reports whether cookies must be Secure (https origin).
func (s *Service) SecureCookies() bool { return s.secure() }

// Session describes the caller's session (works unauthenticated).
func (s *Service) Session(ctx context.Context) (gate.SessionView, error) {
	si, ok := sessFrom(ctx)
	if !ok {
		return gate.SessionView{Kind: "none"}, nil
	}
	v, _ := core.ViewerFrom(ctx)
	sv := gate.SessionView{Kind: si.kind.String(), CSRF: s.csrfToken(si.idHash, si.seed), StepUp: v.StepUp}
	if si.kind != core.SessUser {
		return sv, nil
	}
	me, err := s.me(ctx, v)
	if err != nil {
		return gate.SessionView{}, err
	}
	sv.Authenticated, sv.Me = true, &me
	return sv, nil
}

func (s *Service) me(ctx context.Context, v core.Viewer) (gate.MeView, error) {
	m, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.MeView{}, err
	}
	label, err := s.d.Users.InviterLabel(ctx, v.ID)
	if err != nil {
		return gate.MeView{}, err
	}
	return gate.MeView{
		Ref: s.d.Refs.User(v.ID, v.ID), Handle: m.Handle, DisplayName: m.DisplayName, Role: m.Role.String(), Inviter: label,
		Settings: gate.SettingsView{UILocale: m.Locale, Languages: m.Languages, RankPreset: m.Preset.String(),
			NameInChains: m.NameInChains, Suggestible: m.Suggestible, NamedInLikes: m.NamedInLikes,
			SlipperyDefault: m.SlipperyDefault, UIPrefs: []byte("{}")},
	}, nil
}

// StartGuest creates a guest session (/api/join/start) and returns its token.
func (s *Service) StartGuest(ctx context.Context) (string, error) {
	return s.IssueSession(ctx, nil, core.SessGuest, 0, time.Time{})
}

// Logout deletes the caller's session and publishes SessionEnded.
func (s *Service) Logout(ctx context.Context) error {
	si, ok := sessFrom(ctx)
	if !ok {
		return nil
	}
	v, _ := core.ViewerFrom(ctx)
	if _, err := s.d.DB.Q().Exec(ctx, "DELETE FROM sessions WHERE id_hash = $1", si.idHash[:]); err != nil {
		return err
	}
	s.d.Bus.Publish(core.SessionEnded{User: v.ID, Session: si.idHash})
	return nil
}

// RevokeAll deletes every session of user (core.Passkeys).
func (s *Service) RevokeAll(ctx context.Context, tx store.Q, user core.UserID) error {
	rows, err := tx.Query(ctx, "DELETE FROM sessions WHERE user_id = $1 RETURNING id_hash", int64(user))
	if err != nil {
		return err
	}
	var ended [][32]byte
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		var k [32]byte
		copy(k[:], h)
		ended = append(ended, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range ended {
		s.d.Bus.Publish(core.SessionEnded{User: user, Session: k})
	}
	return nil
}

// IssueEnrollment creates a single-use enrollment token (core.Passkeys): purpose
// 0 genesis, 1 recovery; usable from usableFrom for EnrollTokenTTL.
func (s *Service) IssueEnrollment(ctx context.Context, tx store.Q, user core.UserID, purpose uint8, usableFrom time.Time) (string, error) {
	tok, h, err := newToken()
	if err != nil {
		return "", err
	}
	if usableFrom.IsZero() {
		usableFrom = s.d.Clock.Now()
	}
	_, err = tx.Exec(ctx, `INSERT INTO enrollment_tokens (token_hash, user_id, purpose, created_at, usable_from, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, h[:], int64(user), int16(purpose), s.d.Clock.Now(), usableFrom,
		usableFrom.Add(EnrollTokenTTL))
	if err != nil {
		return "", err
	}
	return tok, nil
}

// GenesisBootstrapLimit: SeedGenesis is refused once this many non-genesis users exist.
const GenesisBootstrapLimit = 50

// SeedGenesis creates a genesis user pending enrollment and returns the one-time
// enrollment URL <Origin>/enroll#<token> (§10.8; CLI only).
func (s *Service) SeedGenesis(ctx context.Context, handle, name string) (string, error) {
	var tok string
	err := s.d.DB.TxSerializable(ctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE NOT is_genesis").Scan(&n); err != nil {
			return err
		}
		if n >= GenesisBootstrapLimit {
			return gate.NewProblem(gate.StatusConflict, "bootstrap_closed")
		}
		id, err := s.d.Users.Create(ctx, tx, core.NewUser{Handle: handle, DisplayName: name, Genesis: true,
			Status: core.StatusPendingEnrollment})
		if err != nil {
			return err
		}
		tok, err = s.IssueEnrollment(ctx, tx, id, 0, s.d.Clock.Now())
		return err
	})
	if err != nil {
		return "", err
	}
	return s.d.Cfg.Origin + "/enroll#" + tok, nil
}

// GenesisToken is SeedGenesis returning the bare token (test hooks only).
func (s *Service) GenesisToken(ctx context.Context, handle string) (string, error) {
	u, err := s.SeedGenesis(ctx, handle, "")
	if err != nil {
		return "", err
	}
	_, tok, ok := strings.Cut(u, "#")
	if !ok {
		return "", errors.New("auth: malformed enrollment url")
	}
	return tok, nil
}

// CSRFForToken returns the CSRF token of the session identified by its cookie
// token (test hooks and wiretest only; the SPA reads it from /api/session).
func (s *Service) CSRFForToken(ctx context.Context, token string) (string, error) {
	h := hashToken(token)
	var seed []byte
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT csrf_seed FROM sessions WHERE id_hash = $1", h[:]).Scan(&seed); err != nil {
		return "", gate.ErrNotFound
	}
	return s.csrfToken(h, seed), nil
}
