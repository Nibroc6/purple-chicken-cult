// Package postindex is the in-memory post index (ARCHITECTURE §4.2): recent[author],
// the feed window, metadata LRU and links. It implements core.PostIndex.
//
// Owner: P0-W05 → P1-W12. Scaffold stub: delegates to coreref.MemPosts (memory
// only; nothing is loaded from posts/post_parents until W05 lands).
package postindex

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
)

// Index implements core.PostIndex.
type Index struct {
	d   *core.Deps
	ref *coreref.MemPosts
}

var _ core.PostIndex = (*Index)(nil)

// New returns the index.
func New(d *core.Deps) *Index { return &Index{d: d, ref: coreref.NewMemPosts(d)} }

// Init loads the window (W05: from posts / post_parents; registers postindex.trim).
func (x *Index) Init(context.Context) error { return nil }

// Meta returns a post's metadata.
func (x *Index) Meta(ctx context.Context, id core.PostID) (core.PostMeta, bool) {
	return x.ref.Meta(ctx, id)
}

// MetaMany returns metadata for ids.
func (x *Index) MetaMany(ctx context.Context, ids []core.PostID) map[core.PostID]core.PostMeta {
	return x.ref.MetaMany(ctx, ids)
}

// Recent returns author's window posts, newest first.
func (x *Index) Recent(author core.UserID) []core.PostID { return x.ref.Recent(author) }

// RecentLen returns len(Recent(author)).
func (x *Index) RecentLen(author core.UserID) int { return x.ref.RecentLen(author) }

// WindowScan visits window posts newest first.
func (x *Index) WindowScan(fn func(core.PostMeta) bool) { x.ref.WindowScan(fn) }

// WindowSize is the number of window posts.
func (x *Index) WindowSize() int { return x.ref.WindowSize() }

// Children visits every link to parent, newest first (RAW: archtest A10).
func (x *Index) Children(ctx context.Context, parent core.PostID, fn func(core.LinkMeta) bool) {
	x.ref.Children(ctx, parent, fn)
}

// Parents returns every link from child (RAW: archtest A10).
func (x *Index) Parents(ctx context.Context, child core.PostID) []core.LinkMeta {
	return x.ref.Parents(ctx, child)
}

// Bodies returns post bodies.
func (x *Index) Bodies(ctx context.Context, ids []core.PostID) map[core.PostID]string {
	return x.ref.Bodies(ctx, ids)
}

// Put inserts or replaces metadata.
func (x *Index) Put(m core.PostMeta) { x.ref.Put(m) }

// SetBody stores a body in memory (scaffold only; W05 reads bodies from posts).
func (x *Index) SetBody(id core.PostID, body string) { x.ref.SetBody(id, body) }

// Delete marks a post deleted.
func (x *Index) Delete(id core.PostID) { x.ref.Delete(id) }

// PutLink adds a link.
func (x *Index) PutLink(l core.LinkMeta) { x.ref.PutLink(l) }

// UpdateLink replaces a link.
func (x *Index) UpdateLink(l core.LinkMeta) { x.ref.UpdateLink(l) }
