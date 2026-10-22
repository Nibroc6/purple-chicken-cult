// Package posts owns posts, parent references, continuation threads, edits,
// deletion, reference policies, detach and mentions (ARCHITECTURE §10.1.1,
// §10.1.4, §10.1.5, §10.2, §10.6). It is the sole writer of posts, post_versions,
// post_parents and mentions; gate/postindex reads them and serves core.PostIndex.
//
// Owner: P0-W05 → P1-W12.
//
// Check order (plan §0.8): the write was charged by apix → resolve every parent
// ref (ErrNotFound if it does not open or the caller cannot see the post) and
// open every media ref → only then Problems (too_many_media, body_empty,
// body_too_long, lang_invalid, too_many_parents, duplicate_parent, cont_not_own,
// cont_taken, ref_not_allowed, ref_cap, too_many_parts). Media is attached in
// the post's transaction (core.Media.Attach: the caller's own ready, unattached
// uploads, all or none); anything else is the uniform not-found.
//
// Records (D3): every version signs a Post record over body_commit =
// sha256(salt ‖ utf8(NFC body)) with a fresh 16-byte salt; delete signs a
// Tombstone and hard-deletes body and salt together.
package posts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Limits (§10.1.5, §10.1.1).
const (
	MaxBodyCodePoints = 20000
	SoftLimit         = 500
	CounterFrom       = 280
	MaxParents        = 3
	MaxThreadParts    = 25
	RefCapPerDay      = 4
	// NotifyWithin is how close (hops, from the parent's author) a child's author
	// must be for the parent's author to be notified (§10.1.1 default).
	NotifyWithin = 2
	saltLen      = 16
	// MaxMedia is the per-post attachment cap (P1-W12).
	MaxMedia = 4
)

// Notification kinds and the preview job (plan §4.7 notify, previews).
const (
	NotifyReferenced = "post_referenced"
	NotifyMention    = "mention"
	JobPreviewFetch  = "preview.fetch"
)

// Service is the posts module.
type Service struct{ d *core.Deps }

var _ core.PostAdmin = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init does nothing: posts has no jobs or subscriptions in Phase 0 (the post
// index owns postindex.trim; previews owns preview.fetch).
func (s *Service) Init(context.Context) error { return nil }

// Draft is a post to create.
type Draft struct {
	Body      string     `json:"body"`
	Lang      string     `json:"lang"`
	Audience  string     `json:"audience,omitempty"`   // "connected" | "d1" | "d2" | "d3" | "friend_plus" (P1)
	RefPolicy string     `json:"ref_policy,omitempty"` // "anyone" | "two_hops" | "friends" | "nobody"
	NoPreview bool       `json:"no_preview,omitempty"`
	Parents   []ParentIn `json:"parents,omitempty"`
	Media     []gate.Ref `json:"media,omitempty"` // P1
}

// ParentIn is a parent reference.
type ParentIn struct {
	Ref  gate.Ref `json:"ref"`
	Kind string   `json:"kind"` // "ref" | "cont"
}

// ---- signed record bodies (plan §4.7 records) ----

type parentRec struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind"`
}

// postRecord is Post{id, version, author, body_commit, lang, media:[id], audience?,
// parents:[{id,kind}], ref_policy?, no_preview?, created_at}.
type postRecord struct {
	ID         uuid.UUID   `json:"id"`
	Version    int         `json:"version"`
	Author     uuid.UUID   `json:"author"`
	BodyCommit sigs.Bytes  `json:"body_commit"`
	Lang       string      `json:"lang"`
	Media      []string    `json:"media"`
	Audience   *string     `json:"audience,omitempty"`
	Parents    []parentRec `json:"parents"`
	RefPolicy  *string     `json:"ref_policy,omitempty"`
	NoPreview  *bool       `json:"no_preview,omitempty"`
	CreatedAt  sigs.Time   `json:"created_at"`
}

// tombstoneBody is the signed Tombstone record body (§10.2).
type tombstoneBody struct {
	Target uuid.UUID `json:"target"`
	By     uuid.UUID `json:"by"`
	At     sigs.Time `json:"at"`
}

// bodyCommit is sha256(salt ‖ utf8(body)) (D3; records.BodyCommit).
func bodyCommit(salt []byte, body string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(body))
	return h.Sum(nil)
}

func newSalt() ([]byte, error) {
	b := make([]byte, saltLen)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// ---- shared helpers ----

// resolve opens a post ref for viewer sr.Viewer(): ErrNotFound unless the ref
// opens, the post exists and the viewer can see it.
func (s *Service) resolve(ctx context.Context, sr core.Search, ref gate.Ref) (core.PostMeta, error) {
	id, ok := s.d.Refs.OpenPost(sr.Viewer(), ref)
	if !ok {
		return core.PostMeta{}, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok {
		return core.PostMeta{}, gate.ErrNotFound
	}
	if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
		return core.PostMeta{}, gate.ErrNotFound
	}
	return m, nil
}

// resolveOwn is resolve restricted to the viewer's own posts: someone else's
// post answers exactly like an unknown one.
func (s *Service) resolveOwn(ctx context.Context, sr core.Search, ref gate.Ref) (core.PostMeta, error) {
	m, err := s.resolve(ctx, sr, ref)
	if err != nil {
		return m, err
	}
	if m.Author != sr.Viewer() {
		return core.PostMeta{}, gate.ErrNotFound
	}
	return m, nil
}

// viewer returns the signed-in viewer and their search.
func (s *Service) viewer(ctx context.Context) (core.Viewer, core.Search, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return v, nil, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return v, nil, err
	}
	return v, sr, nil
}

// views renders metas the viewer can see (with bodies and context rows).
func (s *Service) views(ctx context.Context, sr core.Search, ms []core.PostMeta) ([]gate.PostView, error) {
	cs := s.d.Vis.CheckPosts(ctx, sr, ms)
	if len(cs) != len(ms) {
		return nil, gate.ErrNotFound
	}
	return s.d.Views.Posts(ctx, sr, cs, core.ViewOpts{WithBody: true, WithContext: true})
}

// Get returns a visible post.
func (s *Service) Get(ctx context.Context, p gate.Ref) (gate.PostView, error) {
	_, sr, err := s.viewer(ctx)
	if err != nil {
		return gate.PostView{}, err
	}
	m, err := s.resolve(ctx, sr, p)
	if err != nil {
		return gate.PostView{}, err
	}
	vs, err := s.views(ctx, sr, []core.PostMeta{m})
	if err != nil {
		return gate.PostView{}, err
	}
	return vs[0], nil
}

// now is the clock truncated to the record precision (milliseconds), so the
// stored times equal the signed ones.
func (s *Service) now() time.Time { return s.d.Clock.Now().UTC().Truncate(time.Millisecond) }
