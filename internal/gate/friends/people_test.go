package friends_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/friends"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
)

// allPersonPosts pages through v's view of u's posts in order and returns the
// refs served.
func (e *env) allPersonPosts(v, u core.UserID, order string) []gate.Ref {
	e.t.Helper()
	var out []gate.Ref
	cursor := ""
	for range 20 {
		page, err := e.f.PersonPosts(e.as(v), e.ref(v, u), cursor, order)
		e.must(err)
		if page.Next != "" && len(page.Items) != friends.PostsPageSize {
			e.t.Fatalf("a page with a next cursor is full: %d", len(page.Items))
		}
		for _, p := range page.Items {
			out = append(out, p.Ref)
		}
		if cursor = page.Next; cursor == "" {
			return out
		}
	}
	e.t.Fatal("paging never ended")
	return nil
}

// Acceptance: the person page lists the whole history (older than the feed
// window), newest or oldest first, and every post passes the feed's checks:
// audience by distance, friend+ only for friend+, the probation narrowing to
// friends, and the caller's hides.
func TestPersonPostsHistory(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.w.User("alice"), e.w.User("bob"), e.w.User("carol")
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	start := e.d.Clock.Now().Add(-60 * 24 * time.Hour)

	// bob (distance 1): 45 posts over 60 days; every fifth is friend+ only.
	var want []core.PostID
	for i := range 45 {
		label := fmt.Sprintf("b%02d", i)
		opts := []coretest.PostOpt{coretest.At(start.Add(time.Duration(i) * 32 * time.Hour))}
		if i%5 == 0 {
			opts = append(opts, coretest.Audience(core.AudFriendPlus))
		} else if i%7 == 0 {
			opts = append(opts, coretest.Audience(core.AudD1))
		}
		id := e.w.Post("bob", label, "bob "+label, opts...)
		if i%5 != 0 {
			want = append(want, id)
		}
	}
	// alice hides one of bob's posts.
	hidden := want[3]
	e.must(e.a.Engage.Hide(e.as(alice), e.d.Refs.Post(alice, hidden)))
	want = slices.DeleteFunc(want, func(id core.PostID) bool { return id == hidden })

	refs := func(v core.UserID, ids []core.PostID) []gate.Ref {
		out := make([]gate.Ref, len(ids))
		for i, id := range ids {
			out[i] = e.d.Refs.Post(v, id)
		}
		return out
	}
	oldest := refs(alice, want)
	if got := e.allPersonPosts(alice, bob, friends.OrderOldest); !slices.Equal(got, oldest) {
		t.Fatalf("oldest first: got %d posts, want %d", len(got), len(oldest))
	}
	newest := slices.Clone(oldest)
	slices.Reverse(newest)
	if got := e.allPersonPosts(alice, bob, ""); !slices.Equal(got, newest) {
		t.Fatalf("newest first (default): got %d posts, want %d", len(got), len(newest))
	}
	if got := e.allPersonPosts(alice, bob, friends.OrderNewest); !slices.Equal(got, newest) {
		t.Fatal("newest is the default")
	}

	// carol (distance 2 from alice): a d1 post and a probation post (stored
	// d2, forced to friends) are not alice's; an open post is.
	open := e.w.Post("carol", "c-open", "open", coretest.At(start))
	e.w.Post("carol", "c-d1", "d1", coretest.At(start.Add(time.Hour)), coretest.Audience(core.AudD1))
	forced := e.w.Post("carol", "c-forced", "forced", coretest.At(start.Add(2*time.Hour)), coretest.Audience(core.AudD2))
	e.exec("UPDATE posts SET audience_forced = true WHERE id = $1", forced)
	if got := e.allPersonPosts(alice, carol, ""); !slices.Equal(got, refs(alice, []core.PostID{open})) {
		t.Fatalf("carol for alice: %v", got)
	}
	if got := e.allPersonPosts(bob, carol, ""); len(got) != 3 {
		t.Fatalf("carol's friend sees all three: %d", len(got))
	}
	// The person's own page shows their own posts.
	if got := e.allPersonPosts(carol, carol, ""); len(got) != 3 {
		t.Fatalf("own page: %d", len(got))
	}

	_, err := e.f.PersonPosts(e.as(alice), e.ref(alice, bob), "", "sideways")
	wantProblem(t, err, "order_invalid")
}

// Acceptance: paging reveals nothing about posts the caller cannot see. A page
// that ends where only invisible posts remain has no next cursor; a cursor
// works only for the caller and the person it was made for; a blocked author's
// posts are not found at all.
func TestPersonPostsNoLeak(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.w.User("alice"), e.w.User("bob"), e.w.User("carol")
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	start := e.d.Clock.Now().Add(-30 * 24 * time.Hour)
	// carol: exactly one page of open posts, then (older) many friends-only ones.
	for i := range friends.PostsPageSize {
		e.w.Post("carol", fmt.Sprintf("open%02d", i), "open", coretest.At(start.Add(time.Duration(200+i)*time.Hour)))
	}
	for i := range 150 {
		e.w.Post("carol", fmt.Sprintf("d1-%03d", i), "d1", coretest.At(start.Add(time.Duration(i)*time.Hour)),
			coretest.Audience(core.AudD1))
	}
	page, err := e.f.PersonPosts(e.as(alice), e.ref(alice, carol), "", "")
	e.must(err)
	if len(page.Items) != friends.PostsPageSize || page.Next != "" {
		t.Fatalf("one full page and no next: %d items, next %q", len(page.Items), page.Next)
	}
	// Oldest first walks past the 150 hidden posts to the visible ones.
	page, err = e.f.PersonPosts(e.as(alice), e.ref(alice, carol), "", friends.OrderOldest)
	e.must(err)
	if len(page.Items) != friends.PostsPageSize || page.Next != "" {
		t.Fatalf("oldest: %d items, next %q", len(page.Items), page.Next)
	}

	// bob has two pages: his first page's cursor is bound to alice and bob.
	for i := range friends.PostsPageSize + 1 {
		e.w.Post("bob", fmt.Sprintf("bob%02d", i), "bob", coretest.At(start.Add(time.Duration(i)*time.Hour)))
	}
	page, err = e.f.PersonPosts(e.as(alice), e.ref(alice, bob), "", "")
	e.must(err)
	if page.Next == "" {
		t.Fatal("bob has a second page")
	}
	_, err = e.f.PersonPosts(e.as(alice), e.ref(alice, carol), page.Next, "")
	wantNotFound(t, err)
	_, err = e.f.PersonPosts(e.as(carol), e.ref(carol, bob), page.Next, "")
	wantNotFound(t, err)
	_, err = e.f.PersonPosts(e.as(alice), e.ref(alice, bob), page.Next[:len(page.Next)-2]+"AA", "")
	wantNotFound(t, err)
	// The cursor keeps its order whatever order is passed with it.
	next, err := e.f.PersonPosts(e.as(alice), e.ref(alice, bob), page.Next, friends.OrderOldest)
	e.must(err)
	if len(next.Items) != 1 || next.Next != "" || next.Items[0].Ref != e.d.Refs.Post(alice, e.w.PostID("bob00")) {
		t.Fatalf("second page: %+v", next)
	}

	// alice blocks carol: carol's page is not found.
	e.w.Block("alice", "carol")
	_, err = e.f.PersonPosts(e.as(alice), e.ref(alice, carol), "", "")
	wantNotFound(t, err)
}
