package dm_test

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/dm"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/notify"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush/webpushtest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	s   *dm.Service
	w   *wiretest.TestWorld
	ctx context.Context
}

// newEnv: alice – bob – carol – dave (a chain), alice – erin, xavier alone in
// another component.
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	e := &env{t: t, a: a, d: a.Deps(), s: a.DM, w: wiretest.World(t, a), ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "erin", "xavier"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.FriendsPlus("bob", "carol")
	e.w.Friends("carol", "dave")
	e.w.Friends("alice", "erin")
	if err := a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

func (e *env) as(h string) context.Context {
	var s [32]byte
	_, _ = rand.Read(s[:])
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, Session: s, AuthAt: e.d.Clock.Now()})
}

func (e *env) person(viewer, who string) gate.Ref { return e.d.Refs.User(e.id(viewer), e.id(who)) }

func (e *env) post(viewer, label string) gate.Ref { return e.d.Refs.Post(e.id(viewer), e.w.PostID(label)) }

// conv translates a conversation ref minted for one viewer into another's.
func (e *env) conv(from gate.Ref, fromViewer, to string) gate.Ref {
	e.t.Helper()
	id, ok := e.d.Refs.OpenConv(e.id(fromViewer), from)
	if !ok {
		e.t.Fatal("conversation ref does not open")
	}
	return e.d.Refs.Conv(e.id(to), id)
}

func (e *env) direct(from, to string) gate.ConversationView {
	e.t.Helper()
	c, err := e.s.Direct(e.as(from), e.person(from, to))
	if err != nil {
		e.t.Fatalf("Direct(%s → %s): %v", from, to, err)
	}
	return c
}

func (e *env) send(h string, c gate.Ref, body string) gate.MessageView {
	e.t.Helper()
	m, err := e.s.Send(e.as(h), c, dm.Draft{Body: body})
	if err != nil {
		e.t.Fatalf("Send(%s, %q): %v", h, body, err)
	}
	return m
}

func (e *env) messages(h string, c gate.Ref) []gate.MessageView {
	e.t.Helper()
	p, err := e.s.Messages(e.as(h), c, "")
	if err != nil {
		e.t.Fatalf("Messages(%s): %v", h, err)
	}
	return p.Items
}

func (e *env) unread(h string) int {
	e.t.Helper()
	n, err := e.s.Unread(e.as(h))
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) list(h string) []gate.ConversationView {
	e.t.Helper()
	p, err := e.s.List(e.as(h), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Items
}

func problem(t *testing.T, err error, code string) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("err = %v, want problem %s", err, code)
	}
}

func notFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("err = %v, want not_found", err)
	}
}

func bodies(ms []gate.MessageView) []string {
	var out []string
	for _, m := range ms {
		switch m.Kind {
		case "event":
			out = append(out, "<"+m.Event+">")
		case "deleted":
			out = append(out, "<deleted>")
		default:
			out = append(out, m.Body)
		}
	}
	return out
}

func TestDirectConversationRoundTrip(t *testing.T) {
	e := newEnv(t)
	c := e.direct("alice", "bob")
	if c.Kind != "direct" || len(c.Members) != 1 || c.Members[0].Handle != "bob" || c.Members[0].Relation != "friend" || !c.CanSend {
		t.Fatalf("conversation = %+v", c)
	}
	if again := e.direct("alice", "bob"); again.Ref != c.Ref {
		t.Fatal("a second Direct must open the same conversation")
	}
	m := e.send("alice", c.Ref, "  hi bob\n\n")
	if !m.Mine || m.Body != "  hi bob" || m.From != nil {
		t.Fatalf("own message = %+v", m)
	}
	if n := e.unread("bob"); n != 1 {
		t.Fatalf("bob unread = %d, want 1", n)
	}
	if n := e.unread("alice"); n != 0 {
		t.Fatalf("the sender's own message is unread for them: %d", n)
	}
	bc := e.conv(c.Ref, "alice", "bob")
	ls := e.list("bob")
	if len(ls) != 1 || ls[0].Ref != bc || ls[0].Unread != 1 || ls[0].Last == nil || ls[0].Last.Excerpt != "hi bob" || ls[0].Last.From != "bob" && ls[0].Last.From != "alice" {
		t.Fatalf("bob's list = %+v", ls)
	}
	got := e.messages("bob", bc)
	if len(got) != 1 || got[0].Mine || got[0].From == nil || got[0].From.Handle != "alice" {
		t.Fatalf("bob's messages = %+v", got)
	}
	if err := e.s.MarkRead(e.as("bob"), bc, got[0].Ref); err != nil {
		t.Fatal(err)
	}
	if n := e.unread("bob"); n != 0 {
		t.Fatalf("after read, unread = %d", n)
	}
	// Refs are per viewer: alice's conversation ref does not open for carol.
	_, err := e.s.Messages(e.as("carol"), c.Ref, "")
	notFound(t, err)
	_, err = e.s.Messages(e.as("carol"), e.conv(c.Ref, "alice", "carol"), "")
	notFound(t, err)
}

func TestWhoCanStartADirectConversation(t *testing.T) {
	e := newEnv(t)
	// carol is two steps from alice: visible, not connected.
	_, err := e.s.Direct(e.as("alice"), e.person("alice", "carol"))
	problem(t, err, "not_connected")
	// xavier is in another component: the uniform not-found.
	_, err = e.s.Direct(e.as("alice"), e.person("alice", "xavier"))
	notFound(t, err)
	_, err = e.s.Direct(e.as("alice"), e.person("alice", "alice"))
	problem(t, err, "recipient_self")
	_, err = e.s.Direct(e.as("alice"), "garbage")
	notFound(t, err)
	// carol and bob are friends (friend+): the relation says so.
	if c := e.direct("carol", "bob"); c.Members[0].Relation != "friend_plus" {
		t.Fatalf("relation = %q", c.Members[0].Relation)
	}
}

func TestUnfriendStopsSendingKeepsHistory(t *testing.T) {
	e := newEnv(t)
	c := e.direct("alice", "bob")
	e.send("alice", c.Ref, "before")
	e.d.Graph.RemoveEdge(e.id("alice"), e.id("bob"))
	_, err := e.s.Send(e.as("bob"), e.conv(c.Ref, "alice", "bob"), dm.Draft{Body: "after"})
	problem(t, err, "not_connected")
	_, err = e.s.Send(e.as("alice"), c.Ref, dm.Draft{Body: "after"})
	problem(t, err, "not_connected")
	if got := bodies(e.messages("bob", e.conv(c.Ref, "alice", "bob"))); len(got) != 1 || got[0] != "before" {
		t.Fatalf("history = %v", got)
	}
	cv, err := e.s.Get(e.as("bob"), e.conv(c.Ref, "alice", "bob"))
	if err != nil || cv.CanSend || len(cv.Members) != 1 || cv.Members[0].Relation != "" {
		t.Fatalf("view after unfriend = %+v, %v", cv, err)
	}
	// A block revokes the edge too: the same answer, so it is never revealed.
	e.d.Graph.AddEdge(e.id("alice"), e.id("bob"), core.LevelFriend, time.Now())
	e.send("bob", e.conv(c.Ref, "alice", "bob"), "ok again")
}

func TestBodyRules(t *testing.T) {
	e := newEnv(t)
	c := e.direct("alice", "bob")
	_, err := e.s.Send(e.as("alice"), c.Ref, dm.Draft{Body: " \n "})
	problem(t, err, "body_empty")
	long := make([]rune, dm.MaxBody+1)
	for i := range long {
		long[i] = 'x'
	}
	_, err = e.s.Send(e.as("alice"), c.Ref, dm.Draft{Body: string(long)})
	problem(t, err, "body_too_long")
	_, err = e.s.Send(e.as("alice"), c.Ref, dm.Draft{Body: string(long[:dm.MaxBody])})
	if err != nil {
		t.Fatal(err)
	}
}

// A shared post renders only for recipients who could already see it.
func TestSharedPostRespectsEachViewersHorizon(t *testing.T) {
	e := newEnv(t)
	e.w.Post("carol", "open", "carol for everyone")
	e.w.Post("carol", "close", "carol for friends only", wiretestAudience(core.AudD1))
	e.w.Post("xavier", "far", "xavier far away")
	bob := e.direct("bob", "alice")
	// bob sees both of carol's posts; alice (two steps away) only the open one.
	if _, err := e.s.Send(e.as("bob"), bob.Ref, dm.Draft{Post: e.post("bob", "open")}); err != nil {
		t.Fatal(err)
	}
	mine, err := e.s.Send(e.as("bob"), bob.Ref, dm.Draft{Body: "look", Post: e.post("bob", "close")})
	if err != nil {
		t.Fatal(err)
	}
	if mine.Post == nil || mine.Post.Post == nil || mine.Post.Post.Body != "carol for friends only" {
		t.Fatalf("the sender sees the post they shared: %+v", mine.Post)
	}
	got := e.messages("alice", e.conv(bob.Ref, "bob", "alice"))
	if len(got) != 2 {
		t.Fatalf("alice sees %d messages", len(got))
	}
	closed, open := got[0], got[1]
	if closed.Post == nil || !closed.Post.Unavailable || closed.Post.Post != nil || closed.Body != "look" {
		t.Fatalf("a post alice cannot see leaked: %+v", closed.Post)
	}
	if open.Post == nil || open.Post.Post == nil || open.Post.Post.Body != "carol for everyone" || open.Post.Unavailable {
		t.Fatalf("a post alice can see is missing: %+v", open.Post)
	}
	ls := e.list("alice")
	if ls[0].Last == nil || ls[0].Last.Kind != "post" || ls[0].Last.Excerpt != "look" {
		t.Fatalf("the list must not excerpt a shared post: %+v", ls[0].Last)
	}
	// bob cannot share what he cannot see (xavier's post, garbage).
	_, err = e.s.Send(e.as("bob"), bob.Ref, dm.Draft{Post: e.post("bob", "far")})
	notFound(t, err)
	_, err = e.s.Send(e.as("bob"), bob.Ref, dm.Draft{Post: "nope"})
	notFound(t, err)
	// Once carol deletes the open post, nobody sees it.
	m := e.d.Posts.(interface {
		Delete(core.PostID)
	})
	m.Delete(e.w.PostID("open"))
	got = e.messages("alice", e.conv(bob.Ref, "bob", "alice"))
	if !got[1].Post.Unavailable {
		t.Fatalf("a deleted post still renders: %+v", got[1].Post)
	}
}

func wiretestAudience(a core.Audience) func(*core.PostMeta, *[]core.LinkMeta) {
	return func(m *core.PostMeta, _ *[]core.LinkMeta) { m.Audience = a }
}

func TestGroupInvitationsAndMembership(t *testing.T) {
	e := newEnv(t)
	// alice is connected to bob and erin, not to carol.
	_, err := e.s.CreateGroup(e.as("alice"), "trip", []gate.Ref{e.person("alice", "carol")})
	problem(t, err, "not_connected")
	_, err = e.s.CreateGroup(e.as("alice"), "trip", nil)
	problem(t, err, "members_empty")
	g, err := e.s.CreateGroup(e.as("alice"), "trip", []gate.Ref{e.person("alice", "bob"), e.person("alice", "erin")})
	if err != nil {
		t.Fatal(err)
	}
	if g.Kind != "group" || g.Title != "trip" || !g.Admin || g.MemberCount != 1 || len(g.Members) != 0 {
		t.Fatalf("new group = %+v", g)
	}
	// bob is invited: he sees who invited him, the title and the count, no
	// members and no messages.
	bg := e.conv(g.Ref, "alice", "bob")
	inv, err := e.s.Get(e.as("bob"), bg)
	if err != nil || inv.State != "invited" || inv.InvitedBy == nil || inv.InvitedBy.Handle != "alice" || len(inv.Members) != 0 ||
		inv.MemberCount != 1 || inv.Title != "trip" {
		t.Fatalf("invitation = %+v, %v", inv, err)
	}
	_, err = e.s.Messages(e.as("bob"), bg, "")
	problem(t, err, "not_joined")
	_, err = e.s.Send(e.as("bob"), bg, dm.Draft{Body: "hi"})
	problem(t, err, "not_joined")
	if n := e.unread("bob"); n != 1 {
		t.Fatalf("an invitation counts as unread: %d", n)
	}
	e.send("alice", g.Ref, "before bob joined")
	if _, err := e.s.Join(e.as("bob"), bg); err != nil {
		t.Fatal(err)
	}
	// bob now invites carol, his own friend (alice and carol are not connected).
	if _, err := e.s.Invite(e.as("bob"), bg, []gate.Ref{e.person("bob", "carol")}); err != nil {
		t.Fatal(err)
	}
	cg := e.conv(g.Ref, "alice", "carol")
	if _, err := e.s.Join(e.as("carol"), cg); err != nil {
		t.Fatal(err)
	}
	e.send("carol", cg, "hello all")
	// alice sees carol (not her connection) as a member, with no relation.
	ag, err := e.s.Get(e.as("alice"), g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	handles := map[string]string{}
	for _, m := range ag.Members {
		handles[m.Handle] = m.Relation
	}
	if len(ag.Members) != 2 || handles["bob"] != "friend" || handles["carol"] != "" {
		t.Fatalf("alice's members = %+v (erin is still only invited)", ag.Members)
	}
	got := bodies(e.messages("alice", g.Ref))
	want := []string{"hello all", "<joined>", "<joined>", "before bob joined", "<created>"}
	if len(got) != len(want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("messages = %v, want %v", got, want)
		}
	}
	// Only admins remove; nobody removes themselves.
	err = e.s.Remove(e.as("bob"), bg, e.person("bob", "carol"))
	problem(t, err, "not_admin")
	err = e.s.Remove(e.as("alice"), g.Ref, e.person("alice", "alice"))
	problem(t, err, "remove_self")
	if err := e.s.Remove(e.as("alice"), g.Ref, e.d.Refs.User(e.id("alice"), e.id("carol"))); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.Messages(e.as("carol"), cg, "")
	notFound(t, err)
	// The admin leaves: bob (the earliest remaining member) becomes admin.
	if err := e.s.Leave(e.as("alice"), g.Ref); err != nil {
		t.Fatal(err)
	}
	if bv, err := e.s.Get(e.as("bob"), bg); err != nil || !bv.Admin {
		t.Fatalf("bob after alice left = %+v, %v", bv, err)
	}
	// The last member leaves: the group (with erin's invitation) is gone.
	if err := e.s.Leave(e.as("bob"), bg); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.Get(e.as("erin"), e.conv(g.Ref, "alice", "erin"))
	notFound(t, err)
}

func TestGroupLimitsAndProbation(t *testing.T) {
	e := newEnv(t)
	if _, err := e.d.DB.Q().Exec(e.ctx, `UPDATE users SET is_genesis = false, invited_by = $2,
		probation_until = now() + interval '30 days' WHERE id = $1`, int64(e.id("erin")), int64(e.id("alice"))); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.CreateGroup(e.as("erin"), "", []gate.Ref{e.person("erin", "alice")})
	problem(t, err, "probation")
	// On probation, 1:1 messages to connections still work.
	c := e.direct("erin", "alice")
	e.send("erin", c.Ref, "hi")
	long := make([]rune, dm.MaxTitle+1)
	for i := range long {
		long[i] = 't'
	}
	_, err = e.s.CreateGroup(e.as("alice"), string(long), []gate.Ref{e.person("alice", "bob")})
	problem(t, err, "title_too_long")
	g, err := e.s.CreateGroup(e.as("alice"), "", []gate.Ref{e.person("alice", "bob")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.s.Rename(e.as("alice"), g.Ref, string(long))
	problem(t, err, "title_too_long")
	r, err := e.s.Rename(e.as("alice"), g.Ref, "new name")
	if err != nil || r.Title != "new name" {
		t.Fatalf("rename = %+v, %v", r, err)
	}
	_, err = e.s.Rename(e.as("erin"), c.Ref, "x")
	problem(t, err, "not_group")
}

// A member's own blocks hide the blocked person's messages in a group; the
// blocked person sees no difference.
func TestBlocksInGroups(t *testing.T) {
	e := newEnv(t)
	g, err := e.s.CreateGroup(e.as("bob"), "", []gate.Ref{e.person("bob", "alice"), e.person("bob", "carol")})
	if err != nil {
		t.Fatal(err)
	}
	ag, cg := e.conv(g.Ref, "bob", "alice"), e.conv(g.Ref, "bob", "carol")
	for h, r := range map[string]gate.Ref{"alice": ag, "carol": cg} {
		if _, err := e.s.Join(e.as(h), r); err != nil {
			t.Fatal(err)
		}
	}
	e.w.Block("alice", "carol")
	e.send("carol", cg, "from carol")
	e.send("alice", ag, "from alice")
	for _, b := range bodies(e.messages("alice", ag)) {
		if b == "from carol" {
			t.Fatal("alice sees a message from someone she blocked")
		}
	}
	if n := e.unread("alice"); n != 0 {
		t.Fatalf("a blocked person's message counts as unread: %d", n)
	}
	if got := bodies(e.messages("carol", cg)); got[0] != "from alice" {
		t.Fatalf("carol's view changed: %v", got)
	}
}

func TestDeleteMuteAndClear(t *testing.T) {
	e := newEnv(t)
	c := e.direct("alice", "bob")
	bc := e.conv(c.Ref, "alice", "bob")
	m := e.send("alice", c.Ref, "oops")
	// Only the sender deletes; the row reads "deleted" for everyone.
	theirs := e.d.Refs.Message(e.id("bob"), mustOpenMessage(t, e, "alice", m.Ref))
	notFound(t, e.s.Delete(e.as("bob"), bc, theirs))
	if err := e.s.Delete(e.as("alice"), c.Ref, m.Ref); err != nil {
		t.Fatal(err)
	}
	if got := bodies(e.messages("bob", bc)); len(got) != 1 || got[0] != "<deleted>" {
		t.Fatalf("after delete = %v", got)
	}
	var body *string
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT body FROM dm_messages").Scan(&body); err != nil || body != nil {
		t.Fatalf("the body is not erased: %v, %v", body, err)
	}
	// Muted: no badge.
	if err := e.s.Mute(e.as("bob"), bc, true); err != nil {
		t.Fatal(err)
	}
	e.send("alice", c.Ref, "two")
	if n := e.unread("bob"); n != 0 {
		t.Fatalf("muted conversation counts: %d", n)
	}
	if err := e.s.Mute(e.as("bob"), bc, false); err != nil {
		t.Fatal(err)
	}
	if n := e.unread("bob"); n != 1 {
		t.Fatalf("unmuted unread = %d", n)
	}
	// Clearing hides the conversation for bob only, until a new message.
	if err := e.s.Leave(e.as("bob"), bc); err != nil {
		t.Fatal(err)
	}
	if len(e.list("bob")) != 0 || len(e.list("alice")) != 1 || e.unread("bob") != 0 {
		t.Fatal("clear must hide the conversation for bob only")
	}
	e.send("alice", c.Ref, "three")
	if got := bodies(e.messages("bob", bc)); len(got) != 1 || got[0] != "three" {
		t.Fatalf("bob after clearing sees %v", got)
	}
	// Purge erases only what every member cleared.
	if err := e.s.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e, "SELECT count(*) FROM dm_messages"); n != 3 {
		t.Fatalf("purge erased messages alice still has: %d left", n)
	}
	if err := e.s.Leave(e.as("alice"), c.Ref); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e, "SELECT count(*) FROM dm_messages"); n != 1 {
		t.Fatalf("after both cleared, %d messages left (want bob's uncleared one)", n)
	}
}

func mustOpenMessage(t *testing.T, e *env, viewer string, r gate.Ref) [16]byte {
	t.Helper()
	id, ok := e.d.Refs.OpenMessage(e.id(viewer), r)
	if !ok {
		t.Fatal("message ref does not open")
	}
	return id
}

func count(t *testing.T, e *env, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAccountDeletionErasesItsMessages(t *testing.T) {
	e := newEnv(t)
	c := e.direct("alice", "bob")
	e.send("alice", c.Ref, "from alice")
	e.send("bob", e.conv(c.Ref, "alice", "bob"), "from bob")
	g, err := e.s.CreateGroup(e.as("alice"), "", []gate.Ref{e.person("alice", "bob")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Join(e.as("bob"), e.conv(g.Ref, "alice", "bob")); err != nil {
		t.Fatal(err)
	}
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("alice")})
	if n := count(t, e, "SELECT count(*) FROM dm_messages WHERE sender_id = $1 AND kind = 0", int64(e.id("alice"))); n != 0 {
		t.Fatalf("%d messages of the deleted account remain", n)
	}
	if n := count(t, e, "SELECT count(*) FROM dm_members WHERE user_id = $1", int64(e.id("alice"))); n != 0 {
		t.Fatal("memberships of the deleted account remain")
	}
	bg, err := e.s.Get(e.as("bob"), e.conv(g.Ref, "alice", "bob"))
	if err != nil || !bg.Admin {
		t.Fatalf("bob inherits the group: %+v, %v", bg, err)
	}
	bc, err := e.s.Get(e.as("bob"), e.conv(c.Ref, "alice", "bob"))
	if err != nil || bc.CanSend || len(bc.Members) != 0 {
		t.Fatalf("the 1:1 conversation after deletion = %+v, %v", bc, err)
	}
}

// Every member's open stream hears about a new message, with their own
// conversation ref and unread count; nothing for people outside.
func TestLiveDelivery(t *testing.T) {
	e := newEnv(t)
	c := e.direct("alice", "bob")
	ctx, cancel := context.WithCancel(e.as("bob"))
	defer cancel()
	ch, stop, err := e.a.Notify.Stream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cctx, ccancel := context.WithCancel(e.as("carol"))
	defer ccancel()
	carol, cstop, err := e.a.Notify.Stream(cctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cstop()
	<-ch    // hello
	<-carol // hello
	e.send("alice", c.Ref, "ping")
	select {
	case ev := <-ch:
		data, _ := ev.Data.(map[string]any)
		if ev.Type != notify.TopicMessage || data["conversation"] != string(e.conv(c.Ref, "alice", "bob")) || data["unread"] != 1 {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no live event")
	}
	select {
	case ev := <-carol:
		t.Fatalf("carol got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// A push goes out when a conversation turns unread (never the text), not for
// every message of the same unread stretch, and not when muted.
func TestPushOncePerUnreadStretch(t *testing.T) {
	pub, priv, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	a := wiretest.NewApp(t, wiretest.WithEnv("PCC_VAPID_PUBLIC", pub), wiretest.WithEnv("PCC_VAPID_PRIVATE", priv),
		wiretest.WithEnv("PCC_VAPID_SUBJECT", "mailto:ops@pcc.invalid"))
	e := &env{t: t, a: a, d: a.Deps(), s: a.DM, w: wiretest.World(t, a), ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "erin"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("alice", "erin")
	if err := a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	svc := webpushtest.New(t)
	sub := svc.Subscribe()
	in := gate.PushSub{Endpoint: sub.Endpoint}
	in.Keys.P256dh, in.Keys.Auth = sub.P256dh, sub.Auth
	if err := a.Notify.Subscribe(e.as("bob"), in); err != nil {
		t.Fatal(err)
	}
	pushes := func() []webpush.Message {
		t.Helper()
		if err := a.DrainJobs(e.ctx); err != nil {
			t.Fatal(err)
		}
		var out []webpush.Message
		for _, r := range svc.For(sub.Endpoint) {
			m, err := r.Message()
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		return out
	}
	c := e.direct("alice", "bob")
	bc := e.conv(c.Ref, "alice", "bob")
	e.send("alice", c.Ref, "secret words")
	e.send("alice", c.Ref, "more secret words")
	got := pushes()
	if len(got) != 1 || got[0].Title != "New message from @alice" || got[0].URL != "/messages/"+string(bc) {
		t.Fatalf("pushes = %+v", got)
	}
	ms := e.messages("bob", bc)
	if err := e.s.MarkRead(e.as("bob"), bc, ms[0].Ref); err != nil {
		t.Fatal(err)
	}
	e.send("alice", c.Ref, "again")
	if got = pushes(); len(got) != 2 {
		t.Fatalf("after reading, a new message pushes again: %d", len(got))
	}
	if err := e.s.Mute(e.as("bob"), bc, true); err != nil {
		t.Fatal(err)
	}
	ms = e.messages("bob", bc)
	if err := e.s.MarkRead(e.as("bob"), bc, ms[0].Ref); err != nil {
		t.Fatal(err)
	}
	e.send("alice", c.Ref, "muted")
	if got = pushes(); len(got) != 2 {
		t.Fatalf("a muted conversation pushed: %d", len(got))
	}
	if _, err := e.s.CreateGroup(e.as("alice"), "", []gate.Ref{e.person("alice", "bob")}); err != nil {
		t.Fatal(err)
	}
	if got = pushes(); len(got) != 3 || got[2].Title != "@alice invited you to a group chat" {
		t.Fatalf("group invitation push = %+v", got)
	}
}

func (e *env) mod(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, Role: core.RoleModerator, AuthAt: e.d.Clock.Now()})
}

// A reported message reaches moderators with the text the reporter saw (and
// nothing else of the conversation); remove_message erases it for everyone.
func TestReportAndRemoveMessage(t *testing.T) {
	e := newEnv(t)
	e.w.User("moder")
	c := e.direct("alice", "bob")
	bc := e.conv(c.Ref, "alice", "bob")
	mine := e.send("alice", c.Ref, "buy my pills")
	theirs := e.d.Refs.Message(e.id("bob"), mustOpenMessage(t, e, "alice", mine.Ref))
	// Nobody reports their own message, and outsiders' refs drop silently.
	if err := e.a.Moderation.Report(e.as("alice"), "message", mine.Ref, "spam", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Moderation.Report(e.as("carol"), "message", e.d.Refs.Message(e.id("carol"), mustOpenMessage(t, e, "alice", mine.Ref)), "spam", ""); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e, "SELECT count(*) FROM reports"); n != 0 {
		t.Fatalf("%d reports filed by people who may not", n)
	}
	if err := e.a.Moderation.Report(e.as("bob"), "message", theirs, "spam", "again"); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Moderation.Report(e.as("bob"), "message", theirs, "spam", ""); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e, "SELECT count(*) FROM reports WHERE target_kind = 4 AND evidence = 'buy my pills'"); n != 1 {
		t.Fatalf("message reports = %d, want 1", n)
	}
	// The sender deletes it afterwards: the moderator still sees what was reported.
	if err := e.s.Delete(e.as("alice"), c.Ref, mine.Ref); err != nil {
		t.Fatal(err)
	}
	q, err := e.a.Moderation.Queue(e.mod("moder"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Items) != 1 || q.Items[0].Message == nil || q.Items[0].Message.Body != "buy my pills" || !q.Items[0].Message.Removed ||
		q.Items[0].Post != nil || q.Items[0].Subject == nil || q.Items[0].Subject.Handle != "alice" {
		t.Fatalf("queue = %+v", q.Items)
	}
	err = e.a.Moderation.Resolve(e.mod("moder"), q.Items[0].ID, "remove_message", "spam")
	problem(t, err, "message_gone")
	// A live message: removed by the moderator.
	m2 := e.send("alice", c.Ref, "second spam")
	if err := e.a.Moderation.Report(e.as("bob"), "message", e.d.Refs.Message(e.id("bob"), mustOpenMessage(t, e, "alice", m2.Ref)), "spam", ""); err != nil {
		t.Fatal(err)
	}
	q, err = e.a.Moderation.Queue(e.mod("moder"), "")
	if err != nil {
		t.Fatal(err)
	}
	var item gate.ModItem
	for _, it := range q.Items {
		if it.Message != nil && it.Message.Body == "second spam" {
			item = it
		}
	}
	if item.ID == "" || item.Message.Removed {
		t.Fatalf("queue = %+v", q.Items)
	}
	if err := e.a.Moderation.Resolve(e.mod("moder"), item.ID, "remove_message", "spam"); err != nil {
		t.Fatal(err)
	}
	if got := bodies(e.messages("bob", bc)); got[0] != "<deleted>" {
		t.Fatalf("after removal bob sees %v", got)
	}
}
