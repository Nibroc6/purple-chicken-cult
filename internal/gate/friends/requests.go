package friends

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Request sends a friend request to target. A request to someone who has
// blocked the caller is stored declined and answers exactly like a fresh pending
// one; the target is never notified. Asking someone whose own request to the
// caller is pending accepts it (both sides have now said yes).
func (s *Service) Request(ctx context.Context, target gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, sr, err := s.visible(ctx, v.ID, target)
	if err != nil {
		return err
	}
	if u == v.ID {
		return errSelf()
	}
	if d, _ := sr.Dist(u); d == 1 {
		return gate.NewProblem(gate.StatusConflict, "already_friends")
	}
	if s.RequestState(ctx, v.ID, u) == "incoming" {
		return s.accept(ctx, v.ID, u)
	}
	now := s.d.Clock.Now()
	state := reqPending
	if s.d.Graph.Blocks(u, v.ID) {
		state = reqDeclined
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// Serialize the caller's own requests so the caps hold under concurrency.
		if _, err := tx.Exec(ctx, "SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE", int64(v.ID)); err != nil {
			return err
		}
		var cur *int16
		err := tx.QueryRow(ctx, "SELECT state FROM friend_requests WHERE from_id = $1 AND to_id = $2",
			int64(v.ID), int64(u)).Scan(&cur)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if cur != nil && (*cur == reqPending || *cur == reqDeclined) {
			return nil // already outgoing: idempotent
		}
		if err := s.checkRequestCaps(ctx, tx, v.ID, now); err != nil {
			return err
		}
		var decided *time.Time
		if state == reqDeclined {
			decided = &now
		}
		if _, err := tx.Exec(ctx, `INSERT INTO friend_requests (from_id, to_id, state, created_at, decided_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (from_id, to_id) DO UPDATE SET state = EXCLUDED.state, created_at = EXCLUDED.created_at,
				decided_at = EXCLUDED.decided_at`, int64(v.ID), int64(u), state, now, decided); err != nil {
			return err
		}
		if state != reqPending {
			return nil
		}
		return s.d.Notify.Notify(ctx, tx, core.Event{To: u, Kind: notifyRequest, SubjectUser: v.ID})
	})
}

// checkRequestCaps enforces the daily request limit and the friend cap, both
// on the caller's own numbers only (D23).
func (s *Service) checkRequestCaps(ctx context.Context, tx pgx.Tx, v core.UserID, now time.Time) error {
	m, err := s.d.Users.Get(ctx, v)
	if err != nil {
		return err
	}
	limit := RequestsPerDay
	if m.OnProbation(now) {
		limit = RequestsProbation
	}
	var today int
	var oldest *time.Time
	if err := tx.QueryRow(ctx, `SELECT count(*), min(created_at) FROM friend_requests
		WHERE from_id = $1 AND created_at > $2`, int64(v), now.Add(-24*time.Hour)).Scan(&today, &oldest); err != nil {
		return err
	}
	if today >= limit {
		retry := 24 * time.Hour
		if oldest != nil {
			retry = oldest.Add(24 * time.Hour).Sub(now)
		}
		return gate.RateLimited(retry)
	}
	edges, pending, err := s.count(ctx, tx, v)
	if err != nil {
		return err
	}
	if edges+pending >= FriendCap {
		return gate.NewProblem(gate.StatusConflict, "friend_cap")
	}
	return nil
}

// Accept accepts a pending request from from: Friendship record, both edges
// rows, the edge event, graph memory and a notification to the requester. Only
// the accepter's own counts can make it fail (D23).
func (s *Service) Accept(ctx context.Context, from gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, from)
	if err != nil {
		return err
	}
	return s.accept(ctx, v.ID, u)
}

func (s *Service) accept(ctx context.Context, v, u core.UserID) error {
	if !s.active(ctx, u) {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE", int64(v)); err != nil {
			return err
		}
		var st int16
		if err := tx.QueryRow(ctx, "SELECT state FROM friend_requests WHERE from_id = $1 AND to_id = $2 FOR UPDATE",
			int64(u), int64(v)).Scan(&st); err != nil {
			return notFoundOr(err)
		}
		if st != reqPending {
			return gate.ErrNotFound
		}
		edges, pending, err := s.count(ctx, tx, v)
		if err != nil {
			return err
		}
		if edges+pending >= FriendCap {
			return gate.NewProblem(gate.StatusConflict, "friend_cap")
		}
		eu, ev, err := s.exts(ctx, u, v)
		if err != nil {
			return err
		}
		a, b := ordered(eu, ev)
		h, err := s.d.Signer.Sign(ctx, tx, core.RecFriendship, v,
			friendshipBody{A: a, B: b, Level: levelName(core.LevelFriend), CreatedAt: sigs.T(now)}, []core.UserID{u, v})
		if err != nil {
			return err
		}
		if err := s.Create(ctx, tx, u, v, core.LevelFriend, h); err != nil {
			return err
		}
		// Both directions are settled, including the accepter's own request if any.
		if _, err := tx.Exec(ctx, `UPDATE friend_requests SET state = $3, decided_at = $4
			WHERE (from_id = $1 AND to_id = $2) OR (from_id = $2 AND to_id = $1 AND state IN (0, 1))`,
			int64(u), int64(v), reqAccepted, now); err != nil {
			return err
		}
		return s.d.Notify.Notify(ctx, tx, core.Event{To: u, Kind: notifyAccepted, SubjectUser: v})
	})
	if err != nil {
		return err
	}
	s.d.Graph.AddEdge(u, v, core.LevelFriend, now)
	s.d.Bus.Publish(core.EdgeAdded{A: u, B: v, Level: core.LevelFriend, At: now})
	return nil
}

// Decline declines a pending request from from. The sender keeps seeing it as
// pending.
func (s *Service) Decline(ctx context.Context, from gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, from)
	if err != nil {
		return err
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE friend_requests SET state = $3, decided_at = $4
		WHERE from_id = $1 AND to_id = $2 AND state = 0`, int64(u), int64(v.ID), reqDeclined, s.d.Clock.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return gate.ErrNotFound
	}
	return nil
}

// Withdraw withdraws the caller's outgoing request to to (pending or declined:
// the caller cannot tell them apart).
func (s *Service) Withdraw(ctx context.Context, to gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, to)
	if err != nil {
		return err
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE friend_requests SET state = $3, decided_at = $4
		WHERE from_id = $1 AND to_id = $2 AND state IN (0, 1)`, int64(v.ID), int64(u), reqWithdrawn, s.d.Clock.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return gate.ErrNotFound
	}
	return nil
}

// Requests returns the caller's incoming pending requests and outgoing
// requests (declined ones included), newest first, as people the caller can see.
func (s *Service) Requests(ctx context.Context) (incoming, outgoing []gate.PersonView, err error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, nil, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return nil, nil, err
	}
	in, err := s.requestPeers(ctx, `SELECT from_id FROM friend_requests WHERE to_id = $1 AND state = 0
		ORDER BY created_at DESC NULLS LAST, from_id`, v.ID)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.requestPeers(ctx, `SELECT to_id FROM friend_requests WHERE from_id = $1 AND state IN (0, 1)
		ORDER BY created_at DESC NULLS LAST, to_id`, v.ID)
	if err != nil {
		return nil, nil, err
	}
	incoming, err = s.people(ctx, sr, in)
	if err != nil {
		return nil, nil, err
	}
	outgoing, err = s.people(ctx, sr, out)
	return incoming, outgoing, err
}

func (s *Service) requestPeers(ctx context.Context, sql string, v core.UserID) ([]core.UserID, error) {
	rows, err := s.d.DB.Q().Query(ctx, sql, int64(v))
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	out := make([]core.UserID, len(ids))
	for i, id := range ids {
		out[i] = core.UserID(id)
	}
	return out, nil
}

// people builds person views for us in order, skipping anyone the viewer
// cannot see.
func (s *Service) people(ctx context.Context, sr core.Search, us []core.UserID) ([]gate.PersonView, error) {
	out := make([]gate.PersonView, 0, len(us))
	for _, u := range us {
		pv, err := s.d.Views.Person(ctx, sr, u)
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

// Unfriend revokes the friendship with who (Revocation record; both records
// purge after Retention). The Invite record and the inviter label stay.
func (s *Service) Unfriend(ctx context.Context, who gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return err
	}
	if _, ok := s.d.Graph.Level(v.ID, u); !ok {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	if err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error { return s.Revoke(ctx, tx, v.ID, u, v.ID) }); err != nil {
		return err
	}
	s.d.Graph.RemoveEdge(v.ID, u)
	s.d.Bus.Publish(core.EdgeRemoved{A: v.ID, B: u, By: v.ID, At: now})
	return nil
}
