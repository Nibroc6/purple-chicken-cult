package media

import (
	"context"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// MaxPostMedia is the per-post attachment cap (plan P1-W12).
const MaxPostMedia = 4

// Attach resolves owner's refs to owner's ready, live, not yet attached
// uploads and marks them attached in tx (the post's transaction), all or none.
// Any ref that does not resolve to such an upload — another user's media, a
// preview image, a held or deleted item, one already attached, a duplicate —
// is the uniform gate.ErrNotFound and attaches nothing.
func (s *Service) Attach(ctx context.Context, tx store.Q, owner core.UserID, refs []gate.Ref) ([]uuid.UUID, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if _, err := s.live(); err != nil {
		return nil, err
	}
	if len(refs) > MaxPostMedia {
		return nil, gate.NewProblem(gate.StatusBadRequest, "bad_request", "media")
	}
	ids := make([]uuid.UUID, 0, len(refs))
	seen := map[uuid.UUID]bool{}
	for _, r := range refs {
		id, ok := s.d.Refs.OpenMedia(owner, r)
		if !ok || seen[id] || owner == 0 {
			return nil, gate.ErrNotFound
		}
		seen[id] = true
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, `WITH ok AS (
			SELECT id FROM media WHERE id = ANY($1) AND owner_id = $2 AND status = $3
			  AND deleted_at IS NULL AND attached_at IS NULL FOR UPDATE)
		UPDATE media SET attached_at = $4
		WHERE id IN (SELECT id FROM ok) AND (SELECT count(*) FROM ok) = $5
		RETURNING id`, ids, int64(owner), statusReady, s.now(), len(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if n != len(ids) {
		return nil, gate.ErrNotFound
	}
	return ids, nil
}

// Views renders the ready, live items among ids, in order, with refs for the
// searching viewer. The caller has already decided the post (or preview) is
// visible; Views adds no visibility of its own.
func (s *Service) Views(ctx context.Context, sr core.Search, ids []uuid.UUID) ([]gate.MediaView, error) {
	if len(ids) == 0 || !s.Enabled() || s.d.DB == nil || sr == nil || sr.Viewer() == 0 {
		return nil, nil
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT id, width, height, COALESCE(alt, '') FROM media
		WHERE id = ANY($1) AND status = $2 AND deleted_at IS NULL`, ids, statusReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[uuid.UUID]gate.MediaView{}
	for rows.Next() {
		var (
			id  uuid.UUID
			mv  gate.MediaView
			w32 *int32
			h32 *int32
		)
		if err := rows.Scan(&id, &w32, &h32, &mv.Alt); err != nil {
			return nil, err
		}
		if w32 != nil && h32 != nil {
			mv.W, mv.H = int(*w32), int(*h32)
		}
		mv.Ref = s.d.Refs.Media(sr.Viewer(), id)
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
