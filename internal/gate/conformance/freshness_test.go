package conformance_test

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// freshWorld: V with three friends, each with two further friends, everyone
// posting six connected posts (54 posts: the feed spans three pages).
func freshWorld() *model {
	m := &model{}
	add := func(h string) {
		m.Users = append(m.Users, mUser{Handle: h, AgeDays: 200, slot: baseFixture, index: len(m.Users)})
	}
	add(V)
	for i := 1; i <= 3; i++ {
		f := fmt.Sprintf("va%02d", i)
		add(f)
		m.Edges = append(m.Edges, mEdge{A: V, B: f, AgeDays: 100 + i})
		for j := 0; j < 2; j++ {
			g := fmt.Sprintf("vf%d%d", i, j)
			add(g)
			m.Edges = append(m.Edges, mEdge{A: f, B: g, AgeDays: 100})
		}
	}
	add("vb00") // another component: the foreign viewer
	n := 0
	for _, u := range m.Users {
		if u.Handle == V || u.Handle == "vb00" {
			continue
		}
		for k := 0; k < 6; k++ {
			n++
			m.Posts = append(m.Posts, mPost{Label: fmt.Sprintf("p%02d", n), Author: u.Handle, AgeMin: 5000 - 37*n})
		}
	}
	return m
}

// TestFreshness (fidelity #4): after an unfriend, a block or a deletion, the
// next page served from an existing feed session, the next tree row of an
// open snapshot and the next person-posts page contain no newly invisible post.
func TestFreshness(t *testing.T) {
	checks(t)
	rapid.Check(t, func(rt *rapid.T) {
		m := freshWorld()
		kind := rapid.SampledFrom([]string{"unfriend", "block", "delete", "friend-unfriends"}).Draw(rt, "change")
		pages := rapid.IntRange(0, 1).Draw(rt, "pagesBefore")
		sc := &scope{TB: t}
		defer sc.close()
		w := newWorld(sc, m, baseLayer(m))
		ctx := w.as(V)

		// Read some pages of a session first.
		cursor := ""
		for range pages + 1 {
			fp, err := w.a.Feed.Page(ctx, "latest", cursor)
			if err != nil || fp.Next == "" {
				rt.Fatalf("feed page: %v (next %q)", err, fp.Next)
			}
			cursor = fp.Next
		}
		tv, err := w.a.Tree.Open(ctx, w.postRef(V, "p01"), "")
		if err != nil {
			rt.Fatalf("tree: %v", err)
		}

		// The change.
		after := m.clone()
		switch kind {
		case "unfriend", "block", "friend-unfriends":
			f := rapid.SampledFrom([]string{"va01", "va02", "va03"}).Draw(rt, "friend")
			after.Edges = removeEdge(after.Edges, V, f)
			switch kind {
			case "unfriend":
				err = w.a.Friends.Unfriend(ctx, w.userRef(V, f))
			case "friend-unfriends":
				err = w.a.Friends.Unfriend(w.as(f), w.userRef(f, V))
			case "block":
				after.Blocks = append(after.Blocks, [2]string{V, f})
				err = w.a.Friends.Block(ctx, w.userRef(V, f))
			}
		case "delete":
			p := after.Posts[rapid.IntRange(0, len(after.Posts)-1).Draw(rt, "post")]
			for i := range after.Posts {
				if after.Posts[i].Label == p.Label {
					after.Posts[i].Deleted = true
				}
			}
			err = w.a.Posts.Delete(w.as(p.Author), w.postRef(p.Author, p.Label))
		}
		if err != nil {
			rt.Fatalf("%s: %v", kind, err)
		}
		o := newOracle(after, V)
		visible := map[core.PostID]bool{}
		for _, p := range after.Posts {
			if o.visible(p) {
				visible[w.postID(p.Label)] = true
			}
		}
		check := func(where string, pv gate.PostView) {
			id, ok := w.d.Refs.OpenPost(w.id(V), pv.Ref)
			if !ok || !visible[id] {
				rt.Fatalf("%s after %s served a post V can no longer see (%s)", where, kind, pv.Body)
			}
		}
		for page := 0; cursor != ""; page++ {
			fp, err := w.a.Feed.Page(ctx, "", cursor)
			if err != nil {
				rt.Fatalf("feed page after %s: %v", kind, err)
			}
			for _, it := range fp.Items {
				if it.Post != nil {
					check("feed", *it.Post)
				}
				for _, x := range it.Thread {
					check("feed thread", x)
				}
			}
			cursor = fp.Next
		}
		for _, row := range tv.Rows {
			if len(row.Items) == 0 {
				continue
			}
			r, err := w.a.Tree.Row(ctx, tv.Snapshot, row.Gen+1, row.Items[0].Post.Ref)
			if err != nil {
				continue // the center itself may have become invisible
			}
			for _, it := range r.Items {
				check("tree row", it.Post)
			}
		}
		for _, u := range m.Users {
			pp, err := w.a.Friends.PersonPosts(ctx, w.userRef(V, u.Handle), "")
			if err != nil {
				continue
			}
			for _, pv := range pp.Items {
				check("person posts", pv)
			}
		}
	})
}

func removeEdge(es []mEdge, a, b string) []mEdge {
	var out []mEdge
	for _, e := range es {
		if (e.A == a && e.B == b) || (e.A == b && e.B == a) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TestRequestToBlockerLooksDeclined: a friend request to someone who blocked V
// answers exactly like one to someone who declined V's earlier request, and
// V's requests list shows both as outgoing (§2.2: V never learns of a block).
func TestRequestToBlockerLooksDeclined(t *testing.T) {
	checks(t)
	m := &model{}
	for i, h := range []string{V, "va01", "va02", "va03"} {
		m.Users = append(m.Users, mUser{Handle: h, AgeDays: 200, slot: baseFixture, index: i})
	}
	m.Edges = []mEdge{{A: V, B: "va01", AgeDays: 100}, {A: "va01", B: "va02", AgeDays: 100},
		{A: "va01", B: "va03", AgeDays: 100}}
	w := newWorld(t, m, baseLayer(m))
	ctx := w.as(V)
	if err := w.a.Friends.Request(ctx, w.userRef(V, "va02")); err != nil {
		t.Fatal(err)
	}
	if err := w.a.Friends.Decline(w.as("va02"), w.userRef("va02", V)); err != nil {
		t.Fatal(err)
	}
	if err := w.a.Friends.Block(w.as("va03"), w.userRef("va03", V)); err != nil {
		t.Fatal(err)
	}
	pDeclined, errD := w.a.Friends.Person(ctx, w.userRef(V, "va02"))
	pBlocked, errB := w.a.Friends.Person(ctx, w.userRef(V, "va03"))
	if errD != nil || errB != nil {
		t.Fatalf("person: %v / %v", errD, errB)
	}
	if pDeclined.Request != "outgoing" {
		t.Fatalf("declined request reads %q", pDeclined.Request)
	}
	// Before V asks the blocker: the only difference is V's own earlier request.
	e1 := errCode(w.a.Friends.Request(ctx, w.userRef(V, "va02")))
	e2 := errCode(w.a.Friends.Request(ctx, w.userRef(V, "va03")))
	if e1 != e2 {
		t.Fatalf("request to a decliner: %s; to a blocker: %s", e1, e2)
	}
	pDeclined, _ = w.a.Friends.Person(ctx, w.userRef(V, "va02"))
	pBlocked, _ = w.a.Friends.Person(ctx, w.userRef(V, "va03"))
	if pDeclined.Request != pBlocked.Request || pDeclined.Relation != pBlocked.Relation ||
		pDeclined.Blocked != pBlocked.Blocked {
		t.Fatalf("decliner %+v vs blocker %+v", pDeclined, pBlocked)
	}
	// Repeating the request answers alike as well.
	if a, b := errCode(w.a.Friends.Request(ctx, w.userRef(V, "va02"))),
		errCode(w.a.Friends.Request(ctx, w.userRef(V, "va03"))); a != b {
		t.Fatalf("repeated request: %s vs %s", a, b)
	}
	_, out, err := w.a.Friends.Requests(ctx)
	if err != nil || len(out) != 2 {
		t.Fatalf("outgoing requests: %v %d", err, len(out))
	}
}
