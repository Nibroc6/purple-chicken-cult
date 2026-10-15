package feed

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/rank"
)

// Scan is a planner choice for one tier (§4.2).
type Scan string

// The two candidate routes.
const (
	ScanAuthor Scan = "author" // merge recent[a] for every a in L_k: cost |L_k| + P_k
	ScanTime   Scan = "time"   // walk all W window posts, keep authors at distance k: cost W
)

// Plan picks the cheaper route for a tier with layerSize authors holding
// layerPosts window posts in a window of windowSize posts: the author scan
// unless |L_k| + P_k exceeds W. Ties go to the author scan.
func Plan(layerSize, layerPosts, windowSize int) Scan {
	if layerSize+layerPosts > windowSize {
		return ScanTime
	}
	return ScanAuthor
}

// entry is one slot of a tier snapshot.
type entry struct {
	post      core.PostID // the ranked post (for a thread: its newest unseen visible part)
	thread    bool
	collapsed bool // the placeholder of the tier's collapsed group (post is unused)
}

// snapshot is a ranked tier: every candidate, in order (§5.6), with the
// low-trust group folded into one placeholder entry at its best-ranked spot.
type snapshot struct {
	tier    int
	scan    Scan
	built   time.Time
	entries []entry
	group   []entry // the collapsed group, ranked
}

// filters are the reader's personal candidate filters.
type filterSet struct {
	viewer core.UserID
	muted  map[core.UserID]struct{}
	hidden map[core.PostID]struct{}
}

func (s *Service) filters(ctx context.Context, v core.UserID) filterSet {
	f := filterSet{viewer: v, muted: map[core.UserID]struct{}{}, hidden: map[core.PostID]struct{}{}}
	if s.d.Mutes != nil {
		if m := s.d.Mutes.MutedAuthors(ctx, v); m != nil {
			f.muted = m
		}
	}
	if h := s.d.Engage.HiddenSet(ctx, v); h != nil {
		f.hidden = h
	}
	return f
}

// keep is the per-post filter shared by snapshots and every re-check: never
// the reader's own posts (D16), never a hidden post or a muted author.
func (f filterSet) keep(m core.PostMeta) bool {
	if m.Deleted || m.Author == f.viewer {
		return false
	}
	if _, ok := f.muted[m.Author]; ok {
		return false
	}
	_, ok := f.hidden[m.ID]
	return !ok
}

// tier returns the snapshot of tier k, building it the first time the session
// reaches it. The caller holds sess.mu.
func (s *Service) tier(ctx context.Context, sr core.Search, sess *session, k int, f filterSet) *snapshot {
	if t, ok := sess.tiers[k]; ok {
		return t
	}
	t := s.build(ctx, sr, sess, k, f)
	sess.tiers[k] = t
	return t
}

// build plans, filters, groups and ranks tier k (§4.2, §4.4, §5.6, §10.1.2).
func (s *Service) build(ctx context.Context, sr core.Search, sess *session, k int, f filterSet) *snapshot {
	layer := sr.Layer(k)
	pk := 0
	for _, a := range layer {
		pk += s.d.Posts.RecentLen(a)
	}
	t := &snapshot{tier: k, scan: Plan(len(layer), pk, s.d.Posts.WindowSize()), built: s.now()}
	if s.d.Metrics != nil {
		s.d.Metrics.Inc(MetricPlanner, string(t.scan), 1)
	}
	if s.onPlan != nil {
		s.onPlan(k, t.scan)
	}

	var metas []core.PostMeta
	if t.scan == ScanAuthor {
		var ids []core.PostID
		for _, a := range layer {
			ids = append(ids, s.d.Posts.Recent(a)...)
		}
		byID := s.d.Posts.MetaMany(ctx, ids)
		for _, id := range ids {
			if m, ok := byID[id]; ok {
				metas = append(metas, m)
			}
		}
	} else {
		s.d.Posts.WindowScan(func(m core.PostMeta) bool {
			if d, ok := sr.Dist(m.Author); ok && d == k {
				metas = append(metas, m)
			}
			return true
		})
	}

	// Filter: visible, unseen, not hidden, author not muted, never the reader's own.
	var cands []core.Checked
	for _, m := range metas {
		if !f.keep(m) {
			continue
		}
		if _, seen := sess.seen[m.ID]; seen {
			continue
		}
		if c, ok := s.d.Vis.CheckPost(ctx, sr, m); ok {
			cands = append(cands, c)
		}
	}

	// Threads: one candidate per visible run of a continuation thread, ranked
	// as its newest unseen visible part; the head part decides is_reply.
	type pick struct {
		c      core.Checked
		head   core.Checked
		depth  int
		thread bool
	}
	picks := make([]pick, 0, len(cands))
	byHead := map[core.PostID]int{}
	for _, c := range cands {
		m := c.Meta()
		if m.ContParent == uuid.Nil && m.ContChild == uuid.Nil {
			picks = append(picks, pick{c: c, head: c})
			continue
		}
		head, depth := s.threadHead(ctx, sr, c)
		if i, ok := byHead[head.Meta().ID]; ok {
			if depth > picks[i].depth {
				picks[i].c, picks[i].depth = c, depth
			}
			continue
		}
		byHead[head.Meta().ID] = len(picks)
		picks = append(picks, pick{c: c, head: head, depth: depth, thread: true})
	}

	// Rank every candidate of the tier (never a prefix).
	hops := s.hops(sess.viewer)
	cs := make([]rank.Candidate, len(picks))
	index := make(map[core.PostID]int, len(picks))
	authors := make([]core.UserID, 0, len(picks))
	for i, p := range picks {
		cs[i] = s.candidate(ctx, sr, p.c, p.head, sess.memo, sess.showLess, hops)
		index[p.c.Meta().ID] = i
		authors = append(authors, p.c.Meta().Author)
	}
	params := rank.ParamsFor(sess.preset)
	scored := rank.RankTier(params, s.now(), cs)
	s.logRank(ctx, sess.viewer, k, params, scored)

	// Bot isolation (§4.4): from tier 3 on, young accounts fold into one group.
	young := map[core.UserID]bool{}
	if k >= CollapseFrom {
		metas, _ := s.d.Users.GetMany(ctx, authors)
		cutoff := s.now().Add(-LowTrustAge)
		for _, a := range authors {
			if um, ok := metas[a]; ok && um.CreatedAt.After(cutoff) {
				young[a] = true
			}
		}
	}
	t.entries = make([]entry, 0, len(scored))
	for _, sc := range scored {
		id := core.PostID(sc.Key)
		e := entry{post: id, thread: picks[index[id]].thread}
		if young[core.UserID(sc.Author)] {
			if len(t.group) == 0 {
				t.entries = append(t.entries, entry{collapsed: true})
			}
			t.group = append(t.group, e)
			continue
		}
		t.entries = append(t.entries, e)
	}
	return t
}

// candidate is the rank input of c (head decides is_reply for a thread).
func (s *Service) candidate(ctx context.Context, sr core.Search, c, head core.Checked, memo *core.Memo,
	showLess map[core.UserID]int, hops map[core.UserID]time.Time) rank.Candidate {
	m := c.Meta()
	fh := sr.FirstHop(m.Author)
	if fh == 0 {
		fh = m.Author // a friend: the first hop is the author
	}
	return rank.Candidate{
		Key: [16]byte(m.ID), Author: uint32(m.Author), CreatedAt: m.CreatedAt, IsReply: head.Meta().IsReply,
		N: s.d.Prefer.N(ctx, sr, c, memo), KidsN: s.d.Prefer.KidsN(ctx, sr, c, memo, 1),
		FirstHopPlus: sr.FirstHopPlus(m.Author), FirstHopSince: hops[fh], FirstHopID: uint32(fh),
		ShowLess: showLess[m.Author],
	}
}

// hops maps each of v's friends to the age of v's edge to them (rank ties).
func (s *Service) hops(v core.UserID) map[core.UserID]time.Time {
	out := map[core.UserID]time.Time{}
	for _, e := range s.d.Graph.Friends(v) {
		out[e.To] = e.Since
	}
	return out
}

// threadHead walks c's visible continuation parents up to the first visible
// part (traversal stops at a part the reader cannot see, §10.1.3) and returns
// it with c's depth below it.
func (s *Service) threadHead(ctx context.Context, sr core.Search, c core.Checked) (core.Checked, int) {
	cur, depth := c, 0
	for depth < threadWalk {
		p, ok := s.contParent(ctx, sr, cur)
		if !ok {
			break
		}
		cur = p
		depth++
	}
	return cur, depth
}

// threadContext is the visible parts above c, head first.
func (s *Service) threadContext(ctx context.Context, sr core.Search, c core.Checked) []core.Checked {
	var up []core.Checked
	for cur := c; len(up) < threadWalk; {
		p, ok := s.contParent(ctx, sr, cur)
		if !ok {
			break
		}
		up = append(up, p)
		cur = p
	}
	for i, j := 0, len(up)-1; i < j; i, j = i+1, j-1 {
		up[i], up[j] = up[j], up[i]
	}
	return up
}

// contParent is c's continuation parent when the reader can see it and the link.
func (s *Service) contParent(ctx context.Context, sr core.Search, c core.Checked) (core.Checked, bool) {
	if c.Meta().ContParent == uuid.Nil {
		return core.Checked{}, false
	}
	for _, l := range s.d.Vis.VisibleParents(ctx, sr, c.Meta()) {
		if l.Link.Kind == core.LinkCont {
			return l.Other, true
		}
	}
	return core.Checked{}, false
}

// degreeBucket is the closed bucket of a tier ("1" … "6", "7+").
func degreeBucket(k int) string {
	if k >= 7 {
		return "7+"
	}
	return strconv.Itoa(k)
}
