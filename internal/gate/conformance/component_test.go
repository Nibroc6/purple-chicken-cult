package conformance_test

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// splitComponents returns m with every A user's inviter inside A (an inviter
// label is part of H(V)), and the same world without anything outside
// component A: B, the bot, their posts and likes, and the links from A posts
// to their posts.
func splitComponents(m *model) (full, onlyA *model) {
	full = m.clone()
	inA := func(h string) bool { return strings.HasPrefix(h, "va") }
	for i, u := range full.Users {
		if inA(u.Handle) && u.Inviter != "" && !inA(u.Inviter) {
			full.Users[i].Inviter, full.Users[i].Probation = "", false
		}
	}
	for i, p := range full.Posts {
		if u, _ := full.user(p.Author); !u.Probation && p.Forced {
			full.Posts[i].Forced = false
		}
	}
	onlyA = &model{}
	for _, u := range full.Users {
		if inA(u.Handle) {
			onlyA.Users = append(onlyA.Users, u)
		}
	}
	for _, e := range full.Edges {
		if inA(e.A) && inA(e.B) {
			onlyA.Edges = append(onlyA.Edges, e)
		}
	}
	for _, b := range full.Blocks {
		if inA(b[0]) && inA(b[1]) {
			onlyA.Blocks = append(onlyA.Blocks, b)
		}
	}
	kept := map[string]bool{}
	for _, p := range full.Posts {
		if !inA(p.Author) {
			continue
		}
		q := p
		q.Parents = nil
		for _, pp := range p.Parents {
			if kept[pp.Post] {
				q.Parents = append(q.Parents, pp)
			}
		}
		kept[p.Label] = true
		onlyA.Posts = append(onlyA.Posts, q)
	}
	for _, l := range full.Likes {
		if inA(l.User) && kept[l.Post] {
			onlyA.Likes = append(onlyA.Likes, l)
		}
	}
	return full, onlyA
}

// TestOtherComponentInvisible is the Phase 0 DoD horizon test (§18): V in
// component A gets responses identical to a world where component B (and the
// zero-edge bot) does not exist at all — including A posts that reference B
// posts, which must look exactly like roots (§10.1.3).
func TestOtherComponentInvisible(t *testing.T) {
	checks(t)
	rapid.Check(t, func(rt *rapid.T) {
		full, onlyA := splitComponents(genBase(rt))
		ops := genOps(rt, full)
		if h1, h2 := horizons(full, V, vActions(full, ops)), horizons(onlyA, V, vActions(full, ops)); h1 != h2 {
			rt.Skip("V blocks someone in B: V's own blocks are inside H(V)")
		}
		sc := &scope{TB: t}
		defer sc.close()
		w1 := newWorld(sc, full, baseLayer(full))
		w2 := newWorld(sc, full, baseLayer(onlyA)) // full: refs for B users still resolve to their IDs
		r1, r2 := newReplayer(w1, full, V), newReplayer(w2, full, V)
		r1.run(ops)
		r2.run(ops)
		if i, msg := firstDiff(r1.out, r2.out); i >= 0 {
			rt.Fatalf("other component: %s\n--- world: %s\n--- ops: %+v", msg, describe(baseLayer(full), nil), ops)
		}
	})
}
