package friends_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	f   *friends.Service
	w   *wiretest.TestWorld
	rec *coreref.RecordingNotifier
	ctx context.Context

	bulk int // users created by insertUsers
}

// bulkBase is where insertUsers' explicit IDs start (above coretest's fixtures).
const bulkBase = 3_500_000_000

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	rec := &coreref.RecordingNotifier{D: d}
	d.Notify = rec
	d.Requests = a.Friends
	return &env{t: t, a: a, d: d, f: a.Friends, w: wiretest.World(t, a), rec: rec, ctx: context.Background()}
}

func (e *env) as(id core.UserID) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: id, Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

// ref is viewer v's ref for u.
func (e *env) ref(v, u core.UserID) gate.Ref { return e.d.Refs.User(v, u) }

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

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

// befriend makes a and b friends through the real request/accept path.
func (e *env) befriend(a, b core.UserID) {
	e.t.Helper()
	e.must(e.f.Request(e.as(a), e.ref(a, b)))
	e.must(e.f.Accept(e.as(b), e.ref(b, a)))
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func wantProblem(t *testing.T, err error, code string) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("err = %v, want Problem %s", err, code)
	}
}

func js(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Acceptance: request → accept creates exactly one Friendship record and two
// edges rows, updates graph memory and notifies both steps.
func TestRequestAccept(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.w.User("alice"), e.w.User("bob"), e.w.User("carol")
	e.w.Friends("alice", "carol")
	e.w.Friends("carol", "bob")

	e.must(e.f.Request(e.as(alice), e.ref(alice, bob)))
	if got := e.f.RequestState(e.ctx, alice, bob); got != "outgoing" {
		t.Fatalf("alice sees %q", got)
	}
	if got := e.f.RequestState(e.ctx, bob, alice); got != "incoming" {
		t.Fatalf("bob sees %q", got)
	}
	if evs := e.rec.For(bob); len(evs) != 1 || evs[0].Kind != "friend_request" || evs[0].SubjectUser != alice {
		t.Fatalf("bob notifications %+v", evs)
	}
	in, out, err := e.f.Requests(e.as(bob))
	e.must(err)
	if len(in) != 1 || len(out) != 0 || in[0].Handle != "alice" || in[0].Request != "incoming" {
		t.Fatalf("bob requests in=%+v out=%+v", in, out)
	}
	// Asking again is idempotent.
	e.must(e.f.Request(e.as(alice), e.ref(alice, bob)))

	e.must(e.f.Accept(e.as(bob), e.ref(bob, alice)))
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecFriendship)); n != 1 {
		t.Fatalf("friendship records = %d", n)
	}
	if n := e.count("SELECT count(*) FROM edges WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)",
		int64(alice), int64(bob)); n != 2 {
		t.Fatalf("edges rows = %d", n)
	}
	if n := e.count("SELECT count(*) FROM edge_events WHERE kind = 0"); n != 1 {
		t.Fatalf("edge events = %d", n)
	}
	if n := e.count("SELECT count(*) FROM record_parties rp JOIN signed_records r ON r.hash = rp.record_hash WHERE r.kind = $1",
		int16(core.RecFriendship)); n != 2 {
		t.Fatalf("friendship parties = %d", n)
	}
	if bad, err := e.a.Records.AuditEdges(e.ctx); err != nil || bad != 4 {
		// The two World edges (4 rows) have no record; the accepted edge must audit clean.
		t.Fatalf("audit bad = %d (%v), want only the 4 fixture rows", bad, err)
	}
	if _, ok := e.d.Graph.Level(alice, bob); !ok {
		t.Fatal("graph memory has no edge")
	}
	if evs := e.rec.For(alice); len(evs) != 1 || evs[0].Kind != "request_accepted" || evs[0].SubjectUser != bob {
		t.Fatalf("alice notifications %+v", evs)
	}
	if st := e.f.RequestState(e.ctx, alice, bob); st != "" {
		t.Fatalf("state after accept %q", st)
	}
	wantProblem(t, e.f.Request(e.as(alice), e.ref(alice, bob)), "already_friends")
	wantNotFound(t, e.f.Accept(e.as(bob), e.ref(bob, alice)))

	page, err := e.f.List(e.as(alice), "")
	e.must(err)
	if len(page.Items) != 2 || page.Next != "" {
		t.Fatalf("alice friends %+v", page)
	}
	_ = carol
}

// Asking someone whose request to you is pending accepts it.
func TestCrossedRequestsAccept(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	e.w.User("carol")
	e.w.Friends("alice", "carol")
	e.w.Friends("carol", "bob")
	e.must(e.f.Request(e.as(alice), e.ref(alice, bob)))
	e.must(e.f.Request(e.as(bob), e.ref(bob, alice)))
	if _, ok := e.d.Graph.Level(alice, bob); !ok {
		t.Fatal("crossed requests did not make friends")
	}
}

// Acceptance: the requester's view is byte-identical for declined vs pending
// vs blocked-by-target, and the blocker is never notified.
func TestRequesterViewUniform(t *testing.T) {
	views := map[string]string{}
	for _, mode := range []string{"pending", "declined", "blocked"} {
		e := newEnv(t)
		alice, bob := e.w.User("alice"), e.w.User("bob")
		e.w.User("carol")
		e.w.Friends("alice", "carol")
		e.w.Friends("carol", "bob")
		if mode == "blocked" {
			e.must(e.f.Block(e.as(bob), e.ref(bob, alice)))
		}
		err := e.f.Request(e.as(alice), e.ref(alice, bob))
		e.must(err)
		if mode == "declined" {
			e.must(e.f.Decline(e.as(bob), e.ref(bob, alice)))
		}
		in, out, err := e.f.Requests(e.as(alice))
		e.must(err)
		p, err := e.f.Person(e.as(alice), e.ref(alice, bob))
		e.must(err)
		views[mode] = js(t, map[string]any{"err": err, "in": in, "out": out, "person": p,
			"state": e.f.RequestState(e.ctx, alice, bob)})
		if p.Request != "outgoing" {
			t.Fatalf("%s: person request %q", mode, p.Request)
		}
		switch mode {
		case "blocked":
			if evs := e.rec.For(bob); len(evs) != 0 {
				t.Fatalf("blocker notified: %+v", evs)
			}
			in, _, err := e.f.Requests(e.as(bob))
			e.must(err)
			if len(in) != 0 {
				t.Fatalf("blocker sees request %+v", in)
			}
		case "declined":
			in, _, err := e.f.Requests(e.as(bob))
			e.must(err)
			if len(in) != 0 {
				t.Fatalf("declined still incoming %+v", in)
			}
		}
	}
	if views["pending"] != views["declined"] || views["pending"] != views["blocked"] {
		t.Fatalf("requester views differ:\npending  %s\ndeclined %s\nblocked  %s", views["pending"], views["declined"], views["blocked"])
	}
}

// A blocked user's later requests and accept attempts never reach the blocker.
func TestBlockedUserNeverNotifies(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	e.w.User("carol")
	e.w.Friends("alice", "carol")
	e.w.Friends("carol", "bob")
	e.must(e.f.Request(e.as(bob), e.ref(bob, alice))) // bob → alice pending
	e.must(e.f.Block(e.as(alice), e.ref(alice, bob)))
	// bob's request now looks pending to bob, is gone for alice.
	if st := e.f.RequestState(e.ctx, bob, alice); st != "outgoing" {
		t.Fatalf("bob sees %q", st)
	}
	e.must(e.f.Withdraw(e.as(bob), e.ref(bob, alice)))
	e.must(e.f.Request(e.as(bob), e.ref(bob, alice)))
	if n := len(e.rec.For(alice)); n != 1 { // only the request made before the block
		t.Fatalf("alice notifications = %d", n)
	}
	// Alice cannot accept a request that was declined by the block.
	wantNotFound(t, e.f.Accept(e.as(alice), e.ref(alice, bob)))
}

// insertUsers bulk-creates n active users and returns their IDs.
func (e *env) insertUsers(prefix string, n int) []core.UserID {
	e.t.Helper()
	base := bulkBase + int64(e.bulk)
	e.bulk += n
	rows, err := e.d.DB.Q().Query(e.ctx, `INSERT INTO users (id, ext_id, handle, handle_canon, is_genesis, status, created_at, webauthn_user_id)
		SELECT $4::bigint + i, gen_random_uuid(), 'u_' || $1 || i, 'u_' || $1 || i, true, 0, $3, decode(md5($1 || i), 'hex')
		FROM generate_series(1, $2) i RETURNING id`, prefix, n, e.d.Clock.Now(), base)
	e.must(err)
	var out []core.UserID
	for rows.Next() {
		var id int64
		e.must(rows.Scan(&id))
		out = append(out, core.UserID(id))
	}
	e.must(rows.Err())
	e.must(e.a.Users.Reload(e.ctx))
	return out
}

// edge writes a fixture edge a–b (no record) to the database and memory.
func (e *env) edge(a, b core.UserID) {
	e.t.Helper()
	e.exec("INSERT INTO edges (user_id, friend_id, level, since, record_hash) VALUES ($1, $2, 1, $3, '\\x00'), ($2, $1, 1, $3, '\\x00')",
		int64(a), int64(b), e.d.Clock.Now())
	e.d.Graph.AddEdge(a, b, core.LevelFriend, e.d.Clock.Now())
}

// fill gives u n outgoing pending requests to fresh users.
func (e *env) fill(u core.UserID, prefix string, n int) {
	e.t.Helper()
	ids := e.insertUsers(prefix, n)
	for _, id := range ids {
		e.exec("INSERT INTO friend_requests (from_id, to_id, state, created_at) VALUES ($1, $2, 0, $3)",
			int64(u), int64(id), e.d.Clock.Now().Add(-48*time.Hour))
	}
}

// Acceptance: accept never fails because of the requester's count; friend_cap
// comes only from the caller's own count.
func TestFriendCapOwnCountOnly(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	e.w.User("carol")
	e.w.Friends("alice", "carol")
	e.w.Friends("carol", "bob")

	// alice is one below the cap; her request to bob uses her last slot.
	e.fill(alice, "fa", friends.FriendCap-2) // + carol edge = 999
	e.must(e.f.Request(e.as(alice), e.ref(alice, bob)))
	if ed, p, _ := e.f.Count(e.ctx, alice); ed+p != friends.FriendCap {
		t.Fatalf("alice count = %d", ed+p)
	}
	// bob accepts although alice is at the cap.
	e.must(e.f.Accept(e.as(bob), e.ref(bob, alice)))

	// A full caller cannot ask, and a full target does not stop anyone asking.
	dave := e.w.User("dave")
	e.w.Friends("dave", "carol")
	wantProblem(t, e.f.Request(e.as(alice), e.ref(alice, dave)), "friend_cap")
	e.must(e.f.Request(e.as(dave), e.ref(dave, alice)))
	// alice is full, so she cannot accept either (her own count).
	wantProblem(t, e.f.Accept(e.as(alice), e.ref(alice, dave)), "friend_cap")
}

// Requests per day: 20, or 5 on probation; rate limited after that.
func TestDailyRequestLimit(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	hub := e.w.User("hub")
	e.w.Friends("alice", "hub")
	others := e.insertUsers("o", friends.RequestsPerDay+1)
	for _, o := range others {
		e.edge(hub, o)
	}
	for i := 0; i < friends.RequestsPerDay; i++ {
		e.must(e.f.Request(e.as(alice), e.ref(alice, others[i])))
	}
	err := e.f.Request(e.as(alice), e.ref(alice, others[friends.RequestsPerDay]))
	if !errors.Is(err, gate.ErrRateLimited) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	e.advance(24*time.Hour + time.Second)
	e.must(e.f.Request(e.as(alice), e.ref(alice, others[friends.RequestsPerDay])))

	// On probation the limit is 5.
	p := e.w.User("pia")
	e.exec("UPDATE users SET is_genesis = false, invited_by = $3, probation_until = $2 WHERE id = $1", int64(p), e.d.Clock.Now().Add(7*24*time.Hour), int64(hub))
	e.a.Users.Invalidate(p)
	e.w.Friends("pia", "hub")
	for i := 0; i < friends.RequestsProbation; i++ {
		e.must(e.f.Request(e.as(p), e.ref(p, others[i])))
	}
	if err := e.f.Request(e.as(p), e.ref(p, others[friends.RequestsProbation])); !errors.Is(err, gate.ErrRateLimited) {
		t.Fatalf("probation err = %v", err)
	}
}

// Acceptance: unfriend keeps the invites row and the label; 30 days later the
// Friendship and Revocation records are gone.
func TestUnfriendRetention(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	e.w.User("carol")
	e.w.Friends("alice", "carol")
	bob := e.insertUsers("bob", 1)[0]
	e.exec("UPDATE users SET is_genesis = false, invited_by = $2 WHERE id = $1", int64(bob), int64(alice))
	e.must(e.a.Users.Reload(e.ctx))
	e.exec(`INSERT INTO invites (id, inviter_id, invitee_id, created_at, handshake, invite_record, friendship_record)
		VALUES (gen_random_uuid(), $1, $2, $3, '{}', '\x00', '\x00')`, int64(alice), int64(bob), e.d.Clock.Now())
	e.edge(e.w.ID("carol"), bob)
	e.befriend(alice, bob)

	e.must(e.f.Unfriend(e.as(alice), e.ref(alice, bob)))
	if _, ok := e.d.Graph.Level(alice, bob); ok {
		t.Fatal("edge still in memory")
	}
	if n := e.count("SELECT count(*) FROM edges WHERE user_id = $1 AND friend_id = $2", int64(alice), int64(bob)); n != 0 {
		t.Fatalf("edges = %d", n)
	}
	if n := e.count("SELECT count(*) FROM invites WHERE invitee_id = $1", int64(bob)); n != 1 {
		t.Fatalf("invites = %d", n)
	}
	// Still connected through carol: alice sees bob's inviter label (herself).
	p, err := e.f.Person(e.as(alice), e.ref(alice, bob))
	e.must(err)
	if p.Inviter.Kind != "user" || p.Inviter.Handle != "alice" || p.Relation != "connected" {
		t.Fatalf("person %+v", p)
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind IN (2, 3) AND purge_after IS NOT NULL"); n != 2 {
		t.Fatalf("records scheduled for purge = %d", n)
	}
	wantNotFound(t, e.f.Unfriend(e.as(alice), e.ref(alice, bob)))

	e.advance(friends.Retention + time.Minute)
	if _, err := e.a.Records.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind IN (2, 3)"); n != 0 {
		t.Fatalf("friendship/revocation records left = %d", n)
	}
	if _, err := e.f.PurgeEdgeEvents(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM edge_events WHERE kind = 2"); n != 0 {
		t.Fatalf("revoke events left = %d", n)
	}
	if n := e.count("SELECT count(*) FROM edge_events WHERE kind = 0"); n != 1 {
		t.Fatalf("create events = %d (kept)", n)
	}
}

// Block unfriends, cuts paths, is listed for the blocker only, and unblock
// deletes the Block record.
func TestBlockUnblock(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.w.User("alice"), e.w.User("bob"), e.w.User("carol")
	e.w.User("dan")
	e.w.Friends("bob", "carol")
	e.w.Friends("alice", "dan")
	e.w.Friends("dan", "bob")
	e.befriend(alice, bob)

	e.must(e.f.Block(e.as(alice), e.ref(alice, bob)))
	e.must(e.f.Block(e.as(alice), e.ref(alice, bob))) // idempotent
	if _, ok := e.d.Graph.Level(alice, bob); ok {
		t.Fatal("block did not unfriend")
	}
	if !e.d.Graph.Blocks(alice, bob) {
		t.Fatal("graph block missing")
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecBlock)); n != 1 {
		t.Fatalf("block records = %d", n)
	}
	// carol is only reachable through bob: alice no longer sees her.
	wantNotFound(t, e.f.Mute(e.as(alice), e.ref(alice, carol), true))
	bl, err := e.f.Blocks(e.as(alice))
	e.must(err)
	if len(bl) != 1 || bl[0].Handle != "bob" || !bl[0].Blocked || bl[0].Ref != e.ref(alice, bob) {
		t.Fatalf("blocks %+v", bl)
	}
	p, err := e.f.Person(e.as(alice), e.ref(alice, bob))
	e.must(err)
	if !p.Blocked {
		t.Fatalf("blocker's person view %+v", p)
	}
	// The blocked person sees nothing of it.
	pb, err := e.f.Person(e.as(bob), e.ref(bob, alice))
	if err == nil && pb.Blocked {
		t.Fatal("block revealed to the blocked person")
	}
	bbl, err := e.f.Blocks(e.as(bob))
	e.must(err)
	if len(bbl) != 0 {
		t.Fatalf("bob blocks %+v", bbl)
	}

	e.must(e.f.Unblock(e.as(alice), e.ref(alice, bob)))
	if e.d.Graph.Blocks(alice, bob) {
		t.Fatal("graph block left")
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecBlock)); n != 0 {
		t.Fatalf("block records after unblock = %d", n)
	}
	wantNotFound(t, e.f.Unblock(e.as(alice), e.ref(alice, bob)))
	wantProblem(t, e.f.Block(e.as(alice), e.ref(alice, alice)), "self_not_allowed")
}

func TestMute(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	e.w.Friends("alice", "bob")
	e.must(e.f.Mute(e.as(alice), e.ref(alice, bob), true))
	if _, ok := e.f.MutedAuthors(e.ctx, alice)[bob]; !ok {
		t.Fatal("not muted")
	}
	p, err := e.f.Person(e.as(alice), e.ref(alice, bob))
	e.must(err)
	if !p.Muted || p.Relation != "friend" {
		t.Fatalf("person %+v", p)
	}
	e.must(e.f.Mute(e.as(alice), e.ref(alice, bob), false))
	if len(e.f.MutedAuthors(e.ctx, alice)) != 0 {
		t.Fatal("still muted")
	}
}

// Acceptance: suggestions never include people with < 2 mutual friends,
// opted-out people or people the viewer blocked; mutual friends count only
// through edges at least 7 days old.
func TestSuggestions(t *testing.T) {
	e := newEnv(t)
	v := e.w.User("viewer")
	for _, h := range []string{"fr1", "fr2", "fr3", "two", "one", "optout", "blocked", "young"} {
		e.w.User(h)
	}
	for _, f := range []string{"fr1", "fr2", "fr3"} {
		e.w.Friends("viewer", f)
	}
	e.w.Friends("fr1", "two")
	e.w.Friends("fr2", "two")
	e.w.Friends("fr1", "one")
	e.w.Friends("fr1", "optout")
	e.w.Friends("fr2", "optout")
	e.w.Friends("fr1", "blocked")
	e.w.Friends("fr2", "blocked")
	e.w.Friends("fr3", "blocked")
	e.exec("UPDATE users SET suggestible = false WHERE id = $1", int64(e.w.ID("optout")))
	e.a.Users.Invalidate(e.w.ID("optout"))
	e.advance(8 * 24 * time.Hour)
	// young: two mutual friends, but through brand-new edges.
	now := e.d.Clock.Now()
	e.d.Graph.AddEdge(e.w.ID("fr1"), e.w.ID("young"), core.LevelFriend, now)
	e.d.Graph.AddEdge(e.w.ID("fr2"), e.w.ID("young"), core.LevelFriend, now)
	e.must(e.f.Block(e.as(v), e.ref(v, e.w.ID("blocked"))))

	got, err := e.f.Suggestions(e.as(v))
	e.must(err)
	if len(got) != 1 || got[0].Person.Handle != "two" || got[0].Mutual != 2 {
		t.Fatalf("suggestions %s", js(t, got))
	}
}

// Acceptance: a person ref for someone in another component is the uniform
// not found, like a garbage ref.
func TestPersonOtherComponent(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	x, y := e.w.User("xavier"), e.w.User("yara")
	e.w.Friends("alice", "bob")
	e.w.Friends("xavier", "yara")
	ctx := e.as(alice)
	_, err := e.f.Person(ctx, e.ref(alice, x))
	wantNotFound(t, err)
	_, err = e.f.Person(ctx, "garbage")
	wantNotFound(t, err)
	_, err = e.f.Person(ctx, e.ref(bob, alice)) // another viewer's ref
	wantNotFound(t, err)
	wantNotFound(t, e.f.Request(ctx, e.ref(alice, x)))
	wantNotFound(t, e.f.Block(ctx, e.ref(alice, y)))
	wantNotFound(t, e.f.Mute(ctx, e.ref(alice, y), true))
	_, err = e.f.PersonPosts(ctx, e.ref(alice, x), "")
	wantNotFound(t, err)
	p, err := e.f.Person(ctx, e.ref(alice, bob))
	e.must(err)
	if p.Relation != "friend" {
		t.Fatalf("person %+v", p)
	}
	// Unauthenticated calls are not found too.
	_, err = e.f.Person(e.ctx, e.ref(alice, bob))
	wantNotFound(t, err)
}

func TestFriendsListPaging(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	ids := e.insertUsers("pia", friends.FriendsPageSize+5)
	for _, id := range ids {
		e.edge(alice, id)
	}
	p1, err := e.f.List(e.as(alice), "")
	e.must(err)
	if len(p1.Items) != friends.FriendsPageSize || p1.Next == "" {
		t.Fatalf("page 1: %d items, next %q", len(p1.Items), p1.Next)
	}
	p2, err := e.f.List(e.as(alice), p1.Next)
	e.must(err)
	if len(p2.Items) != 5 || p2.Next != "" {
		t.Fatalf("page 2: %d items, next %q", len(p2.Items), p2.Next)
	}
	// Another viewer's cursor fails.
	_, err = e.f.List(e.as(ids[0]), p1.Next)
	wantNotFound(t, err)
}

func TestRequestsScrub(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	e.w.User("carol")
	e.w.Friends("alice", "carol")
	e.w.Friends("carol", "bob")
	e.must(e.f.Request(e.as(alice), e.ref(alice, bob)))
	e.must(e.f.Decline(e.as(bob), e.ref(bob, alice)))
	if n, err := e.f.ScrubRequests(e.ctx); err != nil || n != 0 {
		t.Fatalf("early scrub %d %v", n, err)
	}
	e.advance(friends.Retention + time.Minute)
	if n, err := e.f.ScrubRequests(e.ctx); err != nil || n != 1 {
		t.Fatalf("scrub %d %v", n, err)
	}
	if n := e.count("SELECT count(*) FROM friend_requests WHERE created_at IS NULL AND decided_at IS NULL AND state = 1"); n != 1 {
		t.Fatalf("scrubbed rows = %d", n)
	}
	// Still looks pending to alice.
	if st := e.f.RequestState(e.ctx, alice, bob); st != "outgoing" {
		t.Fatalf("alice sees %q", st)
	}
}

func TestAccountDeletedCleansRows(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.w.User("alice"), e.w.User("bob")
	carol := e.w.User("carol")
	e.w.Friends("alice", "carol")
	e.w.Friends("carol", "bob")
	e.must(e.f.Request(e.as(alice), e.ref(alice, bob)))
	e.must(e.f.Mute(e.as(carol), e.ref(carol, alice), true))
	e.must(e.f.Block(e.as(bob), e.ref(bob, carol)))
	e.d.Bus.Publish(core.AccountDeleted{ID: bob})
	for _, q := range []string{
		"SELECT count(*) FROM friend_requests WHERE to_id = $1 OR from_id = $1",
		"SELECT count(*) FROM blocks WHERE blocker = $1 OR blocked = $1",
	} {
		if n := e.count(q, int64(bob)); n != 0 {
			t.Fatalf("%s = %d", q, n)
		}
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecBlock)); n != 0 {
		t.Fatalf("block records = %d", n)
	}
	if n := e.count("SELECT count(*) FROM mutes"); n != 1 {
		t.Fatalf("unrelated mutes = %d", n)
	}
}
