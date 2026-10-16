package notify_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/notify"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	n   *notify.Service
	w   *wiretest.TestWorld
	ctx context.Context
}

// newEnv: alice – bob – carol – dave (a chain), xavier alone in another component.
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	e := &env{t: t, a: a, d: a.Deps(), n: a.Notify, w: wiretest.World(t, a), ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "xavier"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	e.w.Friends("carol", "dave")
	if err := a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

func session() [32]byte {
	var s [32]byte
	_, _ = rand.Read(s[:])
	return s
}

func (e *env) as(h string) context.Context { return e.asSession(h, session()) }

func (e *env) asSession(h string, s [32]byte) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, Session: s, AuthAt: e.d.Clock.Now()})
}

func (e *env) person(viewer, who string) gate.Ref { return e.d.Refs.User(e.id(viewer), e.id(who)) }

func (e *env) post(viewer string, p core.PostID) gate.Ref { return e.d.Refs.Post(e.id(viewer), p) }

func (e *env) notify(ev core.Event) {
	e.t.Helper()
	if err := e.n.Notify(e.ctx, nil, ev); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) list(h string) []gate.NotificationView {
	e.t.Helper()
	p, err := e.n.List(e.as(h), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Items
}

func (e *env) unread(h string) int {
	e.t.Helper()
	n, err := e.n.UnreadCount(e.as(h))
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) rows(h string) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT count(*) FROM notifications WHERE user_id = $1", int64(e.id(h))).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) unfriend(a, b string) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, "DELETE FROM edges WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)",
		int64(e.id(a)), int64(e.id(b))); err != nil {
		e.t.Fatal(err)
	}
	e.d.Graph.RemoveEdge(e.id(a), e.id(b))
}

// next waits up to d for the next stream event.
func next(t *testing.T, ch <-chan notify.StreamEvent, d time.Duration) (notify.StreamEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return ev, true
	case <-time.After(d):
		return notify.StreamEvent{}, false
	}
}

func unreadOf(t *testing.T, ev notify.StreamEvent) int {
	t.Helper()
	m, ok := ev.Data.(map[string]any)
	if !ok {
		t.Fatalf("event data %T", ev.Data)
	}
	n, _ := m["unread"].(int)
	return n
}

func (e *env) stream(ctx context.Context) <-chan notify.StreamEvent {
	e.t.Helper()
	ch, stop, err := e.n.Stream(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(stop)
	ev, ok := next(e.t, ch, time.Second)
	if !ok || ev.Type != notify.EventHello {
		e.t.Fatalf("first event %+v", ev)
	}
	return ch
}

func TestDeclinedRequestNotifiesNobody(t *testing.T) {
	e := newEnv(t)
	if err := e.a.Friends.Request(e.as("carol"), e.person("carol", "alice")); err != nil {
		t.Fatal(err)
	}
	got := e.list("alice")
	if len(got) != 1 || got[0].Kind != notify.KindFriendRequest || got[0].Person == nil ||
		got[0].Person.Ref != e.person("alice", "carol") || got[0].Person.Handle != "carol" {
		t.Fatalf("alice: %+v", got)
	}
	if err := e.a.Friends.Decline(e.as("alice"), e.person("alice", "carol")); err != nil {
		t.Fatal(err)
	}
	if n := e.rows("carol"); n != 0 {
		t.Fatalf("carol has %d notifications after a decline", n)
	}
	// Asking again keeps looking pending and still says nothing to anyone.
	if err := e.a.Friends.Request(e.as("carol"), e.person("carol", "alice")); err != nil {
		t.Fatal(err)
	}
	if e.rows("carol") != 0 || e.rows("alice") != 1 {
		t.Fatalf("rows carol=%d alice=%d", e.rows("carol"), e.rows("alice"))
	}
}

func TestAcceptNotifiesRequester(t *testing.T) {
	e := newEnv(t)
	if err := e.a.Friends.Request(e.as("carol"), e.person("carol", "alice")); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Friends.Accept(e.as("alice"), e.person("alice", "carol")); err != nil {
		t.Fatal(err)
	}
	got := e.list("carol")
	if len(got) != 1 || got[0].Kind != notify.KindRequestAccepted || got[0].Person.Ref != e.person("carol", "alice") {
		t.Fatalf("carol: %+v", got)
	}
}

func TestBlockedSubjectProducesNothing(t *testing.T) {
	e := newEnv(t)
	e.w.Friends("alice", "dave") // the block unfriends alice–bob; bob still reaches alice through dave
	p1 := e.w.Post("alice", "p1", "hello")
	if err := e.a.Friends.Block(e.as("alice"), e.person("alice", "bob")); err != nil {
		t.Fatal(err)
	}
	// Bob can still see alice (blocks are one-directional, D7): he references and mentions her.
	if _, err := e.a.Posts.Create(e.as("bob"), posts.Draft{Body: "hi @alice", Lang: "en",
		Parents: []posts.ParentIn{{Ref: e.post("bob", p1), Kind: "ref"}}}); err != nil {
		t.Fatal(err)
	}
	// And straight through the Notifier, whatever the caller checked.
	for _, k := range []string{notify.KindMention, notify.KindFriendRequest} {
		e.notify(core.Event{To: e.id("alice"), Kind: k, SubjectUser: e.id("bob"), SubjectPost: p1})
	}
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: "post_referenced:" + p1.String(),
		SubjectUser: e.id("bob"), SubjectPost: p1})
	if n := e.rows("alice"); n != 0 {
		t.Fatalf("alice has %d notifications from someone she blocked", n)
	}
	// A third person is unaffected.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("carol"), SubjectPost: p1})
	if n := e.rows("alice"); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestMentionAndReferenceFromPosts(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	for i, who := range []string{"carol", "bob", "bob"} {
		if _, err := e.a.Posts.Create(e.as(who), posts.Draft{Body: fmt.Sprintf("reply %d @dave", i), Lang: "en",
			Parents: []posts.ParentIn{{Ref: e.post(who, p1), Kind: "ref"}}}); err != nil {
			t.Fatal(err)
		}
	}
	got := e.list("alice")
	if len(got) != 1 || got[0].Kind != notify.KindPostReferenced || got[0].Count != 3 || got[0].Post == nil ||
		*got[0].Post != e.post("alice", p1) || got[0].Person.Ref != e.person("alice", "bob") {
		t.Fatalf("alice: %+v", got)
	}
	if got := e.list("dave"); len(got) != 3 || got[0].Kind != notify.KindMention || got[0].Person.Handle != "bob" ||
		got[2].Person.Handle != "carol" {
		t.Fatalf("dave: %+v", got)
	}
}

func TestBatchedKindUpsertsOneUnreadRow(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	group := "post_referenced:" + p1.String()
	for _, who := range []string{"bob", "carol", "bob"} {
		e.notify(core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: group, SubjectUser: e.id(who), SubjectPost: p1})
		e.advance(time.Minute)
	}
	got := e.list("alice")
	if len(got) != 1 || got[0].Count != 3 || got[0].Read || e.rows("alice") != 1 {
		t.Fatalf("alice: %+v rows=%d", got, e.rows("alice"))
	}
	if e.unread("alice") != 1 {
		t.Fatalf("unread = %d", e.unread("alice"))
	}
	if err := e.n.MarkRead(e.as("alice"), got[0].ID); err != nil {
		t.Fatal(err)
	}
	if e.unread("alice") != 0 {
		t.Fatal("still unread")
	}
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: group, SubjectUser: e.id("bob"), SubjectPost: p1})
	got = e.list("alice")
	if len(got) != 2 || got[0].Count != 1 || got[0].Read || !got[1].Read || got[1].Count != 3 {
		t.Fatalf("after read: %+v", got)
	}
}

func TestMarkReadKeepsNewerRowsUnread(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1})
	e.advance(time.Minute)
	first := e.list("alice")[0].ID
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("carol"), SubjectPost: p1})
	if err := e.n.MarkRead(e.as("alice"), first); err != nil {
		t.Fatal(err)
	}
	if n := e.unread("alice"); n != 1 {
		t.Fatalf("unread = %d", n)
	}
	// Someone else's notification ref, or garbage: the uniform not-found.
	if err := e.n.MarkRead(e.as("bob"), first); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("bob marking alice's: %v", err)
	}
	if err := e.n.MarkRead(e.as("alice"), "garbage"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestSubjectsNoLongerVisibleAreOmitted(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("carol"), SubjectPost: p1})
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendSecurity, SubjectUser: e.id("carol"),
		Data: map[string]any{"reason": "passkey_added"}})
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: "post_referenced:" + p1.String(),
		SubjectUser: e.id("carol"), SubjectPost: p1})
	if got := e.list("alice"); len(got) != 3 || e.unread("alice") != 3 {
		t.Fatalf("before: %+v", got)
	}
	// Carol leaves alice's horizon: nothing about her is shown any more.
	e.unfriend("bob", "carol")
	got := e.list("alice")
	if len(got) != 2 || e.unread("alice") != 2 {
		t.Fatalf("after unfriend: %+v", got)
	}
	for _, nv := range got {
		if nv.Person != nil || nv.Kind == notify.KindMention {
			t.Fatalf("carol still shown: %+v", nv)
		}
	}
	if got[1].Kind != notify.KindFriendSecurity || got[1].Data["reason"] != "passkey_added" {
		t.Fatalf("data: %+v", got[1])
	}
	// The rows are kept: carol comes back with the edge.
	e.w.Friends("bob", "carol")
	if got := e.list("alice"); len(got) != 3 {
		t.Fatalf("after re-friend: %+v", got)
	}
	// A deleted post takes its notifications with it.
	if err := e.a.Posts.Delete(e.as("alice"), e.post("alice", p1)); err != nil {
		t.Fatal(err)
	}
	if got := e.list("alice"); len(got) != 1 || got[0].Kind != notify.KindFriendSecurity {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestNothingAboutOtherComponents(t *testing.T) {
	e := newEnv(t)
	x1 := e.w.Post("xavier", "x1", "far")
	// A (buggy) event about someone alice cannot see is never rendered.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("xavier"), SubjectPost: x1})
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendSecurity, SubjectUser: e.id("xavier")})
	got := e.list("alice")
	if len(got) != 1 || got[0].Person != nil || got[0].Post != nil {
		t.Fatalf("alice: %+v", got)
	}
}

func TestPagingAndCursors(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	for i := 0; i < 70; i++ {
		e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1})
		e.advance(time.Second)
	}
	var (
		sizes  []int
		cursor string
		seen   = map[gate.Ref]bool{}
	)
	for {
		p, err := e.n.List(e.as("alice"), cursor)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(p.Items))
		for _, nv := range p.Items {
			if seen[nv.ID] {
				t.Fatal("duplicate")
			}
			seen[nv.ID] = true
		}
		if p.Next == "" {
			break
		}
		if _, err := e.n.List(e.as("bob"), p.Next); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("bob with alice's cursor: %v", err)
		}
		cursor = p.Next
	}
	if fmt.Sprint(sizes) != "[30 30 10]" || len(seen) != 70 {
		t.Fatalf("pages %v, %d items", sizes, len(seen))
	}
	if _, err := e.n.List(e.as("alice"), "nope"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("garbage cursor: %v", err)
	}
	if n := e.unread("alice"); n != 70 {
		t.Fatalf("unread = %d", n)
	}
}

func TestMutePost(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	x1 := e.w.Post("xavier", "x1", "far")
	if err := e.n.MutePost(e.as("alice"), e.post("alice", p1), true); err != nil {
		t.Fatal(err)
	}
	ref := core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: "post_referenced:" + p1.String(),
		SubjectUser: e.id("bob"), SubjectPost: p1}
	e.notify(ref)
	if e.rows("alice") != 0 {
		t.Fatal("muted post notified")
	}
	if err := e.n.MutePost(e.as("alice"), e.post("alice", p1), false); err != nil {
		t.Fatal(err)
	}
	e.notify(ref)
	if e.rows("alice") != 1 {
		t.Fatal("unmuted post not notified")
	}
	for _, r := range []gate.Ref{e.post("alice", x1), e.post("bob", p1), "garbage"} {
		if err := e.n.MutePost(e.as("alice"), r, true); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("mute %q: %v", r, err)
		}
	}
	if err := e.n.MutePost(e.ctx, e.post("alice", p1), true); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestRejectsUnknownKindsAndProse(t *testing.T) {
	e := newEnv(t)
	bad := []core.Event{
		{To: e.id("alice"), Kind: "party_invite"},
		{To: e.id("alice"), Kind: notify.KindFriendSecurity, Data: map[string]any{"reason": "Your friend did a thing"}},
		{To: e.id("alice"), Kind: notify.KindFriendSecurity, Data: map[string]any{"Reason": "x"}},
		{To: e.id("alice"), Kind: notify.KindFriendSecurity, Data: map[string]any{"who": []string{"bob"}}},
	}
	for _, ev := range bad {
		if err := e.n.Notify(e.ctx, nil, ev); err == nil {
			t.Fatalf("accepted %+v", ev)
		}
	}
	// Self-notifications are dropped.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendSecurity, SubjectUser: e.id("alice")})
	if e.rows("alice") != 0 {
		t.Fatal("self notification stored")
	}
}

func TestStreamDeliversAfterCommitOnly(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	ch := e.stream(e.as("alice"))
	// Committed later: no event before the commit, one within 1 s after it.
	tx, err := e.d.DB.Pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.n.Notify(e.ctx, tx, core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1}); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(t, ch, 300*time.Millisecond); ok {
		t.Fatalf("event before commit: %+v", ev)
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ev, ok := next(t, ch, time.Second)
	if !ok || ev.Type != notify.EventNotification || unreadOf(t, ev) != 1 {
		t.Fatalf("after commit: %+v %v", ev, ok)
	}
	if time.Since(start) > time.Second {
		t.Fatal("too slow")
	}
	// Rolled back: never announced.
	err = pgx.BeginFunc(e.ctx, e.d.DB.Pool, func(tx pgx.Tx) error {
		if err := e.n.Notify(e.ctx, tx, core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("carol"), SubjectPost: p1}); err != nil {
			return err
		}
		return errors.New("roll back")
	})
	if err == nil {
		t.Fatal("no rollback")
	}
	if ev, ok := next(t, ch, 400*time.Millisecond); ok {
		t.Fatalf("event for a rolled-back row: %+v", ev)
	}
}

func TestStreamClosesWhenItsSessionEnds(t *testing.T) {
	e := newEnv(t)
	s1, s2 := session(), session()
	ch1, stop1, err := e.n.Stream(e.asSession("alice", s1))
	if err != nil {
		t.Fatal(err)
	}
	defer stop1()
	ch2 := e.stream(e.asSession("alice", s2))
	<-ch1 // hello
	e.d.Bus.Publish(core.SessionEnded{User: e.id("alice"), Session: s1})
	select {
	case _, ok := <-ch1:
		if ok {
			t.Fatal("event instead of close")
		}
	case <-time.After(time.Second):
		t.Fatal("stream not closed within 1 s")
	}
	e.a.Notify.Live(e.id("alice"), notify.TopicHandshake, map[string]any{"state": "claimed"})
	if ev, ok := next(t, ch2, time.Second); !ok || ev.Type != notify.TopicHandshake {
		t.Fatalf("other session: %+v", ev)
	}
	// Step-up (P1) reaches the one session only.
	e.d.Bus.Publish(core.SessionStepUp{User: e.id("alice"), Session: s2})
	if ev, ok := next(t, ch2, time.Second); !ok || ev.Type != notify.TopicStepUp {
		t.Fatalf("stepup: %+v", ev)
	}
	// Unknown live topics are dropped.
	e.a.Notify.Live(e.id("alice"), "gossip", nil)
	if ev, ok := next(t, ch2, 200*time.Millisecond); ok {
		t.Fatalf("unknown topic delivered: %+v", ev)
	}
}

func TestBatchedGroupPingsAtMostHourly(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	ch := e.stream(e.as("alice"))
	ev := core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: "post_referenced:" + p1.String(),
		SubjectUser: e.id("bob"), SubjectPost: p1}
	pings := func(wait time.Duration) int {
		n := 0
		for {
			if _, ok := next(t, ch, wait); !ok {
				return n
			}
			n++
		}
	}
	for i := 0; i < 3; i++ {
		e.notify(ev)
	}
	if n := pings(300 * time.Millisecond); n != 1 {
		t.Fatalf("first burst: %d pings", n)
	}
	e.advance(30 * time.Minute)
	e.notify(ev)
	if err := e.n.Flush(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := pings(300 * time.Millisecond); n != 0 {
		t.Fatalf("within the hour: %d pings", n)
	}
	e.advance(31 * time.Minute)
	if err := e.a.DrainJobs(e.ctx); err != nil { // notify.flush
		t.Fatal(err)
	}
	if n := pings(300 * time.Millisecond); n != 1 {
		t.Fatalf("after the hour: %d pings", n)
	}
	if err := e.n.Flush(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.notify(ev)
	if n := pings(300 * time.Millisecond); n != 0 {
		t.Fatalf("right after a flush: %d pings", n)
	}
	if got := e.list("alice"); len(got) != 1 || got[0].Count != 5 {
		t.Fatalf("row: %+v", got)
	}
	// Non-batched kinds ping every time.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1})
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1})
	if n := pings(300 * time.Millisecond); n < 1 {
		t.Fatal("mention did not ping")
	}
}

func TestMarkReadUpdatesOtherTabs(t *testing.T) {
	e := newEnv(t)
	p1 := e.w.Post("alice", "p1", "hello")
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1})
	ch, stop, err := e.n.Stream(e.as("alice"))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if ev := <-ch; ev.Type != notify.EventHello || unreadOf(t, ev) != 1 {
		t.Fatalf("hello: %+v", ev)
	}
	if err := e.n.MarkRead(e.as("alice"), e.list("alice")[0].ID); err != nil {
		t.Fatal(err)
	}
	if ev, ok := next(t, ch, time.Second); !ok || unreadOf(t, ev) != 0 {
		t.Fatalf("after read: %+v", ev)
	}
}

func TestStreamLimitsAndAccountDeletion(t *testing.T) {
	e := newEnv(t)
	var chans []<-chan notify.StreamEvent
	for i := 0; i < notify.StreamsPerUser+1; i++ {
		ch, stop, err := e.n.Stream(e.as("alice"))
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		<-ch
		chans = append(chans, ch)
	}
	if _, ok := <-chans[0]; ok {
		t.Fatal("oldest stream not closed")
	}
	p1 := e.w.Post("alice", "p1", "hello")
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob"), SubjectPost: p1})
	if err := e.n.MutePost(e.as("alice"), e.post("alice", p1), true); err != nil {
		t.Fatal(err)
	}
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("alice")})
	for _, ch := range chans[1:] {
		for range ch { //nolint:revive // drain until closed
		}
	}
	var mutes int
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT count(*) FROM notification_mutes").Scan(&mutes); err != nil {
		t.Fatal(err)
	}
	if e.rows("alice") != 0 || mutes != 0 {
		t.Fatalf("rows %d mutes %d", e.rows("alice"), mutes)
	}
	if _, _, err := e.n.Stream(e.ctx); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous stream: %v", err)
	}
}

func TestResetClosesStreams(t *testing.T) {
	e := newEnv(t)
	ch, stop, err := e.n.Stream(e.as("alice"))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	<-ch
	e.n.Reset()
	if _, ok := <-ch; ok {
		t.Fatal("stream survived a reset")
	}
}
