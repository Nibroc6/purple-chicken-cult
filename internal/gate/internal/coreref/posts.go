package coreref

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// MemPosts is a map-based PostIndex. The feed window comes from D.Cfg.FeedWindow
// (default 7 days) and D.Clock.
type MemPosts struct {
	D *core.Deps

	mu       sync.RWMutex
	metas    map[core.PostID]core.PostMeta
	bodies   map[core.PostID]string
	children map[core.PostID][]core.LinkMeta
	parents  map[core.PostID][]core.LinkMeta
}

var _ core.PostIndex = (*MemPosts)(nil)

// NewMemPosts returns an empty index.
func NewMemPosts(d *core.Deps) *MemPosts {
	return &MemPosts{D: d, metas: map[core.PostID]core.PostMeta{}, bodies: map[core.PostID]string{},
		children: map[core.PostID][]core.LinkMeta{}, parents: map[core.PostID][]core.LinkMeta{}}
}

func (p *MemPosts) windowStart() time.Time {
	now := time.Now().UTC()
	window := 168 * time.Hour
	if p.D != nil {
		if p.D.Clock != nil {
			now = p.D.Clock.Now()
		}
		if p.D.Cfg != nil && p.D.Cfg.FeedWindow > 0 {
			window = p.D.Cfg.FeedWindow
		}
	}
	return now.Add(-window)
}

func newestFirst(a, b core.PostMeta) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

// Meta returns a post's metadata (deleted posts included, marked Deleted).
func (p *MemPosts) Meta(_ context.Context, id core.PostID) (core.PostMeta, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	m, ok := p.metas[id]
	return m, ok
}

// MetaMany returns the metadata of every known id.
func (p *MemPosts) MetaMany(_ context.Context, ids []core.PostID) map[core.PostID]core.PostMeta {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[core.PostID]core.PostMeta, len(ids))
	for _, id := range ids {
		if m, ok := p.metas[id]; ok {
			out[id] = m
		}
	}
	return out
}

func (p *MemPosts) window(filter func(core.PostMeta) bool) []core.PostMeta {
	start := p.windowStart()
	p.mu.RLock()
	var out []core.PostMeta
	for _, m := range p.metas {
		if !m.Deleted && !m.CreatedAt.Before(start) && (filter == nil || filter(m)) {
			out = append(out, m)
		}
	}
	p.mu.RUnlock()
	slices.SortFunc(out, newestFirst)
	return out
}

// Recent returns author's window posts, newest first.
func (p *MemPosts) Recent(author core.UserID) []core.PostID {
	ms := p.window(func(m core.PostMeta) bool { return m.Author == author })
	out := make([]core.PostID, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// RecentLen returns len(Recent(author)).
func (p *MemPosts) RecentLen(author core.UserID) int { return len(p.Recent(author)) }

// WindowScan visits every window post newest first until fn returns false.
func (p *MemPosts) WindowScan(fn func(core.PostMeta) bool) {
	for _, m := range p.window(nil) {
		if !fn(m) {
			return
		}
	}
}

// WindowSize is the number of window posts.
func (p *MemPosts) WindowSize() int { return len(p.window(nil)) }

// Children visits every link to parent, newest first (RAW: archtest A10).
func (p *MemPosts) Children(_ context.Context, parent core.PostID, fn func(core.LinkMeta) bool) {
	p.mu.RLock()
	links := slices.Clone(p.children[parent])
	p.mu.RUnlock()
	for _, l := range links {
		if !fn(l) {
			return
		}
	}
}

// Parents returns every link from child (RAW: archtest A10).
func (p *MemPosts) Parents(_ context.Context, child core.PostID) []core.LinkMeta {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.parents[child])
}

// Bodies returns the bodies of the given (non-deleted) posts.
func (p *MemPosts) Bodies(_ context.Context, ids []core.PostID) map[core.PostID]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[core.PostID]string, len(ids))
	for _, id := range ids {
		if b, ok := p.bodies[id]; ok {
			out[id] = b
		}
	}
	return out
}

// Put inserts or replaces a post's metadata.
func (p *MemPosts) Put(m core.PostMeta) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metas[m.ID] = m
}

// SetBody stores a post body (reference helper; the real index reads bodies from the DB).
func (p *MemPosts) SetBody(id core.PostID, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies[id] = body
}

// Delete marks a post deleted and drops its body.
func (p *MemPosts) Delete(id core.PostID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.metas[id]; ok {
		m.Deleted = true
		p.metas[id] = m
	}
	delete(p.bodies, id)
}

func sortLinks(ls []core.LinkMeta) {
	slices.SortFunc(ls, func(a, b core.LinkMeta) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return bytes.Compare(a.Child[:], b.Child[:])
	})
}

// PutLink adds a link.
func (p *MemPosts) PutLink(l core.LinkMeta) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.children[l.Parent] = append(p.children[l.Parent], l)
	sortLinks(p.children[l.Parent])
	p.parents[l.Child] = append(p.parents[l.Child], l)
}

// UpdateLink replaces the link with the same (Child, Parent).
func (p *MemPosts) UpdateLink(l core.LinkMeta) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range []map[core.PostID][]core.LinkMeta{p.children, p.parents} {
		for k, ls := range m {
			for i := range ls {
				if ls[i].Child == l.Child && ls[i].Parent == l.Parent {
					m[k][i] = l
				}
			}
		}
	}
}
