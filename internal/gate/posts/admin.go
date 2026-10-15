package posts

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Steward (contract request p0-w11-2): hand-off stub with a WORKING body so the
// account.delete job tombstones posts before P0-W05 lands. W05 replaces it with
// the same Tombstone path posts.Delete uses.

var _ core.PostAdmin = (*Service)(nil)

// tombstoneBody is the signed Tombstone record body (§10.2).
type tombstoneBody struct {
	Target uuid.UUID `json:"target"`
	By     uuid.UUID `json:"by"`
	At     sigs.Time `json:"at"`
}

// DeleteAllBy tombstones every live post of author in tx (Tombstone record;
// body and salt hard-deleted) and returns their IDs. The caller runs
// PostIndex.Delete and publishes PostDeleted after commit.
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
	now := s.d.Clock.Now()
	for _, id := range ids {
		if _, err := s.d.Signer.Sign(ctx, tx, core.RecTombstone, author,
			tombstoneBody{Target: id, By: m.ExtID, At: sigs.T(now)}, []core.UserID{author}); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET body = NULL, body_salt = NULL, deleted_at = $2
			WHERE id = $1 AND deleted_at IS NULL`, id, now); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
