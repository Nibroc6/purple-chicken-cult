package cosign_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/cosign"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	s   *cosign.Service
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	e := &env{t: t, a: a, d: a.Deps(), s: a.Cosign, ctx: context.Background()}
	e.s.RegisterPurpose(purposeFriend, core.RecFriendship, e.friendBody)
	return e
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	e.must(e.a.Hooks().AdvanceClock(d))
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.must(e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n))
	return n
}

// purposeFriend drafts a Friendship of the viewer and the target person.
const purposeFriend = "test_friend"

type friendship struct {
	A         uuid.UUID `json:"a"`
	B         uuid.UUID `json:"b"`
	Level     string    `json:"level"`
	CreatedAt sigs.Time `json:"created_at"`
}

func (e *env) ext(id core.UserID) uuid.UUID {
	e.t.Helper()
	m, err := e.a.Users.Get(e.ctx, id)
	e.must(err)
	return m.ExtID
}

func (e *env) friendBody(_ context.Context, v core.Viewer, target gate.Ref) (any, error) {
	other, ok := e.d.Refs.OpenUser(v.ID, target)
	if !ok || other == v.ID {
		return nil, gate.ErrNotFound
	}
	a, b := e.ext(v.ID), e.ext(other)
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return friendship{A: a, B: b, Level: "friend", CreatedAt: sigs.T(e.d.Clock.Now())}, nil
}

// client is one browser: a cookie run through the real session middleware.
type client struct {
	e      *env
	cookie string
	auth   *authtest.Authenticator
	id     core.UserID
}

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

func (c *client) viewer() core.Viewer {
	c.e.t.Helper()
	v, ok := core.ViewerFrom(c.ctx())
	if !ok || v.Kind != core.SessUser {
		c.e.t.Fatalf("not signed in: %+v", v)
	}
	return v
}

// enroll seeds a genesis account and enrolls a passkey of a fresh
// authenticator (prepared by knobs) through the real ceremony.
func (e *env) enroll(handle string, knobs ...func(*authtest.Authenticator)) *client {
	e.t.Helper()
	tok, err := e.a.Auth.GenesisToken(e.ctx, handle)
	e.must(err)
	c := &client{e: e, auth: authtest.New(e.d.Cfg.Origin)}
	for _, k := range knobs {
		k(c.auth)
	}
	ev, err := e.a.Auth.EnrollBegin(c.ctx(), tok)
	e.must(err)
	c.cookie = ev.Token
	resp, err := c.auth.Create(ev.Options)
	e.must(err)
	sess, _, err := e.a.Auth.EnrollFinish(c.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	e.must(err)
	c.cookie = sess
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	c.id = id
	return c
}

// begin opens a friend intent of c towards other.
func (e *env) begin(c *client, other *client) core.Intent {
	e.t.Helper()
	in, err := e.s.BeginIntent(c.ctx(), c.viewer(), purposeFriend, e.d.Refs.User(c.id, other.id))
	e.must(err)
	return in
}

// require runs Require for c in its own transaction; on success it also signs
// the record with the assertion (as a caller module does) and returns its hash.
func (e *env) require(c *client, in core.Intent, assertion []byte) ([]byte, error) {
	e.t.Helper()
	var hash []byte
	err := e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		a, _, err := e.s.Require(c.ctx(), tx, c.viewer(), purposeFriend, core.RecFriendship, nil, in.ID, assertion)
		if err != nil {
			return err
		}
		hash, err = e.d.Signer.Sign(e.ctx, tx, core.RecFriendship, c.id, nil, []core.UserID{c.id}, a)
		return err
	})
	return hash, err
}

func wantRejected(t *testing.T, err error, what string) {
	t.Helper()
	var p *gate.Problem
	ok := errors.Is(err, gate.ErrNotFound) || (errors.As(err, &p) && p.Code == "passkey_rejected")
	if !ok {
		t.Fatalf("%s: want rejection, got %v", what, err)
	}
}

// The record shown is the exact draft; the assertion over it is accepted once
// and the record is stored with it.
func TestIntentShowsDraftAndIsSingleUse(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	in := e.begin(alice, bob)
	var rec map[string]any
	e.must(json.Unmarshal(in.Record, &rec))
	member, _ := rec["cosign"].(map[string]any)
	id, _ := e.d.Refs.OpenHandshake(alice.id, in.ID)
	if rec["kind"] != "friendship" || member["nonce"] != id.String() || member["issued_at"] == nil {
		t.Fatalf("record %s", in.Record)
	}
	asr, err := alice.auth.Get(in.Options)
	e.must(err)
	hash, err := e.require(alice, in, asr)
	e.must(err)
	var assertions []byte
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT assertions FROM signed_records WHERE hash = $1", hash).Scan(&assertions))
	if !bytes.Contains(assertions, []byte(`"draft"`)) || !bytes.Contains(assertions, []byte(`"signer"`)) {
		t.Fatalf("assertions %s", assertions)
	}
	// Used intent refused (even with a fresh ceremony in the session).
	in2 := e.begin(alice, bob)
	asr2, err := alice.auth.Get(in2.Options)
	e.must(err)
	_, err = e.require(alice, in, asr2)
	wantRejected(t, err, "used intent")
	if n := e.count("SELECT count(*) FROM cosign_intents WHERE used_at IS NOT NULL"); n != 1 {
		t.Fatalf("used intents %d", n)
	}
}

// Acceptance: replaying an assertion on an identical second draft is rejected
// (different nonce ⇒ different challenge), and an assertion for another
// record or challenge is rejected.
func TestAssertionBoundToItsIntent(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	first := e.begin(alice, bob)
	asr, err := alice.auth.Get(first.Options)
	e.must(err)
	_, err = e.require(alice, first, asr)
	e.must(err)

	// Identical second draft (same people, same instant): new nonce, new challenge.
	second := e.begin(alice, bob)
	var o1, o2 struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	e.must(json.Unmarshal(first.Options, &o1))
	e.must(json.Unmarshal(second.Options, &o2))
	if o1.PublicKey.Challenge == o2.PublicKey.Challenge || bytes.Equal(first.Record, second.Record) {
		t.Fatal("identical drafts share a challenge")
	}
	_, err = e.require(alice, second, asr)
	wantRejected(t, err, "replayed assertion")

	// Two open intents: the assertion over the later one's challenge does not
	// satisfy the earlier one.
	carol := e.enroll("carol")
	a1 := e.begin(alice, bob)
	a2 := e.begin(alice, carol)
	asr2, err := alice.auth.Get(a2.Options)
	e.must(err)
	_, err = e.require(alice, a1, asr2)
	wantRejected(t, err, "assertion for another intent")

	// A body that is not the drafted record is refused.
	in := e.begin(alice, bob)
	asr3, err := alice.auth.Get(in.Options)
	e.must(err)
	err = e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		_, _, err := e.s.Require(alice.ctx(), tx, alice.viewer(), purposeFriend, core.RecFriendship,
			friendship{A: e.ext(alice.id), B: e.ext(carol.id), Level: "friend", CreatedAt: sigs.T(e.d.Clock.Now())},
			in.ID, asr3)
		return err
	})
	wantRejected(t, err, "other body")
}

// Acceptance: origin mismatch, crossOrigin: true, topOrigin present, UV=0,
// another user's credential, a non-increasing counter and a changed BE flag
// are each rejected.
func TestHostileAssertionsRejected(t *testing.T) {
	cases := map[string]struct {
		enroll func(*authtest.Authenticator)
		assert func(*authtest.Authenticator)
	}{
		"origin mismatch": {assert: func(a *authtest.Authenticator) { a.OriginOverride = "https://evil.example" }},
		"origin suffix":   {assert: func(a *authtest.Authenticator) { a.OriginOverride = a.Origin + "/" }},
		"crossOrigin":     {assert: func(a *authtest.Authenticator) { a.CrossOrigin = true }},
		"topOrigin":       {assert: func(a *authtest.Authenticator) { a.TopOrigin = a.Origin }},
		"no UV":           {assert: func(a *authtest.Authenticator) { a.NoUV = true }},
		"no UP":           {assert: func(a *authtest.Authenticator) { a.NoUP = true }},
		"counter": {enroll: func(a *authtest.Authenticator) { a.StartCounter, a.FrozenCounter = 7, true },
			assert: func(*authtest.Authenticator) {}},
		"BE flag changed": {assert: func(a *authtest.Authenticator) { a.BackupEligible = true }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			var knobs []func(*authtest.Authenticator)
			if c.enroll != nil {
				knobs = append(knobs, c.enroll)
			}
			alice, bob := e.enroll("alice", knobs...), e.enroll("bob")
			in := e.begin(alice, bob)
			c.assert(alice.auth)
			asr, err := alice.auth.Get(in.Options)
			e.must(err)
			_, err = e.require(alice, in, asr)
			wantRejected(t, err, name)
			if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecFriendship)); n != 0 {
				t.Fatalf("record written: %d", n)
			}
		})
	}
	t.Run("another user's credential", func(t *testing.T) {
		e := newEnv(t)
		alice, bob := e.enroll("alice"), e.enroll("bob")
		in := e.begin(alice, bob)
		// Bob's authenticator answers alice's options with bob's credential.
		bobCred := bob.auth.Credentials()[0]
		alice.auth.Import(bobCred)
		asr, err := alice.auth.GetWith(in.Options, bobCred.ID)
		e.must(err)
		_, err = e.require(alice, in, asr)
		wantRejected(t, err, "foreign credential")
	})
}

// The intent is consumed in the caller's transaction: a rolled-back write
// leaves it unused; it expires after TTL; foreign viewers, guests and unknown
// purposes get the uniform not-found.
func TestIntentLifecycle(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	in := e.begin(alice, bob)
	asr, err := alice.auth.Get(in.Options)
	e.must(err)
	errAbort := errors.New("abort")
	err = e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		if _, _, err := e.s.Require(alice.ctx(), tx, alice.viewer(), purposeFriend, core.RecFriendship, nil, in.ID, asr); err != nil {
			return err
		}
		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("require in tx: %v", err)
	}
	if n := e.count("SELECT count(*) FROM cosign_intents WHERE used_at IS NULL"); n != 1 {
		t.Fatalf("rolled-back consume persisted (%d unused)", n)
	}

	// Bob cannot use alice's intent.
	_, _, err = e.s.Require(bob.ctx(), nil, bob.viewer(), purposeFriend, core.RecFriendship, nil, in.ID, asr)
	wantRejected(t, err, "foreign intent")
	// Another purpose or kind does not consume it.
	_, _, err = e.s.Require(alice.ctx(), nil, alice.viewer(), cosign.PurposeBlock, core.RecFriendship, nil, in.ID, asr)
	wantRejected(t, err, "other purpose")
	_, _, err = e.s.Require(alice.ctx(), nil, alice.viewer(), purposeFriend, core.RecBlock, nil, in.ID, asr)
	wantRejected(t, err, "other kind")

	// Expiry.
	in = e.begin(alice, bob)
	asr, err = alice.auth.Get(in.Options)
	e.must(err)
	e.advance(cosign.TTL + time.Second)
	_, err = e.require(alice, in, asr)
	wantRejected(t, err, "expired")

	// Unknown purpose, unresolvable target, guest viewer.
	if _, err := e.s.BeginIntent(alice.ctx(), alice.viewer(), "nope", ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("unknown purpose: %v", err)
	}
	if _, err := e.s.BeginIntent(alice.ctx(), alice.viewer(), purposeFriend, "garbage"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("bad target: %v", err)
	}
	if _, err := e.s.BeginIntent(e.ctx, core.Viewer{Kind: core.SessGuest}, purposeFriend, ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("guest: %v", err)
	}
	if _, err := e.s.Begin(e.ctx, purposeFriend, ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous begin: %v", err)
	}

	// Purge drops intents a day past expiry.
	e.advance(25 * time.Hour)
	n, err := e.s.Purge(e.ctx)
	if err != nil || n == 0 || e.count("SELECT count(*) FROM cosign_intents") != 0 {
		t.Fatalf("purge %d %v", n, err)
	}
}

// The test-only check purpose drafts a record of the viewer only.
func TestCheckPurpose(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	iv, err := e.s.Begin(alice.ctx(), cosign.PurposeCheck, "")
	e.must(err)
	if iv.Intent == "" || !strings.Contains(string(iv.Record), `"op":"check"`) ||
		!strings.Contains(string(iv.Options), `"allowCredentials"`) {
		t.Fatalf("check intent %+v", iv)
	}
}
