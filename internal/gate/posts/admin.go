package posts

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// DeleteAllBy tombstones every live post of author in tx exactly like Delete
// (Tombstone record; body, salt and mentions hard-deleted) and returns their IDs
// (core.PostAdmin, contract request p0-w11-2; used by the account.delete job).
// The caller runs PostIndex.Delete and publishes PostDeleted after commit.
func (s *Service) DeleteAllBy(ctx context.Context, tx store.Q, author core.UserID) ([]core.PostID, error) {
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
	if len(ids) == 0 {
		return nil, nil
	}
	m, err := s.d.Users.Get(ctx, author)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]core.PostID, 0, len(ids))
	for _, id := range ids {
		ok, err := s.tombstone(ctx, tx, id, author, m.ExtID, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// RemovePost tombstones the live post id in tx on a moderator's decision
// (core.PostRemover, contract request p0-w12-1): the Tombstone record names the
// moderator by as the actor; body, salt and mentions are hard-deleted. ok is
// false when the post is not live. The caller runs PostIndex.Delete and
// publishes PostDeleted after commit.
func (s *Service) RemovePost(ctx context.Context, tx store.Q, id core.PostID, by core.UserID) (core.UserID, bool, error) {
	var author int64
	err := tx.QueryRow(ctx, "SELECT author_id FROM posts WHERE id = $1 AND deleted_at IS NULL", id).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	actor, err := s.d.Users.Get(ctx, by)
	if err != nil {
		return 0, false, err
	}
	ok, err := s.tombstone(ctx, tx, id, core.UserID(author), actor.ExtID, s.now()) //nolint:gosec // users.id < 2^32 (DB CHECK)
	if err != nil || !ok {
		return 0, false, err
	}
	return core.UserID(author), true, nil //nolint:gosec // users.id < 2^32 (DB CHECK)
}
