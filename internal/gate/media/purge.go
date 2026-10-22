package media

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
)

// process is the media.process payload: a deleted post or account to fan out,
// or neither (the periodic sweep). Every run also sweeps.
type process struct {
	Post  string `json:"post,omitempty"`
	Owner int64  `json:"owner,omitempty"`
}

// purgeBatch bounds one run's purge work.
const purgeBatch = 200

// enqueue schedules a media.process run (after a Bus event; durable, retried).
func (s *Service) enqueue(p process) {
	if s.d.Jobs == nil || s.d.DB == nil {
		return
	}
	ctx := context.Background()
	if err := s.d.Jobs.Enqueue(ctx, s.d.DB.Q(), JobProcess, p, s.now(), ""); err != nil && s.d.Log != nil {
		s.d.Log.Error("media enqueue", "job", JobProcess, "err", err)
	}
}

func (s *Service) runProcess(ctx context.Context, raw json.RawMessage) error {
	var p process
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
	}
	return s.Process(ctx, p.Post, p.Owner)
}

// Process marks the media of a deleted post (post id, "" for none) or of a
// deleted account (owner, 0 for none) deleted, sweeps unattached uploads older
// than OrphanTTL, deletes deleted media's blobs, purges their CDN keys, and
// deletes reported evidence past its retention (unreported evidence is never
// deleted automatically: deploy/runbooks/csam.md). Held media is never deleted here:
// evidence follows only retain_until. Safe to re-run.
func (s *Service) Process(ctx context.Context, post string, owner int64) error {
	if s.d.DB == nil {
		return nil
	}
	now := s.now()
	q := s.d.DB.Q()
	if post != "" {
		pid, err := uuid.Parse(post)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE media SET deleted_at = $2 WHERE deleted_at IS NULL AND status <> $3
			AND (source_post = $1 OR id IN (SELECT media_id FROM post_media WHERE post_id = $1))`, pid, now, statusHeld); err != nil {
			return err
		}
	}
	if owner != 0 {
		if _, err := q.Exec(ctx, `UPDATE media SET deleted_at = $2 WHERE owner_id = $1 AND deleted_at IS NULL AND status <> $3`,
			owner, now, statusHeld); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `UPDATE media SET deleted_at = $1 WHERE attached_at IS NULL AND deleted_at IS NULL
		AND owner_id IS NOT NULL AND status = $2 AND created_at < $3`, now, statusReady, now.Add(-OrphanTTL)); err != nil {
		return err
	}
	if !s.Enabled() {
		return nil // nothing can be deleted from stores that are not configured
	}
	dr, err := s.drivers()
	if err != nil {
		return err
	}
	if err := s.purgeDeleted(ctx, dr, now); err != nil {
		return err
	}
	return s.expireEvidence(ctx, dr, now)
}

func (s *Service) purgeDeleted(ctx context.Context, dr *Drivers, now time.Time) error {
	q := s.d.DB.Q()
	rows, err := q.Query(ctx, `SELECT m.id, mv.blob_key FROM media m JOIN media_variants mv ON mv.media_id = m.id
		WHERE m.id IN (SELECT id FROM media WHERE deleted_at IS NOT NULL AND purged_at IS NULL ORDER BY deleted_at LIMIT $1)`, purgeBatch)
	if err != nil {
		return err
	}
	keys := map[uuid.UUID][]string{}
	for rows.Next() {
		var (
			id  uuid.UUID
			key string
		)
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return err
		}
		keys[id] = append(keys[id], key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Deleted media without variants (none written) is purged trivially.
	if _, err := q.Exec(ctx, `UPDATE media SET purged_at = $1 WHERE deleted_at IS NOT NULL AND purged_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM media_variants mv WHERE mv.media_id = media.id)`, now); err != nil {
		return err
	}
	for id, ks := range keys {
		urls := make([]string, 0, len(ks))
		for _, k := range ks {
			if err := dr.Blobs.Delete(ctx, k); err != nil {
				return err
			}
			urls = append(urls, blob.PublicURL(s.d.Cfg.MediaBaseURL, k))
		}
		if err := dr.Purger.Purge(ctx, urls); err != nil {
			return err // retried by the job queue; the blobs are already gone at the origin
		}
		if _, err := q.Exec(ctx, `UPDATE media SET purged_at = $2 WHERE id = $1`, id, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) expireEvidence(ctx context.Context, dr *Drivers, now time.Time) error {
	q := s.d.DB.Q()
	rows, err := q.Query(ctx, `SELECT media_id, evidence_key FROM csam_holds
		WHERE reported_at IS NOT NULL AND retain_until < $1 AND evidence_purged_at IS NULL LIMIT $2`, now, purgeBatch)
	if err != nil {
		return err
	}
	type ev struct {
		id  uuid.UUID
		key string
	}
	var due []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.id, &e.key); err != nil {
			rows.Close()
			return err
		}
		due = append(due, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range due {
		if e.key != "" {
			if err := dr.Evidence.Delete(ctx, e.key); err != nil {
				return err
			}
		}
		if _, err := q.Exec(ctx, `UPDATE csam_holds SET evidence_purged_at = $2 WHERE media_id = $1`, e.id, now); err != nil {
			return err
		}
	}
	return nil
}
