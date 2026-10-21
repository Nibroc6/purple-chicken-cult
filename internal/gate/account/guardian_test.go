package account_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/account"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// ---- passkey sessions ----

// member is a browser signed in with a passkey: its viewer carries the
// session's credential, so its account changes must be co-signed.
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

// sessions counts m's live session rows (RevokeAll deletes them; a short
// session also stops authenticating 12 h after its passkey verification).
func (m *member) sessions() int {
	m.e.t.Helper()
	return m.e.count("SELECT count(*) FROM sessions WHERE user_id = $1", int64(m.id))
}

// signedIn reports whether m's session still authenticates as m.
func (m *member) signedIn() bool {
	v, ok := core.ViewerFrom(m.ctx())
	return ok && v.Kind == core.SessUser && v.ID == m.id
}

func (m *member) viewer() core.Viewer {
	m.e.t.Helper()
	v, ok := core.ViewerFrom(m.ctx())
	if !ok || v.Kind != core.SessUser || len(v.Credential) == 0 {
		m.e.t.Fatalf("not signed in with a passkey: %+v", v)
	}
	return v
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
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
	m.cookie, _, err = e.a.Auth.EnrollFinish(m.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	e.must(err)
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	m.id = id
	return m
}

// link makes a and b friends at lvl in the database and in graph memory.
func (e *env) link(a, b core.UserID, lvl core.EdgeLevel) {
	e.t.Helper()
	for _, p := range [][2]core.UserID{{a, b}, {b, a}} {
		e.exec("INSERT INTO edges (user_id, friend_id, level, since, record_hash) VALUES ($1, $2, $3, $4, '\\x00')",
			int64(p[0]), int64(p[1]), int16(lvl), coretest.Epoch)
	}
	e.d.Graph.AddEdge(a, b, lvl, coretest.Epoch)
}

// sign opens m's intent for purpose on target and signs it with m's passkey.
func (e *env) sign(m *member, purpose string, target gate.Ref) *account.Cosig {
	e.t.Helper()
	in, err := e.a.Cosign.BeginIntent(m.ctx(), m.viewer(), purpose, target)
	e.must(err)
	asr, err := m.auth.Get(in.Options)
	e.must(err)
	return &account.Cosig{Intent: in.ID, Assertion: asr}
}

// ---- a scripted recovery handshake ----

// fakeHandshakes stands in for gate/invites' recovery handshakes: the claim
// payload "ok" claims the last opened handshake, and outcomes are set by the
// test (gate/invites tests the protocol; the e2e spec runs both together).
type fakeHandshakes struct {
	d       *core.Deps
	account map[uuid.UUID]core.UserID
	outcome map[uuid.UUID]core.Outcome
	last    uuid.UUID
	claimed map[uuid.UUID]bool
}

func (f *fakeHandshakes) OpenUpgrade(context.Context, core.Viewer, core.UserID) (gate.HandshakeView, error) {
	return gate.HandshakeView{}, gate.ErrNotFound
}

func (f *fakeHandshakes) OpenRecovery(_ context.Context, guardian, acc core.UserID) (gate.HandshakeView, error) {
	if lvl, ok := f.d.Graph.Level(guardian, acc); !ok || lvl != core.LevelFriendPlus {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	id := uuid.New()
	f.account[id], f.last = acc, id
	return gate.HandshakeView{HS: f.d.Refs.Handshake(guardian, id), Mode: "qr_audio"}, nil
}

func (f *fakeHandshakes) ClaimRecovery(_ context.Context, sess [32]byte, payload string) (core.UserID, error) {
	if payload != `"ok"` || sess == ([32]byte{}) || f.claimed[f.last] {
		return 0, gate.ErrNotFound
	}
	f.claimed[f.last] = true
	return f.account[f.last], nil
}

func (f *fakeHandshakes) Result(_ context.Context, hs uuid.UUID) (core.Outcome, error) {
	if _, ok := f.account[hs]; !ok {
		return core.OutcomeNone, gate.ErrNotFound
	}
	return f.outcome[hs], nil
}

func (e *env) handshakes() *fakeHandshakes {
	f := &fakeHandshakes{d: e.d, account: map[uuid.UUID]core.UserID{}, outcome: map[uuid.UUID]core.Outcome{},
		claimed: map[uuid.UUID]bool{}}
	e.d.Handshakes = f
	return f
}

func (e *env) guest(n byte) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{Kind: core.SessGuest, Session: [32]byte{n}})
}

func (e *env) status(ticket string) account.RecoveryStatus {
	e.t.Helper()
	st, err := e.a.Account.RecoveryStatus(e.ctx, ticket)
	e.must(err)
	return st
}

// backdate moves request id's start d into the past (the members' short
// sessions would not survive advancing the clock that far).
func (e *env) backdate(id uuid.UUID, d time.Duration) {
	e.t.Helper()
	e.exec("UPDATE recovery_requests SET created_at = created_at - make_interval(secs => $2) WHERE id = $1", id, d.Seconds())
}

func wantNotFound(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("%s: err = %v, want the uniform not-found", what, err)
	}
}

// recovery is a guardian recovery of alice under way: gina vouched in person,
// gus is the second guardian, pat a plain friend and olga a stranger.
type recovery struct {
	alice, gina, gus, pat, olga *member
	hs                          *fakeHandshakes
	ticket                      string
	request                     uuid.UUID
}

// vouched runs a recovery of alice up to the point where it waits for the
// second guardian.
func (e *env) vouched() *recovery {
	e.t.Helper()
	r := &recovery{alice: e.enroll("alice"), gina: e.enroll("gina"), gus: e.enroll("gus"), pat: e.enroll("pat"),
		olga: e.enroll("olga"), hs: e.handshakes()}
	e.link(r.alice.id, r.gina.id, core.LevelFriendPlus)
	e.link(r.alice.id, r.gus.id, core.LevelFriendPlus)
	e.link(r.alice.id, r.pat.id, core.LevelFriend)

	_, err := e.a.Account.RecoveryStart(r.gina.ctx(), e.d.Refs.User(r.gina.id, r.alice.id))
	e.must(err)
	c, err := e.a.Account.RecoveryClaim(e.guest(7), []byte(`"ok"`))
	e.must(err)
	r.ticket = c.Ticket
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT id FROM recovery_requests WHERE user_id = $1 AND kind = 1",
		int64(r.alice.id)).Scan(&r.request))
	r.hs.outcome[r.hs.last] = core.OutcomeVerified
	if st := e.status(r.ticket); st.State != account.StatusApproving {
		e.t.Fatalf("status %+v, want approving", st)
	}
	return r
}

// approve is gus's co-signed approval.
func (e *env) approve(r *recovery) {
	e.t.Helper()
	list, err := e.a.Account.RecoveryRequests(r.gus.ctx())
	e.must(err)
	if len(list.Items) != 1 {
		e.t.Fatalf("gus's requests %+v", list)
	}
	id := list.Items[0].ID
	e.must(e.a.Account.RecoveryGuardianApprove(r.gus.ctx(), id, e.sign(r.gus, account.PurposeRecoveryApprove, id)))
}

// Acceptance: a guardian recovery needs two distinct friend+ guardians — the
// one who vouched in person and a second who co-signs —, non-guardians get the
// uniform not-found, and the released recovery ends every session and hands
// the recovering device an enrollment link.
func TestGuardianRecovery(t *testing.T) {
	e := newEnv(t)
	alice, gina, gus, pat, olga := e.enroll("alice"), e.enroll("gina"), e.enroll("gus"), e.enroll("pat"), e.enroll("olga")
	hs := e.handshakes()
	e.link(alice.id, gina.id, core.LevelFriendPlus)
	e.link(alice.id, gus.id, core.LevelFriendPlus)
	e.link(alice.id, pat.id, core.LevelFriend)

	// Only a friend+ can start one, and only for that friend.
	_, err := e.a.Account.RecoveryStart(pat.ctx(), e.d.Refs.User(pat.id, alice.id))
	wantNotFound(t, err, "a plain friend starts")
	_, err = e.a.Account.RecoveryStart(olga.ctx(), e.d.Refs.User(olga.id, alice.id))
	wantNotFound(t, err, "a stranger starts")
	_, err = e.a.Account.RecoveryStart(gina.ctx(), e.d.Refs.User(gus.id, alice.id))
	wantNotFound(t, err, "a ref minted for someone else")
	hv, err := e.a.Account.RecoveryStart(gina.ctx(), e.d.Refs.User(gina.id, alice.id))
	e.must(err)
	if hv.HS == "" {
		t.Fatalf("handshake view %+v", hv)
	}
	_, err = e.a.Account.RecoveryStart(gus.ctx(), e.d.Refs.User(gus.id, alice.id))
	wantProblem(t, err, "recovery_exists")
	// gina may open it again (a new code) until her handshake passed.
	_, err = e.a.Account.RecoveryStart(gina.ctx(), e.d.Refs.User(gina.id, alice.id))
	e.must(err)

	// The recovering device claims from a guest session and gets its ticket.
	_, err = e.a.Account.RecoveryClaim(alice.ctx(), []byte(`"ok"`))
	wantNotFound(t, err, "a claim from a user session")
	_, err = e.a.Account.RecoveryClaim(e.guest(7), []byte(`"nope"`))
	wantNotFound(t, err, "a bad claim")
	c, err := e.a.Account.RecoveryClaim(e.guest(7), []byte(`"ok"`))
	e.must(err)
	if len(c.Ticket) < 40 {
		t.Fatalf("ticket %q", c.Ticket)
	}
	_, err = e.a.Account.RecoveryClaim(e.guest(8), []byte(`"ok"`))
	wantNotFound(t, err, "a second claim")
	_, err = e.a.Account.RecoveryStatus(e.ctx, "not-a-ticket")
	wantNotFound(t, err, "an unknown ticket")
	if st := e.status(c.Ticket); st.State != account.StatusClaimed {
		t.Fatalf("status %+v, want claimed", st)
	}
	// Nothing to approve before the handshake passed.
	list, err := e.a.Account.RecoveryRequests(gus.ctx())
	e.must(err)
	if len(list.Items) != 0 {
		t.Fatalf("listed before the handshake passed: %+v", list)
	}
	hs.outcome[hs.last] = core.OutcomeRetry
	if st := e.status(c.Ticket); st.State != account.StatusClaimed {
		t.Fatalf("status after a retry %+v", st)
	}
	hs.outcome[hs.last] = core.OutcomeAccepted
	if st := e.status(c.Ticket); st.State != account.StatusApproving {
		t.Fatalf("status %+v, want approving", st)
	}
	// The other guardians — gus, not gina, not the plain friend — are asked.
	for u, want := range map[core.UserID]int{gus.id: 1, gina.id: 0, pat.id: 0, olga.id: 0} {
		n := 0
		for _, ev := range e.rec.For(u) {
			if ev.Kind == "recovery_approval" && ev.SubjectUser == alice.id {
				n++
			}
		}
		if n != want {
			t.Errorf("user %d got %d recovery_approval notifications, want %d", u, n, want)
		}
	}
	list, err = e.a.Account.RecoveryRequests(gus.ctx())
	e.must(err)
	if len(list.Items) != 1 || list.Items[0].Account.Handle != "alice" {
		t.Fatalf("gus's requests %+v", list)
	}
	id := list.Items[0].ID
	for _, m := range []*member{gina, pat, olga, alice} {
		l, err := e.a.Account.RecoveryRequests(m.ctx())
		e.must(err)
		if len(l.Items) != 0 {
			t.Errorf("user %d sees %+v", m.id, l)
		}
	}

	// Approvals: only a second friend+ guardian, co-signed.
	var rid uuid.UUID
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT id FROM recovery_requests WHERE user_id = $1", int64(alice.id)).Scan(&rid))
	for _, m := range []*member{pat, olga, alice} {
		wantNotFound(t, e.a.Account.RecoveryGuardianApprove(m.ctx(), e.d.Refs.Handshake(m.id, rid), nil), "a non-guardian approves")
		_, err := e.a.Cosign.BeginIntent(m.ctx(), m.viewer(), account.PurposeRecoveryApprove, e.d.Refs.Handshake(m.id, rid))
		wantNotFound(t, err, "a non-guardian's intent")
	}
	wantNotFound(t, e.a.Account.RecoveryGuardianApprove(pat.ctx(), id, nil), "gus's ref used by pat")
	wantProblem(t, e.a.Account.RecoveryGuardianApprove(gina.ctx(), e.d.Refs.Handshake(gina.id, rid), nil),
		"second_guardian_required")
	wantNotFound(t, e.a.Account.RecoveryGuardianApprove(gus.ctx(), id, nil), "a passkey session without a co-signature")
	e.must(e.a.Account.RecoveryGuardianApprove(gus.ctx(), id, e.sign(gus, account.PurposeRecoveryApprove, id)))
	if n := e.count(`SELECT count(*) FROM signed_records WHERE kind = $1 AND jsonb_array_length(assertions) = 1
		AND convert_from(body, 'UTF8')::jsonb->>'method' = 'guardians'`, int16(core.RecRecoveryApproval)); n != 1 {
		t.Fatalf("%d co-signed RecoveryApproval records", n)
	}
	if n := e.count(`SELECT count(*) FROM recovery_approvals WHERE request_id = $1 AND role = 1 AND assertion IS NOT NULL`,
		rid); n != 1 {
		t.Fatalf("%d guardian approvals with an assertion", n)
	}
	for _, f := range []core.UserID{gina.id, gus.id, pat.id} {
		found := false
		for _, ev := range e.rec.For(f) {
			found = found || (ev.Kind == "friend_security" && ev.Data["reason"] == "recovery_pending")
		}
		if !found {
			t.Errorf("friend %d was not told about the pending recovery", f)
		}
	}

	// The hold: the banner on alice's sessions, the device waits.
	st := e.status(c.Ticket)
	if st.State != account.StatusHolding || st.ReleaseAt == nil || !st.ReleaseAt.Equal(e.now().Add(account.HoldFor).UTC()) {
		t.Fatalf("status %+v", st)
	}
	at, err := e.a.Account.RecoveryPending(alice.ctx())
	e.must(err)
	if at == nil {
		t.Fatal("no banner during the hold")
	}
	if l, _ := e.a.Account.RecoveryRequests(gus.ctx()); len(l.Items) != 0 {
		t.Fatalf("still listed after the approval: %+v", l)
	}

	// Released: every session of alice ends, the device gets a link (a fresh
	// one on every ask).
	e.advance(account.HoldFor + time.Second)
	st = e.status(c.Ticket)
	if st.State != account.StatusReleased || !strings.HasPrefix(st.EnrollURL, e.d.Cfg.Origin+"/enroll#") {
		t.Fatalf("status %+v", st)
	}
	if alice.sessions() != 0 {
		t.Fatal("alice's session survived the release")
	}
	if again := e.status(c.Ticket); again.EnrollURL == st.EnrollURL || again.State != account.StatusReleased {
		t.Fatalf("second ask %+v", again)
	}
	if gina.sessions() == 0 || gus.sessions() == 0 {
		t.Fatal("the guardians were signed out")
	}
}

// Acceptance: cancel during the hold works from any live session of the
// account; the device then sees cancelled and no link is ever issued.
func TestGuardianRecoveryCancel(t *testing.T) {
	e := newEnv(t)
	r := e.vouched()
	e.approve(r)
	e.must(e.a.Account.RecoveryCancel(r.alice.ctx()))
	if at, err := e.a.Account.RecoveryPending(r.alice.ctx()); err != nil || at != nil {
		t.Fatalf("banner %v %v", at, err)
	}
	if st := e.status(r.ticket); st.State != account.StatusCancelled || st.EnrollURL != "" {
		t.Fatalf("status %+v", st)
	}
	e.advance(account.HoldFor + time.Second)
	if st := e.status(r.ticket); st.State != account.StatusCancelled {
		t.Fatalf("status after the hold %+v", st)
	}
	if r.alice.sessions() == 0 {
		t.Fatal("a cancelled recovery signed alice out")
	}
}

// The recovery.release job releases a guardian recovery like a manual one;
// a request never approved expires after a day, and so does a released one
// whose device never asked.
func TestGuardianRecoveryJobAndExpiry(t *testing.T) {
	e := newEnv(t)
	r := e.vouched()
	e.approve(r)
	e.advance(account.HoldFor + time.Second)
	e.must(e.a.DrainJobs(e.ctx))
	if n := e.count("SELECT count(*) FROM recovery_requests WHERE id = $1 AND state = 2", r.request); n != 1 {
		t.Fatal("the job did not release the guardian recovery")
	}
	if r.alice.sessions() != 0 {
		t.Fatal("alice's session survived the release")
	}
	e.advance(account.LinkFor + time.Second)
	if st := e.status(r.ticket); st.State != account.StatusExpired || st.EnrollURL != "" {
		t.Fatalf("status after the link window %+v", st)
	}

	// Never approved: expired after OpenFor, and a new one may start.
	e2 := newEnv(t)
	q := e2.vouched()
	e2.backdate(q.request, account.OpenFor+time.Second)
	if st := e2.status(q.ticket); st.State != account.StatusExpired {
		t.Fatalf("status %+v", st)
	}
	wantProblemOrNotFound := func(err error) {
		t.Helper()
		var p *gate.Problem
		if !errors.As(err, &p) && !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	}
	wantProblemOrNotFound(e2.a.Account.RecoveryGuardianApprove(q.gus.ctx(), e2.d.Refs.Handshake(q.gus.id, q.request), nil))
	_, err := e2.a.Account.RecoveryStart(q.gus.ctx(), e2.d.Refs.User(q.gus.id, q.alice.id))
	e2.must(err)
}

// A guardian's approval of an expired, still-open request is recovery_expired.
func TestGuardianApproveExpired(t *testing.T) {
	e := newEnv(t)
	r := e.vouched()
	list, err := e.a.Account.RecoveryRequests(r.gus.ctx())
	e.must(err)
	id := list.Items[0].ID
	cs := e.sign(r.gus, account.PurposeRecoveryApprove, id)
	e.backdate(r.request, account.OpenFor+time.Second)
	wantProblem(t, e.a.Account.RecoveryGuardianApprove(r.gus.ctx(), id, cs), "recovery_expired")
}

// Deleting the account cancels its guardian recovery: the device sees
// cancelled.
func TestDeleteCancelsGuardianRecovery(t *testing.T) {
	e := newEnv(t)
	r := e.vouched()
	e.approve(r)
	e.must(e.a.Account.Delete(r.alice.ctx(), "alice", e.sign(r.alice, account.PurposeAccountDelete, "")))
	if st := e.status(r.ticket); st.State != account.StatusCancelled {
		t.Fatalf("status %+v", st)
	}
}

// Acceptance: account deletion from a passkey session is co-signed (the
// AccountDelete record carries the assertion); without one it is the uniform
// not-found and nothing changes.
func TestCosignedDelete(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	wantNotFound(t, e.a.Account.Delete(alice.ctx(), "alice", nil), "a delete without a co-signature")
	wantNotFound(t, e.a.Account.Delete(alice.ctx(), "alice", &account.Cosig{Intent: "x", Assertion: []byte(`{}`)}),
		"a delete with a bogus co-signature")
	if !alice.signedIn() {
		t.Fatal("a refused delete changed something")
	}
	cs := e.sign(alice, account.PurposeAccountDelete, "")
	wantProblem(t, e.a.Account.Delete(alice.ctx(), "bob", cs), "confirm_mismatch")
	e.must(e.a.Account.Delete(alice.ctx(), "alice", cs))
	if n := e.count(`SELECT count(*) FROM signed_records WHERE kind = $1 AND author_id = $2
		AND jsonb_array_length(assertions) = 1`, int16(core.RecAccountDelete), int64(alice.id)); n != 1 {
		t.Fatalf("%d co-signed AccountDelete records", n)
	}
	if alice.signedIn() {
		t.Fatal("still signed in after the delete")
	}
	// A session without a passkey (test hooks) deletes with a server-signed record.
	bob := e.w.User("bob")
	e.must(e.a.Account.Delete(e.fresh(bob), "bob", nil))
	if n := e.count(`SELECT count(*) FROM signed_records WHERE kind = $1 AND author_id = $2 AND assertions IS NULL`,
		int16(core.RecAccountDelete), int64(bob)); n != 1 {
		t.Fatalf("%d server-signed AccountDelete records", n)
	}
}

// The second moderator of a manual recovery co-signs from a passkey session.
func TestCosignedManualApproval(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	mod1 := e.moderator("mod1")
	mod2 := e.enroll("mod2")
	e.exec("UPDATE users SET role = 1 WHERE id = $1", int64(mod2.id))
	e.a.Users.Invalidate(mod2.id)
	id, err := e.a.Account.RecoveryRequest(e.fresh(mod1), e.d.Refs.ModUser(mod1, alice))
	e.must(err)
	wantNotFound(t, e.a.Account.RecoveryApprove(mod2.ctx(), id, nil), "a passkey moderator without a co-signature")
	e.must(e.a.Account.RecoveryApprove(mod2.ctx(), id, e.sign(mod2, account.PurposeRecoveryApprove, id)))
	if n := e.count(`SELECT count(*) FROM signed_records WHERE kind = $1 AND jsonb_array_length(assertions) = 1
		AND convert_from(body, 'UTF8')::jsonb->>'method' = 'moderators'`, int16(core.RecRecoveryApproval)); n != 1 {
		t.Fatalf("%d co-signed manual RecoveryApproval records", n)
	}
}
