// Package friends owns friendships, requests, blocks, mutes, people pages and
// suggestions (ARCHITECTURE §3, §4.6, §10.4, §10.5). It implements core.Edges and
// core.Mutes and is the sole writer of edges, edge_events, friend_requests,
// blocks, mutes and hide_via.
//
// Owner: P0-W03 → P1-W03. Scaffold stub: core interfaces delegate to coreref;
// the handler-facing API returns gate.ErrNotImplemented after resolving the viewer.
package friends

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the friends module.
type Service struct {
	d     *core.Deps
	edges *coreref.MemEdges
	mutes *coreref.MemMutes
}

var (
	_ core.Edges = (*Service)(nil)
	_ core.Mutes = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, edges: coreref.NewMemEdges(), mutes: &coreref.MemMutes{}}
}

// Init registers jobs (graph.edge_events_purge, friends.requests_scrub) in W03.
func (s *Service) Init(context.Context) error { return nil }

// Suggestion is a people-you-may-know candidate (mutual friend COUNT only).
type Suggestion struct {
	Person gate.PersonView `json:"person"`
	Mutual int             `json:"mutual"`
}

// ---- handler-facing API ----

// List returns the viewer's friends.
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.PersonView], error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return gate.Page[gate.PersonView]{}, err
	}
	return gate.Page[gate.PersonView]{}, gate.ErrNotImplemented
}

// Requests returns incoming and outgoing requests.
func (s *Service) Requests(ctx context.Context) (incoming, outgoing []gate.PersonView, err error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return nil, nil, err
	}
	return nil, nil, gate.ErrNotImplemented
}

// Request sends a friend request to target.
func (s *Service) Request(ctx context.Context, target gate.Ref) error { return s.todo(ctx) }

// Accept accepts a request from from.
func (s *Service) Accept(ctx context.Context, from gate.Ref) error { return s.todo(ctx) }

// Decline declines a request (it keeps looking pending to the sender).
func (s *Service) Decline(ctx context.Context, from gate.Ref) error { return s.todo(ctx) }

// Withdraw withdraws an outgoing request.
func (s *Service) Withdraw(ctx context.Context, to gate.Ref) error { return s.todo(ctx) }

// Unfriend revokes a friendship (the Invite record is untouched).
func (s *Service) Unfriend(ctx context.Context, who gate.Ref) error { return s.todo(ctx) }

// Block blocks (and unfriends) who; never revealed to them.
func (s *Service) Block(ctx context.Context, who gate.Ref) error { return s.todo(ctx) }

// Unblock removes a block and its Block record.
func (s *Service) Unblock(ctx context.Context, who gate.Ref) error { return s.todo(ctx) }

// Blocks lists the people the viewer blocked.
func (s *Service) Blocks(ctx context.Context) ([]gate.PersonView, error) { return nil, s.todo(ctx) }

// Mute mutes or unmutes an author for the viewer.
func (s *Service) Mute(ctx context.Context, who gate.Ref, on bool) error { return s.todo(ctx) }

// Person returns a person the viewer can see.
func (s *Service) Person(ctx context.Context, who gate.Ref) (gate.PersonView, error) {
	return gate.PersonView{}, s.todo(ctx)
}

// PersonPosts returns a person's visible posts (any age).
func (s *Service) PersonPosts(ctx context.Context, who gate.Ref, cursor string) (gate.Page[gate.PostView], error) {
	return gate.Page[gate.PostView]{}, s.todo(ctx)
}

// Suggestions returns people-you-may-know candidates.
func (s *Service) Suggestions(ctx context.Context) ([]Suggestion, error) { return nil, s.todo(ctx) }

// HideVia cuts a hop without unfriending (P1).
func (s *Service) HideVia(ctx context.Context, who gate.Ref, on bool) error { return s.todo(ctx) }

// UpgradeBegin opens a friend+ upgrade handshake (P1).
func (s *Service) UpgradeBegin(ctx context.Context, who gate.Ref) (gate.HandshakeView, error) {
	return gate.HandshakeView{}, s.todo(ctx)
}

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// ---- core.Edges / core.Mutes (reference delegation) ----

// Create records a new edge.
func (s *Service) Create(ctx context.Context, tx store.Q, a, b core.UserID, lvl core.EdgeLevel, recordHash []byte) error {
	return s.edges.Create(ctx, tx, a, b, lvl, recordHash)
}

// Revoke revokes an edge.
func (s *Service) Revoke(ctx context.Context, tx store.Q, a, b, by core.UserID) error {
	return s.edges.Revoke(ctx, tx, a, b, by)
}

// Upgrade makes an edge friend+ (P1).
func (s *Service) Upgrade(ctx context.Context, tx store.Q, a, b core.UserID, recordHash []byte) error {
	return s.edges.Upgrade(ctx, tx, a, b, recordHash)
}

// Count returns u's edges and outgoing pending requests (D23).
func (s *Service) Count(ctx context.Context, u core.UserID) (int, int, error) {
	return s.edges.Count(ctx, u)
}

// MutedAuthors returns v's muted authors.
func (s *Service) MutedAuthors(ctx context.Context, v core.UserID) map[core.UserID]struct{} {
	return s.mutes.MutedAuthors(ctx, v)
}
