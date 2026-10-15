package friends

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Block blocks who: unfriends them, settles pending requests both ways
// without telling them (theirs to the caller turns declined, which still looks
// pending to them), writes the Block record and cuts every path through them
// from the caller's searches. Nothing is ever revealed to the blocked person.
func (s *Service) Block(ctx context.Context, who gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return err
	}
	if s.d.Graph.Blocks(v.ID, u) {
		return nil // idempotent
	}
	if u, _, err = s.visible(ctx, v.ID, who); err != nil {
		return err
	}
	if u == v.ID {
		return errSelf()
	}
	now := s.d.Clock.Now()
	var unfriended, added bool
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		unfriended, added = false, false
		if err := s.Revoke(ctx, tx, v.ID, u, v.ID); err == nil {
			unfriended = true
		} else if !errors.Is(err, gate.ErrNotFound) {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE friend_requests SET state = $3, decided_at = $4
			WHERE from_id = $1 AND to_id = $2 AND state IN (0, 1)`, int64(v.ID), int64(u), reqWithdrawn, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE friend_requests SET state = $3, decided_at = $4
			WHERE from_id = $2 AND to_id = $1 AND state = 0`, int64(v.ID), int64(u), reqDeclined, now); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "INSERT INTO blocks (blocker, blocked, at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
			int64(v.ID), int64(u), now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		added = true
		ev, eu, err := s.exts(ctx, v.ID, u)
		if err != nil {
			return err
		}
		h, err := s.d.Signer.Sign(ctx, tx, core.RecBlock, v.ID, blockBody{Blocker: ev, Blocked: eu, At: sigs.T(now)},
			[]core.UserID{v.ID})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE blocks SET record_hash = $3 WHERE blocker = $1 AND blocked = $2",
			int64(v.ID), int64(u), h)
		return err
	})
	if err != nil {
		return err
	}
	if unfriended {
		s.d.Graph.RemoveEdge(v.ID, u)
		s.d.Bus.Publish(core.EdgeRemoved{A: v.ID, B: u, By: v.ID, At: now})
	}
	if added {
		s.d.Graph.SetBlock(v.ID, u, true)
		s.d.Bus.Publish(core.BlockChanged{Blocker: v.ID, Blocked: u, On: true})
	}
	return nil
}

// Unblock removes the caller's block on who and deletes its Block record.
func (s *Service) Unblock(ctx context.Context, who gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return err
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var h []byte
		if err := tx.QueryRow(ctx, "DELETE FROM blocks WHERE blocker = $1 AND blocked = $2 RETURNING record_hash",
			int64(v.ID), int64(u)).Scan(&h); err != nil {
			return notFoundOr(err)
		}
		if h == nil {
			return nil
		}
		return s.d.Signer.Delete(ctx, tx, h)
	})
	if err != nil {
		return err
	}
	s.d.Graph.SetBlock(v.ID, u, false)
	s.d.Bus.Publish(core.BlockChanged{Blocker: v.ID, Blocked: u, On: false})
	return nil
}

// Blocks lists the people the caller blocked, newest first. Blocked people are
// outside the caller's searches, so they stay addressable here (and only here,
// for the blocker) to allow unblocking.
func (s *Service) Blocks(ctx context.Context) ([]gate.PersonView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.d.DB.Q().Query(ctx, "SELECT blocked FROM blocks WHERE blocker = $1 ORDER BY at DESC, blocked",
		int64(v.ID))
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	out := make([]gate.PersonView, 0, len(ids))
	for _, id := range ids {
		pv, err := s.blockedView(ctx, v.ID, core.UserID(id))
		if errors.Is(err, gate.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, pv)
	}
	return out, nil
}

// blockedView is the blocker's own view of someone they blocked: no chain (no
// path is computed through or to them).
func (s *Service) blockedView(ctx context.Context, v, u core.UserID) (gate.PersonView, error) {
	m, err := s.d.Users.Get(ctx, u)
	if err != nil {
		return gate.PersonView{}, err
	}
	if m.Status == core.StatusDeleted {
		return gate.PersonView{}, gate.ErrNotFound
	}
	label, err := s.d.Users.InviterLabel(ctx, u)
	if err != nil {
		return gate.PersonView{}, err
	}
	_, muted := s.MutedAuthors(ctx, v)[u]
	return gate.PersonView{Ref: s.d.Refs.User(v, u), Handle: m.Handle, DisplayName: m.DisplayName, Inviter: label,
		Relation: "connected", Blocked: true, Muted: muted}, nil
}

// Mute mutes (on) or unmutes an author for the caller's own feed.
func (s *Service) Mute(ctx context.Context, who gate.Ref, on bool) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return err
	}
	if !on {
		_, err := s.d.DB.Q().Exec(ctx, "DELETE FROM mutes WHERE user_id = $1 AND author_id = $2", int64(v.ID), int64(u))
		return err
	}
	if _, ok := s.MutedAuthors(ctx, v.ID)[u]; !ok && !s.d.Graph.Blocks(v.ID, u) {
		if _, _, err := s.visible(ctx, v.ID, who); err != nil {
			return err
		}
	}
	if u == v.ID {
		return errSelf()
	}
	_, err = s.d.DB.Q().Exec(ctx, "INSERT INTO mutes (user_id, author_id, at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
		int64(v.ID), int64(u), s.d.Clock.Now())
	return err
}
