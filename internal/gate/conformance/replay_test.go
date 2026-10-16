package conformance_test

import (
	"fmt"
	"time"

	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
)

// vop is one of V's calls. Targets are indexes resolved per world by the
// replayer (base posts and users have the same IDs, so the same refs, in both).
type vop struct {
	Kind    string
	Target  int   // post or user target
	Targets []int // parents / seen sets
	Preset  string
	On      bool
	Aud     string
	Mention int
	Minutes int
}

const (
	maxCreated   = 4
	extraTargets = 8 // created posts, garbage, nonexistent, foreign …
)

var presets = []string{"", "latest", "balanced", "top"}

// genOps draws V's call sequence (independent of the worlds it is replayed in).
func genOps(t *rapid.T, base *model) []vop {
	nPost := len(base.Posts) + extraTargets
	nUser := len(base.Users) + 3
	postT := func(label string) int { return rapid.IntRange(0, nPost-1).Draw(t, label) }
	userT := func(label string) int { return rapid.IntRange(0, nUser-1).Draw(t, label) }
	n := rapid.IntRange(4, 22).Draw(t, "nOps")
	ops := []vop{{Kind: "feed", Preset: ""}}
	for range n {
		k := rapid.SampledFrom([]string{"feed", "why", "tree", "tree", "person", "likers", "get", "lists", "create",
			"create", "like", "request", "seen", "advance", "report", "hide", "mute", "invite", "block", "unfriend", "muteuser"}).Draw(t, "op")
		op := vop{Kind: k}
		switch k {
		case "feed", "tree":
			op.Preset = rapid.SampledFrom(presets).Draw(t, "preset")
			op.Target = postT("target")
		case "why", "likers", "get", "report", "hide", "mute":
			op.Target = postT("target")
		case "person", "request", "block", "unfriend", "muteuser":
			op.Target = userT("target")
		case "like":
			op.Target, op.On = postT("target"), rapid.IntRange(0, 3).Draw(t, "on") > 0
		case "create":
			for range rapid.IntRange(0, 3).Draw(t, "nParents") {
				op.Targets = append(op.Targets, postT("parent"))
			}
			op.Aud = rapid.SampledFrom([]string{"", "", "d1", "d2", "d3"}).Draw(t, "aud")
			op.Mention = rapid.IntRange(0, len(base.Users)+3).Draw(t, "mention")
		case "seen":
			for range rapid.IntRange(1, 3).Draw(t, "nSeen") {
				op.Targets = append(op.Targets, postT("seen"))
			}
		case "advance":
			op.Minutes = rapid.IntRange(1, 180).Draw(t, "minutes")
		}
		ops = append(ops, op)
	}
	ops = append(ops, vop{Kind: "lists"}, vop{Kind: "feed", Preset: "latest"})
	return ops
}

// replayer runs V's calls in one world and records normalized outputs.
type replayer struct {
	w       *world
	v       string
	n       *normalizer
	out     []string
	vals    []any // the normalized outputs, decoded (component isolation scan)
	created []gate.Ref
	posts   []string // base post labels (target order)
	users   []string // base user handles (target order)
	// tokens kept for the foreign-token check (raw, this world's).
	sessions, cursors, snapshots, collapse []string
}

func newReplayer(w *world, base *model, v string) *replayer {
	r := &replayer{w: w, v: v, n: newNormalizer()}
	for _, p := range base.Posts {
		r.posts = append(r.posts, p.Label)
	}
	for _, u := range base.Users {
		r.users = append(r.users, u.Handle)
	}
	return r
}

func (r *replayer) rec(name string, out any, err error) { r.recX(name, out, err, nil) }

func (r *replayer) recX(name string, out any, err error, extra map[string]string) {
	line, v := r.n.record(name, out, err, extra)
	r.out = append(r.out, line)
	r.vals = append(r.vals, v)
}

// mention is the handle V's create op mentions: a base user, or a name that
// is an extras user in one world only.
func (r *replayer) mention(op vop) string {
	if op.Mention < len(r.users) {
		return r.users[op.Mention]
	}
	return fmt.Sprintf("x%su%02d", []string{"a", "b"}[op.Mention%2], op.Mention%3)
}

// postRef resolves a post target for V.
func (r *replayer) postRef(t int) gate.Ref {
	w, vid := r.w, r.w.id(r.v)
	switch nb := len(r.posts); {
	case t < nb:
		return w.d.Refs.Post(vid, w.postID(r.posts[t]))
	case t < nb+maxCreated:
		if k := t - nb; k < len(r.created) {
			return r.created[k]
		}
		return garbageRef
	case t == nb+maxCreated:
		return w.d.Refs.Post(vid, randomPostID(t)) // well-formed, no such post
	case t == nb+maxCreated+1 && len(r.users) > 1:
		return w.d.Refs.Post(w.id(r.users[1]), w.postID(r.posts[0])) // another viewer's ref
	}
	return garbageRef
}

// userRef resolves a user target for V.
func (r *replayer) userRef(t int) gate.Ref {
	w, vid := r.w, r.w.id(r.v)
	switch nb := len(r.users); {
	case t < nb:
		return w.d.Refs.User(vid, w.id(r.users[t]))
	case t == nb:
		return w.d.Refs.User(vid, 77) // well-formed, no such user
	case t == nb+1:
		return w.d.Refs.User(w.id(r.users[1]), vid) // another viewer's ref
	}
	return garbageRef
}

func (r *replayer) run(ops []vop) {
	for i, op := range ops {
		r.do(i, op)
	}
}

func (r *replayer) do(i int, op vop) {
	w := r.w
	ctx := w.as(r.v)
	name := fmt.Sprintf("#%d %s", i, op.Kind)
	switch op.Kind {
	case "feed":
		r.feed(name, op.Preset)
	case "why":
		out, err := w.a.Feed.Why(ctx, r.postRef(op.Target))
		r.rec(name, out, err)
	case "tree":
		r.tree(name, op.Target, op.Preset)
	case "person":
		ref := r.userRef(op.Target)
		pv, err := w.a.Friends.Person(ctx, ref)
		r.rec(name, pv, err)
		cursor := ""
		for page := 0; page < 10; page++ {
			out, err := w.a.Friends.PersonPosts(ctx, ref, cursor)
			r.rec(name+" posts", out, err)
			if err != nil || out.Next == "" {
				break
			}
			cursor = out.Next
			r.cursors = append(r.cursors, cursor)
		}
	case "likers":
		out, err := w.a.Engage.Likers(ctx, r.postRef(op.Target))
		r.rec(name, out, err)
	case "get":
		out, err := w.a.Posts.Get(ctx, r.postRef(op.Target))
		r.rec(name, out, err)
	case "lists":
		r.lists(name)
	case "create":
		d := posts.Draft{Body: fmt.Sprintf("v says hello %d @%s", i, r.mention(op)), Lang: "en", Audience: op.Aud}
		for _, t := range op.Targets {
			d.Parents = append(d.Parents, posts.ParentIn{Ref: r.postRef(t), Kind: "ref"})
		}
		pv, err := w.a.Posts.Create(ctx, d)
		r.rec(name, pv, err)
		if err == nil && len(r.created) < maxCreated {
			r.created = append(r.created, pv.Ref)
		}
	case "like":
		r.rec(name, nil, w.a.Engage.Like(ctx, r.postRef(op.Target), op.On))
	case "request":
		r.rec(name, nil, w.a.Friends.Request(ctx, r.userRef(op.Target)))
	case "block":
		r.rec(name, nil, w.a.Friends.Block(ctx, r.userRef(op.Target)))
	case "muteuser":
		r.rec(name, nil, w.a.Friends.Mute(ctx, r.userRef(op.Target), true))
	case "unfriend":
		r.rec(name, nil, w.a.Friends.Unfriend(ctx, r.userRef(op.Target)))
	case "seen":
		var refs []gate.Ref
		for _, t := range op.Targets {
			refs = append(refs, r.postRef(t))
		}
		r.rec(name, nil, w.a.Engage.MarkSeen(ctx, refs))
	case "advance":
		w.clock.Advance(time.Duration(op.Minutes) * time.Minute)
		r.rec(name, op.Minutes, nil)
	case "report":
		r.rec(name, nil, w.a.Moderation.Report(ctx, "post", r.postRef(op.Target), "spam", ""))
	case "hide":
		r.rec(name, nil, w.a.Engage.Hide(ctx, r.postRef(op.Target)))
	case "mute":
		r.rec(name, nil, w.a.Notify.MutePost(ctx, r.postRef(op.Target), true))
	case "invite":
		hv, err := w.a.Invites.Open(ctx, "qr", false)
		r.rec(name, hv, err)
		if err == nil {
			st, err := w.a.Invites.Status(ctx, hv.HS)
			r.rec(name+" status", st, err)
		}
	default:
		panic("conformance: unknown op " + op.Kind)
	}
}

// feed reads every page of a new session (all tiers) and expands every
// collapsed group, then asks for the count of newer posts.
func (r *replayer) feed(name, preset string) {
	w := r.w
	ctx := w.as(r.v)
	cursor, session := "", ""
	for page := 0; page < 40; page++ {
		fp, err := w.a.Feed.Page(ctx, preset, cursor)
		r.rec(fmt.Sprintf("%s page %d", name, page), fp, err)
		if err != nil {
			return
		}
		session = fp.Session
		r.sessions = append(r.sessions, fp.Session)
		for _, it := range fp.Items {
			if it.Collapsed != nil {
				r.collapse = append(r.collapse, it.Collapsed.Token)
				items, err := w.a.Feed.Expand(ctx, it.Collapsed.Token)
				r.rec(name+" expand", items, err)
			}
		}
		if fp.Next == "" {
			break
		}
		cursor = fp.Next
		r.cursors = append(r.cursors, cursor)
	}
	if session != "" {
		a, b, err := w.a.Feed.NewAbove(ctx, session)
		r.rec(name+" new-above", []int{a, b}, err)
	}
}

// tree opens a family tree and walks one row further up and down, plus every
// row's next page.
func (r *replayer) tree(name string, target int, preset string) {
	w := r.w
	ctx := w.as(r.v)
	tv, err := w.a.Tree.Open(ctx, r.postRef(target), preset)
	r.rec(name, tv, err)
	if err != nil {
		return
	}
	r.snapshots = append(r.snapshots, tv.Snapshot)
	lo, hi := 0, 0
	var loRef, hiRef gate.Ref
	for _, row := range tv.Rows {
		if row.Cursor != "" {
			r.cursors = append(r.cursors, row.Cursor)
			more, err := w.a.Tree.More(ctx, tv.Snapshot, row.Gen, row.Center, row.Cursor)
			r.rec(name+" more", more, err)
		}
		if len(row.Items) == 0 {
			continue
		}
		if row.Gen <= lo {
			lo, loRef = row.Gen, row.Items[0].Post.Ref
		}
		if row.Gen >= hi {
			hi, hiRef = row.Gen, row.Items[len(row.Items)-1].Post.Ref
		}
	}
	if loRef != "" {
		row, err := w.a.Tree.Row(ctx, tv.Snapshot, lo-1, loRef)
		r.rec(name+" up", row, err)
	}
	if hiRef != "" {
		row, err := w.a.Tree.Row(ctx, tv.Snapshot, hi+1, hiRef)
		r.rec(name+" down", row, err)
	}
}

func (r *replayer) lists(name string) {
	w := r.w
	ctx := w.as(r.v)
	cursor := ""
	for page := 0; page < 10; page++ {
		fl, err := w.a.Friends.List(ctx, cursor)
		r.rec(name+" friends", fl, err)
		if err != nil || fl.Next == "" {
			break
		}
		cursor = fl.Next
	}
	in, outReq, err := w.a.Friends.Requests(ctx)
	r.rec(name+" requests", []any{in, outReq}, err)
	sugg, err := w.a.Friends.Suggestions(ctx)
	r.rec(name+" suggestions", sugg, err)
	bl, err := w.a.Friends.Blocks(ctx)
	r.rec(name+" blocks", bl, err)
	bud, err := w.a.Invites.Budget(ctx)
	r.rec(name+" budget", bud, err)
	inv, err := w.a.Invites.Invitees(ctx)
	r.rec(name+" invitees", inv, err)
	nl, err := w.a.Notify.List(ctx, "")
	r.recX(name+" notifications", nl, err, map[string]string{"id": "notif"})
	un, err := w.a.Notify.UnreadCount(ctx)
	r.rec(name+" unread", un, err)
	seen, err := w.a.Engage.RecentlySeen(ctx, "")
	r.rec(name+" seen", seen, err)
}
