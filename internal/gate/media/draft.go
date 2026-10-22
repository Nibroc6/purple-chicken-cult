package media

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// SetAlt replaces the alt text of one of the viewer's own ready uploads that is
// not attached yet (PATCH /api/media/{ref}). The composer uploads a picture as
// soon as it is picked and sends the alt text when the post is published.
// Anything else (another user's media, attached, held or deleted) is the
// uniform gate.ErrNotFound.
func (s *Service) SetAlt(ctx context.Context, m gate.Ref, alt string) (gate.MediaView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.MediaView{}, err
	}
	if _, err := s.live(); err != nil {
		return gate.MediaView{}, err
	}
	alt, err = cleanAlt(alt)
	if err != nil {
		return gate.MediaView{}, err
	}
	id, ok := s.d.Refs.OpenMedia(v.ID, m)
	if !ok {
		return gate.MediaView{}, gate.ErrNotFound
	}
	var altArg any
	if alt != "" {
		altArg = alt
	}
	var (
		mv     gate.MediaView
		w32, h *int32
	)
	err = s.d.DB.Q().QueryRow(ctx, `UPDATE media SET alt = $3 WHERE id = $1 AND owner_id = $2 AND status = $4
		AND deleted_at IS NULL AND attached_at IS NULL RETURNING width, height`,
		id, int64(v.ID), altArg, statusReady).Scan(&w32, &h)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.MediaView{}, gate.ErrNotFound
		}
		return gate.MediaView{}, err
	}
	if w32 != nil && h != nil {
		mv.W, mv.H = int(*w32), int(*h)
	}
	mv.Ref, mv.Alt = m, alt
	return mv, nil
}

// Discard deletes one of the viewer's own ready uploads that is not attached
// yet (DELETE /api/media/{ref}): a picture removed from a draft, or a draft
// left without publishing. Its blobs go with the next media.process run.
// Discarding something already gone, or not the viewer's, is the uniform
// gate.ErrNotFound.
func (s *Service) Discard(ctx context.Context, m gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	if _, err := s.live(); err != nil {
		return err
	}
	id, ok := s.d.Refs.OpenMedia(v.ID, m)
	if !ok {
		return gate.ErrNotFound
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE media SET deleted_at = $3 WHERE id = $1 AND owner_id = $2 AND status = $4
		AND deleted_at IS NULL AND attached_at IS NULL`, id, int64(v.ID), s.now(), statusReady)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return gate.ErrNotFound
	}
	s.enqueue(process{})
	return nil
}
