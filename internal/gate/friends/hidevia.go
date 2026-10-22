package friends

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// HideVia hides (on) or shows again the paths through who (ARCHITECTURE §4
// rule 2, §4.4, §10.4: "cuts a hop without unfriending"). The caller's
// searches still reach who — their own posts stay — but never route through
// them (transit only, D7), so people the caller sees only through who drop
// out of reach and the others are reached around them. Only one of the
// caller's friends can be hidden via (the first hop of a chain, the blame
// of §4.4); showing again works for anyone the caller hid. Adding bumps the
// graph epoch, so every cached search and feed snapshot narrows on its next
// read. It is never revealed to who.
func (s *Service) HideVia(ctx context.Context, who gate.Ref, on bool) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return err
	}
	if !on {
		tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM hide_via WHERE user_id = $1 AND via_id = $2", int64(v.ID), int64(u))
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			s.d.Graph.SetHideVia(v.ID, u, false)
			s.d.Bus.Publish(core.HideViaChanged{User: v.ID, Via: u, On: false})
		}
		return nil
	}
	if u == v.ID {
		return gate.ErrNotFound
	}
	if _, ok := s.d.Graph.Level(v.ID, u); !ok || !s.active(ctx, u) {
		return gate.ErrNotFound
	}
	tag, err := s.d.DB.Q().Exec(ctx, "INSERT INTO hide_via (user_id, via_id, at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
		int64(v.ID), int64(u), s.d.Clock.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		s.d.Graph.SetHideVia(v.ID, u, true)
		s.d.Bus.Publish(core.HideViaChanged{User: v.ID, Via: u, On: true})
	}
	return nil
}

// UpgradeBegin opens a friend+ upgrade handshake with a plain friend (§7,
// core.Handshakes.OpenUpgrade): the caller's device shows the rotating code,
// the friend scans it from their own signed-in session at /join, both hear
// the sound, and a Verified outcome lets both co-sign the friend+ Friendship.
// Quiet turns the jingle off (the ultrasonic layer still verifies). Anyone
// else — a friend+ already, not a friend, an inactive account — is the
// uniform not-found.
func (s *Service) UpgradeBegin(ctx context.Context, who gate.Ref, quiet bool) (gate.HandshakeView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	if s.d.Handshakes == nil {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	return s.d.Handshakes.OpenUpgrade(ctx, v, u, quiet)
}
