package moderation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/moderation"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	m   *moderation.Service
	w   *wiretest.TestWorld
	ctx context.Context
}

// newEnv: alice (genesis) invited bob and eve; alice – bob – carol are friends,
// eve – frank are friends in a component of their own (alice cannot see eve);
// xavier is alone; mo1 and mo2 are moderators. bob posts b1 and b2, eve e1.
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	e := &env{t: t, a: a, d: a.Deps(), m: a.Moderation, w: wiretest.World(t, a), ctx: context.Background()}
	e.w.User("alice")
	alice := e.w.ID("alice")
	e.w.User("bob", coretest.InvitedBy(alice))
	e.w.User("eve", coretest.InvitedBy(alice))
	for _, u := range []string{"carol", "frank", "xavier", "mo1", "mo2"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	e.w.Friends("eve", "frank")
	e.w.Post("bob", "b1", "a spammy post by bob")
	e.w.Post("bob", "b2", "another post by bob")
	e.w.Post("eve", "e1", "eve posts where alice cannot see")
	e.w.Post("xavier", "x1", "far away")
	for _, h := range []string{"mo1", "mo2"} {
		e.exec("UPDATE users SET role = 1 WHERE id = $1", int64(e.id(h)))
	}
	if err := a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

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

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

// mod is a moderator viewer; fresh = verified with a passkey just now.
func (e *env) mod(h string, fresh bool) context.Context {
	at := e.d.Clock.Now()
	if !fresh {
		at = at.Add(-time.Hour)
	}
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, Role: core.RoleModerator, AuthAt: at})
}

func (e *env) post(viewer, label string) gate.Ref {
	return e.d.Refs.Post(e.id(viewer), e.w.PostID(label))
}

func (e *env) person(viewer, who string) gate.Ref { return e.d.Refs.User(e.id(viewer), e.id(who)) }

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) report(viewer, kind string, ref gate.Ref, reason string) {
	e.t.Helper()
	if err := e.m.Report(e.as(viewer), kind, ref, reason, ""); err != nil {
		e.t.Fatalf("report: %v", err)
	}
}

func (e *env) queue(mod string) []gate.ModItem {
	e.t.Helper()
	p, err := e.m.Queue(e.mod(mod, false), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Items
}

func problem(t *testing.T, err error, code string) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("want problem %s, got %v", code, err)
	}
}

func (e *env) reports() int { return e.count("SELECT count(*) FROM reports") }

func TestReportInputAndSilentDrops(t *testing.T) {
	e := newEnv(t)
	ctx := e.as("carol")
	problem(t, e.m.Report(ctx, "post", e.post("carol", "b1"), "rude", ""), "reason_invalid")
	problem(t, e.m.Report(ctx, "post", e.post("carol", "b1"), "ban_evasion", ""), "reason_invalid")
	problem(t, e.m.Report(ctx, "account", e.post("carol", "b1"), "spam", ""), "kind_invalid")
	problem(t, e.m.Report(ctx, "post", e.post("carol", "b1"), "spam", strings.Repeat("é", moderation.NoteMax+1)), "note_too_long")
	// An invalid reason is refused whatever the target (it is the caller's own input).
	problem(t, e.m.Report(ctx, "post", "garbage", "rude", ""), "reason_invalid")
	// Targets the reporter cannot see answer exactly like stored reports.
	for name, c := range map[string]struct {
		kind string
		ref  gate.Ref
	}{
		"garbage":           {"post", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"another viewer's":  {"post", e.post("bob", "b1")},
		"other component":   {"post", e.post("carol", "x1")},
		"invisible person":  {"person", e.person("carol", "xavier")},
		"own post":          {"post", e.post("bob", "b1")},
		"post ref as user":  {"person", gate.Ref(e.post("carol", "b1"))},
		"genesis inviter":   {"inviter", e.person("carol", "carol")},
		"inviter of nobody": {"inviter", e.person("bob", "alice")},
	} {
		viewer := "carol"
		if name == "own post" {
			viewer = "bob"
		}
		if name == "inviter of nobody" {
			viewer = "bob"
		}
		if err := e.m.Report(e.as(viewer), c.kind, c.ref, "spam", ""); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := e.reports(); n != 0 {
		t.Fatalf("%d reports stored for invisible targets", n)
	}
	// Signed-out callers get the uniform not-found.
	if err := e.m.Report(e.ctx, "post", e.post("carol", "b1"), "spam", ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("signed out: %v", err)
	}

	if err := e.m.Report(ctx, "post", e.post("carol", "b1"), "spam", "  looks \u200bautomated "); err != nil {
		t.Fatal(err)
	}
	e.report("carol", "post", e.post("carol", "b1"), "spam") // a repeat changes nothing
	e.report("carol", "inviter", e.person("carol", "bob"), "bot")
	e.report("carol", "preview", e.post("carol", "b2"), "misleading_preview")
	e.report("alice", "person", e.person("alice", "bob"), "harassment")
	if n := e.reports(); n != 4 {
		t.Fatalf("reports %d", n)
	}
	var note string
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT note FROM reports WHERE target_kind = 0").Scan(&note); err != nil || note != "looks automated" {
		t.Fatalf("note %q %v", note, err)
	}
	// The inviter report is stored against the invitee (the inviter is never addressed).
	if n := e.count("SELECT count(*) FROM reports WHERE target_kind = 2 AND target_user = $1", int64(e.id("bob"))); n != 1 {
		t.Fatal("inviter report not routed through the invitee")
	}
	// Each report is a signed Report record with the reporter as its party.
	if n := e.count(`SELECT count(*) FROM signed_records r JOIN record_parties p ON p.record_hash = r.hash
		WHERE r.kind = 7 AND p.user_id = $1`, int64(e.id("carol"))); n != 3 {
		t.Fatalf("carol's report records %d", n)
	}
}

func TestQueueShowsSubjectInviterAndProbation(t *testing.T) {
	e := newEnv(t)
	e.exec("UPDATE users SET probation_until = $2 WHERE id = $1", int64(e.id("bob")), e.d.Clock.Now().Add(72*time.Hour))
	e.a.Users.Invalidate(e.id("bob"))
	e.report("carol", "post", e.post("carol", "b1"), "spam")
	e.advance(time.Minute)
	e.report("alice", "post", e.post("alice", "b1"), "harassment")
	e.advance(time.Minute)
	e.report("frank", "post", e.post("frank", "e1"), "csam")
	e.report("carol", "inviter", e.person("carol", "bob"), "bot")

	if _, err := e.m.Queue(e.as("carol"), ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("members have no queue: %v", err)
	}
	items := e.queue("mo1")
	if len(items) != 4 {
		t.Fatalf("items %d", len(items))
	}
	// csam first (and never with content), then oldest first.
	if items[0].Reason != "csam" || items[0].Priority != 0 || items[0].Post != nil {
		t.Fatalf("first item %+v", items[0])
	}
	first := items[1]
	if first.Reason != "spam" || first.Source != "report" || first.Post == nil || first.Post.Body != "a spammy post by bob" {
		t.Fatalf("spam item %+v", first)
	}
	if first.Subject == nil || first.Subject.Handle != "bob" || !first.SubjectProbation ||
		first.SubjectInviter == nil || first.SubjectInviter.Kind != "user" || first.SubjectInviter.Handle != "alice" {
		t.Fatalf("subject %+v inviter %+v", first.Subject, first.SubjectInviter)
	}
	if u, ok := e.d.Refs.OpenModUser(e.id("mo1"), first.Subject.Ref); !ok || u != e.id("bob") {
		t.Fatal("the subject ref is moderator-scoped")
	}
	if _, ok := e.d.Refs.OpenUser(e.id("mo1"), first.Subject.Ref); ok {
		t.Fatal("a moderator-scoped ref is not an ordinary ref")
	}
	if first.Summary["reports"] != 2 || first.Post.Author.Handle != "bob" {
		t.Fatalf("summary %v", first.Summary)
	}
	// The inviter report's subject is the inviter, resolved for moderators only.
	inv := items[3]
	if inv.Subject == nil || inv.Subject.Handle != "alice" || inv.SubjectInviter.Kind != "genesis" || inv.Post != nil {
		t.Fatalf("inviter item %+v", inv)
	}
	// Item ids are bound to the moderator.
	if _, ok := e.d.Refs.OpenModItem(e.id("mo2"), first.ID); ok {
		t.Fatal("item ids are per moderator")
	}
	if _, err := e.m.Queue(e.mod("mo1", false), "garbage"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("garbage cursor: %v", err)
	}
}

func TestQueuePaging(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < moderation.QueuePage+5; i++ {
		h := "r" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		e.w.User(h)
		e.w.Friends(h, "bob")
		e.report(h, "person", e.person(h, "bob"), "spam")
	}
	p, err := e.m.Queue(e.mod("mo1", false), "")
	if err != nil || len(p.Items) != moderation.QueuePage || p.Next == "" {
		t.Fatalf("page 1: %d %q %v", len(p.Items), p.Next, err)
	}
	if _, err := e.m.Queue(e.mod("mo2", false), p.Next); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("another moderator's cursor: %v", err)
	}
	p2, err := e.m.Queue(e.mod("mo1", false), p.Next)
	if err != nil || len(p2.Items) != 5 || p2.Next != "" {
		t.Fatalf("page 2: %d %q %v", len(p2.Items), p2.Next, err)
	}
}

func (e *env) item(mod, reason string) gate.Ref {
	e.t.Helper()
	for _, it := range e.queue(mod) {
		if it.Reason == reason {
			return it.ID
		}
	}
	e.t.Fatalf("no %s item", reason)
	return ""
}

func (e *env) notifications(h string) []map[string]any {
	e.t.Helper()
	rows, err := e.d.DB.Q().Query(e.ctx, "SELECT data FROM notifications WHERE user_id = $1 AND kind = 'invitee_action'", int64(e.id(h)))
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			e.t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (e *env) blame(h string) []gate.BlameItem {
	e.t.Helper()
	p, err := e.m.BlameInbox(e.as(h), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Items
}

func TestRemovePostAndBlame(t *testing.T) {
	e := newEnv(t)
	e.report("carol", "post", e.post("carol", "b1"), "spam")
	e.report("alice", "post", e.post("alice", "b1"), "harassment")
	e.report("frank", "post", e.post("frank", "e1"), "spam")
	e.report("carol", "person", e.person("carol", "bob"), "bot")
	it := e.item("mo1", "harassment")

	problem(t, e.m.Resolve(e.mod("mo1", false), it, "ban", "spam"), "action_invalid")
	problem(t, e.m.Resolve(e.mod("mo1", false), it, "remove_post", "rude"), "reason_invalid")
	if err := e.m.Resolve(e.as("carol"), it, "remove_post", "spam"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("members cannot resolve: %v", err)
	}
	if err := e.m.Resolve(e.mod("mo1", false), "garbage", "remove_post", "spam"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("garbage item: %v", err)
	}
	// Nothing reaches the inviter before a moderator acts.
	if len(e.blame("alice")) != 0 || len(e.notifications("alice")) != 0 {
		t.Fatal("blame before any action")
	}
	if err := e.m.Resolve(e.mod("mo1", false), it, "remove_post", "spam"); err != nil {
		t.Fatal(err)
	}
	if m, ok := e.d.Posts.Meta(e.ctx, e.w.PostID("b1")); ok && !m.Deleted {
		t.Fatal("post still live")
	}
	if n := e.count("SELECT count(*) FROM posts WHERE id = $1 AND deleted_at IS NOT NULL AND body IS NULL", e.w.PostID("b1")); n != 1 {
		t.Fatal("post not tombstoned")
	}
	// Both reports on b1 are closed; the others stay open.
	if n := e.count("SELECT count(*) FROM reports WHERE state = 1"); n != 2 {
		t.Fatalf("actioned %d", n)
	}
	if n := len(e.queue("mo2")); n != 2 {
		t.Fatalf("queue after removal %d", n)
	}
	problem(t, e.m.Resolve(e.mod("mo1", false), it, "remove_post", "spam"), "item_resolved")

	items := e.blame("alice")
	if len(items) != 1 || items[0].Action != "remove_post" || items[0].Reason != "spam" || items[0].InviteeHandle != "bob" ||
		items[0].Read || items[0].Post == nil || items[0].Post.Body != "a spammy post by bob" || items[0].Post.Author.Handle != "bob" {
		t.Fatalf("blame %+v", items)
	}
	if again := e.blame("alice"); !again[0].Read {
		t.Fatal("listing marks entries read")
	}
	if n := e.notifications("alice"); len(n) != 1 || n[0]["action"] != "remove_post" || n[0]["reason"] != "spam" {
		t.Fatalf("invitee_action %v", n)
	}
	// The reporter stays anonymous, and nobody else gets anything.
	for _, h := range []string{"bob", "carol", "eve"} {
		if len(e.blame(h)) != 0 {
			t.Fatalf("%s has blame", h)
		}
	}

	// eve's post: alice cannot see it, so her entry has no post.
	if err := e.m.Resolve(e.mod("mo2", false), e.item("mo2", "spam"), "remove_post", "spam"); err != nil {
		t.Fatal(err)
	}
	items = e.blame("alice")
	if len(items) != 2 || items[0].InviteeHandle != "eve" || items[0].Post != nil {
		t.Fatalf("blame for an invisible post %+v", items[0])
	}
	var excerpt *string
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT post_excerpt FROM blame_inbox WHERE invitee_id = $1", int64(e.id("eve"))).Scan(&excerpt); err != nil || excerpt != nil {
		t.Fatalf("an invisible post's text is kept: %v %v", excerpt, err)
	}

	// remove_post needs a post; dismiss closes without blame.
	problem(t, e.m.Resolve(e.mod("mo1", false), e.item("mo1", "bot"), "remove_post", "bot"), "action_invalid")
	if err := e.m.Resolve(e.mod("mo1", false), e.item("mo1", "bot"), "dismiss", "other"); err != nil {
		t.Fatal(err)
	}
	if len(e.blame("alice")) != 2 || e.count("SELECT count(*) FROM reports WHERE state = 2") != 1 {
		t.Fatal("dismiss")
	}
}

func TestRemovePostCSAMKeepsNothing(t *testing.T) {
	e := newEnv(t)
	e.report("carol", "post", e.post("carol", "b2"), "csam")
	if err := e.m.Resolve(e.mod("mo1", false), e.item("mo1", "csam"), "remove_post", "csam"); err != nil {
		t.Fatal(err)
	}
	items := e.blame("alice")
	if len(items) != 1 || items[0].Post != nil || items[0].Reason != "csam" {
		t.Fatalf("csam blame %+v", items)
	}
	if n := e.count("SELECT count(*) FROM blame_inbox WHERE post_id IS NULL AND post_excerpt IS NULL"); n != 1 {
		t.Fatal("csam content kept")
	}
}

func TestSuspendIsTwoPerson(t *testing.T) {
	e := newEnv(t)
	e.report("carol", "person", e.person("carol", "bob"), "bot")
	it := e.item("mo1", "bot")
	if err := e.m.Resolve(e.mod("mo1", false), it, "suspend_user", "bot"); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale first moderator: %v", err)
	}
	if err := e.m.Resolve(e.mod("mo1", true), it, "suspend_user", "bot"); err != nil {
		t.Fatal(err)
	}
	// One moderator's decision stays pending: nothing is applied or told.
	if m, _ := e.a.Users.Get(e.ctx, e.id("bob")); m.Status != core.StatusActive {
		t.Fatal("suspended by one moderator")
	}
	if len(e.blame("alice")) != 0 || len(e.notifications("alice")) != 0 {
		t.Fatal("blame before confirmation")
	}
	if items := e.queue("mo2"); len(items) != 1 || !items[0].AwaitingSecond {
		t.Fatalf("queue %+v", items)
	}
	problem(t, e.m.Resolve(e.mod("mo2", true), e.item("mo2", "bot"), "dismiss", "other"), "action_pending")
	problem(t, e.m.Confirm(e.mod("mo1", true), it), "second_moderator_required")
	it2 := e.item("mo2", "bot")
	if err := e.m.Confirm(e.mod("mo2", false), it2); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale second moderator: %v", err)
	}
	if err := e.m.Confirm(e.as("carol"), it2); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("member confirm: %v", err)
	}
	if err := e.m.Confirm(e.mod("mo2", true), it); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("another moderator's item id: %v", err)
	}
	if err := e.m.Confirm(e.mod("mo2", true), it2); err != nil {
		t.Fatal(err)
	}
	if m, _ := e.a.Users.Get(e.ctx, e.id("bob")); m.Status != core.StatusSuspended {
		t.Fatalf("status %d", m.Status)
	}
	var second int64
	var auth []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT second_moderator, second_auth FROM mod_actions
		WHERE action = 'suspend_user' AND state = 1 AND moderator_id = $1 AND first_auth_at IS NOT NULL`, int64(e.id("mo1"))).
		Scan(&second, &auth); err != nil || second != int64(e.id("mo2")) || !strings.Contains(string(auth), "auth_at") {
		t.Fatalf("action %d %s %v", second, auth, err)
	}
	items := e.blame("alice")
	if len(items) != 1 || items[0].Action != "suspend_user" || items[0].Post != nil {
		t.Fatalf("blame %+v", items)
	}
	if n := e.notifications("alice"); len(n) != 1 || n[0]["action"] != "suspend_user" {
		t.Fatalf("invitee_action %v", n)
	}
	if len(e.queue("mo1")) != 0 {
		t.Fatal("the report is closed")
	}
	// Bob's posts are gone from everyone's view (suspended author).
	sr, _ := e.d.Vis.SearchFor(e.ctx, e.id("carol"))
	if m, ok := e.d.Posts.Meta(e.ctx, e.w.PostID("b2")); !ok {
		t.Fatal("meta")
	} else if _, ok := e.d.Vis.CheckPost(e.ctx, sr, m); ok {
		t.Fatal("a suspended author's post is still visible")
	}
}

func TestSuspendExpiresAfter24h(t *testing.T) {
	e := newEnv(t)
	e.report("carol", "person", e.person("carol", "bob"), "bot")
	if err := e.m.Resolve(e.mod("mo1", true), e.item("mo1", "bot"), "suspend_user", "bot"); err != nil {
		t.Fatal(err)
	}
	e.advance(moderation.SecondWithin)
	problem(t, e.m.Confirm(e.mod("mo2", true), e.item("mo2", "bot")), "action_expired")
	if m, _ := e.a.Users.Get(e.ctx, e.id("bob")); m.Status != core.StatusActive {
		t.Fatal("an expired action was applied")
	}
	if items := e.queue("mo2"); items[0].AwaitingSecond {
		t.Fatal("expired action still awaiting")
	}
	// A new two-person action can start; the expiry job marks stale ones.
	if err := e.m.Resolve(e.mod("mo2", true), e.item("mo2", "bot"), "suspend_user", "bot"); err != nil {
		t.Fatal(err)
	}
	e.advance(moderation.SecondWithin + time.Minute)
	if err := e.m.Expire(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM mod_actions WHERE state = 2"); n != 2 {
		t.Fatalf("expired %d", n)
	}
}

func TestPublicLogPseudonyms(t *testing.T) {
	e := newEnv(t)
	e.report("carol", "post", e.post("carol", "b1"), "spam")
	e.report("carol", "post", e.post("carol", "b2"), "spam")
	e.report("frank", "post", e.post("frank", "e1"), "harassment")
	e.report("frank", "person", e.person("frank", "eve"), "bot")
	for _, r := range []string{"spam", "spam", "harassment"} {
		if err := e.m.Resolve(e.mod("mo1", false), e.item("mo1", r), "remove_post", r); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.m.Resolve(e.mod("mo1", false), e.item("mo1", "bot"), "dismiss", "other"); err != nil {
		t.Fatal(err)
	}
	logFor := func(h string) []gate.LogItem {
		p, err := e.m.Log(e.as(h), "")
		if err != nil {
			t.Fatal(err)
		}
		return p.Items
	}
	day := e.d.Clock.Now().UTC().Format("2006-01-02")
	carol := logFor("carol")
	if len(carol) != 2 {
		t.Fatalf("carol's log %+v", carol)
	}
	var bobItem, eveItem gate.LogItem
	for _, it := range carol {
		if it.Day != day || it.Action != "remove_post" || it.Subject == nil {
			t.Fatalf("item %+v", it)
		}
		if it.Reason == "spam" {
			bobItem = it
		} else {
			eveItem = it
		}
	}
	// bob is in carol's horizon: named, with carol's ref. eve is not: a pseudonym.
	if bobItem.Count != 2 || bobItem.Subject.Handle != "bob" || bobItem.Subject.Ref != e.person("carol", "bob") || bobItem.Subject.Pseudonym != "" {
		t.Fatalf("bob %+v", bobItem.Subject)
	}
	if eveItem.Subject.Handle != "" || eveItem.Subject.Ref != "" || len(eveItem.Subject.Pseudonym) != 2*moderation.PseudonymBytes {
		t.Fatalf("eve %+v", eveItem.Subject)
	}
	// The pseudonym is the same for everyone outside the horizon; xavier sees no names.
	for _, it := range logFor("xavier") {
		if it.Subject.Handle != "" || it.Subject.Ref != "" {
			t.Fatalf("xavier sees %+v", it.Subject)
		}
		if it.Reason == "harassment" && it.Subject.Pseudonym != eveItem.Subject.Pseudonym {
			t.Fatal("pseudonyms differ between viewers")
		}
	}
	// Signed-out: not found. Garbage cursor: not found.
	if _, err := e.m.Log(e.ctx, ""); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.m.Log(e.as("carol"), "AAAA"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCSPReportCountsOnly(t *testing.T) {
	e := newEnv(t)
	for _, d := range []string{"script-src-elem", "script-src-elem 'self'", "IMG-SRC", "https://evil.example/x", ""} {
		if err := e.m.CSPReport(e.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.m.CSPCounts(e.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got["script-src-elem"] != 2 || got["img-src"] != 1 || got["other"] != 2 || len(got) != 3 {
		t.Fatalf("counts %v", got)
	}
}

func TestGrantModerator(t *testing.T) {
	e := newEnv(t)
	if err := e.m.GrantModerator(e.ctx, "carol", "alice", false); err == nil {
		t.Fatal("--by must be an admin")
	}
	if err := e.m.GrantModerator(e.ctx, "@Alice", "", true); err != nil {
		t.Fatal(err)
	}
	if err := e.m.GrantModerator(e.ctx, "frank", "", true); err == nil {
		t.Fatal("a second bootstrap")
	}
	if err := e.m.GrantModerator(e.ctx, "carol", "alice", false); err != nil {
		t.Fatal(err)
	}
	if err := e.m.GrantModerator(e.ctx, "nobody", "alice", false); !errors.Is(err, gate.ErrNotFound) {
		t.Fatal(err)
	}
	if m, _ := e.a.Users.Get(e.ctx, e.id("carol")); m.Role != core.RoleModerator {
		t.Fatalf("role %d", m.Role)
	}
	if m, _ := e.a.Users.Get(e.ctx, e.id("alice")); m.Role != core.RoleAdmin {
		t.Fatalf("role %d", m.Role)
	}
}
