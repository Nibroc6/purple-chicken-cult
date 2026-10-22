package invitesapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/invitesapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites/hstest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

const notFoundBody = "{\"error\":\"not_found\"}\n"

type suite struct {
	t    *testing.T
	h    *apitest.Harness
	spec routers.Router
}

// newSuite mounts every module (the inviter needs /enroll and /cosign too) and
// validates the invites routes against invites.yaml.
func newSuite(t *testing.T) *suite {
	t.Helper()
	h := apitest.App(t)
	doc := apitest.LoadSpec(t, "invites")
	doc.Servers = openapi3.Servers{{URL: "/"}}
	r, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return &suite{t: t, h: h, spec: r}
}

// browser is one first-party client with its own cookie, CSRF token and,
// on the join side, the handshake device.
type browser struct {
	s      *suite
	cookie string
	csrf   string
	auth   *authtest.Authenticator
	dev    *hstest.Claimer
	sigFor string // the join call the next request is signed as
}

func (s *suite) browser() *browser {
	return &browser{s: s, auth: authtest.New(s.h.App.Config().Origin), dev: hstest.NewClaimer()}
}

// user is a signed-in genesis account's browser without a passkey.
func (s *suite) user(handle string) *browser {
	tok, csrf := wiretest.Login(s.t, s.h.App, handle)
	b := s.browser()
	b.cookie, b.csrf = tok, csrf
	return b
}

// enrolled is a genesis account that enrolled a passkey over HTTP.
func (s *suite) enrolled(handle string) *browser {
	s.t.Helper()
	tok, err := s.h.App.Auth.GenesisToken(context.Background(), handle)
	if err != nil {
		s.t.Fatal(err)
	}
	b := s.browser()
	rec := b.do(http.MethodPost, "/api/enroll/begin", map[string]any{"token": tok})
	want(s.t, rec, http.StatusOK, "")
	ev := decode[struct {
		Options json.RawMessage `json:"options"`
		CSRF    string          `json:"csrf"`
	}](s.t, rec)
	b.csrf = ev.CSRF
	cred, err := b.auth.Create(ev.Options)
	if err != nil {
		s.t.Fatal(err)
	}
	rec = b.do(http.MethodPost, "/api/enroll/finish", map[string]any{"credential": json.RawMessage(cred),
		"tos_version": s.h.App.Config().TOSVersion, "age_attested": true})
	want(s.t, rec, http.StatusOK, "")
	b.csrf = decode[struct {
		CSRF string `json:"csrf"`
	}](s.t, rec).CSRF
	return b
}

func (b *browser) signed(call string) *browser {
	b.sigFor = call
	return b
}

func (b *browser) do(method, path string, body any) *httptest.ResponseRecorder {
	b.s.t.Helper()
	h := map[string]string{"Cookie": "", "X-PCC-CSRF": b.csrf}
	if b.cookie != "" {
		h["Cookie"] = httpx.CookieName(false) + "=" + b.cookie
	}
	if b.sigFor != "" {
		h[invitesapi.JoinSigHeader] = b.dev.Call(b.sigFor)
		b.sigFor = ""
	}
	rec := b.s.h.Raw(method, path, body, h)
	for _, c := range rec.Result().Cookies() {
		if c.Name == httpx.CookieName(false) {
			b.cookie = c.Value
		}
	}
	if strings.HasPrefix(path, "/api/invites") || strings.HasPrefix(path, "/api/join") {
		b.s.validate(method, path, body, h, rec)
	}
	return rec
}

func (s *suite) validate(method, path string, body any, hdr map[string]string, rec *httptest.ResponseRecorder) {
	s.t.Helper()
	if rec.Code == http.StatusNotFound {
		return
	}
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			s.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, strings.TrimPrefix(path, "/api"), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	route, params, err := s.spec.FindRoute(req)
	if err != nil {
		s.t.Errorf("%s %s: no operation in invites.yaml (%d)", method, path, rec.Code)
		return
	}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	ctx := context.Background()
	if rec.Code < 400 {
		if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
			s.t.Errorf("%s %s request: %v", method, path, err)
		}
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: rec.Code, Header: rec.Header(),
		Body: io.NopCloser(bytes.NewReader(rec.Body.Bytes()))}
	if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
		s.t.Errorf("%s %s response %d: %v", method, path, rec.Code, err)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func want(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if code != "" && !strings.Contains(rec.Body.String(), `"error":"`+code+`"`) {
		t.Fatalf("body %s, want %s", rec.Body, code)
	}
}

func wantUniform404(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s: %d %q %q", what, rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
}

type handshakeOut struct {
	HS         string `json:"hs"`
	Mode       string `json:"mode"`
	FrameTTLms int    `json:"frame_ttl_ms"`
	FrameKey   string `json:"frame_key"`
}

type statusOut struct {
	State        string `json:"state"`
	SAS          string `json:"sas"`
	Outcome      string `json:"outcome"`
	Sign         string `json:"sign"`
	PeerSigned   bool   `json:"peer_signed"`
	SignupToken  string `json:"signup_token"`
	AttemptsLeft int    `json:"attempts_left"`
}

type startOut struct {
	CSRF       string `json:"csrf"`
	TOSVersion int    `json:"tos_version"`
	MinAge     int    `json:"min_age"`
}

// joiner starts a join flow in a fresh browser.
func (s *suite) joiner() *browser {
	s.t.Helper()
	b := s.browser()
	rec := b.do(http.MethodPost, "/api/join/start", map[string]any{})
	want(s.t, rec, http.StatusOK, "")
	st := decode[startOut](s.t, rec)
	if st.CSRF == "" || b.cookie == "" || st.TOSVersion != s.h.App.Config().TOSVersion || st.MinAge != s.h.App.Config().MinAge {
		s.t.Fatalf("join start %s", rec.Body)
	}
	b.csrf = st.CSRF
	return b
}

// opened is a handshake with the initiator's device.
type opened struct {
	hs      string
	dev     *hstest.Initiator
	payload string
}

// open opens a handshake (mode), posts E_i and returns the current frame.
func (b *browser) open(mode string) opened {
	b.s.t.Helper()
	rec := b.do(http.MethodPost, "/api/invites/handshakes", map[string]any{"mode": mode})
	want(b.s.t, rec, http.StatusCreated, "")
	hv := decode[handshakeOut](b.s.t, rec)
	if hv.Mode != mode || hv.FrameTTLms != 250 || hv.FrameKey == "" {
		b.s.t.Fatalf("handshake %s", rec.Body)
	}
	dev, err := hstest.NewInitiator(hv.FrameKey)
	if err != nil {
		b.s.t.Fatal(err)
	}
	want(b.s.t, b.do(http.MethodPost, "/api/invites/handshakes/"+hv.HS+"/epub", map[string]any{"e_i": dev.EI()}),
		http.StatusNoContent, "")
	rec = b.do(http.MethodGet, "/api/invites/handshakes/"+hv.HS+"/frame", nil)
	want(b.s.t, rec, http.StatusOK, "")
	p := decode[struct {
		Payload string `json:"payload"`
	}](b.s.t, rec).Payload
	if err := dev.Sync(p); err != nil {
		b.s.t.Fatal(err)
	}
	return opened{hs: hv.HS, dev: dev, payload: p}
}

func (b *browser) claim(payload string) *httptest.ResponseRecorder {
	return b.do(http.MethodPost, "/api/join/claim", b.dev.Claim(payload))
}

func (b *browser) status() statusOut {
	b.s.t.Helper()
	rec := b.signed("status").do(http.MethodGet, "/api/join/status", nil)
	want(b.s.t, rec, http.StatusOK, "")
	return decode[statusOut](b.s.t, rec)
}

// sound runs one attempt: the joiner starts, the inviter reports what it heard.
func (s *suite) sound(inviter, g *browser, o opened, ultra bool) string {
	s.t.Helper()
	rec := g.signed("audio_start").do(http.MethodPost, "/api/join/audio/start", map[string]any{"kmac": g.dev.KmacB64()})
	want(s.t, rec, http.StatusOK, "")
	attempt := decode[struct {
		Attempt int `json:"attempt"`
	}](s.t, rec).Attempt
	rec = inviter.do(http.MethodGet, "/api/invites/handshakes/"+o.hs+"/peer", nil)
	want(s.t, rec, http.StatusOK, "")
	kmac, err := o.dev.Kmac(decode[struct {
		EV string `json:"e_v"`
	}](s.t, rec).EV)
	if err != nil {
		s.t.Fatal(err)
	}
	u, j := g.dev.Bits(attempt)
	if !ultra {
		u = "AAAAAA"
	}
	rec = inviter.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/report",
		map[string]any{"ultra_bits": u, "jingle_bits": j, "kmac_commit": hstest.Commit(kmac), "t_ultra_ms": 900,
			"t_jingle_ms": 1200, "offset_ms": 20, "motif_level_db": -20.5})
	want(s.t, rec, http.StatusOK, "")
	return decode[struct {
		Outcome string `json:"outcome"`
	}](s.t, rec).Outcome
}

func (s *suite) form(handle, token string) map[string]any {
	return map[string]any{"handle": handle, "display_name": "Newbie", "locale": "en", "languages": []string{"en"},
		"tos_version": s.h.App.Config().TOSVersion, "age_attested": true, "signup_token": token}
}

func TestInviteAndJoinOverHTTP(t *testing.T) {
	s := newSuite(t)
	alice := s.enrolled("alice")
	rec := alice.do(http.MethodGet, "/api/invites/budget", nil)
	want(t, rec, http.StatusOK, "")
	if b := decode[struct {
		Available int `json:"available"`
		Cap       int `json:"cap"`
	}](t, rec); b.Available != 5 || b.Cap != 5 {
		t.Fatalf("budget %s", rec.Body)
	}
	rec = alice.do(http.MethodPost, "/api/invites/handshakes", map[string]any{"mode": "qr"})
	want(t, rec, http.StatusUnprocessableEntity, "mode_unsupported")

	o := alice.open("qr_audio")
	g := s.joiner()
	rec = g.signed("register_begin").do(http.MethodPost, "/api/join/register/begin", s.form("newbie", ""))
	wantUniform404(t, rec, "register before claim")
	wantUniform404(t, g.do(http.MethodPost, "/api/join/claim", map[string]any{"payload": o.payload, "e_v": "", "sig_pub": "",
		"proof": ""}), "a photographed QR without e_v")
	want(t, g.claim(o.payload), http.StatusNoContent, "")

	rec = alice.do(http.MethodGet, "/api/invites/handshakes/"+o.hs, nil)
	want(t, rec, http.StatusOK, "")
	inv := decode[statusOut](t, rec)
	mine := g.status()
	if inv.State != "claimed" || inv.SAS == "" || inv.SAS != mine.SAS {
		t.Fatalf("inviter %+v invitee %+v", inv, mine)
	}
	want(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/confirm", nil), http.StatusNoContent, "")
	if out := s.sound(alice, g, o, true); out != "verified" {
		t.Fatalf("outcome %s", out)
	}
	mine = g.status()
	if mine.Outcome != "verified" || mine.SignupToken == "" {
		t.Fatalf("join status %+v", mine)
	}

	rec = g.signed("register_begin").do(http.MethodPost, "/api/join/register/begin", s.form("alice", mine.SignupToken))
	want(t, rec, http.StatusConflict, "handle_taken")
	f := s.form("newbie", mine.SignupToken)
	f["age_attested"] = false
	rec = g.signed("register_begin").do(http.MethodPost, "/api/join/register/begin", f)
	want(t, rec, http.StatusUnprocessableEntity, "age_required")
	rec = g.signed("register_begin").do(http.MethodPost, "/api/join/register/begin", s.form("newbie", "wrong"))
	wantUniform404(t, rec, "wrong signup token")

	rec = g.signed("register_begin").do(http.MethodPost, "/api/join/register/begin", s.form("newbie", mine.SignupToken))
	want(t, rec, http.StatusOK, "")
	opts := decode[struct {
		Options json.RawMessage `json:"options"`
	}](t, rec).Options
	cred, err := g.auth.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	rec = g.signed("register_finish").do(http.MethodPost, "/api/join/register/finish",
		map[string]any{"credential": json.RawMessage(cred)})
	want(t, rec, http.StatusOK, "")
	signup := decode[struct {
		Options json.RawMessage  `json:"options"`
		Records []map[string]any `json:"records"`
	}](t, rec)
	if len(signup.Records) != 2 || signup.Records[1]["level"] != "friend+" {
		t.Fatalf("signup ceremony %s", rec.Body)
	}
	assertion, err := g.auth.Get(signup.Options)
	if err != nil {
		t.Fatal(err)
	}
	rec = g.signed("register_cosign").do(http.MethodPost, "/api/join/register/cosign",
		map[string]any{"assertion": json.RawMessage(assertion)})
	want(t, rec, http.StatusConflict, "awaiting_inviter")

	// The inviter's co-sign dialog: the intent from /api/cosign, then the handshake.
	if st := decode[statusOut](t, alice.do(http.MethodGet, "/api/invites/handshakes/"+o.hs, nil)); st.Sign != "needed" {
		t.Fatalf("inviter status %+v", st)
	}
	rec = alice.do(http.MethodPost, "/api/cosign", map[string]any{"purpose": "invite_inviter", "target": o.hs})
	want(t, rec, http.StatusOK, "")
	intent := decode[struct {
		Intent  string          `json:"intent"`
		Record  map[string]any  `json:"record"`
		Options json.RawMessage `json:"options"`
	}](t, rec)
	if intent.Record["kind"] != "invite" {
		t.Fatalf("intent record %v", intent.Record)
	}
	inviterAssertion, err := alice.auth.Get(intent.Options)
	if err != nil {
		t.Fatal(err)
	}
	want(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/cosign",
		map[string]any{"intent": intent.Intent, "assertion": json.RawMessage(inviterAssertion)}), http.StatusNoContent, "")
	if st := g.status(); !st.PeerSigned {
		t.Fatalf("join status %+v", st)
	}

	guestCookie := g.cookie
	rec = g.signed("register_cosign").do(http.MethodPost, "/api/join/register/cosign",
		map[string]any{"assertion": json.RawMessage(assertion)})
	want(t, rec, http.StatusCreated, "")
	sv := decode[struct {
		Authenticated bool   `json:"authenticated"`
		Kind          string `json:"kind"`
		CSRF          string `json:"csrf"`
		Me            struct {
			Handle  string `json:"handle"`
			Inviter struct {
				Kind   string `json:"kind"`
				Handle string `json:"handle"`
			} `json:"inviter"`
		} `json:"me"`
	}](t, rec)
	if !sv.Authenticated || sv.Kind != "user" || sv.CSRF == "" || g.cookie == guestCookie ||
		sv.Me.Handle != "newbie" || sv.Me.Inviter.Handle != "alice" {
		t.Fatalf("session %s", rec.Body)
	}
	g.csrf = sv.CSRF

	// The new account is signed in, on probation (friend+ rule), and appears in alice's list.
	rec = g.do(http.MethodGet, "/api/invites/budget", nil)
	want(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"friend_plus":true`) {
		t.Fatalf("invitee budget %s", rec.Body)
	}
	// On probation its code only connects signed-in friends.
	rec = g.do(http.MethodPost, "/api/invites/handshakes", map[string]any{"mode": "qr_audio"})
	want(t, rec, http.StatusCreated, "")
	if strings.Contains(rec.Body.String(), `"invite":true`) {
		t.Fatalf("probation handshake %s", rec.Body)
	}
	rec = alice.do(http.MethodGet, "/api/invites/invitees", nil)
	want(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"handle":"newbie"`) || !strings.Contains(rec.Body.String(), `"on_probation":true`) {
		t.Fatalf("invitees %s", rec.Body)
	}
	rec = alice.do(http.MethodGet, "/api/invites/handshakes/"+o.hs, nil)
	if decode[statusOut](t, rec).State != "done" {
		t.Fatalf("final status %s", rec.Body)
	}
	// Replaying the finished handshake is the uniform 404.
	wantUniform404(t, s.joiner().claim(o.dev.Frame(o.dev.N)), "reused handshake")
}

func TestJoinCallsWithoutAValidSignatureAre404(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	o := alice.open("qr_audio")
	g := s.joiner()
	want(t, g.claim(o.payload), http.StatusNoContent, "")
	want(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/confirm", nil), http.StatusNoContent, "")
	thief := s.browser()
	thief.cookie, thief.csrf = g.cookie, g.csrf // the stolen cookie, without the device key
	thief.dev.Claim(o.payload)
	wantUniform404(t, g.do(http.MethodGet, "/api/join/status", nil), "status without a signature")
	wantUniform404(t, g.signed("audio_start").do(http.MethodGet, "/api/join/status", nil), "status signed as another call")
	wantUniform404(t, thief.signed("status").do(http.MethodGet, "/api/join/status", nil), "stolen cookie")
	wantUniform404(t, thief.signed("audio_start").do(http.MethodPost, "/api/join/audio/start",
		map[string]any{"kmac": g.dev.KmacB64()}), "stolen cookie starts the sound")
	wantUniform404(t, g.do(http.MethodPost, "/api/join/dual/frame", map[string]any{"payload": "x", "kmac": "y"}),
		"dual frame without a signature")

	// The device's own signature works; the jingle alone is Accepted, and a
	// passed handshake takes no further sound.
	if out := s.sound(alice, g, o, false); out != "accepted" {
		t.Fatalf("jingle only: %s", out)
	}
	wantUniform404(t, g.signed("audio_start").do(http.MethodPost, "/api/join/audio/start",
		map[string]any{"kmac": g.dev.KmacB64()}), "a sound after a passed outcome")
}

func TestSixthSoundAttemptIs404(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	o := alice.open("qr_audio")
	g := s.joiner()
	want(t, g.claim(o.payload), http.StatusNoContent, "")
	want(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/confirm", nil), http.StatusNoContent, "")
	for i := range 5 {
		rec := g.signed("audio_start").do(http.MethodPost, "/api/join/audio/start", map[string]any{"kmac": g.dev.KmacB64()})
		want(t, rec, http.StatusOK, "")
		rec = alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/report",
			map[string]any{"kmac_commit": "AAAA", "ultra_bits": "AAAAAA"})
		want(t, rec, http.StatusOK, "")
		if !strings.Contains(rec.Body.String(), `"outcome":"retry"`) {
			t.Fatalf("attempt %d: %s", i+1, rec.Body)
		}
	}
	if st := g.status(); st.AttemptsLeft != 0 || st.Outcome != "retry" {
		t.Fatalf("status %+v", st)
	}
	wantUniform404(t, g.signed("audio_start").do(http.MethodPost, "/api/join/audio/start",
		map[string]any{"kmac": g.dev.KmacB64()}), "sixth attempt")
}

func TestClaimFailuresAreByteIdentical(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	o := alice.open("qr_audio")
	parts := strings.Split(o.payload, ".")
	forged := strings.Join(append(parts[:4:4], "AAAAAAAAAAAAAA"), ".")
	if err := s.h.App.Hooks().AdvanceClock(2 * time.Second); err != nil { // eight frames
		t.Fatal(err)
	}
	g := s.joiner()
	for name, p := range map[string]string{"stale": o.payload, "forged": forged, "garbage": "pcc2.x.y.1.z", "empty": "",
		"phase 0": "pcc1." + parts[1] + ".1.AAAAAAAAAAAAAA"} {
		wantUniform404(t, g.claim(p), name)
	}
	// Wrong session: a signed-in user cannot claim an invite, and an anonymous request has no session.
	fresh := alice.open("qr_audio")
	wantUniform404(t, alice.claim(fresh.payload), "user session")
	wantUniform404(t, s.browser().signed("status").do(http.MethodGet, "/api/join/status", nil), "no session")
	// Someone else's handshake, and the anonymous inviter surface.
	bob := s.user("bob")
	wantUniform404(t, bob.do(http.MethodGet, "/api/invites/handshakes/"+o.hs, nil), "foreign handshake")
	wantUniform404(t, bob.do(http.MethodGet, "/api/invites/handshakes/"+o.hs+"/frame", nil), "foreign frame")
	wantUniform404(t, bob.do(http.MethodGet, "/api/invites/handshakes/"+o.hs+"/peer", nil), "foreign peer")
	wantUniform404(t, bob.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/report",
		map[string]any{"kmac_commit": "AAAA"}), "foreign report")
	wantUniform404(t, s.browser().do(http.MethodGet, "/api/invites/budget", nil), "anonymous budget")
	wantUniform404(t, s.browser().do(http.MethodGet, "/api/invites/invitees", nil), "anonymous invitees")
	// The current frame (rendered by the device) still works.
	want(t, g.claim(fresh.dev.Frame(fresh.dev.N)), http.StatusNoContent, "")
}

func TestDualQROverHTTP(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	o := alice.open("dual_qr")
	g := s.joiner()
	want(t, g.claim(o.payload), http.StatusNoContent, "")
	want(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/confirm", nil), http.StatusNoContent, "")
	rec := alice.do(http.MethodGet, "/api/invites/handshakes/"+o.hs+"/peer", nil)
	kmac, err := o.dev.Kmac(decode[struct {
		EV string `json:"e_v"`
	}](t, rec).EV)
	if err != nil {
		t.Fatal(err)
	}
	rec = g.signed("dual_frame").do(http.MethodPost, "/api/join/dual/frame",
		map[string]any{"payload": hstest.DualFrame(kmac, o.dev.HS, o.dev.N), "kmac": g.dev.KmacB64()})
	want(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"outcome":"accepted"`) {
		t.Fatalf("dual frame %s", rec.Body)
	}
	if st := g.status(); st.Outcome != "accepted" || st.SignupToken == "" {
		t.Fatalf("status %+v", st)
	}
}

func TestCancelEndsTheHandshake(t *testing.T) {
	s := newSuite(t)
	alice := s.user("alice")
	o := alice.open("qr_audio")
	g := s.joiner()
	want(t, g.claim(o.payload), http.StatusNoContent, "")
	want(t, alice.do(http.MethodDelete, "/api/invites/handshakes/"+o.hs, nil), http.StatusNoContent, "")
	if st := g.status(); st.State != "cancelled" || st.SAS != "" {
		t.Fatalf("status %+v", st)
	}
	wantUniform404(t, alice.do(http.MethodPost, "/api/invites/handshakes/"+o.hs+"/confirm", nil), "confirm cancelled")
	wantUniform404(t, g.signed("register_begin").do(http.MethodPost, "/api/join/register/begin", s.form("late", "x")),
		"register cancelled")
}
