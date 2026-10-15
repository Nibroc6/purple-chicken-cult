package posts_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	p   *posts.Service
	w   *wiretest.TestWorld
	rec *coreref.RecordingNotifier
	ctx context.Context
}

// newEnv: alice – bob – carol – dave (a chain), erin – frank in another
// component.
func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	rec := &coreref.RecordingNotifier{D: d}
	d.Notify = rec
	e := &env{t: t, a: a, d: d, p: a.Posts, w: wiretest.World(t, a), rec: rec, ctx: context.Background()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "erin", "frank"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	e.w.Friends("carol", "dave")
	e.w.Friends("erin", "frank")
	return e
}

func (e *env) id(h string) core.UserID { return e.w.ID(h) }

func (e *env) as(h string) context.Context {
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, AuthAt: e.d.Clock.Now()})
}

// ref is viewer's ref for post p.
func (e *env) ref(viewer string, p core.PostID) gate.Ref { return e.d.Refs.Post(e.id(viewer), p) }

func (e *env) post(h, body string, parents ...posts.ParentIn) gate.PostView {
	e.t.Helper()
	pv, err := e.p.Create(e.as(h), posts.Draft{Body: body, Lang: "en", Parents: parents})
	if err != nil {
		e.t.Fatalf("create by %s: %v", h, err)
	}
	return pv
}

// pid opens author's own ref of a view.
func (e *env) pid(h string, pv gate.PostView) core.PostID {
	e.t.Helper()
	id, ok := e.d.Refs.OpenPost(e.id(h), pv.Ref)
	if !ok {
		e.t.Fatal("ref does not open")
	}
	return id
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) events(kind string) []core.Event {
	e.rec.Events = append([]core.Event(nil), e.rec.Events...)
	var out []core.Event
	for _, ev := range e.rec.Events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func ref(r gate.Ref) posts.ParentIn  { return posts.ParentIn{Ref: r, Kind: "ref"} }
func cont(r gate.Ref) posts.ParentIn { return posts.ParentIn{Ref: r, Kind: "cont"} }

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

// recordCommit reads the signed Post record of the post's current version and
// returns its body_commit and version.
func (e *env) recordCommit(id core.PostID) ([]byte, int) {
	e.t.Helper()
	var body []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT r.body FROM signed_records r JOIN posts p ON p.record_hash = r.hash
		WHERE p.id = $1`, id).Scan(&body); err != nil {
		e.t.Fatal(err)
	}
	var rec struct {
		Kind       string `json:"kind"`
		Version    int    `json:"version"`
		BodyCommit string `json:"body_commit"`
		Parents    []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"parents"`
	}
	if err := json.Unmarshal(body, &rec); err != nil || rec.Kind != "post" {
		e.t.Fatalf("record %s: %v", body, err)
	}
	c, err := base64.RawURLEncoding.DecodeString(rec.BodyCommit)
	if err != nil {
		e.t.Fatal(err)
	}
	return c, rec.Version
}

func commit(salt []byte, body string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(body))
	return h.Sum(nil)
}

func TestCreateGetAndRecord(t *testing.T) {
	e := newEnv(t)
	pv, err := e.p.Create(e.as("alice"), posts.Draft{Body: "  \n\nCafé time  \n\n", Lang: "en-us"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Body != "Café time" || pv.Lang != "en-US" || !pv.Mine || pv.Audience != "connected" || pv.RefPolicy != "anyone" ||
		pv.Tier != 0 {
		t.Fatalf("view %+v", pv)
	}
	id := e.pid("alice", pv)
	var (
		body string
		salt []byte
	)
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT body, body_salt FROM posts WHERE id = $1", id).Scan(&body, &salt); err != nil {
		t.Fatal(err)
	}
	if body != "Café time" || len(salt) != 16 {
		t.Fatalf("stored %q salt %d", body, len(salt))
	}
	c, v := e.recordCommit(id)
	if v != 1 || !bytes.Equal(c, commit(salt, body)) {
		t.Fatal("record does not commit to the salted NFC body")
	}
	if e.count("SELECT count(*) FROM post_versions WHERE post_id = $1", id) != 1 {
		t.Fatal("post_versions")
	}
	if e.count("SELECT count(*) FROM jobs WHERE kind = 'preview.fetch'") != 1 {
		t.Fatal("preview job not enqueued")
	}

	// Friends and friends of friends see it; another component does not.
	got, err := e.p.Get(e.as("bob"), e.ref("bob", id))
	if err != nil || got.Body != "Café time" || got.Tier != 1 || got.Mine {
		t.Fatalf("bob: %+v %v", got, err)
	}
	if got, err := e.p.Get(e.as("carol"), e.ref("carol", id)); err != nil || got.Tier != 2 {
		t.Fatalf("carol: %+v %v", got, err)
	}
	_, err = e.p.Get(e.as("erin"), e.ref("erin", id))
	wantNotFound(t, err)
	_, err = e.p.Get(e.as("bob"), e.ref("alice", id)) // another viewer's ref
	wantNotFound(t, err)
	_, err = e.p.Get(e.ctx, e.ref("alice", id)) // signed out
	wantNotFound(t, err)

	// no_preview skips the job.
	if _, err := e.p.Create(e.as("alice"), posts.Draft{Body: "quiet", Lang: "en", NoPreview: true}); err != nil {
		t.Fatal(err)
	}
	if e.count("SELECT count(*) FROM jobs WHERE kind = 'preview.fetch'") != 1 {
		t.Fatal("no_preview enqueued a preview job")
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		d    posts.Draft
		code string
	}{
		{posts.Draft{Body: " \n\t ", Lang: "en"}, "body_empty"},
		{posts.Draft{Body: "x", Lang: "not a tag!"}, "lang_invalid"},
		{posts.Draft{Body: "x", Lang: "en", Audience: "everyone"}, "audience_invalid"},
		{posts.Draft{Body: "x", Lang: "en", Audience: "friend_plus"}, "audience_invalid"},
		{posts.Draft{Body: "x", Lang: "en", RefPolicy: "some"}, "ref_policy_invalid"},
		{posts.Draft{Body: "a\x00b", Lang: "en"}, "bad_request"},
	} {
		_, err := e.p.Create(e.as("alice"), c.d)
		wantProblem(t, err, c.code)
	}
	_, err := e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Media: []gate.Ref{"abc"}})
	if !errors.Is(err, gate.ErrDisabled) {
		t.Fatalf("media: %v", err)
	}
	if pv, err := e.p.Create(e.as("alice"), posts.Draft{Body: "x"}); err != nil || pv.Lang != "und" {
		t.Fatalf("empty lang: %+v %v", pv, err)
	}
}

// Acceptance: a 20,000-code-point emoji post is accepted, 20,001 is rejected.
func TestLengthIsCountedInCodePoints(t *testing.T) {
	e := newEnv(t)
	pv, err := e.p.Create(e.as("alice"), posts.Draft{Body: strings.Repeat("😀", posts.MaxBodyCodePoints), Lang: "und"})
	if err != nil || posts.CodePoints(pv.Body) != posts.MaxBodyCodePoints {
		t.Fatalf("20000: %v", err)
	}
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: strings.Repeat("😀", posts.MaxBodyCodePoints+1), Lang: "und"})
	wantProblem(t, err, "body_too_long")
}

// Acceptance: referencing an invisible or foreign post is the uniform not-found,
// and Problems are only evaluated after every ref resolved.
func TestReferenceResolution(t *testing.T) {
	e := newEnv(t)
	far := e.post("erin", "other component")
	farID := e.pid("erin", far)
	limited, err := e.p.Create(e.as("dave"), posts.Draft{Body: "friends only", Lang: "en", Audience: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	limID := e.pid("dave", limited)
	own := e.post("alice", "mine")
	for name, r := range map[string]gate.Ref{
		"other component": e.ref("alice", farID),
		"audience":        e.ref("alice", limID), // dave is 3 hops from alice
		"foreign ref":     e.ref("bob", e.pid("alice", own)),
		"garbage":         "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		// An empty body would be body_empty, but the ref is resolved first.
		_, err := e.p.Create(e.as("alice"), posts.Draft{Body: "", Lang: "en", Parents: []posts.ParentIn{ref(r)}})
		if !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := e.count("SELECT count(*) FROM posts WHERE author_id = $1", int64(e.id("alice"))); n != 1 {
		t.Fatalf("alice has %d posts", n)
	}
}

func TestParentProblems(t *testing.T) {
	e := newEnv(t)
	a := e.post("alice", "a")
	b := e.post("bob", "b")
	aRef, bRef := a.Ref, e.ref("alice", e.pid("bob", b))
	c := e.post("alice", "c")
	d := e.post("alice", "d")

	_, err := e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{ref(aRef), ref(bRef), ref(c.Ref), ref(d.Ref)}})
	wantProblem(t, err, "too_many_parents")
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{ref(aRef), ref(aRef)}})
	wantProblem(t, err, "duplicate_parent")
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{{Ref: aRef, Kind: "quote"}}})
	wantProblem(t, err, "bad_request")
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{cont(aRef), cont(c.Ref)}})
	wantProblem(t, err, "cont_multiple")

	// Acceptance: cont on another's post ⇒ cont_not_own; a second cont ⇒ cont_taken.
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{cont(bRef)}})
	wantProblem(t, err, "cont_not_own")
	next := e.post("alice", "a, continued", cont(aRef))
	if next.Thread == nil || next.Thread.Index != 2 || next.Thread.Total != 2 {
		t.Fatalf("thread pos %+v", next.Thread)
	}
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{cont(aRef)}})
	wantProblem(t, err, "cont_taken")
	if m, _ := e.d.Posts.Meta(e.ctx, e.pid("alice", a)); m.ContChild != e.pid("alice", next) {
		t.Fatalf("parent meta %+v", m)
	}

	// Policies: friends ⇒ carol (2 hops) may not reference; nobody ⇒ no one but the author.
	fr, err := e.p.Create(e.as("bob"), posts.Draft{Body: "friends may reply", Lang: "en", RefPolicy: "friends"})
	if err != nil {
		t.Fatal(err)
	}
	frID := e.pid("bob", fr)
	_, err = e.p.Create(e.as("dave"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{ref(e.ref("dave", frID))}})
	wantProblem(t, err, "ref_not_allowed")
	e.post("alice", "ok", ref(e.ref("alice", frID)))
	sl, err := e.p.Create(e.as("bob"), posts.Draft{Body: "slippery", Lang: "en", RefPolicy: "nobody"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{ref(e.ref("alice", e.pid("bob", sl)))}})
	wantProblem(t, err, "ref_not_allowed")
	e.post("bob", "my own slippery post", ref(sl.Ref))
}

// Acceptance: a third reference of the same post within 24 h ⇒ ref_cap.
func TestRefCap(t *testing.T) {
	e := newEnv(t)
	p := e.post("bob", "popular")
	r := e.ref("alice", e.pid("bob", p))
	e.post("alice", "one", ref(r))
	e.advance(time.Hour)
	e.post("alice", "two", ref(r))
	_, err := e.p.Create(e.as("alice"), posts.Draft{Body: "three", Lang: "en", Parents: []posts.ParentIn{ref(r)}})
	wantProblem(t, err, "ref_cap")
	e.post("carol", "someone else", ref(e.ref("carol", e.pid("bob", p))))
	e.advance(23*time.Hour + time.Second)
	e.post("alice", "three, a day later", ref(r))
}

// Concurrent references by one author never exceed the cap.
func TestRefCapConcurrent(t *testing.T) {
	e := newEnv(t)
	p := e.post("bob", "popular")
	r := e.ref("alice", e.pid("bob", p))
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.p.Create(e.as("alice"), posts.Draft{Body: "ref " + string(rune('a'+i)), Lang: "en",
				Parents: []posts.ParentIn{ref(r)}})
		}()
	}
	wg.Wait()
	if n := e.count("SELECT count(*) FROM post_parents WHERE parent_id = $1", e.pid("bob", p)); n != posts.RefCapPerDay {
		t.Fatalf("%d references", n)
	}
}

// Acceptance: a reference from a user the parent's author blocked ⇒ created,
// shadow link visible only to the child's author, no notification.
func TestShadowLink(t *testing.T) {
	e := newEnv(t)
	p := e.post("bob", "parent")
	pid := e.pid("bob", p)
	if err := e.a.Friends.Block(e.as("bob"), e.d.Refs.User(e.id("bob"), e.id("carol"))); err != nil {
		t.Fatal(err)
	}
	// carol is still connected to bob's post through... nobody: the block unfriended
	// them. Make carol reach bob through dave–alice instead.
	e.w.Friends("carol", "alice")
	e.rec.Events = nil
	child := e.post("carol", "dunk", ref(e.ref("carol", pid)))
	cid := e.pid("carol", child)
	if e.count("SELECT count(*) FROM post_parents WHERE child_id = $1 AND shadow", cid) != 1 {
		t.Fatal("link not stored as shadow")
	}
	if len(e.events(posts.NotifyReferenced)) != 0 {
		t.Fatalf("notified %+v", e.rec.Events)
	}
	if len(child.Context) != 1 {
		t.Fatalf("author context %+v", child.Context)
	}
	// alice sees both posts but never the link; bob never sees carol's post at all.
	av, err := e.p.Get(e.as("alice"), e.ref("alice", cid))
	if err != nil || len(av.Context) != 0 {
		t.Fatalf("alice view %+v %v", av, err)
	}
	pv, err := e.p.Get(e.as("bob"), p.Ref)
	if err != nil || pv.ReplyCount != 0 {
		t.Fatalf("bob view %+v %v", pv, err)
	}
	// Detach on a shadow link answers like an unknown child.
	wantNotFound(t, e.p.Detach(e.as("bob"), p.Ref, e.ref("bob", cid)))
}

func TestNotifications(t *testing.T) {
	e := newEnv(t)
	p := e.post("alice", "hello")
	pid := e.pid("alice", p)
	e.post("bob", "reply 1", ref(e.ref("bob", pid)))
	e.post("carol", "reply 2", ref(e.ref("carol", pid)))
	e.post("dave", "reply 3 from 3 hops", ref(e.ref("dave", pid)))
	e.post("alice", "self reply", ref(p.Ref))
	ev := e.events(posts.NotifyReferenced)
	if len(ev) != 1 || ev[0].To != e.id("alice") || ev[0].GroupKey != "post_referenced:"+pid.String() {
		t.Fatalf("events %+v", ev)
	}
	total := 0
	for k, n := range e.rec.Counts {
		if strings.HasSuffix(k, "|post_referenced:"+pid.String()) {
			total += n
		}
	}
	if total != 2 { // bob and carol; dave is 3 hops from alice
		t.Fatalf("batched count %d", total)
	}
	// A child alice cannot see (audience d1 from carol, 2 hops) does not notify.
	before := total
	if _, err := e.p.Create(e.as("carol"), posts.Draft{Body: "limited", Lang: "en", Audience: "d1",
		Parents: []posts.ParentIn{ref(e.ref("carol", pid))}}); err != nil {
		t.Fatal(err)
	}
	total = 0
	for k, n := range e.rec.Counts {
		if strings.HasSuffix(k, "|post_referenced:"+pid.String()) {
			total += n
		}
	}
	if total != before {
		t.Fatal("notified about an invisible child")
	}
}

// Mentions resolve only inside the author's horizon and notify only people
// who can see the post.
func TestMentions(t *testing.T) {
	e := newEnv(t)
	e.rec.Events = nil
	pv := e.post("alice", "hi @Carol and @erin and @nobody_here, `@dave` in code, mail x@bob.com, @alice")
	id := e.pid("alice", pv)
	got := map[int]core.UserID{}
	rows, err := e.d.DB.Q().Query(e.ctx, "SELECT ord, target_id FROM mentions WHERE post_id = $1 ORDER BY ord", id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ord int16
		var u int64
		if err := rows.Scan(&ord, &u); err != nil {
			t.Fatal(err)
		}
		got[int(ord)] = core.UserID(u)
	}
	rows.Close()
	// ords: 0 carol, 1 erin (other component: not stored), 2 nobody_here, 3 alice.
	if len(got) != 2 || got[0] != e.id("carol") || got[3] != e.id("alice") {
		t.Fatalf("mentions %v", got)
	}
	if len(pv.Mentions) != 2 || pv.Mentions[0].Ord != 0 || pv.Mentions[0].Ref == "" {
		t.Fatalf("view mentions %+v", pv.Mentions)
	}
	ev := e.events(posts.NotifyMention)
	if len(ev) != 1 || ev[0].To != e.id("carol") {
		t.Fatalf("mention events %+v", ev)
	}
	// A d1 post mentioning carol (2 hops): stored, but carol cannot see it ⇒ no notification.
	e.rec.Events = nil
	if _, err := e.p.Create(e.as("alice"), posts.Draft{Body: "@carol secret", Lang: "en", Audience: "d1"}); err != nil {
		t.Fatal(err)
	}
	if len(e.events(posts.NotifyMention)) != 0 {
		t.Fatal("notified someone who cannot see the post")
	}
	// Edits notify only newly mentioned users.
	e.rec.Events = nil
	if _, err := e.p.Edit(e.as("alice"), pv.Ref, "hi @carol and @bob", ""); err != nil {
		t.Fatal(err)
	}
	ev = e.events(posts.NotifyMention)
	if len(ev) != 1 || ev[0].To != e.id("bob") {
		t.Fatalf("edit mention events %+v", ev)
	}
	if mt := e.d.Mentions.MentionTargets(e.ctx, id); len(mt) != 2 || mt[1] != e.id("bob") {
		t.Fatalf("mention index %v", mt)
	}
}

// Acceptance: edit keeps the id, bumps the version, uses a new salt and a new
// record; parents are immutable.
func TestEdit(t *testing.T) {
	e := newEnv(t)
	parent := e.post("bob", "parent")
	pv := e.post("alice", "first", ref(e.ref("alice", e.pid("bob", parent))))
	id := e.pid("alice", pv)
	var salt1 []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT body_salt FROM posts WHERE id = $1", id).Scan(&salt1); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	ed, err := e.p.Edit(e.as("alice"), pv.Ref, "second", "de")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Ref != pv.Ref || ed.Body != "second" || ed.Lang != "de" || ed.EditedAt == nil || len(ed.Context) != 1 {
		t.Fatalf("edited %+v", ed)
	}
	var (
		salt2   []byte
		version int
	)
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT body_salt, version FROM posts WHERE id = $1", id).Scan(&salt2, &version); err != nil {
		t.Fatal(err)
	}
	c, v := e.recordCommit(id)
	if version != 2 || v != 2 || bytes.Equal(salt1, salt2) || !bytes.Equal(c, commit(salt2, "second")) {
		t.Fatalf("version %d record v%d", version, v)
	}
	if e.count("SELECT count(*) FROM post_versions WHERE post_id = $1", id) != 2 ||
		e.count("SELECT count(*) FROM post_parents WHERE child_id = $1", id) != 1 {
		t.Fatal("versions or parents changed")
	}
	if m, _ := e.d.Posts.Meta(e.ctx, id); m.Version != 2 || m.EditedAt == nil {
		t.Fatalf("index meta %+v", m)
	}
	// Someone else's post is not found; problems apply.
	_, err = e.p.Edit(e.as("bob"), e.ref("bob", id), "mine now", "")
	wantNotFound(t, err)
	_, err = e.p.Edit(e.as("alice"), pv.Ref, " ", "")
	wantProblem(t, err, "body_empty")
}

// Acceptance: delete nulls body and salt and leaves children rendering as roots
// for others.
func TestDelete(t *testing.T) {
	e := newEnv(t)
	parent := e.post("alice", "parent @bob")
	pid := e.pid("alice", parent)
	child := e.post("bob", "child", ref(e.ref("bob", pid)))
	cid := e.pid("bob", child)
	wantNotFound(t, e.p.Delete(e.as("bob"), e.ref("bob", pid)))
	if err := e.p.Delete(e.as("alice"), parent.Ref); err != nil {
		t.Fatal(err)
	}
	var (
		body *string
		salt []byte
	)
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT body, body_salt FROM posts WHERE id = $1 AND deleted_at IS NOT NULL", pid).
		Scan(&body, &salt); err != nil {
		t.Fatal(err)
	}
	if body != nil || salt != nil {
		t.Fatal("body or salt survived")
	}
	if e.count("SELECT count(*) FROM signed_records WHERE kind = 5") != 1 ||
		e.count("SELECT count(*) FROM mentions WHERE post_id = $1", pid) != 0 {
		t.Fatal("tombstone or mentions")
	}
	_, err := e.p.Get(e.as("alice"), parent.Ref)
	wantNotFound(t, err)
	wantNotFound(t, e.p.Delete(e.as("alice"), parent.Ref))
	for _, h := range []string{"bob", "carol"} {
		cv, err := e.p.Get(e.as(h), e.ref(h, cid))
		if err != nil || len(cv.Context) != 0 || cv.Body != "child" {
			t.Fatalf("%s sees %+v %v", h, cv, err)
		}
	}
	_, err = e.p.Create(e.as("carol"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{ref(e.ref("carol", pid))}})
	wantNotFound(t, err)
}

// Acceptance: a probation author's post is d1 + audience_forced and renders to
// friends byte-identically to a voluntary d1.
func TestProbationForcesD1(t *testing.T) {
	e := newEnv(t)
	e.w.User("pat", coretest.InvitedBy(e.id("alice")))
	e.w.Friends("pat", "alice")
	e.exec("UPDATE users SET probation_until = $2 WHERE id = $1", int64(e.id("pat")), e.d.Clock.Now().Add(72*time.Hour))
	e.a.Users.Invalidate(e.id("pat"))
	forced, err := e.p.Create(e.as("pat"), posts.Draft{Body: "same", Lang: "en", Audience: "d3"})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Audience != "d1" || !forced.AudienceForced {
		t.Fatalf("author view %+v", forced)
	}
	e.exec("UPDATE users SET probation_cleared_at = $2 WHERE id = $1", int64(e.id("pat")), e.d.Clock.Now())
	e.a.Users.Invalidate(e.id("pat"))
	voluntary, err := e.p.Create(e.as("pat"), posts.Draft{Body: "same", Lang: "en", Audience: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	if voluntary.AudienceForced {
		t.Fatal("voluntary d1 marked forced")
	}
	if e.count("SELECT count(*) FROM posts WHERE audience = 1 AND audience_forced") != 1 {
		t.Fatal("stored flags")
	}
	fv, err := e.p.Get(e.as("alice"), e.ref("alice", e.pid("pat", forced)))
	if err != nil {
		t.Fatal(err)
	}
	vv, err := e.p.Get(e.as("alice"), e.ref("alice", e.pid("pat", voluntary)))
	if err != nil {
		t.Fatal(err)
	}
	fv.Ref, vv.Ref = "", ""
	fv.CreatedAt, vv.CreatedAt = time.Time{}, time.Time{}
	if js(t, fv) != js(t, vv) {
		t.Fatalf("friends tell them apart:\n%s\n%s", js(t, fv), js(t, vv))
	}
	_, err = e.p.Get(e.as("bob"), e.ref("bob", e.pid("pat", forced)))
	wantNotFound(t, err)
}

func TestSlipperyDefault(t *testing.T) {
	e := newEnv(t)
	e.exec("UPDATE users SET slippery_default = true WHERE id = $1", int64(e.id("alice")))
	e.a.Users.Invalidate(e.id("alice"))
	if pv := e.post("alice", "x"); pv.RefPolicy != "nobody" {
		t.Fatalf("policy %s", pv.RefPolicy)
	}
	pv, err := e.p.Create(e.as("alice"), posts.Draft{Body: "x", Lang: "en", RefPolicy: "anyone"})
	if err != nil || pv.RefPolicy != "anyone" {
		t.Fatalf("%+v %v", pv, err)
	}
}

func TestThread(t *testing.T) {
	e := newEnv(t)
	parent := e.post("bob", "question")
	parts := []posts.Draft{
		{Body: "one", Lang: "en", Audience: "d2", Parents: []posts.ParentIn{ref(e.ref("alice", e.pid("bob", parent)))}},
		{Body: "two", Lang: "en", Audience: "d2"},
		{Body: "three", Lang: "en", Audience: "d2"},
	}
	vs, err := e.p.CreateThread(e.as("alice"), parts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 {
		t.Fatalf("%d parts", len(vs))
	}
	for i, v := range vs {
		if v.Thread == nil || v.Thread.Index != i+1 || v.Thread.Total != 3 || v.Audience != "d2" {
			t.Fatalf("part %d %+v", i, v)
		}
	}
	ids := []core.PostID{e.pid("alice", vs[0]), e.pid("alice", vs[1]), e.pid("alice", vs[2])}
	if e.count("SELECT count(*) FROM post_parents WHERE kind = 1") != 2 ||
		e.count("SELECT count(*) FROM post_parents WHERE child_id = $1 AND parent_id = $2 AND kind = 1", ids[2], ids[1]) != 1 {
		t.Fatal("cont chain")
	}
	m0, _ := e.d.Posts.Meta(e.ctx, ids[0])
	m1, _ := e.d.Posts.Meta(e.ctx, ids[1])
	if !m0.IsReply || m0.ContChild != ids[1] || m1.ContParent != ids[0] || m1.ContChild != ids[2] || m1.IsReply {
		t.Fatalf("metas %+v %+v", m0, m1)
	}
	// Extending the thread with a cont on its last part works; a part with its
	// own parents does not.
	e.post("alice", "four", cont(e.ref("alice", ids[2])))
	_, err = e.p.CreateThread(e.as("alice"), []posts.Draft{{Body: "a", Lang: "en"}, {Body: "b", Lang: "en", Parents: []posts.ParentIn{ref(vs[0].Ref)}}})
	wantProblem(t, err, "bad_request")
	many := make([]posts.Draft, posts.MaxThreadParts+1)
	for i := range many {
		many[i] = posts.Draft{Body: "p", Lang: "en"}
	}
	_, err = e.p.CreateThread(e.as("alice"), many)
	wantProblem(t, err, "too_many_parts")
	_, err = e.p.CreateThread(e.as("alice"), nil)
	wantProblem(t, err, "body_empty")
}

func TestRefPolicyAndDetach(t *testing.T) {
	e := newEnv(t)
	p := e.post("alice", "parent")
	pid := e.pid("alice", p)
	offer, err := e.p.SetRefPolicy(e.as("alice"), p.Ref, "nobody")
	if err != nil || offer {
		t.Fatalf("no children: %v %v", offer, err)
	}
	if _, err := e.p.SetRefPolicy(e.as("alice"), p.Ref, "anyone"); err != nil {
		t.Fatal(err)
	}
	c1 := e.post("bob", "c1", ref(e.ref("bob", pid)))
	e.post("alice", "a self reply", ref(p.Ref))
	_, err = e.p.SetRefPolicy(e.as("alice"), p.Ref, "sometimes")
	wantProblem(t, err, "ref_policy_invalid")
	_, err = e.p.SetRefPolicy(e.as("bob"), e.ref("bob", pid), "nobody")
	wantNotFound(t, err)
	offer, err = e.p.SetRefPolicy(e.as("alice"), p.Ref, "nobody")
	if err != nil || !offer {
		t.Fatalf("offer: %v %v", offer, err)
	}
	_, err = e.p.Create(e.as("bob"), posts.Draft{Body: "x", Lang: "en", Parents: []posts.ParentIn{ref(e.ref("bob", pid))}})
	wantProblem(t, err, "ref_not_allowed")

	// Detach one: the link disappears, the child stays.
	c1id := e.pid("bob", c1)
	if err := e.p.Detach(e.as("alice"), p.Ref, e.ref("alice", c1id)); err != nil {
		t.Fatal(err)
	}
	wantNotFound(t, e.p.Detach(e.as("alice"), p.Ref, e.ref("alice", c1id))) // already detached
	wantNotFound(t, e.p.Detach(e.as("bob"), e.ref("bob", pid), c1.Ref))     // not bob's post
	cv, err := e.p.Get(e.as("bob"), c1.Ref)
	if err != nil || len(cv.Context) != 0 {
		t.Fatalf("detached child %+v %v", cv, err)
	}
	pv, err := e.p.Get(e.as("alice"), p.Ref)
	if err != nil || pv.ReplyCount != 1 {
		t.Fatalf("parent %+v %v", pv, err)
	}
	if err := e.p.DetachAll(e.as("alice"), p.Ref); err != nil {
		t.Fatal(err)
	}
	if pv, _ := e.p.Get(e.as("alice"), p.Ref); pv.ReplyCount != 0 {
		t.Fatalf("after detach-all %+v", pv)
	}
	if e.count("SELECT count(*) FROM post_parents WHERE parent_id = $1 AND detached", pid) != 2 {
		t.Fatal("detached rows")
	}
	offer, err = e.p.SetRefPolicy(e.as("alice"), p.Ref, "nobody")
	if err != nil || offer {
		t.Fatalf("offer after detach-all: %v %v", offer, err)
	}

	// Continuations cannot be detached.
	next := e.post("alice", "more", cont(p.Ref))
	wantProblem(t, e.p.Detach(e.as("alice"), p.Ref, next.Ref), "cont_not_detachable")
}

func TestDeleteAllBy(t *testing.T) {
	e := newEnv(t)
	e.post("alice", "one")
	e.post("alice", "two")
	gone := e.post("alice", "three")
	if err := e.p.Delete(e.as("alice"), gone.Ref); err != nil {
		t.Fatal(err)
	}
	e.post("bob", "bob's")
	tx, err := e.d.DB.Pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := e.p.DeleteAllBy(e.ctx, tx, e.id("alice"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || e.count("SELECT count(*) FROM posts WHERE author_id = $1 AND body IS NOT NULL", int64(e.id("alice"))) != 0 ||
		e.count("SELECT count(*) FROM posts WHERE body IS NOT NULL") != 1 || e.count("SELECT count(*) FROM signed_records WHERE kind = 5") != 3 {
		t.Fatalf("deleted %d", len(ids))
	}
}

// The detach-all offer counts references the author could see before the
// change, also from people beyond their friends (who stop seeing the link once
// the post is slippery, §10.1.3).
func TestDetachAllOfferCountsDistantReplies(t *testing.T) {
	e := newEnv(t)
	p := e.post("alice", "parent")
	e.post("carol", "from two hops", ref(e.ref("carol", e.pid("alice", p))))
	offer, err := e.p.SetRefPolicy(e.as("alice"), p.Ref, "nobody")
	if err != nil || !offer {
		t.Fatalf("offer %v %v", offer, err)
	}
}
