// Package postindex is the in-memory post index (ARCHITECTURE §4.2): recent[author]
// and the feed window in memory, an LRU of older post metadata, and parent/child
// links and mentions loaded from post_parents / mentions on demand behind LRUs.
// It implements core.PostIndex and core.MentionIndex and is the only direct SQL
// reader of posts, post_parents and mentions besides gate/posts (plan §3.4).
//
// Owner: P0-W05 → P1-W12.
//
// Consistency: gate/posts writes the tables and calls Put / PutLink / UpdateLink /
// Delete after commit. Every mutation bumps a sequence number; a DB load stores
// its result only if no mutation happened while it ran, so a load that raced a
// commit can never pin stale metadata or links in a cache.
package postindex

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Cache sizes and the trim job.
const (
	MetaLRU    = 50_000 // metadata of posts outside the window
	LinkLRU    = 50_000 // children lists and parent lists (each)
	MentionLRU = 20_000
	JobTrim    = "postindex.trim"
	TrimEvery  = 24 * time.Hour
)

// Index implements core.PostIndex and core.MentionIndex.
type Index struct {
	d *core.Deps

	mu     sync.Mutex
	seq    uint64
	win    map[core.PostID]core.PostMeta // window posts (not deleted, created inside the window)
	order  []core.PostID                 // window posts, oldest first
	recent map[core.UserID][]core.PostID // per author, oldest first
	old    *lru[core.PostID, core.PostMeta]
	kids   *lru[core.PostID, []core.LinkMeta] // newest first
	pars   *lru[core.PostID, []core.LinkMeta] // parent order (ord)
	ments  *lru[core.PostID, map[int]core.UserID]

	// Window posts' links and mentions live in memory too (plan §7: the feed
	// reads only memory, Postgres only for seen and hides): complete children
	// and parent lists of every window post (a child is never older than its
	// parent, so a window post's children are window posts) and the mentions
	// of the window posts known since Load. Posts leaving the window move
	// their lists to the LRUs.
	wkids  map[core.PostID][]core.LinkMeta
	wpars  map[core.PostID][]core.LinkMeta
	wments map[core.PostID]map[int]core.UserID
}

var (
	_ core.PostIndex    = (*Index)(nil)
	_ core.MentionIndex = (*Index)(nil)
)

// New returns an empty index.
func New(d *core.Deps) *Index {
	x := &Index{d: d}
	x.reset()
	return x
}

func (x *Index) reset() {
	x.win = map[core.PostID]core.PostMeta{}
	x.order = nil
	x.recent = map[core.UserID][]core.PostID{}
	x.old = newLRU[core.PostID, core.PostMeta](MetaLRU)
	x.kids = newLRU[core.PostID, []core.LinkMeta](LinkLRU)
	x.pars = newLRU[core.PostID, []core.LinkMeta](LinkLRU)
	x.ments = newLRU[core.PostID, map[int]core.UserID](MentionLRU)
	x.wkids = map[core.PostID][]core.LinkMeta{}
	x.wpars = map[core.PostID][]core.LinkMeta{}
	x.wments = map[core.PostID]map[int]core.UserID{}
}

// Init loads the window and registers the nightly trim.
func (x *Index) Init(ctx context.Context) error {
	if x.d.Jobs != nil {
		x.d.Jobs.Handle(JobTrim, func(context.Context, json.RawMessage) error {
			x.Trim()
			return nil
		})
		x.d.Jobs.Every(JobTrim, TrimEvery)
	}
	if x.d.DB == nil {
		return nil
	}
	return x.Load(ctx)
}

func (x *Index) now() time.Time { return x.d.Clock.Now() }

// windowStart is the oldest creation time inside the feed window.
func (x *Index) windowStart() time.Time {
	w := 168 * time.Hour
	if x.d.Cfg != nil && x.d.Cfg.FeedWindow > 0 {
		w = x.d.Cfg.FeedWindow
	}
	return x.now().Add(-w)
}

// older reports whether a sorts before b in oldest-first order. Newest-first
// order (what every reader sees) is created_at desc, then id asc.
func older(a, b core.PostMeta) bool {
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c < 0
	}
	return bytes.Compare(a.ID[:], b.ID[:]) > 0
}

// pos is the insertion index of m in an oldest-first id slice over x.win.
func (x *Index) pos(ids []core.PostID, m core.PostMeta) int {
	return sort.Search(len(ids), func(i int) bool { return !older(x.win[ids[i]], m) })
}

func (x *Index) insertSorted(ids []core.PostID, m core.PostMeta) []core.PostID {
	i := x.pos(ids, m)
	return slices.Insert(ids, i, m.ID)
}

func (x *Index) removeSorted(ids []core.PostID, m core.PostMeta) []core.PostID {
	i := x.pos(ids, m)
	if i < len(ids) && ids[i] == m.ID {
		return slices.Delete(ids, i, i+1)
	}
	if j := slices.Index(ids, m.ID); j >= 0 { // defensive: never leave a stale entry
		return slices.Delete(ids, j, j+1)
	}
	return ids
}

// putLocked stores m: in the window when it is a live window post, in the LRU
// otherwise. A version change drops the cached mentions (edits rebuild them).
func (x *Index) putLocked(m core.PostMeta, start time.Time) {
	prev, inWin := x.win[m.ID]
	have := inWin
	if !inWin {
		prev, have = x.old.peek(m.ID)
	}
	if have && prev.Version != m.Version {
		x.ments.remove(m.ID)
		delete(x.wments, m.ID)
	}
	live := !m.Deleted && !m.CreatedAt.Before(start)
	if inWin {
		if live && prev.CreatedAt.Equal(m.CreatedAt) && prev.Author == m.Author {
			x.win[m.ID] = m
			return
		}
		x.removeWinLocked(prev)
	}
	if !live {
		x.old.put(m.ID, m)
		return
	}
	x.old.remove(m.ID)
	x.win[m.ID] = m
	x.order = x.insertSorted(x.order, m)
	x.recent[m.Author] = x.insertSorted(x.recent[m.Author], m)
}

// removeWinLocked drops a window post (m must be the stored meta); its
// in-memory links and mentions move to the LRUs.
func (x *Index) removeWinLocked(m core.PostMeta) {
	if ls, ok := x.wkids[m.ID]; ok {
		x.kids.put(m.ID, ls)
		delete(x.wkids, m.ID)
	}
	if ls, ok := x.wpars[m.ID]; ok {
		x.pars.put(m.ID, ls)
		delete(x.wpars, m.ID)
	}
	if mt, ok := x.wments[m.ID]; ok {
		x.ments.put(m.ID, mt)
		delete(x.wments, m.ID)
	}
	x.order = x.removeSorted(x.order, m)
	if r := x.removeSorted(x.recent[m.Author], m); len(r) > 0 {
		x.recent[m.Author] = r
	} else {
		delete(x.recent, m.Author)
	}
	delete(x.win, m.ID)
}

// ---- reads ----

// Meta returns a post's metadata (deleted posts included, marked Deleted):
// memory for window posts, the LRU or the database otherwise.
func (x *Index) Meta(ctx context.Context, id core.PostID) (core.PostMeta, bool) {
	ms := x.MetaMany(ctx, []core.PostID{id})
	m, ok := ms[id]
	return m, ok
}

// MetaMany returns the metadata of every known id.
func (x *Index) MetaMany(ctx context.Context, ids []core.PostID) map[core.PostID]core.PostMeta {
	out := make(map[core.PostID]core.PostMeta, len(ids))
	var miss []core.PostID
	x.mu.Lock()
	for _, id := range ids {
		if m, ok := x.win[id]; ok {
			out[id] = m
		} else if m, ok := x.old.get(id); ok {
			out[id] = m
		} else {
			miss = append(miss, id)
		}
	}
	seq := x.seq
	x.mu.Unlock()
	if len(miss) == 0 || x.d.DB == nil {
		return out
	}
	loaded, err := loadMetas(ctx, x.d.DB.Q(), metaByIDSQL, uuidStrings(miss))
	if err != nil {
		x.logErr("postindex.meta_load", err)
		return out // fail closed: unknown posts are invisible
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	keep := x.seq == seq
	start := x.windowStart()
	for _, m := range loaded {
		out[m.ID] = m
		if keep {
			x.putLocked(m, start)
		}
	}
	return out
}

// Recent returns author's window posts, newest first.
func (x *Index) Recent(author core.UserID) []core.PostID {
	start := x.windowStart()
	x.mu.Lock()
	defer x.mu.Unlock()
	ids := x.recent[author]
	out := make([]core.PostID, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if x.win[ids[i]].CreatedAt.Before(start) {
			break
		}
		out = append(out, ids[i])
	}
	return out
}

// RecentLen returns len(Recent(author)).
func (x *Index) RecentLen(author core.UserID) int { return len(x.Recent(author)) }

// window returns the window metas, newest first.
func (x *Index) window() []core.PostMeta {
	start := x.windowStart()
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]core.PostMeta, 0, len(x.order))
	for i := len(x.order) - 1; i >= 0; i-- {
		m := x.win[x.order[i]]
		if m.CreatedAt.Before(start) {
			break
		}
		out = append(out, m)
	}
	return out
}

// WindowScan visits every window post newest first until fn returns false (fn
// runs without the index lock, over a snapshot).
func (x *Index) WindowScan(fn func(core.PostMeta) bool) {
	for _, m := range x.window() {
		if !fn(m) {
			return
		}
	}
}

// WindowSize is the number of window posts (no copy: the planner asks for
// every tier it plans).
func (x *Index) WindowSize() int {
	start := x.windowStart()
	x.mu.Lock()
	defer x.mu.Unlock()
	i := sort.Search(len(x.order), func(i int) bool { return !x.win[x.order[i]].CreatedAt.Before(start) })
	return len(x.order) - i
}

// Children visits every link to parent, newest first (RAW: archtest A10).
func (x *Index) Children(ctx context.Context, parent core.PostID, fn func(core.LinkMeta) bool) {
	links := x.links(ctx, parent, x.kids, true)
	for _, l := range links {
		if !fn(l) {
			return
		}
	}
}

// Parents returns every link from child, in parent order (RAW: archtest A10).
func (x *Index) Parents(ctx context.Context, child core.PostID) []core.LinkMeta {
	return x.links(ctx, child, x.pars, false)
}

func (x *Index) links(ctx context.Context, id core.PostID, cache *lru[core.PostID, []core.LinkMeta], byParent bool) []core.LinkMeta {
	x.mu.Lock()
	win := x.wpars
	if byParent {
		win = x.wkids
	}
	if ls, ok := win[id]; ok {
		x.mu.Unlock()
		return slices.Clone(ls)
	}
	if ls, ok := cache.get(id); ok {
		x.mu.Unlock()
		return slices.Clone(ls)
	}
	seq := x.seq
	x.mu.Unlock()
	if x.d.DB == nil {
		return nil
	}
	ls, err := loadLinks(ctx, x.d.DB.Q(), byParent, id)
	if err != nil {
		x.logErr("postindex.link_load", err)
		return nil
	}
	x.mu.Lock()
	if x.seq == seq {
		cache.put(id, slices.Clone(ls))
	}
	x.mu.Unlock()
	return ls
}

// Bodies returns the bodies of the given live posts (from the database; bodies
// are never cached, so an edit or a delete is visible on the next read).
func (x *Index) Bodies(ctx context.Context, ids []core.PostID) map[core.PostID]string {
	out := make(map[core.PostID]string, len(ids))
	if len(ids) == 0 || x.d.DB == nil {
		return out
	}
	rows, err := x.d.DB.Q().Query(ctx, `SELECT id, body FROM posts
		WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL AND body IS NOT NULL`, uuidStrings(ids))
	if err != nil {
		x.logErr("postindex.body_load", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id core.PostID
		var body string
		if err := rows.Scan(&id, &body); err != nil {
			x.logErr("postindex.body_scan", err)
			return out
		}
		out[id] = body
	}
	return out
}

// MentionTargets returns the post's stored mentions, ord → target
// (core.MentionIndex, contract request p0-w02-1).
func (x *Index) MentionTargets(ctx context.Context, p core.PostID) map[int]core.UserID {
	x.mu.Lock()
	if m, ok := x.wments[p]; ok {
		x.mu.Unlock()
		return cloneMentions(m)
	}
	if m, ok := x.ments.get(p); ok {
		x.mu.Unlock()
		return cloneMentions(m)
	}
	seq := x.seq
	x.mu.Unlock()
	if x.d.DB == nil {
		return nil
	}
	rows, err := x.d.DB.Q().Query(ctx, "SELECT ord, target_id FROM mentions WHERE post_id = $1", p)
	if err != nil {
		x.logErr("postindex.mention_load", err)
		return nil
	}
	m := map[int]core.UserID{}
	for rows.Next() {
		var ord int16
		var target int64
		if err := rows.Scan(&ord, &target); err != nil {
			rows.Close()
			x.logErr("postindex.mention_scan", err)
			return nil
		}
		m[int(ord)] = core.UserID(target) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil
	}
	x.mu.Lock()
	if x.seq == seq {
		x.ments.put(p, m)
	}
	x.mu.Unlock()
	if len(m) == 0 {
		return nil
	}
	return cloneMentions(m)
}

func cloneMentions(m map[int]core.UserID) map[int]core.UserID {
	if len(m) == 0 {
		return nil
	}
	out := make(map[int]core.UserID, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---- mutations (posts module only, after commit) ----

// Put inserts or replaces a post's metadata.
func (x *Index) Put(m core.PostMeta) {
	start := x.windowStart()
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq++
	_, inWin := x.win[m.ID]
	_, inOld := x.old.peek(m.ID)
	x.putLocked(m, start)
	if _, live := x.win[m.ID]; live && !inWin && !inOld {
		// A new post: no children yet; its parents follow through PutLink.
		x.wkids[m.ID] = []core.LinkMeta{}
		x.wpars[m.ID] = []core.LinkMeta{}
	}
}

// Delete marks a post deleted: it leaves the window and its mentions are
// forgotten. Links stay (visibility hides links to deleted posts).
func (x *Index) Delete(id core.PostID) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq++
	x.ments.remove(id)
	delete(x.wments, id)
	if m, ok := x.win[id]; ok {
		x.removeWinLocked(m)
		m.Deleted = true
		x.old.put(id, m)
		return
	}
	if m, ok := x.old.peek(id); ok {
		m.Deleted = true
		x.old.put(id, m)
	}
}

// PutLink adds a link to the cached lists (idempotent per (child, parent)).
func (x *Index) PutLink(l core.LinkMeta) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq++
	if ls, ok := x.wkids[l.Parent]; ok {
		ls = upsertLink(ls, l)
		slices.SortStableFunc(ls, newestLinkFirst)
		x.wkids[l.Parent] = ls
	}
	if ls, ok := x.wpars[l.Child]; ok {
		x.wpars[l.Child] = upsertLink(ls, l)
	}
	if ls, ok := x.kids.peek(l.Parent); ok {
		ls = upsertLink(ls, l)
		slices.SortStableFunc(ls, newestLinkFirst)
		x.kids.put(l.Parent, ls)
	}
	if ls, ok := x.pars.peek(l.Child); ok {
		x.pars.put(l.Child, upsertLink(ls, l))
	}
}

// UpdateLink replaces the cached link with the same (child, parent).
func (x *Index) UpdateLink(l core.LinkMeta) { x.PutLink(l) }

func upsertLink(ls []core.LinkMeta, l core.LinkMeta) []core.LinkMeta {
	out := slices.Clone(ls)
	for i := range out {
		if out[i].Child == l.Child && out[i].Parent == l.Parent {
			out[i] = l
			return out
		}
	}
	return append(out, l)
}

func newestLinkFirst(a, b core.LinkMeta) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	return bytes.Compare(a.Child[:], b.Child[:])
}

// ---- load and trim ----

// Load replaces the in-memory state with the window read from posts /
// post_parents and empties every cache (Init, test-hook reset, devseed).
func (x *Index) Load(ctx context.Context) error {
	start := x.windowStart()
	ms, err := loadMetas(ctx, x.d.DB.Q(), metaWindowSQL, start)
	if err != nil {
		return err
	}
	links, err := loadWindowLinks(ctx, x.d.DB.Q(), start)
	if err != nil {
		return err
	}
	ments, err := loadWindowMentions(ctx, x.d.DB.Q(), start)
	if err != nil {
		return err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq++
	x.reset()
	slices.SortFunc(ms, func(a, b core.PostMeta) int {
		switch {
		case older(a, b):
			return -1
		case older(b, a):
			return 1
		}
		return 0
	})
	for _, m := range ms { // oldest first: every insert appends
		x.putLocked(m, start)
		x.wkids[m.ID] = []core.LinkMeta{}
		x.wpars[m.ID] = []core.LinkMeta{}
		x.wments[m.ID] = map[int]core.UserID{}
	}
	for _, l := range links { // ordered by (child, ord)
		if ls, ok := x.wpars[l.Child]; ok {
			x.wpars[l.Child] = append(ls, l)
		}
		if ls, ok := x.wkids[l.Parent]; ok {
			x.wkids[l.Parent] = append(ls, l)
		}
	}
	for id, ls := range x.wkids {
		slices.SortStableFunc(ls, newestLinkFirst)
		x.wkids[id] = ls
	}
	for _, mt := range ments {
		if m, ok := x.wments[mt.post]; ok {
			m[mt.ord] = mt.target
		}
	}
	return nil
}

// Trim moves posts that aged out of the window into the metadata LRU (nightly
// job postindex.trim; reads already ignore them).
func (x *Index) Trim() {
	start := x.windowStart()
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq++
	for len(x.order) > 0 {
		m := x.win[x.order[0]]
		if !m.CreatedAt.Before(start) {
			break
		}
		x.removeWinLocked(m)
		x.old.put(m.ID, m)
	}
}

// logErr logs a failed read (code is a dotted code, not prose: archtest A9).
func (x *Index) logErr(code string, err error) {
	if x.d.Log != nil && ctxAlive(err) {
		x.d.Log.Error("postindex read failed", "code", code, "err", err.Error())
	}
}
