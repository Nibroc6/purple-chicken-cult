package feed

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// slot is one item of a page before its views are built.
type slot struct {
	kind  string
	tier  int
	post  core.Checked   // post / thread: the ranked part
	parts []core.Checked // thread: visible parts above the ranked part, head first
	count int            // collapsed: group items that pass the re-check
}

// recheck re-validates a snapshot entry against the reader's current search
// (fidelity #4): the post still exists, is visible, sits in the same tier and
// passes the personal filters. Anything else silently disappears.
func (s *Service) recheck(ctx context.Context, sr core.Search, f filterSet, tier int, e entry) (slot, bool) {
	m, ok := s.d.Posts.Meta(ctx, e.post)
	if !ok || !f.keep(m) {
		return slot{}, false
	}
	if d, ok := sr.Dist(m.Author); !ok || d != tier {
		return slot{}, false
	}
	c, ok := s.d.Vis.CheckPost(ctx, sr, m)
	if !ok {
		return slot{}, false
	}
	sl := slot{kind: ItemPost, tier: tier, post: c}
	if e.thread {
		if sl.parts = s.threadContext(ctx, sr, c); len(sl.parts) > 0 {
			sl.kind = ItemThread
		}
	}
	return sl, true
}

// serve assembles one page from pos: tier markers around the surviving items
// of each tier (an empty tier emits nothing), PageSize items, then the cursor
// of the next page or "caught up".
func (s *Service) serve(ctx context.Context, sr core.Search, sess *session, pos position) (gate.FeedPage, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	f := s.filters(ctx, sess.viewer)
	maxK := sr.MaxLayer()

	var slots []slot
	n := 0
	k, off := pos.tier, pos.offset
	for n < PageSize && k <= maxK {
		t := s.tier(ctx, sr, sess, k, f)
		started := off > 0 // a tier continued from an earlier page already has its header
		for off < len(t.entries) && n < PageSize {
			e := t.entries[off]
			off++
			var sl slot
			if e.collapsed {
				c := s.groupCount(ctx, sr, f, t)
				if c == 0 {
					continue
				}
				sl = slot{kind: ItemCollapsed, tier: k, count: c}
			} else {
				var ok bool
				if sl, ok = s.recheck(ctx, sr, f, k, e); !ok {
					continue
				}
			}
			if !started {
				slots = append(slots, slot{kind: ItemTierStart, tier: k})
				started = true
			}
			slots = append(slots, sl)
			n++
			if int(sess.reached.Load()) < k {
				sess.reached.Store(int32(k))
			}
		}
		if off < len(t.entries) {
			break // the page is full inside tier k
		}
		if started {
			slots = append(slots, slot{kind: ItemTierEnd, tier: k})
		}
		k, off = k+1, 0
	}
	// A full page that ended on a tier boundary: look ahead (building the next
	// tiers as they are reached) so the last page says "caught up" itself.
	for off == 0 && k <= maxK && len(s.tier(ctx, sr, sess, k, f).entries) == 0 {
		k++
	}

	page := gate.FeedPage{Session: s.sessionString(sess), Preset: sess.preset.String()}
	if k > maxK {
		slots = append(slots, slot{kind: ItemCaughtUp})
	} else {
		page.Next = s.cursorString(sess, position{tier: k, offset: off})
	}
	items, err := s.render(ctx, sr, sess, slots)
	if err != nil {
		return gate.FeedPage{}, err
	}
	page.Items = items
	return page, nil
}

// groupCount is the number of collapsed items of t that pass the re-check.
func (s *Service) groupCount(ctx context.Context, sr core.Search, f filterSet, t *snapshot) int {
	n := 0
	for _, e := range t.group {
		if _, ok := s.recheck(ctx, sr, f, t.tier, e); ok {
			n++
		}
	}
	return n
}

// expand serves the collapsed group of tier k (§4.4).
func (s *Service) expand(ctx context.Context, sr core.Search, sess *session, k int) ([]gate.FeedItem, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	t, ok := sess.tiers[k]
	if !ok {
		return []gate.FeedItem{}, nil
	}
	f := s.filters(ctx, sess.viewer)
	var slots []slot
	for _, e := range t.group {
		if len(slots) == ExpandMax {
			break
		}
		if sl, ok := s.recheck(ctx, sr, f, k, e); ok {
			slots = append(slots, sl)
		}
	}
	return s.render(ctx, sr, sess, slots)
}

// render builds the views of every post on the page in one Views.Posts call
// and turns the slots into feed items.
func (s *Service) render(ctx context.Context, sr core.Search, sess *session, slots []slot) ([]gate.FeedItem, error) {
	var checked []core.Checked
	for _, sl := range slots {
		if sl.kind == ItemPost || sl.kind == ItemThread {
			checked = append(checked, sl.parts...)
			checked = append(checked, sl.post)
		}
	}
	views, err := s.d.Views.Posts(ctx, sr, checked, core.ViewOpts{WithBody: true, WithContext: true})
	if err != nil {
		return nil, err
	}
	items := make([]gate.FeedItem, 0, len(slots))
	i := 0
	for _, sl := range slots {
		switch sl.kind {
		case ItemPost:
			pv := views[i]
			i++
			items = append(items, gate.FeedItem{Type: ItemPost, Post: &pv})
		case ItemThread:
			n := len(sl.parts) + 1
			items = append(items, gate.FeedItem{Type: ItemThread, Thread: views[i : i+n : i+n]})
			i += n
		case ItemCollapsed:
			items = append(items, gate.FeedItem{Type: ItemCollapsed, Tier: sl.tier, Collapsed: &gate.Collapsed{
				Count: sl.count, Token: s.tokenString(sess, sl.tier), Reason: ReasonLowTrust}})
		default:
			items = append(items, gate.FeedItem{Type: sl.kind, Tier: sl.tier})
		}
	}
	return items, nil
}
