package invites_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
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

// guest is an invitee's browser: a guest session and a software authenticator.
type guest struct {
	e    *env
	sess [32]byte
	ctx  context.Context
	auth *authtest.Authenticator
}

func (e *env) guest() *guest {
	e.t.Helper()
	tok, err := e.a.Auth.StartGuest(e.ctx)
	e.must(err)
	h := sha256.Sum256([]byte(tok))
	ctx := core.WithViewer(e.ctx, core.Viewer{Kind: core.SessGuest, Session: h})
	return &guest{e: e, sess: h, ctx: ctx, auth: authtest.New(e.d.Cfg.Origin)}
}

// form is a valid join form for handle.
func (e *env) form(handle string) invites.Registration {
	return invites.Registration{Handle: handle, DisplayName: "New " + handle, Locale: "en", Languages: []string{"en", "de"},
		TOSVersion: e.d.Cfg.TOSVersion, AgeAttested: true}
}

// open opens a handshake as inviter and returns its ref and current frame.
func (e *env) open(inviter core.UserID) (gate.Ref, string) {
	e.t.Helper()
	hv, err := e.s.Open(e.as(inviter), "qr", false)
	e.must(err)
	f, err := e.s.Frame(e.as(inviter), hv.HS)
	e.must(err)
	return hv.HS, f.Payload
}

// confirmed runs open → claim → confirm and returns the guest and the handshake.
func (e *env) confirmed(inviter core.UserID) (*guest, gate.Ref) {
	e.t.Helper()
	hs, payload := e.open(inviter)
	g := e.guest()
	e.must(e.s.Claim(g.ctx, payload))
	e.must(e.s.Confirm(e.as(inviter), hs))
	return g, hs
}

// register runs the passkey ceremony for form f and finishes the join.
func (g *guest) register(f invites.Registration) (string, gate.MeView, error) {
	opts, err := g.e.s.RegisterBegin(g.ctx, f)
	if err != nil {
		return "", gate.MeView{}, err
	}
	cred, err := g.auth.Create(opts)
	g.e.must(err)
	return g.e.s.RegisterFinish(g.ctx, cred)
}

// join runs the whole invite for handle and returns the new account.
func (e *env) join(inviter core.UserID, handle string) core.UserID {
	e.t.Helper()
	g, _ := e.confirmed(inviter)
	_, me, err := g.register(e.form(handle))
	e.must(err)
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok || me.Handle != handle {
		e.t.Fatalf("joined %q: me %+v", handle, me)
	}
	return id
}

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

func TestJoinEndToEnd(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	bob := e.w.User("bob")
	e.w.Friends("bob", "alice")
	before, err := e.s.Budget(e.as(alice))
	e.must(err)
	if before.Available != invites.GenesisCap || before.Cap != invites.GenesisCap || before.NextAt != nil {
		t.Fatalf("genesis budget %+v", before)
	}

	hs, payload := e.open(alice)
	if st, _ := e.s.Status(e.as(alice), hs); st.State != "open" || st.SAS != "" {
		t.Fatalf("status %+v", st)
	}
	g := e.guest()
	e.must(e.s.Claim(g.ctx, payload))
	inv, err := e.s.Status(e.as(alice), hs)
	e.must(err)
	mine, err := e.s.JoinStatus(g.ctx)
	e.must(err)
	if inv.State != "claimed" || len(inv.SAS) != 6 || mine.SAS != inv.SAS || mine.State != "claimed" {
		t.Fatalf("inviter %+v invitee %+v", inv, mine)
	}
	if len(e.rec.LiveEv) == 0 {
		t.Fatal("no live event for the inviter")
	}

	// Registration waits for "codes match".
	_, err = e.s.RegisterBegin(g.ctx, e.form("newbie"))
	wantNotFound(t, err, "register before confirm")
	e.must(e.s.Confirm(e.as(alice), hs))

	f := e.form("newbie")
	f.TOSVersion = 0
	_, err = e.s.RegisterBegin(g.ctx, f)
	wantProblem(t, err, "tos_required")
	f = e.form("newbie")
	f.AgeAttested = false
	_, err = e.s.RegisterBegin(g.ctx, f)
	wantProblem(t, err, "age_required")
	_, err = e.s.RegisterBegin(g.ctx, e.form("admin"))
	wantProblem(t, err, "handle_invalid")
	_, err = e.s.RegisterBegin(g.ctx, e.form("x"))
	wantProblem(t, err, "handle_invalid")
	_, err = e.s.RegisterBegin(g.ctx, e.form("Alice"))
	wantProblem(t, err, "handle_taken")

	tok, me, err := g.register(e.form("@NewBie"))
	e.must(err)
	if tok == "" || me.Handle != "newbie" || me.Inviter.Kind != "user" || me.Inviter.Handle != "alice" ||
		me.DisplayName != "New @NewBie" {
		t.Fatalf("me %+v", me)
	}
	nb, ok := e.a.Users.ByHandle(e.ctx, "newbie")
	if !ok {
		t.Fatal("no account")
	}
	m, err := e.d.Users.Get(e.ctx, nb)
	e.must(err)
	if m.InvitedBy != alice || !m.OnProbation(e.d.Clock.Now()) ||
		!m.ProbationUntil.Equal(e.d.Clock.Now().Add(invites.ProbationFor)) || len(m.Languages) != 2 {
		t.Fatalf("meta %+v", m)
	}
	if lvl, ok := e.d.Graph.Level(alice, nb); !ok || lvl != core.LevelFriend {
		t.Fatal("no friendship in graph memory")
	}
	if n := e.count("SELECT count(*) FROM edges WHERE user_id = $1 AND friend_id = $2", int64(alice), int64(nb)); n != 1 {
		t.Fatalf("edges = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM invites i JOIN signed_records a ON a.hash = i.invite_record
		JOIN signed_records b ON b.hash = i.friendship_record WHERE i.inviter_id = $1 AND i.invitee_id = $2
		AND a.kind = 1 AND b.kind = 2`, int64(alice), int64(nb)); n != 1 {
		t.Fatalf("invites rows with records = %d", n)
	}
	if n := e.count("SELECT count(*) FROM record_parties WHERE user_id = $1", int64(nb)); n < 3 {
		t.Fatalf("records of the invitee = %d, want invite, friendship and passkey", n)
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE id_hash = $1", g.sess[:]); n != 0 {
		t.Fatal("guest session survived the upgrade")
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE user_id = $1 AND kind = 1", int64(nb)); n != 1 {
		t.Fatalf("user sessions = %d", n)
	}
	var got []core.Event
	for _, ev := range e.rec.Events {
		if ev.Kind == "invite_redeemed" {
			got = append(got, ev)
		}
	}
	if len(got) != 1 || got[0].To != alice || got[0].SubjectUser != nb {
		t.Fatalf("notifications %+v", e.rec.Events)
	}
	if e.met.Get("invites.redeemed", "") != 1 {
		t.Fatal("metric not counted")
	}
	after, err := e.s.Budget(e.as(alice))
	e.must(err)
	if after.Available != invites.GenesisCap-1 || after.NextAt == nil ||
		!after.NextAt.Equal(e.d.Clock.Now().Add(invites.TokenEvery)) {
		t.Fatalf("budget after %+v", after)
	}
	if st, _ := e.s.Status(e.as(alice), hs); st.State != "done" || st.SAS != "" {
		t.Fatalf("final status %+v", st)
	}

	// The invitee list shows the probation state to the inviter only.
	list, err := e.s.Invitees(e.as(alice))
	e.must(err)
	if len(list) != 1 || list[0].Person.Handle != "newbie" || !list[0].OnProbation || list[0].ProbationUntil == nil {
		t.Fatalf("invitees %+v", list)
	}
	other, err := e.s.Invitees(e.as(bob))
	e.must(err)
	if len(other) != 0 {
		t.Fatalf("bob sees %+v", other)
	}
	_, err = e.s.Invitees(e.ctx)
	wantNotFound(t, err, "anonymous invitees")

	// The new account is on probation: no handshakes.
	_, err = e.s.Open(e.as(nb), "qr", false)
	wantProblem(t, err, "on_probation")
	b, err := e.s.Budget(e.as(nb))
	e.must(err)
	if b.Available != 0 || b.Probation == nil || b.Probation.NeedOutsideFriends != 2 || b.NextAt != nil {
		t.Fatalf("probation budget %+v (the inviter counts as one outside friend)", b)
	}
}

func TestOpenProblems(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	_, err := e.s.Open(e.as(alice), "qr_audio", false)
	wantProblem(t, err, "mode_unsupported")
	_, err = e.s.Open(e.ctx, "qr", false)
	wantNotFound(t, err, "anonymous open")

	_, err = e.s.Budget(e.as(alice)) // creates the bucket
	e.must(err)
	e.exec("UPDATE invite_budgets SET tokens = 0, refreshed_at = $2 WHERE user_id = $1", int64(alice), e.d.Clock.Now())
	_, err = e.s.Open(e.as(alice), "qr", false)
	wantProblem(t, err, "no_invites")
	e.advance(invites.TokenEvery)
	_, err = e.s.Open(e.as(alice), "qr", false)
	e.must(err)

	e.exec("UPDATE invite_budgets SET paused_until = $2 WHERE user_id = $1", int64(alice), e.d.Clock.Now().Add(time.Hour))
	_, err = e.s.Open(e.as(alice), "qr", false)
	wantProblem(t, err, "invites_paused")
	e.exec("UPDATE invite_budgets SET paused_until = NULL, revoked = true WHERE user_id = $1", int64(alice))
	_, err = e.s.Open(e.as(alice), "qr", false)
	wantProblem(t, err, "invites_paused")
}

func TestFriendCap(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	e.exec(`INSERT INTO users (id, ext_id, handle, handle_canon, is_genesis, webauthn_user_id)
		SELECT 3500000000 + g, gen_random_uuid(), 'cap' || g, 'cap' || g, true, sha256(g::text::bytea)
		FROM generate_series(1, 1000) g`)
	e.exec(`INSERT INTO friend_requests (from_id, to_id, state, created_at)
		SELECT $1, 3500000000 + g, 0, $2 FROM generate_series(1, 999) g`, int64(alice), e.d.Clock.Now())
	_, err := e.s.Open(e.as(alice), "qr", false)
	e.must(err)
	e.exec("INSERT INTO friend_requests (from_id, to_id, state, created_at) VALUES ($1, 3500001000, 1, $2)",
		int64(alice), e.d.Clock.Now())
	_, err = e.s.Open(e.as(alice), "qr", false)
	wantProblem(t, err, "friend_cap")
}

func TestBudgetAccruesToTheCap(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	carol := e.w.User("carol", invitedBy(alice))
	b, err := e.s.Budget(e.as(carol))
	e.must(err)
	if b.Available != 1 || b.Cap != invites.DefaultCap {
		t.Fatalf("first budget %+v", b)
	}
	e.exec("UPDATE invite_budgets SET tokens = 0.5, refreshed_at = $2 WHERE user_id = $1", int64(carol), e.d.Clock.Now())
	b, err = e.s.Budget(e.as(carol))
	e.must(err)
	if b.Available != 0 || b.NextAt == nil || !b.NextAt.Equal(e.d.Clock.Now().Add(12*time.Hour)) {
		t.Fatalf("half a token %+v", b)
	}
	e.advance(30 * 24 * time.Hour)
	b, err = e.s.Budget(e.as(carol))
	e.must(err)
	if b.Available != invites.DefaultCap || b.NextAt != nil {
		t.Fatalf("budget never exceeds the cap: %+v", b)
	}
}

func TestClaimFailuresAreUniform(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	hs, payload := e.open(alice)
	var id uuid.UUID
	var secret []byte
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT id, secret FROM handshake_sessions").Scan(&id, &secret))
	now := e.d.Clock.Now()

	g := e.guest()
	// Stale frame (two windows old).
	stale := invites.FramePayload(secret, id, now.Add(-2*invites.FrameWindow))
	wantNotFound(t, e.s.Claim(g.ctx, stale), "stale frame")
	// Forged MAC.
	parts := strings.Split(payload, ".")
	forged := strings.Join(append(parts[:3:3], "AAAAAAAAAAAAAA"), ".")
	wantNotFound(t, e.s.Claim(g.ctx, forged), "forged mac")
	wantNotFound(t, e.s.Claim(g.ctx, "garbage"), "garbage")
	// A user session is not a guest session.
	wantNotFound(t, e.s.Claim(e.as(alice), payload), "user session")
	_, err := e.s.JoinStatus(g.ctx)
	wantNotFound(t, err, "status before claim")

	// The previous window still works (a frame scanned just before rotation).
	e.advance(invites.FrameWindow)
	e.must(e.s.Claim(g.ctx, payload))
	// Reused handshake: a second claimer gets nothing.
	g2 := e.guest()
	wantNotFound(t, e.s.Claim(g2.ctx, payload), "reused handshake")
	_, err = e.s.JoinStatus(g2.ctx)
	wantNotFound(t, err, "status of another session")
	_, err = e.s.Frame(e.as(alice), hs)
	wantNotFound(t, err, "frame after claim")
	// Someone else's handshake ref.
	bob := e.w.User("bob")
	_, err = e.s.Status(e.as(bob), hs)
	wantNotFound(t, err, "foreign status")
	wantNotFound(t, e.s.Confirm(e.as(bob), hs), "foreign confirm")
}

func TestBystanderGetsADifferentCode(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	hs, payload := e.open(alice)
	bystander, invitee := e.guest(), e.guest()
	e.must(e.s.Claim(bystander.ctx, payload))
	first, err := e.s.Status(e.as(alice), hs)
	e.must(err)
	// Codes don't match: the inviter cancels and opens a new handshake.
	e.must(e.s.Cancel(e.as(alice), hs))
	if st, _ := e.s.JoinStatus(bystander.ctx); st.State != "cancelled" || st.SAS != "" {
		t.Fatalf("bystander status %+v", st)
	}
	_, err = e.s.RegisterBegin(bystander.ctx, e.form("thief"))
	wantNotFound(t, err, "cancelled register")
	hs2, payload2 := e.open(alice)
	e.must(e.s.Claim(invitee.ctx, payload2))
	second, err := e.s.Status(e.as(alice), hs2)
	e.must(err)
	if first.SAS == second.SAS {
		t.Fatalf("both claimers got %s", first.SAS)
	}
	var secret []byte
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT secret FROM handshake_sessions WHERE state = 1").Scan(&secret))
	if invites.SAS(secret, bystander.sess[:]) == invites.SAS(secret, invitee.sess[:]) {
		t.Fatal("SAS does not depend on the claiming session")
	}
}

func TestHandshakeLifecycle(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	hs, _ := e.open(alice)
	wantNotFound(t, e.s.Confirm(e.as(alice), hs), "confirm before claim")
	// Opening again cancels the first.
	hs2, payload2 := e.open(alice)
	if st, _ := e.s.Status(e.as(alice), hs); st.State != "cancelled" {
		t.Fatalf("first handshake %+v", st)
	}
	wantNotFound(t, e.s.Cancel(e.as(alice), hs), "cancel twice")
	// Expiry: an unclaimed handshake lives OpenTTL.
	e.advance(invites.OpenTTL)
	if st, _ := e.s.Status(e.as(alice), hs2); st.State != "expired" {
		t.Fatalf("expired handshake %+v", st)
	}
	wantNotFound(t, e.s.Claim(e.guest().ctx, payload2), "expired claim")

	// A claimed handshake must finish within ClaimedTTL.
	g, hs3 := e.confirmed(alice)
	e.advance(invites.ClaimedTTL)
	if st, _ := e.s.Status(e.as(alice), hs3); st.State != "expired" {
		t.Fatalf("claimed handshake %+v", st)
	}
	_, err := e.s.RegisterBegin(g.ctx, e.form("late"))
	wantNotFound(t, err, "expired register")
}

func TestHandleTakenIsAnsweredTenTimes(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	for i := range invites.HandleTries {
		e.w.User("taken" + string(rune('a'+i)))
	}
	g, _ := e.confirmed(alice)
	for i := range invites.HandleTries {
		_, err := g.e.s.RegisterBegin(g.ctx, e.form("taken"+string(rune('a'+i))))
		wantProblem(t, err, "handle_taken")
	}
	_, err := e.s.RegisterBegin(g.ctx, e.form("alice"))
	wantNotFound(t, err, "11th probe")
	_, err = e.s.RegisterBegin(g.ctx, e.form("freehandle"))
	wantNotFound(t, err, "after the probes")
}

func TestConcurrentRedeemCreatesOneUser(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	g, _ := e.confirmed(alice)
	opts, err := e.s.RegisterBegin(g.ctx, e.form("racer"))
	e.must(err)
	raw, err := g.auth.Create(opts)
	e.must(err)
	cred, err := e.d.Passkeys.FinishRegistration(e.ctx, g.sess, raw)
	e.must(err)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
		errs []error
	)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := e.s.Redeem(e.ctx, g.sess, cred)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if !errors.Is(err, gate.ErrNotFound) {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || len(errs) != 0 {
		t.Fatalf("wins = %d, errors %v", wins, errs)
	}
	if n := e.count("SELECT count(*) FROM users WHERE invited_by = $1", int64(alice)); n != 1 {
		t.Fatalf("users = %d", n)
	}
	if n := e.count("SELECT count(*) FROM invites"); n != 1 {
		t.Fatalf("invites = %d", n)
	}
	var tokens float64
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT tokens FROM invite_budgets WHERE user_id = $1", int64(alice)).Scan(&tokens))
	if tokens != invites.GenesisCap-1 {
		t.Fatalf("tokens = %v", tokens)
	}
}

func TestRegisterFinishWithoutCeremonyIsNotFound(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	g, _ := e.confirmed(alice)
	_, _, err := e.s.RegisterFinish(g.ctx, json.RawMessage(`{}`))
	wantNotFound(t, err, "finish before begin")
	opts, err := e.s.RegisterBegin(g.ctx, e.form("solo"))
	e.must(err)
	raw, err := g.auth.Create(opts)
	e.must(err)
	_, _, err = e.s.RegisterFinish(e.guest().ctx, raw)
	wantNotFound(t, err, "finish from another session")
}

// friend makes a and b friends the way a module that owns edges does.
func (e *env) friend(a, b core.UserID) {
	e.t.Helper()
	h := sha256.Sum256([]byte{byte(a), byte(a >> 8), byte(b), byte(b >> 8)})
	e.must(e.d.Edges.Create(e.ctx, nil, a, b, core.LevelFriend, h[:]))
	now := e.d.Clock.Now()
	e.d.Graph.AddEdge(a, b, core.LevelFriend, now)
	e.d.Bus.Publish(core.EdgeAdded{A: a, B: b, Level: core.LevelFriend, At: now})
}

func (e *env) onProbation(u core.UserID) bool {
	e.t.Helper()
	m, err := e.d.Users.Get(e.ctx, u)
	e.must(err)
	return m.OnProbation(e.d.Clock.Now())
}

func TestProbationClearsWhenBothPartsHold(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	x, y, z := e.w.User("xavier"), e.w.User("yara"), e.w.User("zoe")
	cleared := 0
	core.Subscribe(e.d.Bus, func(core.ProbationCleared) { cleared++ })
	nb := e.join(alice, "newbie")

	// An invitee of newbie is inside its subtree and never counts.
	e.exec(`INSERT INTO users (id, ext_id, handle, handle_canon, invited_by, webauthn_user_id, probation_until)
		VALUES (3600000000, $1, 'kid', 'kid', $2, '\x01', $3)`, uuid.New(), int64(nb), e.d.Clock.Now().Add(time.Hour))
	e.friend(nb, 3600000000)
	e.friend(nb, x)
	e.friend(nb, y) // alice, xavier, yara: three outside friends, but the time part is not over
	if !e.onProbation(nb) || cleared != 0 {
		t.Fatal("cleared before the time part")
	}
	e.advance(invites.ProbationFor - time.Second)
	n, err := e.s.EvaluateAll(e.ctx)
	e.must(err)
	if n != 0 || !e.onProbation(nb) {
		t.Fatal("cleared a second early")
	}
	e.advance(time.Second)
	n, err = e.s.EvaluateAll(e.ctx)
	e.must(err)
	if n != 1 || e.onProbation(nb) || cleared != 1 {
		t.Fatalf("cleared %d", n)
	}
	b, err := e.s.Budget(e.as(nb))
	e.must(err)
	if b.Available != 1 || b.Probation != nil || b.Cap != invites.DefaultCap {
		t.Fatalf("budget after clearance %+v", b)
	}
	_, err = e.s.Open(e.as(nb), "qr", false)
	e.must(err)
	// Monotonic: losing friends does not put the account back on probation.
	e.exec("DELETE FROM edges WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)", int64(nb), int64(x))
	e.d.Graph.RemoveEdge(nb, x)
	if n, _ := e.s.EvaluateAll(e.ctx); n != 0 || e.onProbation(nb) {
		t.Fatal("probation came back")
	}

	// Time over but too few outside friends: the edge that completes the set
	// clears probation at once (EdgeAdded hook).
	nb2 := e.join(alice, "second")
	e.friend(nb2, z)
	e.advance(invites.ProbationFor)
	if n, _ := e.s.EvaluateAll(e.ctx); n != 0 || !e.onProbation(nb2) {
		t.Fatal("cleared with two outside friends")
	}
	b, err = e.s.Budget(e.as(nb2))
	e.must(err)
	if b.Probation == nil || b.Probation.NeedOutsideFriends != 1 {
		t.Fatalf("probation view %+v", b.Probation)
	}
	e.friend(nb2, y)
	if e.onProbation(nb2) || cleared != 2 {
		t.Fatal("the third outside friend did not clear probation")
	}
}

func TestResetPutsUsersBackOnProbation(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	carol := e.w.User("carol", invitedBy(alice))
	until := e.d.Clock.Now().Add(30 * 24 * time.Hour)
	e.must(e.d.Probation.Reset(e.ctx, nil, []core.UserID{alice, carol}, until))
	if e.onProbation(alice) {
		t.Fatal("genesis accounts have no probation")
	}
	if !e.onProbation(carol) {
		t.Fatal("reset did not apply")
	}
	_, err := e.s.Open(e.as(carol), "qr", false)
	wantProblem(t, err, "on_probation")
}
