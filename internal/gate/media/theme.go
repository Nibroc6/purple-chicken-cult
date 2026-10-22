package media

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

var _ core.ThemeMedia = (*Service)(nil)

// Release marks owner's media ids deleted in tx: theme images the theme no
// longer uses (contract request p1-w09-3). Held media is left to its hold.
func (s *Service) Release(ctx context.Context, tx store.Q, owner core.UserID, ids []uuid.UUID) error {
	if len(ids) == 0 || owner == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE media SET deleted_at = $3 WHERE id = ANY($1) AND owner_id = $2
		AND deleted_at IS NULL AND status <> $4`, ids, int64(owner), s.now(), statusHeld)
	return err
}

// Own renders owner's ready, live media among ids, in order, with owner's
// refs (theme images: the owner may always get their URLs).
func (s *Service) Own(ctx context.Context, owner core.UserID, ids []uuid.UUID) ([]gate.MediaView, error) {
	if len(ids) == 0 || owner == 0 || !s.Enabled() || s.d.DB == nil {
		return nil, nil
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT id, width, height, COALESCE(alt, '') FROM media
		WHERE id = ANY($1) AND owner_id = $2 AND status = $3 AND deleted_at IS NULL`, ids, int64(owner), statusReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[uuid.UUID]gate.MediaView{}
	for rows.Next() {
		var (
			id     uuid.UUID
			mv     gate.MediaView
			w32, h *int32
		)
		if err := rows.Scan(&id, &w32, &h, &mv.Alt); err != nil {
			return nil, err
		}
		if w32 != nil && h != nil {
			mv.W, mv.H = int(*w32), int(*h)
		}
		mv.Ref = s.d.Refs.Media(owner, id)
		got[id] = mv
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]gate.MediaView, 0, len(ids))
	for _, id := range ids {
		if mv, ok := got[id]; ok {
			out = append(out, mv)
		}
	}
	return out, nil
}
