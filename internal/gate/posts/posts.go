// Package posts owns posts, parent references, continuation threads, edits,
// deletion, reference policies, detach and mentions (ARCHITECTURE §10.1.1,
// §10.1.4, §10.1.5, §10.2, §10.6). It is the sole writer of posts, post_versions,
// post_parents and mentions.
//
// Owner: P0-W05 → P1-W12. Scaffold stub: every method returns
// gate.ErrNotImplemented after checking the viewer.
//
// Check order (plan §0.8): the write was charged by apix → resolve every parent
// ref (ErrNotFound) → only then Problems (body_empty, body_too_long, lang_invalid,
// too_many_parents, duplicate_parent, cont_not_own, cont_taken, ref_not_allowed,
// ref_cap, too_many_parts).
package posts

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Limits (§10.1.5).
const (
	MaxBodyCodePoints = 20000
	SoftLimit         = 500
	CounterFrom       = 280
	MaxParents        = 3
	MaxThreadParts    = 25
	RefCapPerDay      = 2
)

// Service is the posts module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers jobs and Bus subscriptions in W05.
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

// Create creates a post.
func (s *Service) Create(ctx context.Context, d Draft) (gate.PostView, error) {
	return gate.PostView{}, s.todo(ctx)
}

// CreateThread creates a continuation thread (≤25 parts; one write charge).
func (s *Service) CreateThread(ctx context.Context, parts []Draft) ([]gate.PostView, error) {
	return nil, s.todo(ctx)
}

// Get returns a visible post.
func (s *Service) Get(ctx context.Context, p gate.Ref) (gate.PostView, error) {
	return gate.PostView{}, s.todo(ctx)
}

// Edit creates a new version (parents are immutable).
func (s *Service) Edit(ctx context.Context, p gate.Ref, body, lang string) (gate.PostView, error) {
	return gate.PostView{}, s.todo(ctx)
}

// Delete tombstones a post and hard-deletes its body and salt.
func (s *Service) Delete(ctx context.Context, p gate.Ref) error { return s.todo(ctx) }

// SetRefPolicy changes who may reference a post.
func (s *Service) SetRefPolicy(ctx context.Context, p gate.Ref, pol string) (bool, error) {
	return false, s.todo(ctx)
}

// Detach detaches child from parent (parent's author only).
func (s *Service) Detach(ctx context.Context, parent, child gate.Ref) error { return s.todo(ctx) }

// DetachAll detaches every child of parent.
func (s *Service) DetachAll(ctx context.Context, parent gate.Ref) error { return s.todo(ctx) }

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}
