package invites_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites/hstest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	s   *invites.Service
	w   *wiretest.TestWorld
	rec *coreref.RecordingNotifier
	met *coreref.MemMetrics
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	rec := &coreref.RecordingNotifier{D: d}
	met := &coreref.MemMetrics{}
	d.Notify, d.Metrics = rec, met
	return &env{t: t, a: a, d: d, s: a.Invites, w: wiretest.World(t, a), rec: rec, met: met, ctx: context.Background()}
}

// as is a signed-in viewer without a real session (enough for everything but
// passkey ceremonies).
func (e *env) as(id core.UserID) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: id, Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
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

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	e.must(e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n))
	return n
}

// member is a signed-in browser with a real session and a passkey.
type member struct {
	e      *env
	id     core.UserID
	cookie string
	auth   *authtest.Authenticator
}

func (m *member) ctx() context.Context {
	m.e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: m.cookie}) //nolint:gosec // test request
	var got context.Context
	m.e.a.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	if v, ok := core.ViewerFrom(got); !ok || v.Kind != core.SessUser {
		m.e.t.Fatalf("not signed in: %+v", v)
	}
	return got
}

func (m *member) viewer() core.Viewer {
	v, _ := core.ViewerFrom(m.ctx())
	return v
}

// enroll seeds a genesis account that enrolls a passkey through the real ceremony.
func (e *env) enroll(handle string) *member {
	e.t.Helper()
	tok, err := e.a.Auth.GenesisToken(e.ctx, handle)
	e.must(err)
	m := &member{e: e, auth: authtest.New(e.d.Cfg.Origin)}
	ev, err := e.a.Auth.EnrollBegin(m.guestCtx(), tok)
	e.must(err)
	m.cookie = ev.Token
	enrollCtx := m.guestCtx()
	resp, err := m.auth.Create(ev.Options)
	e.must(err)
	sess, _, err := e.a.Auth.EnrollFinish(enrollCtx, resp, e.d.Cfg.TOSVersion, true)
	e.must(err)
	m.cookie = sess
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	m.id = id
	return m
}

// guestCtx runs the cookie through the middleware without requiring a user session.
func (m *member) guestCtx() context.Context {
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	if m.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: m.cookie}) //nolint:gosec // test request
	}
	var got context.Context
	m.e.a.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// guest is the other device: a guest session, a software authenticator and
// the handshake device keys.
type guest struct {
	e    *env
	sess [32]byte
	tok  string
	ctx  context.Context
	auth *authtest.Authenticator
	dev  *hstest.Claimer
}

func (e *env) guest() *guest {
	e.t.Helper()
	tok, err := e.a.Auth.StartGuest(e.ctx)
	e.must(err)
	h := sha256.Sum256([]byte(tok))
	ctx := core.WithViewer(e.ctx, core.Viewer{Kind: core.SessGuest, Session: h})
	return &guest{e: e, sess: h, tok: tok, ctx: ctx, auth: authtest.New(e.d.Cfg.Origin), dev: hstest.NewClaimer()}
}

// peerOf is a signed-in member acting as the claiming device of an upgrade.
func (e *env) peerOf(m *member) *guest {
	return &guest{e: e, sess: m.viewer().Session, ctx: m.ctx(), auth: m.auth, dev: hstest.NewClaimer()}
}

// hs is an opened handshake with the initiator's device.
type hs struct {
	ref     gate.Ref
	ctx     context.Context
	dev     *hstest.Initiator
	payload string // the server-rendered current frame
}

// openWith opens an invite handshake in ctx (mode, quiet) and posts E_i.
func (e *env) openWith(ctx context.Context, mode string, quiet bool) *hs {
	e.t.Helper()
	hv, err := e.s.Open(ctx, mode, quiet)
	e.must(err)
	return e.started(ctx, hv)
}

func (e *env) started(ctx context.Context, hv gate.HandshakeView) *hs {
	e.t.Helper()
	dev, err := hstest.NewInitiator(hv.FrameKey)
	e.must(err)
	e.must(e.s.Epub(ctx, hv.HS, dev.EI()))
	f, err := e.s.Frame(ctx, hv.HS)
	e.must(err)
	e.must(dev.Sync(f.Payload))
	return &hs{ref: hv.HS, ctx: ctx, dev: dev, payload: f.Payload}
}

func claimIn(b hstest.ClaimBody) invites.ClaimIn {
	return invites.ClaimIn{Payload: b.Payload, EV: b.EV, SigPub: b.SigPub, Proof: b.Proof}
}

// claim scans payload with g's device.
func (g *guest) claim(payload string) error {
	return g.e.s.Claim(g.ctx, claimIn(g.dev.Claim(payload)))
}

func (g *guest) status() (invites.StatusView, error) {
	return g.e.s.JoinStatus(g.ctx, g.dev.Call(invites.CallStatus))
}

// confirmed runs claim → confirm.
func (e *env) confirmed(h *hs) *guest {
	e.t.Helper()
	g := e.guest()
	e.must(g.claim(h.payload))
	e.must(e.s.Confirm(h.ctx, h.ref))
	return g
}

// sound is one attempt's decode, as the initiator's device reports it.
type sound struct {
	ultra, jingle bool // the layers the initiator's device decoded correctly
	wrongCommit   bool
	tUltra        *int
}

// play runs one sound attempt and returns its outcome.
func (e *env) play(h *hs, g *guest, s sound) (invites.ReportOut, error) {
	e.t.Helper()
	st, err := e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	if err != nil {
		return invites.ReportOut{}, err
	}
	ev, err := e.s.Peer(h.ctx, h.ref)
	e.must(err)
	kmac, err := h.dev.Kmac(ev)
	e.must(err)
	u, j := g.dev.Bits(st.Attempt)
	in := invites.ReportIn{KmacCommit: hstest.Commit(kmac), TUltraMs: s.tUltra}
	if s.ultra {
		in.UltraBits = u
	} else {
		in.UltraBits = "AAAAAA"
	}
	if s.jingle {
		in.JingleBits = j
	}
	if s.wrongCommit {
		in.KmacCommit = hstest.Commit([]byte("other"))
	}
	return e.s.Report(h.ctx, h.ref, in)
}

// passed runs claim → confirm → a sound attempt with outcome want.
func (e *env) passed(h *hs, s sound, want string) *guest {
	e.t.Helper()
	g := e.confirmed(h)
	out, err := e.play(h, g, s)
	e.must(err)
	if out.Outcome != want {
		e.t.Fatalf("outcome %+v, want %s", out, want)
	}
	return g
}

// form is a valid join form for handle (without the signup token).
func (e *env) form(handle string) invites.Registration {
	return invites.Registration{Handle: handle, DisplayName: "New " + handle, Locale: "en", Languages: []string{"en", "de"},
		TOSVersion: e.d.Cfg.TOSVersion, AgeAttested: true}
}

// begin sends register/begin with the signup token from the join status.
func (g *guest) begin(f invites.Registration) (json.RawMessage, error) {
	st, err := g.status()
	g.e.must(err)
	f.SignupToken = st.SignupToken
	return g.e.s.RegisterBegin(g.ctx, g.dev.Call(invites.CallRegisterBegin), f)
}

// finish creates the passkey and drafts the records; it returns the get
// options of the signup ceremony.
func (g *guest) finish(f invites.Registration) (invites.RegisterFinishOut, error) {
	opts, err := g.begin(f)
	if err != nil {
		return invites.RegisterFinishOut{}, err
	}
	cred, err := g.auth.Create(opts)
	g.e.must(err)
	return g.e.s.RegisterFinish(g.ctx, g.dev.Call(invites.CallRegisterFinish), cred)
}

// cosign sends register/cosign with assertion.
func (g *guest) cosign(assertion []byte) (string, gate.MeView, error) {
	return g.e.s.RegisterCosign(g.ctx, g.dev.Call(invites.CallRegisterCosign), assertion)
}

// signs runs the co-sign dialog of m for purpose on handshake ref.
func (e *env) signs(m *member, purpose string, ref gate.Ref) error {
	e.t.Helper()
	in, err := e.a.Cosign.BeginIntent(m.ctx(), m.viewer(), purpose, ref)
	if err != nil {
		return err
	}
	a, err := m.auth.Get(in.Options)
	e.must(err)
	return e.s.Cosign(m.ctx(), ref, invites.CosignIn{Intent: in.ID, Assertion: a})
}

// register runs the whole registration for f with the inviter's co-signature.
func (e *env) register(inviter *member, h *hs, g *guest, f invites.Registration) (string, gate.MeView, error) {
	e.t.Helper()
	out, err := g.finish(f)
	if err != nil {
		return "", gate.MeView{}, err
	}
	e.must(e.signs(inviter, invites.PurposeInviteInviter, h.ref))
	a, err := g.auth.Get(out.Options)
	e.must(err)
	return g.cosign(a)
}

// join runs a whole invite of inviter for handle with outcome want and returns
// the new account as a member.
func (e *env) join(inviter *member, handle string, s sound, want string) *member {
	e.t.Helper()
	h := e.openWith(inviter.ctx(), "qr_audio", false)
	g := e.passed(h, s, want)
	tok, me, err := e.register(inviter, h, g, e.form(handle))
	e.must(err)
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok || me.Handle != handle {
		e.t.Fatalf("joined %q: me %+v", handle, me)
	}
	return &member{e: e, id: id, cookie: tok, auth: g.auth}
}

var verified = sound{ultra: true, jingle: true}

func invitedBy(id core.UserID) func(*core.UserMeta) {
	return func(m *core.UserMeta) { m.InvitedBy, m.Genesis = id, false }
}

func wantNotFound(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("%s: err = %v, want not found", what, err)
	}
}

func wantProblem(t *testing.T, err error, code string) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("err = %v, want problem %s", err, code)
	}
}

func (e *env) level(a, b core.UserID) core.EdgeLevel {
	e.t.Helper()
	var lvl int16
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT level FROM edges WHERE user_id = $1 AND friend_id = $2", int64(a), int64(b)).
		Scan(&lvl))
	if mem, ok := e.d.Graph.Level(a, b); !ok || int16(mem) != lvl {
		e.t.Fatalf("graph memory level %v, db %d", mem, lvl)
	}
	return core.EdgeLevel(lvl) //nolint:gosec // 1 or 2
}
