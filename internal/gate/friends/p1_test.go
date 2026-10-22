package friends_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// ---- passkey sessions ----

// member is a browser signed in with a passkey: its viewer carries the
// session's credential, so its graph changes must be co-signed.
type member struct {
	e      *env
	cookie string
	auth   *authtest.Authenticator
	id     core.UserID
}

func (m *member) ctx() context.Context {
	m.e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: m.cookie}) //nolint:gosec // test request
	var got context.Context
	m.e.a.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func (m *member) viewer() core.Viewer {
	m.e.t.Helper()
	v, ok := core.ViewerFrom(m.ctx())
	if !ok || v.Kind != core.SessUser || len(v.Credential) == 0 {
		m.e.t.Fatalf("not signed in with a passkey: %+v", v)
	}
	return v
}

// enroll makes handle a genesis account with a passkey (the real ceremony).
func (e *env) enroll(handle string) *member {
	e.t.Helper()
	tok, err := e.a.Auth.GenesisToken(e.ctx, handle)
	e.must(err)
	m := &member{e: e, auth: authtest.New(e.d.Cfg.Origin)}
	ev, err := e.a.Auth.EnrollBegin(m.ctx(), tok)
	e.must(err)
	m.cookie = ev.Token
	resp, err := m.auth.Create(ev.Options)
	e.must(err)
	m.cookie, _, err = e.a.Auth.EnrollFinish(m.ctx(), resp, e.d.Cfg.TOSVersion, true)
	e.must(err)
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	m.id = id
	return m
}

// link makes a and b friends in the database and in graph memory (fixture edge).
func (e *env) link(a, b core.UserID) {
	e.t.Helper()
	for _, p := range [][2]core.UserID{{a, b}, {b, a}} {
		e.exec("INSERT INTO edges (user_id, friend_id, level, since, record_hash) VALUES ($1, $2, 1, $3, '\\x00')",
			int64(p[0]), int64(p[1]), coretest.Epoch)
	}
	e.d.Graph.AddEdge(a, b, core.LevelFriend, coretest.Epoch)
}

// sign opens m's intent for purpose on target and signs it with m's passkey.
func (e *env) sign(m *member, purpose string, target core.UserID) friends.Cosig {
	e.t.Helper()
	in, err := e.a.Cosign.BeginIntent(m.ctx(), m.viewer(), purpose, e.ref(m.id, target))
	e.must(err)
	asr, err := m.auth.Get(in.Options)
	e.must(err)
	return friends.Cosig{Intent: in.ID, Assertion: asr}
}

// assertions counts the passkey assertions stored with the record hash.
func (e *env) assertions(hash []byte) int {
	e.t.Helper()
	return e.count("SELECT COALESCE(jsonb_array_length(assertions), 0) FROM signed_records WHERE hash = $1", hash)
}

func (e *env) edgeHash(a, b core.UserID) []byte {
	e.t.Helper()
	var h []byte
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT record_hash FROM edges WHERE user_id = $1 AND friend_id = $2",
		int64(a), int64(b)).Scan(&h))
	return h
}

// verifyExport exports u's records with the co-sign cut-over set before
// everything and returns the failure codes.
func (e *env) verifyExport(u core.UserID) map[string]int {
	e.t.Helper()
	e.must(e.a.Records.EnforceCosign(e.ctx, coretest.Epoch.Add(-time.Hour)))
	var buf bytes.Buffer
	e.must(e.a.Account.Export(coretest.AsUser(e.ctx, u, e.d.Clock.Now()), &buf))
	rep, err := records.VerifyExport(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
	e.must(err)
	out := map[string]int{}
	for _, f := range rep.Failures {
		out[f.Code]++
	}
	return out
}

// Acceptance: a co-signed request and acceptance write one Friendship record
// carrying both passkey assertions; the requester's draft waits on the request
// and is cleared once the record exists; the export verifies with the co-sign
// cut-over on.
func TestCosignedRequestAccept(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	carol := e.w.User("carol")
	e.link(alice.id, carol)
	e.link(carol, bob.id)

	e.must(e.f.Request(alice.ctx(), e.ref(alice.id, bob.id), e.sign(alice, friends.PurposeFriendRequest, bob.id)))
	if n := e.count("SELECT count(*) FROM friend_requests WHERE from_id = $1 AND draft IS NOT NULL AND draft_assertion IS NOT NULL",
		int64(alice.id)); n != 1 {
		t.Fatal("the requester's signed draft waits on the request")
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecFriendship)); n != 0 {
		t.Fatal("no record before the acceptance")
	}
	// The accepter's intent shows the requester's record (same people, level, time).
	in, err := e.a.Cosign.BeginIntent(bob.ctx(), bob.viewer(), friends.PurposeFriendAccept, e.ref(bob.id, alice.id))
	e.must(err)
	var rec map[string]any
	e.must(json.Unmarshal(in.Record, &rec))
	if rec["kind"] != "friendship" || rec["level"] != "friend" {
		t.Fatalf("accept draft %s", in.Record)
	}
	asr, err := bob.auth.Get(in.Options)
	e.must(err)
	e.must(e.f.Accept(bob.ctx(), e.ref(bob.id, alice.id), friends.Cosig{Intent: in.ID, Assertion: asr}))
	h := e.edgeHash(alice.id, bob.id)
	if e.assertions(h) != 2 {
		t.Fatalf("friendship assertions = %d, want the requester's and the accepter's", e.assertions(h))
	}
	if n := e.count("SELECT count(*) FROM friend_requests WHERE draft IS NOT NULL OR draft_assertion IS NOT NULL"); n != 0 {
		t.Fatal("the drafts live in the record now")
	}
	if lvl, ok := e.d.Graph.Level(alice.id, bob.id); !ok || lvl != core.LevelFriend {
		t.Fatal("graph memory")
	}
	if codes := e.verifyExport(bob.id); codes["missing_assertion"] != 0 || codes["bad_cosig"] != 0 {
		t.Fatalf("export failures %v", codes)
	}
}

// Acceptance: graph-changing writes without a fresh assertion fail — none,
// expired, used twice, drafted for someone else, of another purpose — and
// change nothing.
func TestGraphWritesWithoutFreshAssertionFail(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	carol, dave := e.w.User("carol"), e.w.User("dave")
	e.link(alice.id, carol)
	e.link(carol, bob.id)
	e.link(carol, dave)
	bobRef := e.ref(alice.id, bob.id)
	requests := func() int { return e.count("SELECT count(*) FROM friend_requests WHERE from_id = $1", int64(alice.id)) }

	wantNotFound(t, e.f.Request(alice.ctx(), bobRef))
	wantNotFound(t, e.f.Request(alice.ctx(), bobRef, friends.Cosig{}))
	expired := e.sign(alice, friends.PurposeFriendRequest, bob.id)
	e.advance(6 * time.Minute)
	wantNotFound(t, e.f.Request(alice.ctx(), bobRef, expired))
	wantNotFound(t, e.f.Request(alice.ctx(), bobRef, e.sign(alice, friends.PurposeFriendRequest, dave)))
	wantNotFound(t, e.f.Request(alice.ctx(), bobRef, e.sign(alice, friends.PurposeUnfriend, carol)))
	if requests() != 0 {
		t.Fatal("a rejected co-signature wrote a request")
	}
	c := e.sign(alice, friends.PurposeFriendRequest, bob.id)
	e.must(e.f.Request(alice.ctx(), bobRef, c))
	e.must(e.f.Withdraw(alice.ctx(), bobRef))
	wantNotFound(t, e.f.Request(alice.ctx(), bobRef, c)) // used
	if n := e.count("SELECT count(*) FROM friend_requests WHERE from_id = $1 AND state IN (0, 1)", int64(alice.id)); n != 0 {
		t.Fatal("a replayed co-signature renewed the request")
	}

	// Accept, unfriend and block likewise.
	e.must(e.f.Request(bob.ctx(), e.ref(bob.id, alice.id), e.sign(bob, friends.PurposeFriendRequest, alice.id)))
	wantNotFound(t, e.f.Accept(alice.ctx(), e.ref(alice.id, bob.id)))
	wantNotFound(t, e.f.Accept(alice.ctx(), e.ref(alice.id, bob.id), e.sign(alice, friends.PurposeFriendRequest, dave)))
	if _, ok := e.d.Graph.Level(alice.id, bob.id); ok {
		t.Fatal("accepted without a co-signature")
	}
	carolRef := e.ref(alice.id, carol)
	wantNotFound(t, e.f.Unfriend(alice.ctx(), carolRef))
	wantNotFound(t, e.f.Unfriend(alice.ctx(), carolRef, e.sign(alice, friends.PurposeBlock, carol)))
	wantNotFound(t, e.f.Block(alice.ctx(), e.ref(alice.id, dave)))
	wantNotFound(t, e.f.Block(alice.ctx(), e.ref(alice.id, dave), e.sign(alice, friends.PurposeBlock, bob.id)))
	if _, ok := e.d.Graph.Level(alice.id, carol); !ok || e.d.Graph.Blocks(alice.id, dave) {
		t.Fatal("a write went through without its co-signature")
	}
	// A session without a credential (test hooks only) keeps the Phase 0 path.
	e.must(e.f.Unfriend(e.as(alice.id), carolRef))
}

// Unfriending and blocking are co-signed: the Revocation and the Block record
// carry the caller's assertion; blocking a friend co-signs the Revocation it
// writes (one ceremony per change).
func TestCosignedUnfriendAndBlock(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	carol, dave := e.w.User("carol"), e.w.User("dave")
	e.link(alice.id, bob.id)
	e.link(alice.id, carol)
	e.link(alice.id, dave)

	revocation := func() []byte {
		var h []byte
		e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT record_hash FROM edge_events WHERE kind = 2 ORDER BY at DESC, id DESC LIMIT 1").Scan(&h))
		return h
	}
	e.must(e.f.Unfriend(alice.ctx(), e.ref(alice.id, bob.id), e.sign(alice, friends.PurposeUnfriend, bob.id)))
	if e.assertions(revocation()) != 1 {
		t.Fatal("the Revocation carries alice's assertion")
	}
	// Blocking a friend co-signs the Revocation: a Block draft is refused.
	wantNotFound(t, e.f.Block(alice.ctx(), e.ref(alice.id, carol), e.sign(alice, friends.PurposeBlock, carol)))
	if _, ok := e.d.Graph.Level(alice.id, carol); !ok || e.d.Graph.Blocks(alice.id, carol) {
		t.Fatal("a wrongly signed block changed something")
	}
	e.must(e.f.Block(alice.ctx(), e.ref(alice.id, carol), e.sign(alice, friends.PurposeUnfriend, carol)))
	if _, ok := e.d.Graph.Level(alice.id, carol); ok || !e.d.Graph.Blocks(alice.id, carol) {
		t.Fatal("block of a friend")
	}
	var bh []byte
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT record_hash FROM blocks WHERE blocker = $1 AND blocked = $2",
		int64(alice.id), int64(carol)).Scan(&bh))
	if e.assertions(bh) != 0 || e.assertions(revocation()) != 1 {
		t.Fatal("the Revocation carries alice's assertion, the Block record is author-signed")
	}
	// Someone who is not a friend: the Block record is co-signed.
	e.link(dave, bob.id)
	wantNotFound(t, e.f.Block(bob.ctx(), e.ref(bob.id, alice.id), e.sign(bob, friends.PurposeUnfriend, dave)))
	e.must(e.f.Block(bob.ctx(), e.ref(bob.id, alice.id), e.sign(bob, friends.PurposeBlock, alice.id)))
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT record_hash FROM blocks WHERE blocker = $1 AND blocked = $2",
		int64(bob.id), int64(alice.id)).Scan(&bh))
	if e.assertions(bh) != 1 {
		t.Fatal("the Block record carries bob's assertion")
	}
	if codes := e.verifyExport(alice.id); codes["missing_assertion"] != 0 {
		t.Fatalf("export failures %v", codes)
	}
}

// A co-signed request to someone who blocked the requester is stored declined
// and answers like a pending one; crossed co-signed requests make friends with
// one record signed by both.
func TestCosignedRequestToBlockerAndCrossed(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	carol := e.w.User("carol")
	e.link(alice.id, carol)
	e.link(carol, bob.id)
	e.must(e.f.Block(e.as(bob.id), e.ref(bob.id, alice.id)))
	e.must(e.f.Request(alice.ctx(), e.ref(alice.id, bob.id), e.sign(alice, friends.PurposeFriendRequest, bob.id)))
	if st := e.f.RequestState(e.ctx, alice.id, bob.id); st != "outgoing" {
		t.Fatalf("state %q", st)
	}
	if n := e.count("SELECT count(*) FROM friend_requests WHERE from_id = $1 AND state = 1", int64(alice.id)); n != 1 {
		t.Fatal("stored declined")
	}
	if len(e.rec.For(bob.id)) != 0 {
		t.Fatal("the blocker is never notified")
	}
	e.must(e.f.Unblock(e.as(bob.id), e.ref(bob.id, alice.id)))
	e.must(e.f.Withdraw(alice.ctx(), e.ref(alice.id, bob.id)))

	e.must(e.f.Request(alice.ctx(), e.ref(alice.id, bob.id), e.sign(alice, friends.PurposeFriendRequest, bob.id)))
	// bob asks back: his intent drafts alice's record, so both sign one record.
	in, err := e.a.Cosign.BeginIntent(bob.ctx(), bob.viewer(), friends.PurposeFriendRequest, e.ref(bob.id, alice.id))
	e.must(err)
	asr, err := bob.auth.Get(in.Options)
	e.must(err)
	e.must(e.f.Request(bob.ctx(), e.ref(bob.id, alice.id), friends.Cosig{Intent: in.ID, Assertion: asr}))
	if _, ok := e.d.Graph.Level(alice.id, bob.id); !ok {
		t.Fatal("crossed requests make friends")
	}
	if e.assertions(e.edgeHash(alice.id, bob.id)) != 2 {
		t.Fatal("one record signed by both")
	}
}

// ---- hide-via ----

// Hide-via cuts a hop without unfriending: the hidden friend and their posts
// stay, the paths through them go (only for the caller), it needs a friend,
// bumps the epoch, persists, publishes HideViaChanged and is undone.
func TestHideVia(t *testing.T) {
	e := newEnv(t)
	v, h, y, x, w := e.w.User("vic"), e.w.User("hal"), e.w.User("yan"), e.w.User("xan"), e.w.User("wes")
	z := e.w.User("zed")
	e.w.Friends("vic", "hal")
	e.w.Friends("vic", "yan")
	e.w.Friends("hal", "xan")
	e.w.Friends("yan", "zed")
	e.w.Friends("zed", "xan")
	e.w.Friends("hal", "wes") // w only through h
	e.w.Post("hal", "hpost", "h's own", coretest.Audience(core.AudD1))
	var events []core.HideViaChanged
	core.Subscribe(e.d.Bus, func(ev core.HideViaChanged) { events = append(events, ev) })

	person := func(viewer, who core.UserID) (gate.PersonView, error) {
		return e.f.Person(e.as(viewer), e.ref(viewer, who))
	}
	px, err := person(v, x)
	e.must(err)
	if px.Chain.Dist != 2 || px.Chain.FirstHop.Handle != "hal" {
		t.Fatalf("before: %+v", px.Chain)
	}
	epoch := e.d.Graph.Epoch()
	e.must(e.f.HideVia(e.as(v), e.ref(v, h), true))
	if e.d.Graph.Epoch() <= epoch {
		t.Fatal("a hide-via bumps the epoch")
	}
	if len(events) != 1 || events[0] != (core.HideViaChanged{User: v, Via: h, On: true}) {
		t.Fatalf("events %+v", events)
	}
	ph, err := person(v, h)
	e.must(err)
	if !ph.HiddenVia || ph.Relation != "friend" || ph.Chain.Dist != 1 {
		t.Fatalf("the hidden friend stays a friend at distance 1: %+v", ph)
	}
	posts, err := e.f.PersonPosts(e.as(v), e.ref(v, h), "", "")
	e.must(err)
	if len(posts.Items) != 1 {
		t.Fatal("the hidden friend's own posts stay")
	}
	px, err = person(v, x)
	e.must(err)
	if px.Chain.Dist != 3 || px.Chain.FirstHop.Handle != "yan" {
		t.Fatalf("x is reached around h: %+v", px.Chain)
	}
	_, err = person(v, w)
	wantNotFound(t, err)
	if pw, err := person(h, w); err != nil || pw.Chain.Dist != 1 {
		t.Fatal("only the caller's own searches change")
	}
	if ph, _ := person(h, v); ph.HiddenVia {
		t.Fatal("never revealed to the hidden person")
	}
	// Persisted: a reload keeps it.
	e.must(e.d.Graph.Load(e.ctx))
	if _, err := person(v, w); err == nil {
		t.Fatal("hide-via must survive a reload")
	}
	// Idempotent; only friends; never oneself.
	e.must(e.f.HideVia(e.as(v), e.ref(v, h), true))
	if len(events) != 1 {
		t.Fatal("an existing hide-via publishes nothing")
	}
	wantNotFound(t, e.f.HideVia(e.as(v), e.ref(v, x), true))
	wantNotFound(t, e.f.HideVia(e.as(v), e.ref(v, v), true))
	wantNotFound(t, e.f.HideVia(e.as(v), gate.Ref("garbage"), true))
	// Showing the paths again.
	e.must(e.f.HideVia(e.as(v), e.ref(v, h), false))
	if pw, err := person(v, w); err != nil || pw.Chain.Dist != 2 {
		t.Fatalf("un-hidden: %+v %v", pw, err)
	}
	if n := e.count("SELECT count(*) FROM hide_via"); n != 0 || len(events) != 2 || events[1].On {
		t.Fatalf("rows %d events %+v", n, events)
	}
	e.must(e.f.HideVia(e.as(v), e.ref(v, h), false))
	_, _ = y, z
}

// ---- friend+ upgrade ----

// UpgradeBegin opens a friend+ upgrade handshake with a plain friend only.
func TestUpgradeBegin(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	carol, dave := e.w.User("carol"), e.w.User("dave")
	e.link(alice.id, bob.id)
	e.link(alice.id, carol)
	e.link(carol, dave)
	hv, err := e.f.UpgradeBegin(alice.ctx(), e.ref(alice.id, bob.id), false)
	e.must(err)
	if hv.HS == "" || hv.FrameKey == "" || hv.Mode != "qr_audio" {
		t.Fatalf("handshake %+v", hv)
	}
	st, err := e.a.Invites.Status(alice.ctx(), hv.HS)
	e.must(err)
	if st.Kind != "upgrade" || st.State != "open" {
		t.Fatalf("status %+v", st)
	}
	_, err = e.f.UpgradeBegin(alice.ctx(), e.ref(alice.id, dave), false)
	wantNotFound(t, err) // not a friend
	_, err = e.f.UpgradeBegin(alice.ctx(), e.ref(alice.id, alice.id), false)
	wantNotFound(t, err)
	_, err = e.f.UpgradeBegin(alice.ctx(), gate.Ref("garbage"), false)
	wantNotFound(t, err)
	e.d.Graph.AddEdge(alice.id, carol, core.LevelFriendPlus, coretest.Epoch)
	_, err = e.f.UpgradeBegin(alice.ctx(), e.ref(alice.id, carol), false)
	wantNotFound(t, err) // already friend+
	_, err = e.f.UpgradeBegin(e.ctx, e.ref(alice.id, bob.id), false)
	wantNotFound(t, err) // anonymous
}
