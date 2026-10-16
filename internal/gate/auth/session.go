package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/secheaders"
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

// slideEvery limits how often a user session's expiry is written.
const slideEvery = time.Minute

// tokenLen is the length of a session or enrollment token (base64url of 32 bytes).
const tokenLen = 43

// sessInfo is the loaded session, kept in ctx for APIGuard (CSRF) and Session.
type sessInfo struct {
	idHash [32]byte
	seed   []byte
	kind   core.SessionKind
	user   core.UserID // set for user and enrollment sessions
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

// wellFormed reports whether tok looks like a token this package minted.
func wellFormed(tok string) bool {
	if len(tok) != tokenLen {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(tok)
	return err == nil
}

func (s *Service) secure() bool { return strings.HasPrefix(s.d.Cfg.Origin, "https://") }

// csrfToken = base64url(HMAC(CSRFKey, id_hash ‖ csrf_seed)[:16]).
func (s *Service) csrfToken(idHash [32]byte, seed []byte) string {
	m := hmac.New(sha256.New, s.d.Keys.CSRF)
	m.Write(idHash[:])
	m.Write(seed)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

// Middleware runs on ALL routes: it loads the session cookie, puts the viewer in
// the context (core.WithViewer) and slides a user session's expiry (re-sending
// the cookie when it does). A malformed cookie is cleared. A well-formed cookie
// naming no live session is ignored (the request runs anonymous) but NOT
// cleared: a request still in flight with the old cookie when a ceremony
// rotates the session (login, enroll, join) would otherwise answer after the
// rotation and wipe the new cookie from the browser. It never rejects a
// request: origin and CSRF checks are APIGuard's job (/api/* only).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(httpx.CookieName(s.secure()))
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !wellFormed(c.Value) {
			httpx.ClearSessionCookie(w, s.secure())
			next.ServeHTTP(w, r)
			return
		}
		ctx, slid, ok := s.load(r.Context(), hashToken(c.Value))
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if slid > 0 {
			httpx.SetSessionCookie(w, c.Value, int(slid/time.Second), s.secure())
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// load resolves a session by its hash. slid is the new remaining lifetime when
// the expiry was extended (0 otherwise).
func (s *Service) load(ctx context.Context, idHash [32]byte) (context.Context, time.Duration, bool) {
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
		return ctx, 0, false
	}
	si := sessInfo{idHash: idHash, seed: seed, kind: core.SessionKind(kind)} //nolint:gosec // sessions.kind is 0..2
	if userID != nil {
		si.user = core.UserID(*userID) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	v := core.Viewer{Session: idHash, Kind: si.kind, StepUp: stepUp, Credential: cred}
	if si.kind == core.SessUser {
		if status == nil || uint8(*status) != core.StatusActive { //nolint:gosec // users.status is 0..3
			return ctx, 0, false // suspended/deleted accounts have no live sessions
		}
		v.ID = si.user
		if role != nil {
			v.Role = core.Role(*role) //nolint:gosec // users.role is 0..2
		}
		if authAt != nil {
			v.AuthAt = *authAt
		}
	}
	var slid time.Duration
	if v.Kind == core.SessUser && now.Sub(lastSeen) > slideEvery {
		exp := now.Add(UserIdle)
		if abs := createdAt.Add(UserAbsolute); abs.Before(exp) {
			exp = abs
		}
		if _, err := s.d.DB.Q().Exec(ctx, "UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE id_hash = $1",
			idHash[:], now, exp); err == nil {
			slid = exp.Sub(now)
		}
	}
	ctx = context.WithValue(ctx, sessKey{}, si)
	return core.WithViewer(ctx, v), slid, true
}

// APIGuard protects /api/* (§17.2(1), plan §5.1): Sec-Fetch-Site must be
// same-origin or absent; unsafe methods also need Origin == cfg.Origin exactly
// (authenticated or not) and, when a session exists, a valid X-PCC-CSRF.
// Any failure is the uniform 404.
//
// The CSP report endpoint (POST /api/csp-report, anonymous, aggregate counts
// only) is delivered by the browser's Reporting API, which sends the page's
// cookies but never X-PCC-CSRF and may omit Origin: it needs no CSRF token and
// accepts an absent Origin (a present one must still match). INT-0.
func (s *Service) APIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			writeNotFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Method == http.MethodPost && r.URL.Path == secheaders.ReportPath {
				if o := r.Header.Get("Origin"); o != "" && o != s.d.Cfg.Origin {
					writeNotFound(w)
					return
				}
				break
			}
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

func ttlOf(kind core.SessionKind) time.Duration {
	switch kind {
	case core.SessUser:
		return UserIdle
	case core.SessEnrollment:
		return EnrollmentTTL
	}
	return GuestTTL
}

// issue creates a session and returns its cookie token. cred is the credential
// that authenticated a user session; cer an initial pending ceremony (JSON).
func (s *Service) issue(ctx context.Context, q store.Q, kind core.SessionKind, user core.UserID, authAt time.Time,
	cred, cer []byte) (string, error) {
	tok, idHash, err := newToken()
	if err != nil {
		return "", err
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return "", err
	}
	now := s.d.Clock.Now()
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
	_, err = q.Exec(ctx, `INSERT INTO sessions (id_hash, kind, user_id, csrf_seed, created_at, last_seen_at, expires_at,
		auth_at, credential_id, ceremony) VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $9)`,
		idHash[:], int16(kind), uid, seed, now, now.Add(ttlOf(kind)), at, cred, cer)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// IssueSession creates a session of kind for user (0 for guests) and returns its
// cookie token. Used by guest creation and the test hooks.
func (s *Service) IssueSession(ctx context.Context, q store.Q, kind core.SessionKind, user core.UserID, authAt time.Time) (string, error) {
	return s.issue(ctx, q, kind, user, authAt, nil, nil)
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
	me, err := s.me(ctx, v.ID)
	if err != nil {
		return gate.SessionView{}, err
	}
	sv.Authenticated, sv.Me = true, &me
	return sv, nil
}

// sessionFor describes the session identified by its fresh cookie token (the
// answer of login and enrollment, which rotate the token).
func (s *Service) sessionFor(ctx context.Context, token string) (gate.SessionView, error) {
	h := hashToken(token)
	var (
		kind int16
		uid  *int64
		seed []byte
	)
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT kind, user_id, csrf_seed FROM sessions WHERE id_hash = $1", h[:]).
		Scan(&kind, &uid, &seed); err != nil {
		return gate.SessionView{}, err
	}
	k := core.SessionKind(kind) //nolint:gosec // sessions.kind is 0..2
	sv := gate.SessionView{Kind: k.String(), CSRF: s.csrfToken(h, seed)}
	if k != core.SessUser || uid == nil {
		return sv, nil
	}
	me, err := s.me(ctx, core.UserID(*uid)) //nolint:gosec // users.id < 2^32 (DB CHECK)
	if err != nil {
		return gate.SessionView{}, err
	}
	sv.Authenticated, sv.Me = true, &me
	return sv, nil
}

func (s *Service) me(ctx context.Context, id core.UserID) (gate.MeView, error) {
	m, err := s.d.Users.Get(ctx, id)
	if err != nil {
		return gate.MeView{}, err
	}
	label, err := s.d.Users.InviterLabel(ctx, id)
	if err != nil {
		return gate.MeView{}, err
	}
	prefs := json.RawMessage(m.UIPrefs)
	if len(prefs) == 0 {
		prefs = json.RawMessage("{}")
	}
	return gate.MeView{
		Ref: s.d.Refs.User(id, id), Handle: m.Handle, DisplayName: m.DisplayName, Role: m.Role.String(), Inviter: label,
		Settings: gate.SettingsView{UILocale: m.Locale, Languages: m.Languages, RankPreset: m.Preset.String(),
			NameInChains: m.NameInChains, Suggestible: m.Suggestible, NamedInLikes: m.NamedInLikes,
			SlipperyDefault: m.SlipperyDefault, TranslateTo: m.TranslateTo, Autotranslate: m.Autotranslate, UIPrefs: prefs},
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
	evs, err := endSessions(ctx, s.d.DB.Q(), sqlEndSession, si.idHash[:])
	if err != nil {
		return err
	}
	s.publishEnded(evs)
	return nil
}

// endCaller deletes the caller's current session (if any) inside q, when a
// flow replaces it with a new one; the events are published after commit.
func endCaller(ctx context.Context, q store.Q) ([]core.SessionEnded, error) {
	si, ok := sessFrom(ctx)
	if !ok {
		return nil, nil
	}
	return endSessions(ctx, q, sqlEndSession, si.idHash[:])
}

// RevokeAll deletes every session of user inside the caller's tx
// (core.Passkeys). SessionEnded is published only after that tx commits, and
// never if it rolls back: see publishAfterCommit.
func (s *Service) RevokeAll(ctx context.Context, tx store.Q, user core.UserID) error {
	evs, err := endSessions(ctx, tx, sqlEndUser, int64(user))
	if err != nil {
		return err
	}
	s.publishAfterCommit(ctx, evs)
	return nil
}

// revokeWait bounds how long a RevokeAll publisher waits for the caller's tx.
const revokeWait = 5 * time.Minute

// publishAfterCommit publishes evs once the transaction that deleted their
// session rows has ended, for the rows whose deletion committed. The deleting
// tx holds those rows' locks until it ends, so a FOR KEY SHARE read of them
// from another connection blocks until then: a row that is still there means
// the tx rolled back (no event); a missing row means the delete committed.
// core.Passkeys.RevokeAll hands us no commit hook (store.Tx has none), so this
// is how "publish after commit" holds for a delete in someone else's tx.
func (s *Service) publishAfterCommit(ctx context.Context, evs []core.SessionEnded) {
	if len(evs) == 0 {
		return
	}
	ids := make([][]byte, len(evs))
	for i := range evs {
		ids[i] = evs[i].Session[:]
	}
	s.pending.Add(1)
	go func() {
		defer s.pending.Done()
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeWait)
		defer cancel()
		rows, err := s.d.DB.Q().Query(wctx, "SELECT id_hash FROM sessions WHERE id_hash = ANY($1) FOR KEY SHARE", ids)
		if err != nil {
			s.logRevokeErr(err)
			return
		}
		alive := map[[32]byte]bool{}
		for rows.Next() {
			var h []byte
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				s.logRevokeErr(err)
				return
			}
			var k [32]byte
			copy(k[:], h)
			alive[k] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			s.logRevokeErr(err)
			return
		}
		for _, e := range evs {
			if !alive[e.Session] {
				s.d.Bus.Publish(e)
			}
		}
	}()
}

func (s *Service) logRevokeErr(err error) {
	if s.d.Log != nil {
		s.d.Log.Warn("auth: SessionEnded after RevokeAll not published", "err", err)
	}
}

// purgeExpired deletes expired sessions and publishes SessionEnded for each.
func (s *Service) purgeExpired(ctx context.Context) error {
	evs, err := endSessions(ctx, s.d.DB.Q(), sqlEndExpired, s.d.Clock.Now())
	if err != nil {
		return err
	}
	s.publishEnded(evs)
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

var _ core.Enrollments = (*Service)(nil)

// CancelEnrollment voids the unused, not yet cancelled enrollment token whose
// sha256 is tokenHash (core.Enrollments; steward, contract request p0-w11-2).
// EnrollBegin/EnrollFinish refuse a token with cancelled_at set (plan §3.2).
func (s *Service) CancelEnrollment(ctx context.Context, tx store.Q, tokenHash []byte) error {
	_, err := tx.Exec(ctx, `UPDATE enrollment_tokens SET cancelled_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND cancelled_at IS NULL`, tokenHash, s.d.Clock.Now())
	return err
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
		tok, err = s.IssueEnrollment(ctx, tx, id, purposeGenesis, s.d.Clock.Now())
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
