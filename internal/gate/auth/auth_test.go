package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	w   *wiretest.TestWorld
	rec *coreref.RecordingNotifier
	ctx context.Context

	mu    sync.Mutex
	ended []core.SessionEnded
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	rec := &coreref.RecordingNotifier{D: d}
	d.Notify = rec
	e := &env{t: t, a: a, d: d, w: wiretest.World(t, a), rec: rec, ctx: context.Background()}
	core.Subscribe(d.Bus, func(ev core.SessionEnded) {
		e.mu.Lock()
		e.ended = append(e.ended, ev)
		e.mu.Unlock()
	})
	return e
}

func (e *env) endedFor(user core.UserID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, ev := range e.ended {
		if ev.User == user {
			n++
		}
	}
	return n
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) authn() *authtest.Authenticator { return authtest.New(e.d.Cfg.Origin) }

// client is one browser: a cookie run through the real session middleware.
type client struct {
	e      *env
	cookie string
}

// ctx returns the request context the middleware builds for the cookie.
func (c *client) ctx() context.Context {
	c.e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: c.cookie}) //nolint:gosec // test request
	}
	var got context.Context
	c.e.a.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func (c *client) viewer() (core.Viewer, bool) { return core.ViewerFrom(c.ctx()) }

// enroll seeds a genesis account and enrolls a's passkey through the real ceremony.
func (e *env) enroll(handle string, a *authtest.Authenticator) (*client, core.UserID) {
	e.t.Helper()
	tok, err := e.a.Auth.GenesisToken(e.ctx, handle)
	if err != nil {
		e.t.Fatal(err)
	}
	c := &client{e: e}
	ev, err := e.a.Auth.EnrollBegin(c.ctx(), tok)
	if err != nil {
		e.t.Fatalf("enroll begin: %v", err)
	}
	if ev.Purpose != "genesis" || ev.Token == "" || ev.CSRF == "" {
		e.t.Fatalf("enroll view %+v", ev)
	}
	c.cookie = ev.Token
	resp, err := a.Create(ev.Options)
	if err != nil {
		e.t.Fatal(err)
	}
	sess, sv, err := e.a.Auth.EnrollFinish(c.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	if err != nil {
		e.t.Fatalf("enroll finish: %v", err)
	}
	if !sv.Authenticated || sv.Me == nil || sv.Me.Handle != handle || sv.CSRF == "" {
		e.t.Fatalf("enroll session %+v", sv)
	}
	c.cookie = sess
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	return c, id
}

// login runs the discoverable login ceremony from a fresh browser.
func (e *env) login(a *authtest.Authenticator) (*client, error) {
	e.t.Helper()
	c := &client{e: e}
	bv, err := e.a.Auth.LoginBegin(c.ctx())
	if err != nil {
		e.t.Fatal(err)
	}
	c.cookie = bv.Token
	resp, err := a.Get(bv.Options)
	if err != nil {
		e.t.Fatal(err)
	}
	tok, sv, err := e.a.Auth.LoginFinish(c.ctx(), resp)
	if err != nil {
		return c, err
	}
	if !sv.Authenticated || sv.Kind != "user" || sv.CSRF == "" {
		e.t.Fatalf("login session %+v", sv)
	}
	c.cookie = tok
	return c, nil
}

// stepUp runs the step-up ceremony with a.
func (e *env) stepUp(c *client, a *authtest.Authenticator) error {
	e.t.Helper()
	opts, err := e.a.Auth.StepUpBegin(c.ctx())
	if err != nil {
		return err
	}
	resp, err := a.Get(opts)
	if err != nil {
		return err
	}
	return e.a.Auth.StepUpFinish(c.ctx(), resp)
}

// addPasskey adds a's new credential to the client's account (fresh session).
func (e *env) addPasskey(c *client, a *authtest.Authenticator, name string) error {
	e.t.Helper()
	opts, err := e.a.Auth.AddPasskeyBegin(c.ctx())
	if err != nil {
		return err
	}
	resp, err := a.Create(opts)
	if err != nil {
		return err
	}
	return e.a.Auth.AddPasskeyFinish(c.ctx(), resp, name)
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("want not_found, got %v", err)
	}
}

func wantProblem(t *testing.T, err error, code string) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("want problem %s, got %v", code, err)
	}
}

func TestGenesisEnrollThenLogin(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	v, ok := c.viewer()
	if !ok || v.Kind != core.SessUser || v.ID != alice || v.AuthAt.IsZero() || len(v.Credential) == 0 {
		t.Fatalf("viewer after enroll %+v", v)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE id = $1 AND status = 0 AND tos_version = $2
		AND tos_accepted_at IS NOT NULL AND age_attested_at IS NOT NULL`, int64(alice), e.d.Cfg.TOSVersion); n != 1 {
		t.Fatal("genesis activation not recorded")
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1 AND used_at IS NOT NULL", int64(alice)); n != 1 {
		t.Fatal("token not marked used")
	}

	// PasskeyChange record carries the COSE public key of the stored credential.
	var body []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT body FROM signed_records WHERE kind = $1", int16(core.RecPasskeyChange)).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Op, CredentialID, PublicKey string
		Alg                         int
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	rec.Op, _ = raw["op"].(string)
	rec.CredentialID, _ = raw["credential_id"].(string)
	rec.PublicKey, _ = raw["public_key"].(string)
	var pub, id []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT id, public_key FROM webauthn_credentials WHERE user_id = $1", int64(alice)).Scan(&id, &pub); err != nil {
		t.Fatal(err)
	}
	if rec.Op != "add" || rec.PublicKey != base64.RawURLEncoding.EncodeToString(pub) ||
		rec.CredentialID != base64.RawURLEncoding.EncodeToString(id) || raw["alg"] != float64(-7) {
		t.Fatalf("PasskeyChange body %s", body)
	}

	// Logout ends the session (SessionEnded); login with the passkey works and
	// rotates the token (the guest token never becomes the user token).
	if err := e.a.Auth.Logout(c.ctx()); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.viewer(); ok {
		t.Fatal("session survived logout")
	}
	if e.endedFor(alice) != 1 {
		t.Fatal("no SessionEnded on logout")
	}
	g := &client{e: e}
	bv, err := e.a.Auth.LoginBegin(g.ctx())
	if err != nil {
		t.Fatal(err)
	}
	g.cookie = bv.Token
	if v, ok := g.viewer(); !ok || v.Kind != core.SessGuest {
		t.Fatalf("login begin session %+v", v)
	}
	resp, err := a.Get(bv.Options)
	if err != nil {
		t.Fatal(err)
	}
	tok, sv, err := e.a.Auth.LoginFinish(g.ctx(), resp)
	if err != nil {
		t.Fatal(err)
	}
	if tok == bv.Token || sv.CSRF == bv.CSRF || sv.Me.Handle != "alice" {
		t.Fatal("login did not rotate the token and CSRF seed")
	}
	if _, ok := g.viewer(); ok {
		t.Fatal("the guest session survived login")
	}
	g.cookie = tok
	if v, ok := g.viewer(); !ok || v.ID != alice || !bytes.Equal(v.Credential, id) {
		t.Fatalf("user session %+v", v)
	}
}

func TestTokensNeverStoredInClear(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	tok, err := e.a.Auth.GenesisToken(e.ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE token_hash = $1", sha256Of(tok)); n != 1 {
		t.Fatal("enrollment token is not stored as its sha256")
	}
	c, _ := e.enroll("bob", a)
	l, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range []string{c.cookie, l.cookie} {
		if n := e.count("SELECT count(*) FROM sessions WHERE id_hash = $1", sha256Of(tk)); n != 1 {
			t.Fatal("session is not keyed by the token hash")
		}
	}
	rows, err := e.d.DB.Q().Query(e.ctx, "SELECT id_hash, csrf_seed, coalesce(ceremony::text, '') FROM sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var h, seed []byte
		var cer string
		if err := rows.Scan(&h, &seed, &cer); err != nil {
			t.Fatal(err)
		}
		for _, tk := range []string{c.cookie, l.cookie, tok} {
			raw, _ := base64.RawURLEncoding.DecodeString(tk)
			for _, needle := range [][]byte{[]byte(tk), raw} {
				if bytes.Contains(h, needle) || bytes.Contains(seed, needle) || strings.Contains(cer, string(needle)) {
					t.Fatal("a token is stored in clear")
				}
			}
		}
	}
}

func sha256Of(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func TestLoginCeremonyIsSingleUse(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	e.enroll("alice", a)
	g := &client{e: e}
	bv, err := e.a.Auth.LoginBegin(g.ctx())
	if err != nil {
		t.Fatal(err)
	}
	g.cookie = bv.Token
	resp, err := a.Get(bv.Options)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.a.Auth.LoginFinish(g.ctx(), resp); err != nil {
		t.Fatal(err)
	}
	// The same assertion again: the guest session (and its ceremony) is gone.
	_, _, err = e.a.Auth.LoginFinish(g.ctx(), resp)
	wantNotFound(t, err)
	// Replayed into a NEW ceremony: the challenge differs.
	h := &client{e: e}
	bv2, err := e.a.Auth.LoginBegin(h.ctx())
	if err != nil {
		t.Fatal(err)
	}
	h.cookie = bv2.Token
	_, _, err = e.a.Auth.LoginFinish(h.ctx(), resp)
	wantNotFound(t, err)
	// That failed attempt cleared the new ceremony too: a valid answer is now refused.
	fresh, err := a.Get(bv2.Options)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.a.Auth.LoginFinish(h.ctx(), fresh)
	wantNotFound(t, err)
}

func TestLoginCeremonyExpires(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	e.enroll("alice", a)
	g := &client{e: e}
	bv, err := e.a.Auth.LoginBegin(g.ctx())
	if err != nil {
		t.Fatal(err)
	}
	g.cookie = bv.Token
	resp, err := a.Get(bv.Options)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(auth.CeremonyTTL + time.Second)
	_, _, err = e.a.Auth.LoginFinish(g.ctx(), resp)
	wantNotFound(t, err)
}

func TestLoginRefusals(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	_, alice := e.enroll("alice", a)
	stranger := e.authn()
	if _, err := stranger.Create(mustJSON(t, map[string]any{"publicKey": map[string]any{
		"challenge": "AAAAAAAAAAAAAAAAAAAAAA", "rp": map[string]any{"id": e.d.Cfg.RPID}, "user": map[string]any{"id": "AAAA"}}})); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		knob  func(*authtest.Authenticator)
		authn *authtest.Authenticator
	}{
		{"wrong origin", func(x *authtest.Authenticator) { x.OriginOverride = "http://localhost:8081" }, a},
		{"origin with path", func(x *authtest.Authenticator) { x.OriginOverride = e.d.Cfg.Origin + "/" }, a},
		{"cross origin", func(x *authtest.Authenticator) { x.CrossOrigin = true }, a},
		{"top origin", func(x *authtest.Authenticator) { x.TopOrigin = "https://evil.example" }, a},
		{"no user verification", func(x *authtest.Authenticator) { x.NoUV = true }, a},
		{"no user presence", func(x *authtest.Authenticator) { x.NoUP = true }, a},
		{"backup eligibility changed", func(x *authtest.Authenticator) { x.BackupEligible = true }, a},
		{"unknown credential", func(*authtest.Authenticator) {}, stranger},
	}
	for _, c := range cases {
		c.knob(c.authn)
		_, err := e.login(c.authn)
		if !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("%s: %v", c.name, err)
		}
		a.NoUV, a.NoUP, a.CrossOrigin, a.TopOrigin, a.OriginOverride, a.BackupEligible = false, false, false, "", "", false
	}
	if _, err := e.login(a); err != nil {
		t.Fatalf("clean login after refusals: %v", err)
	}
	// Counter replay (a cloned authenticator): the counter must increase.
	a.FrozenCounter = true
	_, err := e.login(a)
	wantNotFound(t, err)
	a.FrozenCounter = false
	if _, err := e.login(a); err != nil {
		t.Fatal(err)
	}
	// A suspended account's valid assertion gets the same 404, and its
	// existing sessions stop working.
	live, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []uint8{core.StatusSuspended, core.StatusDeleted, core.StatusPendingEnrollment} {
		e.exec("UPDATE users SET status = $2 WHERE id = $1", int64(alice), int16(st))
		e.d.Bus.Publish(core.UserChanged{ID: alice})
		_, err = e.login(a)
		wantNotFound(t, err)
		if _, ok := live.viewer(); ok {
			t.Fatalf("status %d: session still live", st)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStepUpAndAddPasskey(t *testing.T) {
	e := newEnv(t)
	a, b := e.authn(), e.authn()
	c, alice := e.enroll("alice", a)
	_, bob := e.enroll("bob", b)
	_ = bob
	e.advance(core.FreshFor + time.Second)

	// Passkey add needs a fresh step-up.
	_, err := e.a.Auth.AddPasskeyBegin(c.ctx())
	if !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("add without fresh auth: %v", err)
	}
	if err := e.a.Auth.AddPasskeyFinish(c.ctx(), []byte(`{}`), ""); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("add finish without fresh auth: %v", err)
	}

	// Step-up options list only alice's own credentials.
	opts, err := e.a.Auth.StepUpBegin(c.ctx())
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		PublicKey struct {
			Allow []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
			UV string `json:"userVerification"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(opts, &o); err != nil {
		t.Fatal(err)
	}
	aliceCred := a.Credentials()[0].ID
	if len(o.PublicKey.Allow) != 1 || o.PublicKey.Allow[0].ID != base64.RawURLEncoding.EncodeToString(aliceCred) ||
		o.PublicKey.UV != "required" {
		t.Fatalf("step-up options %s", opts)
	}
	// Bob's passkey (another account) is rejected, even when the client ignores allowCredentials.
	resp, err := b.GetWith(opts, b.Credentials()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.a.Auth.StepUpFinish(c.ctx(), resp), "passkey_rejected")
	// The same key imported into alice's authenticator is still bob's credential.
	thief := e.authn()
	thief.Import(b.Credentials()[0])
	err = e.stepUp(c, thief)
	if err == nil {
		t.Fatal("step-up with another account's passkey accepted")
	}
	if v, _ := c.viewer(); e.d.Clock.Now().Sub(v.AuthAt) <= core.FreshFor {
		t.Fatal("rejected step-up refreshed auth_at")
	}
	// A step-up without UV is rejected.
	a.NoUV = true
	wantProblem(t, e.stepUp(c, a), "passkey_rejected")
	a.NoUV = false
	// Replaying a step-up answer is rejected (ceremony single use).
	opts, err = e.a.Auth.StepUpBegin(c.ctx())
	if err != nil {
		t.Fatal(err)
	}
	resp, err = a.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.a.Auth.StepUpFinish(c.ctx(), resp); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.a.Auth.StepUpFinish(c.ctx(), resp), "passkey_rejected")
	if v, _ := c.viewer(); !v.AuthAt.Equal(e.d.Clock.Now()) {
		t.Fatalf("auth_at %v", v.AuthAt)
	}

	// Add a second passkey; excludeCredentials stops re-registering the first.
	if err := e.addPasskey(c, a, "again"); !errors.Is(err, authtest.ErrExcluded) {
		t.Fatalf("existing authenticator: %v", err)
	}
	e.w.User("carol")
	e.exec(`INSERT INTO edges (user_id, friend_id, level, since, record_hash) VALUES ($1, $2, 1, now(), '\x01'), ($2, $1, 1, now(), '\x01')`,
		int64(alice), int64(e.w.ID("carol")))
	e.d.Graph.AddEdge(alice, e.w.ID("carol"), core.LevelFriend, e.d.Clock.Now())
	a2 := e.authn()
	wantProblem(t, e.addPasskey(c, e.authn(), strings.Repeat("x", auth.MaxPasskeyName+1)), "passkey_name_invalid")
	if err := e.addPasskey(c, a2, "  Laptop  "); err != nil {
		t.Fatal(err)
	}
	list, err := e.a.Auth.Passkeys(c.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Current || list[1].Current || list[1].Name != "Laptop" || list[0].ID == "" {
		t.Fatalf("passkeys %+v", list)
	}
	if evs := e.rec.For(e.w.ID("carol")); len(evs) != 1 || evs[0].Kind != "friend_security" ||
		evs[0].SubjectUser != alice || evs[0].Data["reason"] != "passkey_added" {
		t.Fatalf("friend notifications %+v", evs)
	}
	if n := e.count(`SELECT count(*) FROM signed_records r JOIN record_parties p ON p.record_hash = r.hash
		WHERE r.kind = $1 AND p.user_id = $2`, int16(core.RecPasskeyChange), int64(alice)); n != 2 {
		t.Fatalf("%d PasskeyChange records", n)
	}
	// The new passkey logs in.
	if _, err := e.login(a2); err != nil {
		t.Fatal(err)
	}
	// Another account cannot list alice's passkeys by ref.
	bc, err := e.login(b)
	if err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, e.a.Auth.RemovePasskey(bc.ctx(), list[0].ID))
}

func TestRemovePasskey(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	a2 := e.authn()
	e.advance(time.Second) // distinct created_at: passkeys list oldest first
	if err := e.addPasskey(c, a2, "second"); err != nil {
		t.Fatal(err)
	}
	s1, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := e.login(a2)
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.a.Auth.Passkeys(s2.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Current || !list[1].Current {
		t.Fatalf("passkeys from s2 %+v", list)
	}
	e.advance(core.FreshFor + time.Second)
	if err := e.a.Auth.RemovePasskey(s2.ctx(), list[0].ID); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("remove without fresh auth: %v", err)
	}
	if err := e.stepUp(s2, a2); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, e.a.Auth.RemovePasskey(s2.ctx(), "not-a-ref"))
	before := e.endedFor(alice)
	if err := e.a.Auth.RemovePasskey(s2.ctx(), list[0].ID); err != nil {
		t.Fatal(err)
	}
	// Every session the removed credential authenticated is gone (s1 and the
	// enrollment session c); s2 lives on.
	for name, cl := range map[string]*client{"login": s1, "enroll": c} {
		if _, ok := cl.viewer(); ok {
			t.Fatalf("%s session survived removal of its credential", name)
		}
	}
	if _, ok := s2.viewer(); !ok {
		t.Fatal("unrelated session ended")
	}
	if e.endedFor(alice)-before != 2 {
		t.Fatalf("SessionEnded events: %d", e.endedFor(alice)-before)
	}
	if _, err := e.login(a); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("removed passkey still logs in: %v", err)
	}
	// The last passkey cannot be removed.
	wantProblem(t, e.a.Auth.RemovePasskey(s2.ctx(), list[1].ID), "last_passkey")
	if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE user_id = $1", int64(alice)); n != 1 {
		t.Fatalf("%d credentials left", n)
	}
	var op string
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT convert_from(body, 'UTF8')::jsonb->>'op' FROM signed_records WHERE kind = $1 ORDER BY created_at DESC, convert_from(body, 'UTF8')::jsonb->>'op' DESC LIMIT 1`,
		int16(core.RecPasskeyChange)).Scan(&op); err != nil || op != "remove" {
		t.Fatalf("remove record: %q %v", op, err)
	}
}

func TestEnrollmentTokenBindingAndRules(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	tok, err := e.a.Auth.GenesisToken(e.ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	// Malformed and unknown tokens.
	for _, bad := range []string{"", "x", strings.Repeat("A", 43), tok + "A"} {
		_, err := e.a.Auth.EnrollBegin((&client{e: e}).ctx(), bad)
		wantNotFound(t, err)
	}
	x := &client{e: e}
	ev, err := e.a.Auth.EnrollBegin(x.ctx(), tok)
	if err != nil {
		t.Fatal(err)
	}
	x.cookie = ev.Token
	// Another browser cannot take the token over while x's session lives.
	_, err = e.a.Auth.EnrollBegin((&client{e: e}).ctx(), tok)
	wantNotFound(t, err)
	// x may begin again (a reload) without a new session.
	ev2, err := e.a.Auth.EnrollBegin(x.ctx(), tok)
	if err != nil || ev2.Token != "" || ev2.CSRF != ev.CSRF {
		t.Fatalf("re-begin: %+v %v", ev2, err)
	}
	resp, err := a.Create(ev2.Options)
	if err != nil {
		t.Fatal(err)
	}
	// Genesis needs the current terms and the age attestation.
	_, _, err = e.a.Auth.EnrollFinish(x.ctx(), resp, false, e.d.Cfg.TOSVersion-1, true)
	wantProblem(t, err, "tos_required")
	_, _, err = e.a.Auth.EnrollFinish(x.ctx(), resp, false, e.d.Cfg.TOSVersion, false)
	wantProblem(t, err, "age_required")
	// A session not bound to the token cannot finish.
	_, _, err = e.a.Auth.EnrollFinish((&client{e: e}).ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	wantNotFound(t, err)
	tokUser, _, err := e.a.Auth.EnrollFinish(x.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	// Used: no second enrollment, from any session.
	_, err = e.a.Auth.EnrollBegin((&client{e: e}).ctx(), tok)
	wantNotFound(t, err)
	_, _, err = e.a.Auth.EnrollFinish(x.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	wantNotFound(t, err)
	if (&client{e: e, cookie: tokUser}).ctx() == nil {
		t.Fatal("no context")
	}

	// Expired and cancelled tokens.
	tok2, err := e.a.Auth.GenesisToken(e.ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	tok3, err := e.a.Auth.GenesisToken(e.ctx, "carol")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.a.Auth.CancelEnrollment(e.ctx, e.d.DB.Q(), sha256Of(tok3)); err != nil {
		t.Fatal(err)
	}
	_, err = e.a.Auth.EnrollBegin((&client{e: e}).ctx(), tok3)
	wantNotFound(t, err)
	e.advance(auth.EnrollTokenTTL + time.Second)
	_, err = e.a.Auth.EnrollBegin((&client{e: e}).ctx(), tok2)
	wantNotFound(t, err)
}

func TestEnrollmentSessionExpiryReleasesBinding(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	tok, err := e.a.Auth.GenesisToken(e.ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	x := &client{e: e}
	ev, err := e.a.Auth.EnrollBegin(x.ctx(), tok)
	if err != nil {
		t.Fatal(err)
	}
	x.cookie = ev.Token
	e.advance(auth.EnrollmentTTL + time.Second)
	if _, ok := x.viewer(); ok {
		t.Fatal("enrollment session outlived its TTL")
	}
	y := &client{e: e}
	ev, err = e.a.Auth.EnrollBegin(y.ctx(), tok)
	if err != nil {
		t.Fatalf("rebinding after expiry: %v", err)
	}
	y.cookie = ev.Token
	resp, err := a.Create(ev.Options)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.a.Auth.EnrollFinish(y.ctx(), resp, false, e.d.Cfg.TOSVersion, true); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryEnrollment(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	e.w.User("carol")
	e.d.Graph.AddEdge(alice, e.w.ID("carol"), core.LevelFriend, e.d.Clock.Now())
	other, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	var tok string
	if err := e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		var err error
		tok, err = e.d.Passkeys.IssueEnrollment(e.ctx, tx, alice, 1, e.d.Clock.Now().Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Held until usable_from.
	_, err = e.a.Auth.EnrollBegin((&client{e: e}).ctx(), tok)
	wantNotFound(t, err)
	e.advance(time.Hour)
	r := &client{e: e}
	ev, err := e.a.Auth.EnrollBegin(r.ctx(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Purpose != "recovery" || len(ev.ExistingPasskeys) != 1 {
		t.Fatalf("recovery view %+v", ev)
	}
	r.cookie = ev.Token
	if v, _ := r.viewer(); v.Kind != core.SessEnrollment || v.ID != 0 {
		t.Fatalf("enrollment viewer %+v", v)
	}
	if _, err := core.RequireUser(r.ctx()); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("an enrollment session acts as a user")
	}
	fresh := e.authn()
	resp, err := fresh.Create(ev.Options)
	if err != nil {
		t.Fatal(err)
	}
	before := e.endedFor(alice)
	// Recovery needs no terms (accepted at join); remove the old passkeys.
	tokUser, sv, err := e.a.Auth.EnrollFinish(r.ctx(), resp, true, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !sv.Authenticated || sv.Me.Handle != "alice" {
		t.Fatalf("recovered session %+v", sv)
	}
	for name, cl := range map[string]*client{"enroll": c, "login": other} {
		if _, ok := cl.viewer(); ok {
			t.Fatalf("old %s session survived recovery", name)
		}
	}
	if e.endedFor(alice)-before < 2 {
		t.Fatal("no SessionEnded for revoked sessions")
	}
	if _, err := e.login(a); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("removed old passkey still logs in")
	}
	if _, err := e.login(fresh); err != nil {
		t.Fatal(err)
	}
	r.cookie = tokUser
	if v, ok := r.viewer(); !ok || v.ID != alice {
		t.Fatal("recovered session not live")
	}
	if evs := e.rec.For(e.w.ID("carol")); len(evs) != 1 || evs[0].Data["reason"] != "recovery_enrolled" {
		t.Fatalf("friend notifications %+v", evs)
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1 AND convert_from(body, 'UTF8')::jsonb->>'op' = 'remove'",
		int16(core.RecPasskeyChange)); n != 1 {
		t.Fatalf("%d remove records", n)
	}
}

func TestSessionLifetimes(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	_, alice := e.enroll("alice", a)
	c, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	// Sliding: active use keeps the session past the idle limit...
	for range 3 {
		e.advance(9 * 24 * time.Hour)
		if _, ok := c.viewer(); !ok {
			t.Fatal("active session expired")
		}
	}
	// ...but never past the absolute limit.
	e.advance(auth.UserAbsolute - 27*24*time.Hour)
	if _, ok := c.viewer(); ok {
		t.Fatal("session outlived the absolute limit")
	}
	// Idle: 14 days without use ends a session; the purge publishes SessionEnded.
	d, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(auth.UserIdle + time.Second)
	if _, ok := d.viewer(); ok {
		t.Fatal("idle session survived")
	}
	before := e.endedFor(alice)
	if err := e.a.Auth.PurgeExpired(e.ctx); err != nil {
		t.Fatal(err)
	}
	if e.endedFor(alice)-before < 2 {
		t.Fatal("purge did not publish SessionEnded")
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE user_id = $1", int64(alice)); n != 0 {
		t.Fatalf("%d expired sessions kept", n)
	}
	// A guest session lasts 30 minutes.
	g := &client{e: e}
	bv, err := e.a.Auth.LoginBegin(g.ctx())
	if err != nil {
		t.Fatal(err)
	}
	g.cookie = bv.Token
	e.advance(auth.GuestTTL + time.Second)
	if _, ok := g.viewer(); ok {
		t.Fatal("guest session outlived its TTL")
	}
}

func TestMiddlewareCookies(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, _ := e.enroll("alice", a)
	run := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: cookie}) //nolint:gosec // test request
		rec := httptest.NewRecorder()
		e.a.Auth.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
		return rec
	}
	// A malformed cookie is cleared.
	if ck := run("nope").Result().Cookies(); len(ck) != 1 || ck[0].MaxAge >= 0 {
		t.Fatalf("malformed: cookies %+v", ck)
	}
	// A well-formed cookie naming no live session is left alone: a request still
	// in flight across a session rotation must not wipe the new cookie.
	if ck := run(strings.Repeat("A", 43)).Result().Cookies(); len(ck) != 0 {
		t.Fatalf("unknown: cookies %+v", ck)
	}
	// Sliding re-sends the cookie, HttpOnly and SameSite=Strict.
	e.advance(2 * time.Minute)
	rec := run(c.cookie)
	ck := rec.Result().Cookies()
	if len(ck) != 1 || ck[0].Value != c.cookie || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode ||
		ck[0].MaxAge != int(auth.UserIdle/time.Second) {
		t.Fatalf("slid cookie %+v", ck)
	}
	if ck := run(c.cookie).Result().Cookies(); len(ck) != 0 {
		t.Fatalf("cookie re-sent within the slide interval: %+v", ck)
	}
}

func TestAssertionForCosign(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	v, _ := c.viewer()
	challenge := bytes.Repeat([]byte{7}, 32)
	opts, err := e.d.Passkeys.BeginAssertion(e.ctx, v, challenge, "cosign")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(opts), base64.RawURLEncoding.EncodeToString(challenge)) {
		t.Fatalf("challenge not used: %s", opts)
	}
	resp, err := a.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong purpose: rejected, and the ceremony is gone.
	if _, err := e.d.Passkeys.VerifyAssertion(e.ctx, v, "stepup", resp); err == nil {
		t.Fatal("purpose mismatch accepted")
	}
	if _, err := e.d.Passkeys.VerifyAssertion(e.ctx, v, "cosign", resp); err == nil {
		t.Fatal("ceremony survived a failed finish")
	}
	if _, err := e.d.Passkeys.BeginAssertion(e.ctx, v, challenge, "cosign"); err != nil {
		t.Fatal(err)
	}
	resp, err = a.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	as, err := e.d.Passkeys.VerifyAssertion(e.ctx, v, "cosign", resp)
	if err != nil {
		t.Fatal(err)
	}
	if as.User != alice || !bytes.Equal(as.CredentialID, a.Credentials()[0].ID) || len(as.Signature) == 0 ||
		len(as.AuthenticatorData) < 37 || !strings.Contains(string(as.ClientDataJSON), "webauthn.get") {
		t.Fatalf("assertion %+v", as)
	}
	// Guests have no assertions.
	if _, err := e.d.Passkeys.BeginAssertion(e.ctx, core.Viewer{Kind: core.SessGuest}, nil, "cosign"); err == nil {
		t.Fatal("guest assertion")
	}
}

func TestJoinRegistrationAndUpgrade(t *testing.T) {
	e := newEnv(t)
	inviter := e.w.User("inviter")
	a := e.authn()
	guest, err := e.a.Auth.StartGuest(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess := [32]byte(sha256Of(guest))
	wid := bytes.Repeat([]byte{9}, 32)
	opts, err := e.d.Passkeys.BeginRegistration(e.ctx, sess, core.PendingUser{WebAuthnID: wid, Handle: "newbie"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := e.d.Passkeys.FinishRegistration(e.ctx, sess, resp)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Alg != -7 || !cred.UV || len(cred.PublicKey) == 0 {
		t.Fatalf("credential %+v", cred)
	}
	if _, err := e.d.Passkeys.FinishRegistration(e.ctx, sess, resp); err == nil {
		t.Fatal("registration replay accepted")
	}
	var tok string
	err = e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		id, err := e.d.Users.Create(e.ctx, tx, core.NewUser{Handle: "newbie", InvitedBy: inviter, WebAuthnID: wid,
			TOSVersion: 1, AgeAttested: true})
		if err != nil {
			return err
		}
		if err := e.d.Passkeys.BindCredential(e.ctx, tx, id, cred); err != nil {
			return err
		}
		tok, err = e.d.Passkeys.Upgrade(e.ctx, tx, sess, id, cred.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok == guest {
		t.Fatal("upgrade kept the guest token")
	}
	if _, ok := (&client{e: e, cookie: guest}).viewer(); ok {
		t.Fatal("guest session survived the upgrade")
	}
	if v, ok := (&client{e: e, cookie: tok}).viewer(); !ok || v.Kind != core.SessUser || v.AuthAt.IsZero() {
		t.Fatalf("upgraded viewer %+v", v)
	}
	if _, err := e.login(a); err != nil {
		t.Fatal(err)
	}
}

func TestAccountDeletedForgetsCredentials(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	_, alice := e.enroll("alice", a)
	e.d.Bus.Publish(core.AccountDeleted{ID: alice})
	if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE user_id = $1", int64(alice)) +
		e.count("SELECT count(*) FROM sessions WHERE user_id = $1", int64(alice)); n != 0 {
		t.Fatalf("%d rows left", n)
	}
}
