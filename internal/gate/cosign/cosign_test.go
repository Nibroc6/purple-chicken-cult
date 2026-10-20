package cosign_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/cosign"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	return &env{t: t, a: a, d: a.Deps(), ctx: context.Background()}
}

func (e *env) authn() *authtest.Authenticator { return authtest.New(e.d.Cfg.Origin) }

// client is one browser: a cookie run through the real session middleware.
type client struct {
	e      *env
	cookie string
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
	if !ok {
		c.e.t.Fatal("no viewer")
	}
	return v
}

// enroll seeds a genesis account and enrolls a's passkey through the real ceremony.
func (e *env) enroll(handle string, a *authtest.Authenticator) (*client, core.UserID, uuid.UUID) {
	e.t.Helper()
	tok, err := e.a.Auth.GenesisToken(e.ctx, handle)
	if err != nil {
		e.t.Fatal(err)
	}
	c := &client{e: e}
	ev, err := e.a.Auth.EnrollBegin(c.ctx(), tok)
	if err != nil {
		e.t.Fatal(err)
	}
	c.cookie = ev.Token
	resp, err := a.Create(ev.Options)
	if err != nil {
		e.t.Fatal(err)
	}
	sess, _, err := e.a.Auth.EnrollFinish(c.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	if err != nil {
		e.t.Fatal(err)
	}
	c.cookie = sess
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	m, err := e.a.Users.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return c, id, m.ExtID
}

type friendship struct {
	A         uuid.UUID `json:"a"`
	B         uuid.UUID `json:"b"`
	Level     string    `json:"level"`
	CreatedAt sigs.Time `json:"created_at"`
}

// befriendDrafter stands in for P1-W03: the friend_accept draft is the
// Friendship of the viewer and the person behind target.
func (e *env) befriendDrafter() core.Drafter {
	return func(_ context.Context, v core.Viewer, target gate.Ref, issued time.Time) (core.RecordKind, any, error) {
		peer, ok := e.d.Refs.OpenUser(v.ID, target)
		if !ok {
			return 0, nil, gate.ErrNotFound
		}
		a, err := e.a.Users.Get(e.ctx, v.ID)
		if err != nil {
			return 0, nil, err
		}
		b, err := e.a.Users.Get(e.ctx, peer)
		if err != nil {
			return 0, nil, gate.ErrNotFound
		}
		x, y := a.ExtID, b.ExtID
		if bytes.Compare(x[:], y[:]) > 0 {
			x, y = y, x
		}
		return core.RecFriendship, friendship{A: x, B: y, Level: "friend", CreatedAt: sigs.T(issued)}, nil
	}
}

// friendshipBody is what the writer passes to Require: everything but the time.
func friendshipBody(a, b uuid.UUID) map[string]any {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return map[string]any{"a": a, "b": b, "level": "friend"}
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func wantRejected(t *testing.T, err error) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != "passkey_rejected" {
		t.Fatalf("want passkey_rejected, got %v", err)
	}
}

// pair enrolls alice and bob and registers the friend_accept drafter.
type pair struct {
	e             *env
	ca, cb        *client
	aa, ab        *authtest.Authenticator
	alice, bob    core.UserID
	extA, extB    uuid.UUID
	bobForAlice   gate.Ref
	acceptPurpose string
}

func newPair(t *testing.T) *pair {
	e := newEnv(t)
	p := &pair{e: e, aa: e.authn(), ab: e.authn(), acceptPurpose: cosign.PurposeFriendAccept}
	p.ca, p.alice, p.extA = e.enroll("alice", p.aa)
	p.cb, p.bob, p.extB = e.enroll("bob", p.ab)
	p.bobForAlice = e.d.Refs.User(p.alice, p.bob)
	e.a.Cosign.RegisterDrafter(cosign.PurposeFriendAccept, e.befriendDrafter())
	return p
}

// begin opens alice's friend_accept intent for bob.
func (p *pair) begin() core.Intent {
	p.e.t.Helper()
	in, err := p.e.a.Cosign.BeginIntent(p.ca.ctx(), p.ca.viewer(), p.acceptPurpose, p.bobForAlice)
	if err != nil {
		p.e.t.Fatal(err)
	}
	return in
}

// require runs Require in its own transaction and, when it succeeds, signs the
// draft with the assertion (as the owning module would).
func (p *pair) require(in core.Intent, body any, resp []byte) ([]byte, error) {
	p.e.t.Helper()
	var hash []byte
	err := p.e.d.DB.Tx(p.e.ctx, func(tx pgx.Tx) error {
		a, draft, err := p.e.a.Cosign.Require(p.ca.ctx(), tx, p.ca.viewer(), p.acceptPurpose, core.RecFriendship, body,
			in.ID, resp)
		if err != nil {
			return err
		}
		hash, err = p.e.a.Records.Sign(p.e.ctx, tx, core.RecFriendship, p.alice, json.RawMessage(draft),
			[]core.UserID{p.alice, p.bob}, a)
		return err
	})
	return hash, err
}

func (p *pair) export(u core.UserID) []byte {
	p.e.t.Helper()
	var buf bytes.Buffer
	ctx := core.WithViewer(p.e.ctx, core.Viewer{ID: u, Kind: core.SessUser, AuthAt: p.e.d.Clock.Now()})
	if err := p.e.a.Account.Export(ctx, &buf); err != nil {
		p.e.t.Fatalf("export: %v", err)
	}
	return buf.Bytes()
}

func verify(t *testing.T, z []byte) gate.VerifyReport {
	t.Helper()
	rep, err := records.VerifyExport(bytes.NewReader(z), int64(len(z)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestIntentChallengesTheDraft(t *testing.T) {
	p := newPair(t)
	in := p.begin()
	var rec map[string]any
	if err := json.Unmarshal(in.Record, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["kind"] != "friendship" || rec["nonce"] == nil || rec["issued_at"] == nil || rec["level"] != "friend" {
		t.Fatalf("record %s", in.Record)
	}
	want := sigs.Challenge("friendship", in.Record)
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			UV        string `json:"userVerification"`
			Allow     []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(in.Options, &o); err != nil {
		t.Fatal(err)
	}
	if o.PublicKey.Challenge != base64.RawURLEncoding.EncodeToString(want[:]) || o.PublicKey.UV != "required" ||
		len(o.PublicKey.Allow) != 1 {
		t.Fatalf("options %s", in.Options)
	}
	// The nonce is the intent's own id and is never reused.
	in2 := p.begin()
	var rec2 map[string]any
	_ = json.Unmarshal(in2.Record, &rec2)
	if rec2["nonce"] == rec["nonce"] || in2.ID == in.ID {
		t.Fatal("nonce reused")
	}

	// Unknown purpose is the caller's own problem; a purpose nobody drafts and a
	// target the viewer cannot name are the uniform not-found.
	var pr *gate.Problem
	if _, err := p.e.a.Cosign.BeginIntent(p.ca.ctx(), p.ca.viewer(), "launch_missiles", p.bobForAlice); !errors.As(err, &pr) {
		t.Fatalf("unknown purpose: %v", err)
	}
	_, err := p.e.a.Cosign.BeginIntent(p.ca.ctx(), p.ca.viewer(), cosign.PurposeAccountDelete, "")
	wantNotFound(t, err)
	_, err = p.e.a.Cosign.BeginIntent(p.ca.ctx(), p.ca.viewer(), p.acceptPurpose, p.e.d.Refs.User(p.bob, p.alice))
	wantNotFound(t, err)
	_, err = p.e.a.Cosign.BeginIntent(p.ca.ctx(), core.Viewer{Kind: core.SessGuest}, p.acceptPurpose, p.bobForAlice)
	wantNotFound(t, err)
}

// Acceptance: a co-signed edge is signed by the author's custodial key,
// countersigned by the server, carries the assertion and verifies offline; the
// intent is single use and the assertion cannot be replayed on an identical
// second draft.
func TestCosignedRecordVerifiesAndIntentIsSingleUse(t *testing.T) {
	p := newPair(t)
	in := p.begin()
	resp, err := p.aa.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	body := friendshipBody(p.extA, p.extB)
	hash, err := p.require(in, body, resp)
	if err != nil {
		t.Fatal(err)
	}
	var keyID string
	var cosigs, assertions []byte
	if err := p.e.d.DB.Q().QueryRow(p.e.ctx, "SELECT key_id, cosigs, assertions FROM signed_records WHERE hash = $1", hash).
		Scan(&keyID, &cosigs, &assertions); err != nil {
		t.Fatal(err)
	}
	if len(keyID) < 4 || keyID[:4] != records.UserKeyPrefix || len(cosigs) == 0 || len(assertions) == 0 {
		t.Fatalf("stored record key=%s cosigs=%s assertions=%s", keyID, cosigs, assertions)
	}
	for _, u := range []core.UserID{p.alice, p.bob} {
		rep := verify(t, p.export(u))
		if rep.Valid != rep.Records || len(rep.Failures) > 0 {
			t.Fatalf("export of %d: %+v", u, rep)
		}
	}

	// Used intent: refused, even with a fresh assertion.
	resp2, err := p.aa.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.require(in, body, resp2)
	wantNotFound(t, err)

	// An identical second draft has another nonce, so another challenge: the
	// first assertion does not sign it.
	in2 := p.begin()
	_, err = p.require(in2, body, resp)
	wantRejected(t, err)
}

// Acceptance: an assertion for another record or challenge is rejected, and a
// body that differs from the draft never consumes a valid assertion.
func TestAssertionForAnotherRecordRejected(t *testing.T) {
	p := newPair(t)
	first := p.begin()
	respFirst, err := p.aa.Get(first.Options)
	if err != nil {
		t.Fatal(err)
	}
	second := p.begin() // replaces the pending ceremony: only second's challenge verifies now
	_, err = p.require(first, friendshipBody(p.extA, p.extB), respFirst)
	wantRejected(t, err)

	// The second intent with an assertion over the first draft: rejected too.
	_, err = p.require(second, friendshipBody(p.extA, p.extB), respFirst)
	wantRejected(t, err)

	// A body that is not the draft (friend+ instead of friend) is not found.
	third := p.begin()
	resp, err := p.aa.Get(third.Options)
	if err != nil {
		t.Fatal(err)
	}
	other := friendshipBody(p.extA, p.extB)
	other["level"] = "friend+"
	_, err = p.require(third, other, resp)
	wantNotFound(t, err)
	// Another purpose or another record kind for the same intent: not found.
	err = p.e.d.DB.Tx(p.e.ctx, func(tx pgx.Tx) error {
		_, _, err := p.e.a.Cosign.Require(p.ca.ctx(), tx, p.ca.viewer(), cosign.PurposeUnfriend, core.RecFriendship,
			friendshipBody(p.extA, p.extB), third.ID, resp)
		return err
	})
	wantNotFound(t, err)
	err = p.e.d.DB.Tx(p.e.ctx, func(tx pgx.Tx) error {
		_, _, err := p.e.a.Cosign.Require(p.ca.ctx(), tx, p.ca.viewer(), p.acceptPurpose, core.RecRevocation,
			friendshipBody(p.extA, p.extB), third.ID, resp)
		return err
	})
	wantNotFound(t, err)
}

// Acceptance: origin mismatch, crossOrigin, topOrigin, UV=0 and another
// user's credential are each rejected.
func TestHostileAssertionsRejected(t *testing.T) {
	p := newPair(t)
	cases := []struct {
		name string
		knob func(*authtest.Authenticator)
		use  func() *authtest.Authenticator
	}{
		{"wrong origin", func(x *authtest.Authenticator) { x.OriginOverride = "http://localhost:8081" }, nil},
		{"cross origin", func(x *authtest.Authenticator) { x.CrossOrigin = true }, nil},
		{"top origin", func(x *authtest.Authenticator) { x.TopOrigin = "https://evil.example" }, nil},
		{"no user verification", func(x *authtest.Authenticator) { x.NoUV = true }, nil},
		{"another user's credential", func(*authtest.Authenticator) {}, func() *authtest.Authenticator { return p.ab }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := p.begin()
			x := authtest.New(p.e.d.Cfg.Origin)
			x.Import(p.aa.Credentials()[0])
			if c.use != nil {
				x = c.use()
			}
			c.knob(x)
			var resp []byte
			var err error
			if c.use != nil {
				resp, err = x.GetWith(in.Options, x.Credentials()[0].ID)
			} else {
				resp, err = x.Get(in.Options)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.require(in, friendshipBody(p.extA, p.extB), resp)
			wantRejected(t, err)
		})
	}
	// Bob cannot use alice's intent, and an expired intent is gone.
	in := p.begin()
	resp, err := p.aa.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	err = p.e.d.DB.Tx(p.e.ctx, func(tx pgx.Tx) error {
		_, _, err := p.e.a.Cosign.Require(p.cb.ctx(), tx, p.cb.viewer(), p.acceptPurpose, core.RecFriendship,
			friendshipBody(p.extA, p.extB), in.ID, resp)
		return err
	})
	wantNotFound(t, err)
	in = p.begin()
	resp, err = p.aa.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.e.a.Hooks().AdvanceClock(cosign.TTL + time.Second); err != nil {
		t.Fatal(err)
	}
	_, err = p.require(in, friendshipBody(p.extA, p.extB), resp)
	wantNotFound(t, err)
	if n, err := p.e.a.Cosign.Purge(p.e.ctx); err != nil || n != 0 {
		t.Fatalf("purge before a day: %d %v", n, err)
	}
	if err := p.e.a.Hooks().AdvanceClock(25 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if n, err := p.e.a.Cosign.Purge(p.e.ctx); err != nil || n == 0 {
		t.Fatalf("purge: %d %v", n, err)
	}
}

// editRecords rewrites the records.jsonl lines of an export zip.
func editRecords(t *testing.T, z []byte, edit func(lines [][]byte) [][]byte) []byte {
	t.Helper()
	files := unzip(t, z)
	ls := bytes.Split(bytes.TrimRight(files[records.FileRecords], "\n"), []byte{'\n'})
	ls = edit(ls)
	files[records.FileRecords] = append(bytes.Join(ls, []byte{'\n'}), '\n')
	return rezip(t, files)
}

func unzip(t *testing.T, z []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

func rezip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func codes(rep gate.VerifyReport) map[string]int {
	out := map[string]int{}
	for _, f := range rep.Failures {
		out[f.Code]++
	}
	return out
}

// Acceptance: a forged edge without an assertion is detected by VerifyExport;
// so are a stripped assertion, a stripped server countersignature and an
// assertion moved onto another record.
func TestVerifyExportDetectsForgedEdges(t *testing.T) {
	p := newPair(t)
	in := p.begin()
	resp, err := p.aa.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.require(in, friendshipBody(p.extA, p.extB), resp); err != nil {
		t.Fatal(err)
	}
	z := p.export(p.alice)
	if rep := verify(t, z); len(rep.Failures) > 0 {
		t.Fatalf("clean export: %+v", rep)
	}
	friendshipLine := func(ls [][]byte) int {
		for i, l := range ls {
			if bytes.Contains(l, []byte(`"kind":"friendship"`)) {
				return i
			}
		}
		t.Fatal("no friendship line")
		return -1
	}
	mutate := func(f func(env map[string]json.RawMessage)) []byte {
		return editRecords(t, z, func(ls [][]byte) [][]byte {
			i := friendshipLine(ls)
			var env map[string]json.RawMessage
			if err := json.Unmarshal(ls[i], &env); err != nil {
				t.Fatal(err)
			}
			f(env)
			b, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			ls[i] = b
			return ls
		})
	}
	cases := map[string]struct {
		f    func(env map[string]json.RawMessage)
		code string
	}{
		"assertion stripped":  {func(env map[string]json.RawMessage) { delete(env, "assertions") }, records.FailMissingAssertion},
		"cosig stripped":      {func(env map[string]json.RawMessage) { delete(env, "cosigs") }, records.FailBadCosig},
		"assertion corrupted": {func(env map[string]json.RawMessage) { env["assertions"] = corruptSig(t, env["assertions"]) }, records.FailBadAssertion},
	}
	for name, c := range cases {
		rep := verify(t, mutate(c.f))
		if codes(rep)[c.code] != 1 {
			t.Fatalf("%s: %+v", name, rep)
		}
	}

	// Once co-signing is mandatory, an edge forged without any assertion (and
	// without a nonce, P0-style) is flagged; edges before the cutover are not.
	before := p.export(p.alice)
	now := p.e.d.Clock.Now()
	if err := p.e.a.Records.SetCosignRequiredFrom(p.e.ctx, &now); err != nil {
		t.Fatal(err)
	}
	carol := p.e.a.Users
	cid, err := carol.Create(p.e.ctx, p.e.d.DB.Q(), core.NewUser{Handle: "carol", Genesis: true})
	if err != nil {
		t.Fatal(err)
	}
	cm, _ := carol.Get(p.e.ctx, cid)
	x, y := p.extA, cm.ExtID
	if bytes.Compare(x[:], y[:]) > 0 {
		x, y = y, x
	}
	if _, err := p.e.a.Records.Sign(p.e.ctx, nil, core.RecFriendship, cid,
		friendship{A: x, B: y, Level: "friend", CreatedAt: sigs.T(now)}, []core.UserID{p.alice, cid}); err != nil {
		t.Fatal(err)
	}
	rep := verify(t, p.export(p.alice))
	if codes(rep)[records.FailMissingAssertion] != 1 || rep.Valid != rep.Records-1 {
		t.Fatalf("forged edge after the cutover: %+v", rep)
	}
	if rep := verify(t, before); len(rep.Failures) > 0 {
		t.Fatalf("export before the cutover: %+v", rep)
	}
}

func corruptSig(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var as []map[string]any
	if err := json.Unmarshal(raw, &as); err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(as[0]["signature"].(string))
	sig[len(sig)-1] ^= 1
	as[0]["signature"] = base64.RawURLEncoding.EncodeToString(sig)
	b, _ := json.Marshal(as)
	return b
}

type passkeyChange struct {
	User         uuid.UUID  `json:"user"`
	Op           string     `json:"op"`
	CredentialID sigs.Bytes `json:"credential_id"`
	PublicKey    sigs.Bytes `json:"public_key"`
	Alg          int        `json:"alg"`
	At           sigs.Time  `json:"at"`
}

func newCOSEKey(t *testing.T) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: b[1:33], -3: b[33:65]})
	if err != nil {
		t.Fatal(err)
	}
	return cose
}

// Acceptance: once co-signing is mandatory, a passkey add not co-signed by an
// existing credential fails verification; one co-signed by the account's first
// credential verifies.
func TestPasskeyChainNeedsAnExistingCredential(t *testing.T) {
	p := newPair(t)
	now := p.e.d.Clock.Now()
	if err := p.e.a.Records.SetCosignRequiredFrom(p.e.ctx, &now); err != nil {
		t.Fatal(err)
	}
	if rep := verify(t, p.export(p.alice)); len(rep.Failures) > 0 {
		t.Fatalf("first passkey: %+v", rep)
	}
	// Stands in for P1-W07's passkey_add drafter.
	cred, pub := []byte("second-credential"), newCOSEKey(t)
	p.e.a.Cosign.RegisterDrafter(cosign.PurposePasskeyAdd, func(_ context.Context, v core.Viewer, _ gate.Ref, issued time.Time) (core.RecordKind, any, error) {
		return core.RecPasskeyChange, passkeyChange{User: p.extA, Op: "add", CredentialID: cred, PublicKey: pub, Alg: -7,
			At: sigs.T(issued)}, nil
	})
	addBody := map[string]any{"user": p.extA, "op": "add", "credential_id": sigs.Bytes(cred), "public_key": sigs.Bytes(pub), "alg": -7}

	// Not co-signed: a passkey add signed without an assertion. (Exports list
	// records by creation time, so the clock moves between writes.)
	tick := func() {
		if err := p.e.a.Hooks().AdvanceClock(time.Second); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	if _, err := p.e.a.Records.Sign(p.e.ctx, nil, core.RecPasskeyChange, p.alice,
		passkeyChange{User: p.extA, Op: "add", CredentialID: []byte("rogue"), PublicKey: newCOSEKey(t), Alg: -7,
			At: sigs.T(p.e.d.Clock.Now())}, []core.UserID{p.alice}); err != nil {
		t.Fatal(err)
	}
	tick()
	rep := verify(t, p.export(p.alice))
	if codes(rep)[records.FailUnchainedPasskey] != 1 {
		t.Fatalf("rogue add: %+v", rep)
	}

	// Co-signed by alice's first credential.
	in, err := p.e.a.Cosign.BeginIntent(p.ca.ctx(), p.ca.viewer(), cosign.PurposePasskeyAdd, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.aa.Get(in.Options)
	if err != nil {
		t.Fatal(err)
	}
	err = p.e.d.DB.Tx(p.e.ctx, func(tx pgx.Tx) error {
		a, draft, err := p.e.a.Cosign.Require(p.ca.ctx(), tx, p.ca.viewer(), cosign.PurposePasskeyAdd, core.RecPasskeyChange,
			addBody, in.ID, resp)
		if err != nil {
			return err
		}
		_, err = p.e.a.Records.Sign(p.e.ctx, tx, core.RecPasskeyChange, p.alice, json.RawMessage(draft), []core.UserID{p.alice}, a)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rep = verify(t, p.export(p.alice))
	if codes(rep)[records.FailUnchainedPasskey] != 1 || rep.Valid != rep.Records-1 {
		t.Fatalf("co-signed add: %+v", rep)
	}
}
