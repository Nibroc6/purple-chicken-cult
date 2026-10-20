package auth_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/dbsc/dbsctest"
)

var challengeRe = regexp.MustCompile(`challenge="([A-Za-z0-9_-]+)"|^"([A-Za-z0-9_-]+)"`)

func headerChallenge(t *testing.T, h string) []byte {
	t.Helper()
	m := challengeRe.FindStringSubmatch(h)
	if m == nil {
		t.Fatalf("no challenge in %q", h)
	}
	v := m[1] + m[2]
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dbscEnv(t *testing.T) *env { return newEnv(t, wiretest.WithEnv("PCC_DBSC", "1")) }

// bind registers dev for c's session through the offer of /api/session.
func (e *env) bind(c *client, dev *dbsctest.Device) error {
	e.t.Helper()
	h := e.a.Auth.DBSCOffer(c.ctx(), "")
	if h == "" {
		e.t.Fatal("no DBSC offer")
	}
	jwt := dev.Registration(headerChallenge(e.t, h), e.d.Cfg.Origin+dbsc.RegisterPath)
	v, err := e.a.Auth.DBSCRegister(c.ctx(), jwt)
	if err != nil {
		return err
	}
	if v.Token == "" || len(v.Config) == 0 {
		e.t.Fatalf("register view %+v", v)
	}
	c.bound = v.Token
	return nil
}

// refresh runs the refresh ceremony of c's bound session with dev.
func (e *env) refresh(c *client, dev *dbsctest.Device) ([]byte, error) {
	e.t.Helper()
	ctx := c.ctxAt(http.MethodPost, dbsc.RefreshPath)
	v, err := e.a.Auth.DBSCRefresh(ctx, nil)
	if err != nil {
		return nil, err
	}
	m := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(v.Challenge)
	if m == nil {
		e.t.Fatalf("challenge header %q", v.Challenge)
	}
	jwt := dev.Refresh(headerChallenge(e.t, v.Challenge), e.d.Cfg.Origin+dbsc.RefreshPath, m[1])
	v, err = e.a.Auth.DBSCRefresh(ctx, jwt)
	if err != nil {
		return jwt, err
	}
	c.bound = v.Token
	return jwt, nil
}

func TestDBSCBindsASession(t *testing.T) {
	e := dbscEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	dev := dbsctest.NewES256()
	if err := e.bind(c, dev); err != nil {
		t.Fatal(err)
	}
	// With both cookies the session works; the long-lived cookie alone does not
	// (a copied cookie on another machine), anywhere but the refresh endpoint.
	if v, ok := c.viewer(); !ok || v.ID != alice {
		t.Fatal("bound session with its bound cookie refused")
	}
	stolen := &client{e: e, cookie: c.cookie}
	if _, ok := stolen.viewer(); ok {
		t.Fatal("long-lived cookie of a bound session accepted without the bound cookie")
	}
	if _, ok := core.ViewerFrom(stolen.ctxAt(http.MethodPost, dbsc.RefreshPath)); ok {
		t.Fatal("the refresh endpoint got a viewer")
	}
	// No second registration, and no offer for a bound session.
	if h := e.a.Auth.DBSCOffer(c.ctx(), ""); h != "" {
		t.Fatalf("bound session offered %q", h)
	}
	// The bound cookie lasts 10 minutes.
	e.advance(dbsc.BoundTTL + time.Second)
	if _, ok := c.viewer(); ok {
		t.Fatal("expired bound cookie accepted")
	}
	// A thief with the long-lived cookie and another key cannot refresh.
	if _, err := e.refresh(stolen, dbsctest.NewES256()); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("refresh with another key: %v", err)
	}
	// The device refreshes; a replayed proof fails.
	jwt, err := e.refresh(c, dev)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.viewer(); !ok {
		t.Fatal("refreshed bound cookie refused")
	}
	if _, err := e.a.Auth.DBSCRefresh(c.ctxAt(http.MethodPost, dbsc.RefreshPath), jwt); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("replayed refresh: %v", err)
	}
	// A bound session is long-lived again: active use slides it past ShortTTL
	// and the idle limit, never past the absolute limit.
	for range 3 {
		e.advance(9 * 24 * time.Hour)
		if _, err := e.refresh(c, dev); err != nil {
			t.Fatalf("refresh after 9 days: %v", err)
		}
	}
	e.advance(auth.UserAbsolute - 27*24*time.Hour)
	if _, err := e.refresh(c, dev); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("bound session outlived the absolute limit: %v", err)
	}
}

func TestDBSCRegistrationRules(t *testing.T) {
	e := dbscEnv(t)
	a := e.authn()
	c, _ := e.enroll("alice", a)
	aud := e.d.Cfg.Origin + dbsc.RegisterPath
	// The offer repeats while it is pending (no race with a registering browser).
	h1 := e.a.Auth.DBSCOffer(c.ctx(), "")
	if h2 := e.a.Auth.DBSCOffer(c.ctx(), ""); h1 != h2 || h1 == "" {
		t.Fatalf("offers %q %q", h1, h2)
	}
	ch := headerChallenge(t, h1)
	dev := dbsctest.NewES256()
	// A wrong aud fails and burns the offer: the right proof for it fails too.
	if _, err := e.a.Auth.DBSCRegister(c.ctx(), dev.Registration(ch, e.d.Cfg.Origin+dbsc.RefreshPath)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("wrong aud: %v", err)
	}
	if _, err := e.a.Auth.DBSCRegister(c.ctx(), dev.Registration(ch, aud)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("burnt offer: %v", err)
	}
	// An offer older than ChallengeTTL is refused.
	ch = headerChallenge(t, e.a.Auth.DBSCOffer(c.ctx(), ""))
	e.advance(auth.ChallengeTTL + time.Second)
	if _, err := e.a.Auth.DBSCRegister(c.ctx(), dev.Registration(ch, aud)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("stale offer: %v", err)
	}
	// An RS256 key (RSA-only TPMs) registers.
	if err := e.bind(c, dbsctest.NewRS256()); err != nil {
		t.Fatalf("RS256: %v", err)
	}
	var jwk string
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT dbsc_public_jwk->>'alg' FROM sessions WHERE dbsc_bound").Scan(&jwk); err != nil || jwk != "RS256" {
		t.Fatalf("stored key alg %q %v", jwk, err)
	}
	// Guests and anonymous callers get nothing.
	g := &client{e: e}
	if h := e.a.Auth.DBSCOffer(g.ctx(), ""); h != "" {
		t.Fatal("anonymous offer")
	}
	if _, err := e.a.Auth.DBSCRegister(g.ctx(), dev.Registration(ch, aud)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal("anonymous registration")
	}
	// An unbound session cannot use the refresh endpoint.
	d, err := e.login(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Auth.DBSCRefresh(d.ctxAt(http.MethodPost, dbsc.RefreshPath), nil); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("refresh of an unbound session: %v", err)
	}
}

func TestDBSCDisabledFailsClosed(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, _ := e.enroll("alice", a)
	if h := e.a.Auth.DBSCOffer(c.ctx(), ""); h != "" {
		t.Fatalf("offer with DBSC off: %q", h)
	}
	dev := dbsctest.NewES256()
	if _, err := e.a.Auth.DBSCRegister(c.ctx(), dev.Registration(make([]byte, 32), e.d.Cfg.Origin+dbsc.RegisterPath)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("register with DBSC off: %v", err)
	}
	// Sessions stay short.
	e.advance(auth.ShortTTL + time.Second)
	if _, ok := c.viewer(); ok {
		t.Fatal("unbound session outlived ShortTTL")
	}
}

func TestSignalStepUp(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	var events []core.SessionStepUp
	core.Subscribe(e.d.Bus, func(ev core.SessionStepUp) { events = append(events, ev) })
	v, _ := c.viewer()
	if err := e.a.Auth.Require(e.ctx, alice, v.Session, "sustained_rate,webdriver"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RequireUser(c.ctx()); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("pending step-up: %v", err)
	}
	if sv, _ := e.a.Auth.Session(c.ctx()); !sv.StepUp || !sv.Authenticated {
		t.Fatalf("session view %+v", sv)
	}
	// Idempotent while pending.
	if err := e.a.Auth.Require(e.ctx, alice, v.Session, "sustained_rate"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].User != alice {
		t.Fatalf("events %+v", events)
	}
	if n := e.count("SELECT count(*) FROM stepups WHERE user_id = $1 AND reason = 'sustained_rate,webdriver' AND satisfied_at IS NULL", int64(alice)); n != 1 {
		t.Fatalf("%d open step-ups", n)
	}
	// The ceremony stays reachable and clears it.
	if err := e.stepUp(c, a); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RequireUser(c.ctx()); err != nil {
		t.Fatalf("after step-up: %v", err)
	}
	if n := e.count("SELECT count(*) FROM stepups WHERE user_id = $1 AND satisfied_at IS NOT NULL", int64(alice)); n != 1 {
		t.Fatal("step-up not satisfied")
	}
	// Another account's session is never touched.
	_, bob := e.enroll("bob", e.authn())
	if err := e.a.Auth.Require(e.ctx, bob, v.Session, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RequireUser(c.ctx()); err != nil {
		t.Fatal("a step-up for bob hit alice's session")
	}
	// Repeated step-ups go to moderators (one merged item).
	for i := range auth.RepeatMin - 1 {
		v, _ := c.viewer()
		if err := e.a.Auth.Require(e.ctx, alice, v.Session, "chain_speed"); err != nil {
			t.Fatal(err)
		}
		if i < auth.RepeatMin-2 {
			if n := e.count("SELECT count(*) FROM mod_queue WHERE source = 3"); n != 0 {
				t.Fatalf("queued after %d step-ups", i+2)
			}
		}
		if err := e.stepUp(c, a); err != nil {
			t.Fatal(err)
		}
	}
	var summary []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT summary FROM mod_queue WHERE source = 3 AND subject_user = $1 AND priority = 3",
		int64(alice)).Scan(&summary); err != nil {
		t.Fatalf("no stepup-repeat item: %v", err)
	}
	var sm map[string]int
	if err := json.Unmarshal(summary, &sm); err != nil || sm["stepups"] != auth.RepeatMin {
		t.Fatalf("summary %s", summary)
	}
}

// The step-up re-verification of a short session extends it (§17.2(2)); a
// bound session is not shortened by it.
func TestStepUpExtendsOnlyShortSessions(t *testing.T) {
	e := dbscEnv(t)
	a := e.authn()
	c, _ := e.enroll("alice", a)
	if err := e.bind(c, dbsctest.NewES256()); err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT expires_at FROM sessions WHERE dbsc_bound").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := e.stepUp(c, a); err != nil {
		t.Fatal(err)
	}
	var after time.Time
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT expires_at FROM sessions WHERE dbsc_bound").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) || after.Sub(e.d.Clock.Now()) < 13*24*time.Hour {
		t.Fatalf("bound expiry %v → %v", before, after)
	}
}

func TestCosignedPasskeyAddCarriesAnAssertion(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	c, alice := e.enroll("alice", a)
	a2 := e.authn()
	opts, err := e.a.Auth.AddPasskeyBegin(c.ctx())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a2.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.a.Auth.AddPasskeyFinish(c.ctx(), resp, "Phone", "", nil)
	if err != nil || res.Cosign != auth.PurposePasskeyAdd {
		t.Fatalf("finish: %+v %v", res, err)
	}
	// Nothing stored yet.
	if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE user_id = $1", int64(alice)); n != 1 {
		t.Fatalf("%d credentials before the co-signature", n)
	}
	// The new passkey cannot sign its own addition (it is not the account's yet).
	in, err := e.a.Cosign.Begin(c.ctx(), auth.PurposePasskeyAdd, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a2.Get(in.Options); err == nil {
		t.Fatal("the new authenticator answered a get restricted to existing passkeys")
	}
	ownResp, err := a2.GetWith(in.Options, a2.Credentials()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Auth.AddPasskeyFinish(c.ctx(), nil, "", in.Intent, ownResp); err == nil {
		t.Fatal("self-signed addition accepted")
	}
	// The pending credential was single use: start over, sign with the old key.
	if err := e.addPasskey(c, a2, "Phone"); err != nil {
		t.Fatal(err)
	}
	var assertions []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT assertions FROM signed_records WHERE kind = $1
		AND convert_from(body, 'UTF8')::jsonb->>'credential_id' = $2`, int16(core.RecPasskeyChange),
		base64.RawURLEncoding.EncodeToString(a2.Credentials()[len(a2.Credentials())-1].ID)).Scan(&assertions); err != nil {
		t.Fatal(err)
	}
	var as []map[string]any
	if err := json.Unmarshal(assertions, &as); err != nil || len(as) != 1 ||
		as[0]["credential_id"] != base64.RawURLEncoding.EncodeToString(a.Credentials()[0].ID) {
		t.Fatalf("assertions %s", assertions)
	}
	// An intent for another credential's removal does not remove this one.
	list, err := e.a.Auth.Passkeys(c.ctx())
	if err != nil || len(list) != 2 {
		t.Fatalf("passkeys %+v %v", list, err)
	}
	intent, assertion, err := e.cosign(c, a, auth.PurposePasskeyRemove, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, e.a.Auth.RemovePasskey(c.ctx(), list[1].ID, intent, assertion))
	if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE user_id = $1", int64(alice)); n != 2 {
		t.Fatal("mismatched co-signature removed a passkey")
	}
	// A parked credential expires.
	opts, _ = e.a.Auth.AddPasskeyBegin(c.ctx())
	a3 := e.authn()
	resp, _ = a3.Create(opts)
	if _, err := e.a.Auth.AddPasskeyFinish(c.ctx(), resp, "", "", nil); err != nil {
		t.Fatal(err)
	}
	e.advance(auth.PendingTTL + time.Second)
	if _, err := e.a.Cosign.Begin(c.ctx(), auth.PurposePasskeyAdd, ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("expired parked credential drafted: %v", err)
	}
}

// Credential-less sessions (test hooks) keep the Phase 0 path.
func TestHookSessionPasskeyChangesStayServerSigned(t *testing.T) {
	e := newEnv(t)
	a := e.authn()
	_, alice := e.enroll("alice", a)
	h := e.hookSession("alice")
	a2 := e.authn()
	opts, err := e.a.Auth.AddPasskeyBegin(h.ctx())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a2.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := e.a.Auth.AddPasskeyFinish(h.ctx(), resp, "x", "", nil); err != nil || res.Cosign != "" {
		t.Fatalf("hook add: %+v %v", res, err)
	}
	if n := e.count("SELECT count(*) FROM webauthn_credentials WHERE user_id = $1", int64(alice)); n != 2 {
		t.Fatalf("%d credentials", n)
	}
}
