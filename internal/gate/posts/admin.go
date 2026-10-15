package posts

import (
	"context"

	"github.com/google/uuid"

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
