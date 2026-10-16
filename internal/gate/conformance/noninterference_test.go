package conformance_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/feed"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
)

// V is the viewer of every generated world.
const V = "va00"

// svcAct is something another person does through the gate services after the
// fixtures are loaded (reports, invites, likes, replies, blocks: plan §7's
// "reports, invites in B" and activity that produces notifications).
type svcAct struct {
	Kind  string
	Actor string
	Post  string
	Other string
	Aud   string
}

// genSvc draws service-level actions of people other than V that leave H(V)
// unchanged (per the oracle), applying their effect to m.
func genSvc(t *rapid.T, m *model, prefix string, vBlocks []string) []svcAct {
	want := horizons(m, V, vBlocks)
	var acts []svcAct
	var hs []string
	for _, u := range m.Users {
		if u.Handle != V {
			hs = append(hs, u.Handle)
		}
	}
	n := rapid.IntRange(0, 6).Draw(t, prefix+"nSvc")
	for i := range n {
		a := svcAct{Kind: rapid.SampledFrom([]string{"reply", "like", "invite", "report", "block"}).Draw(t, prefix+"svc"),
			Actor: rapid.SampledFrom(hs).Draw(t, "actor")}
		try := m.clone()
		switch a.Kind {
		case "reply":
			parent := try.Posts[rapid.IntRange(0, len(try.Posts)-1).Draw(t, "parent")]
			a.Post = parent.Label
			a.Aud = rapid.SampledFrom([]string{"connected", "d1", "d2", "d3"}).Draw(t, "aud")
			p := mPost{Label: fmt.Sprintf("%ss%02d", prefix, i), Author: a.Actor, Pol: core.PolAnyone, svc: true,
				Parents: []mParent{{Post: parent.Label, Shadow: try.blocks(parent.Author, a.Actor)}}}
			for _, au := range []core.Audience{core.AudConnected, core.AudD1, core.AudD2, core.AudD3} {
				if au.String() == a.Aud {
					p.Aud = au
				}
			}
			if u, _ := try.user(a.Actor); u.Probation {
				p.Forced = true
				p.Aud = effAudience(p)
			}
			try.Posts = append(try.Posts, p)
		case "like":
			a.Post = try.Posts[rapid.IntRange(0, len(try.Posts)-1).Draw(t, "post")].Label
			if try.liked(a.Actor, a.Post) {
				continue
			}
			try.Likes = append(try.Likes, mLike{User: a.Actor, Post: a.Post})
		case "report":
			a.Post = try.Posts[rapid.IntRange(0, len(try.Posts)-1).Draw(t, "post")].Label
		case "block":
			a.Other = rapid.SampledFrom(append(slices.Clone(hs), V)).Draw(t, "other")
			if a.Other == a.Actor || try.friends(a.Actor, a.Other) || try.blocks(a.Actor, a.Other) {
				continue
			}
			try.Blocks = append(try.Blocks, [2]string{a.Actor, a.Other})
		}
		if horizons(try, V, vBlocks) != want {
			continue
		}
		*m = *try
		acts = append(acts, a)
	}
	return acts
}

// runSvc performs the actions as their actors (in the other-people ID lane).
// Failures are part of the world (an actor may not see the target).
func (w *world) runSvc(acts []svcAct) {
	w.other(func() {
		for _, a := range acts {
			ctx := w.as(a.Actor)
			switch a.Kind {
			case "reply":
				_, _ = w.a.Posts.Create(ctx, posts.Draft{Body: "svc reply", Lang: "en", Audience: a.Aud,
					Parents: []posts.ParentIn{{Ref: w.postRef(a.Actor, a.Post), Kind: "ref"}}})
			case "like":
				_ = w.a.Engage.Like(ctx, w.postRef(a.Actor, a.Post), true)
			case "invite":
				_, _ = w.a.Invites.Open(ctx, "qr", false)
			case "report":
				_ = w.a.Moderation.Report(ctx, "post", w.postRef(a.Actor, a.Post), "spam", "")
			case "block":
				_ = w.a.Friends.Block(ctx, w.userRef(a.Actor, a.Other))
			}
		}
	})
}

// checks skips the database-backed properties under -short.
func checks(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("conformance worlds need Postgres; skipped in -short")
	}
}

// TestNoninterference is the plan §7 property: W1 and W2 differ only outside
// H(V); replaying V's calls through the gate services gives identical
// normalized outputs (bodies, errors, counts, orderings, notifications).
func TestNoninterference(t *testing.T) {
	checks(t)
	rapid.Check(t, func(rt *rapid.T) {
		base := genBase(rt)
		ops := genOps(rt, base)
		vb := vBlocks(base, ops)
		m1, e1 := genExtras(rt, base, V, "xa", vb)
		m2, e2 := genExtras(rt, base, V, "xb", vb)
		s1 := genSvc(rt, m1, "xa", vb)
		s2 := genSvc(rt, m2, "xb", vb)

		sc := &scope{TB: t}
		defer sc.close()
		w1 := newWorld(sc, m1, baseLayer(base), e1)
		w1.runSvc(s1)
		w2 := newWorld(sc, m2, baseLayer(base), e2)
		w2.runSvc(s2)

		// Oracle properties on W1 (fresh, before V acts).
		checkOracle(rt, w1)

		r1, r2 := newReplayer(w1, base, V), newReplayer(w2, base, V)
		r1.run(ops)
		r2.run(ops)
		if i, msg := firstDiff(r1.out, r2.out); i >= 0 {
			rt.Fatalf("noninterference: %s\n--- base: %s\n--- W1 extras: %s\n--- W2 extras: %s\n--- ops: %+v", msg,
				describe(baseLayer(base), nil), describe(e1, s1), describe(e2, s2), ops)
		}
		checkIsolation(rt, w1, r1, ops)
		checkForeignTokens(rt, w1, r1)
	})
}

// vBlocks lists the base users V blocks during ops, in order.
func vBlocks(base *model, ops []vop) []string {
	var out []string
	for _, op := range ops {
		if op.Kind == "block" && op.Target < len(base.Users) {
			out = append(out, base.Users[op.Target].Handle)
		}
	}
	return out
}

func describe(l *layer, acts []svcAct) string {
	var parts []string
	for _, u := range l.users {
		parts = append(parts, fmt.Sprintf("user %s inv=%q; ", u.Handle, u.Inviter))
	}
	for _, e := range l.edges {
		parts = append(parts, fmt.Sprintf("edge %s–%s; ", e.A, e.B))
	}
	for _, x := range l.blocks {
		parts = append(parts, fmt.Sprintf("block %s→%s; ", x[0], x[1]))
	}
	for _, p := range l.posts {
		parts = append(parts, fmt.Sprintf("post %s by %s aud=%s parents=%v; ", p.Label, p.Author, p.Aud, p.Parents))
	}
	for _, lk := range l.likes {
		parts = append(parts, fmt.Sprintf("like %s %s; ", lk.User, lk.Post))
	}
	for _, a := range acts {
		parts = append(parts, fmt.Sprintf("svc %+v; ", a))
	}
	return strings.Join(parts, "")
}

// ---- oracle properties (plan §7 "additional properties") ----

// checkOracle compares V's fresh view of w against the reference oracle: people
// (chain oracle, inviter labels, oracle uniformity) and the whole feed under
// Latest and Balanced (exact visibility, tier = reference distance, Latest is
// chronological, Balanced is a permutation of it, reply counts, context rows,
// in-network like counts).
func checkOracle(t *rapid.T, w *world) {
	o := newOracle(w.m, V)
	ctx := w.as(V)
	vid := w.id(V)
	for _, u := range w.m.Users {
		pv, err := w.a.Friends.Person(ctx, w.userRef(V, u.Handle))
		d, reach := o.distOf(u.Handle)
		if !reach {
			if w.m.blocks(V, u.Handle) {
				continue // V's own block: V's blocks list shows them
			}
			if err == nil || err.Error() != gate.ErrNotFound.Error() {
				t.Fatalf("person %s outside the horizon: %v %+v, want the uniform not-found", u.Handle, err, pv)
			}
			continue
		}
		if err != nil {
			t.Fatalf("person %s (dist %d): %v", u.Handle, d, err)
		}
		checkPerson(t, o, u, pv.Handle, pv.DisplayName, pv.Inviter, pv.Chain)
	}
	// Uniform not-found for every post V cannot see and for refs that are not posts.
	for _, p := range w.m.Posts {
		if p.svc || o.visible(p) {
			continue
		}
		for name, call := range map[string]func() error{
			"get":    func() error { _, err := w.a.Posts.Get(ctx, w.postRef(V, p.Label)); return err },
			"why":    func() error { _, err := w.a.Feed.Why(ctx, w.postRef(V, p.Label)); return err },
			"likers": func() error { _, err := w.a.Engage.Likers(ctx, w.postRef(V, p.Label)); return err },
			"tree":   func() error { _, err := w.a.Tree.Open(ctx, w.postRef(V, p.Label), ""); return err },
		} {
			if err := call(); err == nil || err.Error() != gate.ErrNotFound.Error() || !errors.Is(err, gate.ErrNotFound) {
				t.Fatalf("%s on invisible post %s: %v, want the uniform not-found", name, p.Label, err)
			}
		}
	}

	byID := map[core.PostID]mPost{}
	for _, p := range w.m.Posts {
		if !p.svc {
			byID[w.postID(p.Label)] = p
		}
	}
	want := map[string]bool{}
	for _, p := range w.m.Posts {
		if !p.svc && p.Author != V && o.visible(p) {
			want[p.Label] = true
		}
	}
	tiers := map[string]map[int][]string{}
	for _, preset := range []string{"latest", "balanced"} {
		got := map[int][]string{}
		seen := map[string]bool{}
		lastTier := 0
		var lastAt time.Time
		take := func(pv gate.PostView, inGroup bool) {
			id, ok := w.d.Refs.OpenPost(vid, pv.Ref)
			if !ok {
				t.Fatalf("%s feed: post ref does not open for V", preset)
			}
			p, ok := byID[id]
			if !ok {
				t.Fatalf("%s feed: unknown post %s", preset, pv.Ref)
			}
			if !want[p.Label] {
				t.Fatalf("%s feed served %s (by %s) which V cannot see", preset, p.Label, p.Author)
			}
			if seen[p.Label] {
				t.Fatalf("%s feed served %s twice", preset, p.Label)
			}
			seen[p.Label] = true
			d, _ := o.distOf(p.Author)
			if pv.Tier != d || pv.Author.Chain.Dist != d {
				t.Fatalf("%s feed: %s at tier %d (chain %d), reference distance %d", preset, p.Label, pv.Tier,
					pv.Author.Chain.Dist, d)
			}
			au, _ := w.m.user(p.Author)
			checkPerson(t, o, au, pv.Author.Handle, pv.Author.DisplayName, pv.Author.Inviter, pv.Author.Chain)
			checkPostView(t, w, o, p, pv)
			got[d] = append(got[d], p.Label)
			if preset == "latest" && !inGroup {
				at := now.Add(-time.Duration(p.AgeMin) * time.Minute)
				if d == lastTier && at.After(lastAt) {
					t.Fatalf("latest feed is not chronological at %s", p.Label)
				}
				lastTier, lastAt = d, at
			}
		}
		cursor := ""
		curTier := 0
		for page := 0; ; page++ {
			if page > 50 {
				t.Fatalf("%s feed does not end", preset)
			}
			fp, err := w.a.Feed.Page(ctx, preset, cursor)
			if err != nil {
				t.Fatalf("%s feed page %d: %v", preset, page, err)
			}
			for _, it := range fp.Items {
				switch it.Type {
				case feed.ItemTierStart:
					if it.Tier <= curTier {
						t.Fatalf("%s feed: tier %d starts after tier %d", preset, it.Tier, curTier)
					}
					curTier = it.Tier
				case feed.ItemPost:
					if it.Post.Tier != curTier {
						t.Fatalf("%s feed: tier-%d post under the tier-%d header", preset, it.Post.Tier, curTier)
					}
					take(*it.Post, false)
				case feed.ItemThread:
					t.Fatalf("%s feed: unexpected thread item (no generated continuations)", preset)
				case feed.ItemCollapsed:
					if curTier < feed.CollapseFrom {
						t.Fatalf("%s feed: collapsed group at tier %d", preset, curTier)
					}
					items, err := w.a.Feed.Expand(ctx, it.Collapsed.Token)
					if err != nil {
						t.Fatalf("expand: %v", err)
					}
					if len(items) != it.Collapsed.Count {
						t.Fatalf("collapsed group of %d expands to %d", it.Collapsed.Count, len(items))
					}
					for _, x := range items {
						if x.Post == nil {
							t.Fatalf("expanded item without a post: %+v", x)
						}
						take(*x.Post, true)
					}
				}
			}
			if fp.Next == "" {
				break
			}
			cursor = fp.Next
		}
		for l := range want {
			if !seen[l] {
				p, _ := w.m.post(l)
				d, _ := o.distOf(p.Author)
				t.Fatalf("%s feed never served %s (by %s, dist %d, aud %s)", preset, l, p.Author, d, p.Aud)
			}
		}
		tiers[preset] = got
	}
	// Balanced is a permutation of Latest within every tier.
	for k, lat := range tiers["latest"] {
		bal := slices.Clone(tiers["balanced"][k])
		lat = slices.Clone(lat)
		slices.Sort(bal)
		slices.Sort(lat)
		if !slices.Equal(bal, lat) {
			t.Fatalf("tier %d: balanced %v is not a permutation of latest %v", k, bal, lat)
		}
	}
}

// checkPerson is the chain oracle (plan §7): dist = reference BFS; first hop =
// reference first hop; len(chain) = dist; a named middle only when every
// named person opted in, and then a true shortest path; the inviter label.
func checkPerson(t *rapid.T, o *oracle, u mUser, handle, name string, inv gate.InviterLabel, c gate.ChainView) {
	d, _ := o.distOf(u.Handle)
	if handle != u.Handle || name != displayName(u.Handle) {
		t.Fatalf("person %s shown as %q / %q", u.Handle, handle, name)
	}
	wantInv := gate.InviterLabel{Kind: "genesis"}
	if u.Inviter != "" {
		wantInv = gate.InviterLabel{Kind: "user", Handle: u.Inviter}
	}
	if inv != wantInv {
		t.Fatalf("person %s: inviter label %+v, want %+v", u.Handle, inv, wantInv)
	}
	if c.Dist != d {
		t.Fatalf("person %s: chain dist %d, reference %d", u.Handle, c.Dist, d)
	}
	if d <= 1 {
		if c.FirstHop != nil || len(c.Middle) != 0 || c.HiddenMiddle != 0 {
			t.Fatalf("person %s at dist %d has a chain %+v", u.Handle, d, c)
		}
		return
	}
	fh := o.byID[o.first[o.m.id(u.Handle)]].Handle
	if c.FirstHop == nil || c.FirstHop.Handle != fh {
		t.Fatalf("person %s: first hop %+v, reference %s", u.Handle, c.FirstHop, fh)
	}
	if len(c.Middle)+c.HiddenMiddle != d-2 || (len(c.Middle) > 0 && c.HiddenMiddle > 0) {
		t.Fatalf("person %s: chain %+v does not have length %d", u.Handle, c, d)
	}
	path := []string{V, fh}
	for _, h := range c.Middle {
		path = append(path, h.Handle)
	}
	path = append(path, u.Handle)
	if len(c.Middle) > 0 {
		for _, h := range path[1:] {
			if mu, _ := o.m.user(h); !mu.NameInChains {
				t.Fatalf("person %s: middle named although %s did not opt in", u.Handle, h)
			}
		}
		for i, h := range path {
			if hd, ok := o.distOf(h); !ok || hd != i {
				t.Fatalf("person %s: chain %v is not a shortest path (%s at %d)", u.Handle, path, h, hd)
			}
			if i > 0 && !o.m.friends(path[i-1], h) {
				t.Fatalf("person %s: chain %v has non-adjacent neighbours %s, %s", u.Handle, path, path[i-1], h)
			}
		}
	}
}

// checkPostView: body, reply count, context rows and in-network likes.
func checkPostView(t *rapid.T, w *world, o *oracle, p mPost, pv gate.PostView) {
	if pv.Body != body(p.Label) {
		t.Fatalf("post %s body %q", p.Label, pv.Body)
	}
	replies := 0
	for _, c := range w.m.Posts {
		for _, pp := range c.Parents {
			if pp.Post == p.Label && o.linkShown(c, pp) {
				replies++
			}
		}
	}
	if pv.ReplyCount != replies {
		t.Fatalf("post %s reply count %d, oracle %d", p.Label, pv.ReplyCount, replies)
	}
	var ctxWant []gate.Ref
	for _, pp := range p.Parents {
		if o.linkShown(p, pp) {
			ctxWant = append(ctxWant, w.postRef(V, pp.Post))
		}
	}
	var ctxGot []gate.Ref
	for _, c := range pv.Context {
		ctxGot = append(ctxGot, c.Ref)
	}
	if !slices.Equal(ctxGot, ctxWant) {
		t.Fatalf("post %s context %v, oracle %v", p.Label, ctxGot, ctxWant)
	}
	likes := 0
	for _, l := range w.m.Likes {
		if l.Post != p.Label {
			continue
		}
		if d, ok := o.distOf(l.User); ok && d <= 2 {
			likes++ // d = 0 is V's own like
		}
	}
	if pv.Likes.InNetwork != likes {
		t.Fatalf("post %s in-network likes %d, oracle %d", p.Label, pv.Likes.InNetwork, likes)
	}
}

// ---- component isolation (§13) ----

// checkIsolation scans every response V received for identifiers of people V
// cannot see (handles, display names, refs minted under any viewer), bodies of
// posts V cannot see and refs of those posts, and for refs minted for any
// other viewer. The only allowed hit is the inviter label of a user V can see
// (and what V itself typed or blocked).
func checkIsolation(t *rapid.T, w *world, r *replayer, ops []vop) {
	o := newOracle(w.m, V)
	forbidden := map[string]string{}
	allowedInviter := map[string]bool{}
	for id := range o.dist {
		if inv := o.byID[id].Inviter; inv != "" {
			allowedInviter[inv] = true
		}
	}
	for _, b := range w.m.Blocks {
		if u, ok := w.m.user(b[1]); ok && b[0] == V && u.Inviter != "" {
			allowedInviter[u.Inviter] = true // V's own blocks list labels the people V blocked
		}
	}
	typed := map[string]bool{}
	for _, op := range ops {
		if op.Kind == "create" {
			typed[r.mention(op)] = true
		}
	}
	vid := w.id(V)
	for _, u := range w.m.Users {
		_, reach := o.distOf(u.Handle)
		if !reach && !w.m.blocks(V, u.Handle) {
			if !typed[u.Handle] {
				forbidden[u.Handle] = "handle of " + u.Handle
			}
			forbidden[displayName(u.Handle)] = "display name of " + u.Handle
			forbidden[string(w.userRef(V, u.Handle))] = "V's ref of " + u.Handle
		}
		for _, x := range w.m.Users {
			if x.Handle != V {
				forbidden[string(w.d.Refs.User(w.id(x.Handle), u.id()))] = "a ref of " + u.Handle + " minted for " + x.Handle
			}
		}
	}
	for _, p := range w.m.Posts {
		if p.svc {
			continue
		}
		if !o.visible(p) {
			forbidden[body(p.Label)] = "body of invisible post " + p.Label
			forbidden[string(w.d.Refs.Post(vid, w.postID(p.Label)))] = "V's ref of invisible post " + p.Label
		}
		for _, x := range w.m.Users {
			if x.Handle != V {
				forbidden[string(w.d.Refs.Post(w.id(x.Handle), w.postID(p.Label)))] = "a ref of " + p.Label + " minted for " + x.Handle
			}
		}
	}
	var scan func(v any, key, parent string, at int)
	scan = func(v any, key, parent string, at int) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				scan(c, k, key, at)
			}
		case []any:
			for _, c := range x {
				scan(c, key, parent, at)
			}
		case string:
			if why, ok := forbidden[x]; ok && (key != "handle" || parent != "inviter" || !allowedInviter[x]) {
				t.Fatalf("component isolation: %s in V's response %s", why, r.out[at])
			}
			for _, word := range strings.FieldsFunc(x, func(c rune) bool {
				return !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_'
			}) {
				if why, ok := forbidden[word]; ok && word != x {
					t.Fatalf("component isolation: %s inside %q in V's response %s", why, x, r.out[at])
				}
			}
		}
	}
	for i, v := range r.vals {
		scan(v, "", "", i)
	}
}

// checkForeignTokens: another viewer presenting V's session, cursor, collapse
// token or tree snapshot gets the uniform not-found (security #2).
func checkForeignTokens(t *rapid.T, w *world, r *replayer) {
	other := "vb00"
	ctx := w.as(other)
	fail := func(what string, err error) {
		if err == nil || err.Error() != gate.ErrNotFound.Error() {
			t.Fatalf("foreign %s: %v, want the uniform not-found", what, err)
		}
	}
	for _, c := range r.cursors {
		_, err := w.a.Feed.Page(ctx, "", c)
		fail("cursor", err)
	}
	for _, s := range r.sessions {
		_, _, err := w.a.Feed.NewAbove(ctx, s)
		fail("session", err)
	}
	for _, c := range r.collapse {
		_, err := w.a.Feed.Expand(ctx, c)
		fail("collapse token", err)
	}
	for _, s := range r.snapshots {
		_, err := w.a.Tree.Row(ctx, s, 1, garbageRef)
		fail("snapshot", err)
	}
}
