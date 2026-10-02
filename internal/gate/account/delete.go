package account

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

type deletePayload struct {
	User int64 `json:"user"`
}

// Notification kind sent to friends on account deletion and recovery.
const notifyFriendSecurity = "friend_security"

// Delete deletes the viewer's account (fresh step-up; confirmHandle must name
// the account). The account stops working at once — status deleted, every
// session revoked, any pending recovery cancelled — and the account.delete job
// does the fan-out. Problem confirm_mismatch.
func (s *Service) Delete(ctx context.Context, confirmHandle string) error {
	v, err := core.RequireFresh(ctx, s.d.Clock.Now())
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var (
			canon  string
			status int16
		)
		if err := tx.QueryRow(ctx, "SELECT handle_canon, status FROM users WHERE id = $1 FOR UPDATE", int64(v.ID)).
			Scan(&canon, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return gate.ErrNotFound
			}
			return err
		}
		if status == int16(core.StatusDeleted) {
			return gate.ErrNotFound
		}
		typed := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(confirmHandle), "@"))
		if typed != canon {
			return gate.NewProblem(gate.StatusUnprocessable, "confirm_mismatch", "confirm_handle")
		}
		if _, err := tx.Exec(ctx, "UPDATE users SET status = $2, deleted_at = $3 WHERE id = $1",
			int64(v.ID), int16(core.StatusDeleted), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE user_id = $1 AND state IN (0, 1)",
			int64(v.ID), stateCancelled); err != nil {
			return err
		}
		if err := s.d.Passkeys.RevokeAll(ctx, tx, v.ID); err != nil {
			return err
		}
		return s.d.Jobs.Enqueue(ctx, tx, JobDelete, deletePayload{User: int64(v.ID)}, time.Time{},
			"u"+strconv.FormatInt(int64(v.ID), 10))
	})
	if err != nil {
		return err
	}
	s.d.Bus.Publish(core.UserChanged{ID: v.ID})
	return nil
}

type tombstoneBody struct {
	Target uuid.UUID `json:"target"`
	By     uuid.UUID `json:"by"`
	At     sigs.Time `json:"at"`
}

// runDelete is the account.delete job (§10.2): tombstone every post, revoke
// every edge, keep the moderator-only stub, notify friends, then publish
// AccountDeleted so every owner deletes its own rows. The handle stays
// reserved (the users row is kept with status deleted).
func (s *Service) runDelete(ctx context.Context, user core.UserID) error {
	m, err := s.d.Users.Get(ctx, user)
	if errors.Is(err, gate.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var status int16
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT status FROM users WHERE id = $1", int64(user)).Scan(&status); err != nil {
		return err
	}
	if status != int16(core.StatusDeleted) {
		return nil // never fan out for a live account
	}
	friends := s.d.Graph.Friends(user)
	invitees, err := s.d.Invites.InviteesOf(ctx, user)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	var (
		posts   []core.PostID
		revoked []core.UserID
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		posts, revoked = nil, nil
		for _, f := range friends {
			err := s.d.Edges.Revoke(ctx, tx, user, f.To, user)
			if errors.Is(err, gate.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			revoked = append(revoked, f.To)
		}
		ids, err := s.tombstonePosts(ctx, tx, user, m.ExtID, now)
		if err != nil {
			return err
		}
		posts = ids
		inv := make([]int64, 0, len(invitees))
		for _, u := range invitees {
			inv = append(inv, int64(u))
		}
		var invitedBy *int64
		if m.InvitedBy != 0 {
			v := int64(m.InvitedBy)
			invitedBy = &v
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_stubs (user_id, invited_by, invitees, deleted_at)
			VALUES ($1, $2, $3, $4) ON CONFLICT (user_id) DO NOTHING`, int64(user), invitedBy, inv, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET display_name = '', ui_prefs = '{}'::jsonb, translate_to = NULL
			WHERE id = $1`, int64(user)); err != nil {
			return err
		}
		for _, f := range friends {
			if err := s.d.Notify.Notify(ctx, tx, core.Event{To: f.To, Kind: notifyFriendSecurity, SubjectUser: user,
				Data: map[string]any{"reason": "account_deleted"}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range revoked {
		s.d.Graph.RemoveEdge(user, f)
		s.d.Bus.Publish(core.EdgeRemoved{A: user, B: f, By: user, At: now})
	}
	for _, id := range posts {
		s.d.Posts.Delete(id)
		s.d.Bus.Publish(core.PostDeleted{ID: id, Author: user})
	}
	s.d.Bus.Publish(core.AccountDeleted{ID: user})
	s.d.Bus.Publish(core.UserChanged{ID: user})
	return nil
}

// tombstonePosts signs a Tombstone for every live post of author and
// hard-deletes its body and salt (§10.2, security #25).
//
// Local adapter: the posts table belongs to gate/posts (P0-W05), which has no
// core interface for an author-wide delete yet. docs/contract-requests/
// p0-w11-2.md asks for core.PostAdmin.DeleteAllBy; once it lands this becomes a
// single call and the SQL below moves to gate/posts.
func (s *Service) tombstonePosts(ctx context.Context, tx store.Q, author core.UserID, ext uuid.UUID, now time.Time) ([]core.PostID, error) {
	rows, err := tx.Query(ctx, "SELECT id FROM posts WHERE author_id = $1 AND deleted_at IS NULL ORDER BY created_at, id",
		int64(author))
	if err != nil {
		return nil, err
	}
	var ids []core.PostID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := s.d.Signer.Sign(ctx, tx, core.RecTombstone, author,
			tombstoneBody{Target: id, By: ext, At: sigs.T(now)}, []core.UserID{author}); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET body = NULL, body_salt = NULL, deleted_at = $2
			WHERE id = $1 AND deleted_at IS NULL`, id, now); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
