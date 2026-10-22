package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/blob"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/imageproc"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/scan"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// holdSubject is who and what a match is about.
type holdSubject struct {
	user      core.UserID // uploader, or the linking post's author (0 = none)
	post      core.PostID // preview images: the linking post
	sourceURL string      // preview images: the canonical URL that served the image
	source    string      // "upload" | "preview" (queue summary code)
}

// hold handles a scanner match (ARCHITECTURE §6.1, security #7; deploy/runbooks/csam.md):
//
//  1. the exact bytes go to the SEPARATE evidence store under a random key
//     (preserved one year, never served, never in the media store);
//  2. one transaction writes the media row (status held, no variants), the
//     csam_holds row (match metadata, source URL, subject, retain_until) and the
//     priority-0 moderation item naming the held media;
//  3. from then on the subject's uploads are media_rejected (account hold).
//
// If the evidence store fails, the hold and the queue item are still written
// (match_info "evidence": "unavailable") so the account is held and moderators
// see the match; the error is logged for the operator.
func (s *Service) hold(ctx context.Context, dr *Drivers, img []byte, info imageproc.Info, v scan.Verdict, sub holdSubject) error {
	ctx = context.WithoutCancel(ctx) // a client hang-up must not abandon a hold half-way
	id := s.d.IDs.New()
	now := s.now()
	match := map[string]string{}
	for k, val := range v.Info {
		match[k] = val
	}
	match["source"] = sub.source
	evKey, err := blob.NewKey()
	if err == nil {
		err = dr.Evidence.Put(ctx, evKey, bytes.NewReader(img), int64(len(img)), info.MIME())
	}
	if err != nil {
		evKey = ""
		match["evidence"] = "unavailable"
		if s.d.Log != nil {
			s.d.Log.Error("csam evidence not preserved", "feature", "media", "err", err)
		}
	}
	mi, err := json.Marshal(match)
	if err != nil {
		return err
	}
	var userArg, postArg, urlArg any
	if sub.user != 0 {
		userArg = int64(sub.user)
	}
	if sub.post != uuid.Nil {
		postArg = sub.post
	}
	if sub.sourceURL != "" {
		urlArg = sub.sourceURL
	}
	var ownerArg any
	if sub.source == "upload" {
		ownerArg = userArg
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO media (id, owner_id, source_post, status, kind, width, height, scan_driver, scanned_at, created_at)
			VALUES ($1, $2, $3, $4, 0, $5, $6, $7, $8, $8)`, id, ownerArg, postArg, statusHeld, info.Width, info.Height, match["driver"], now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO csam_holds (media_id, evidence_key, match_info, source_url, subject_user, held_at, retain_until)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, evKey, mi, urlArg, userArg, now, now.Add(Retention)); err != nil {
			return err
		}
		if s.d.ModQueue == nil {
			return errors.New("media: no moderation queue")
		}
		return s.d.ModQueue.Enqueue(ctx, tx, core.ModQueueItem{
			Source: core.ModSrcCSAM, Priority: 0, SubjectUser: sub.user, SubjectPost: sub.post, Media: id,
			Summary: map[string]int{"csam_match": 1, sub.source: 1},
		})
	})
	if err != nil {
		if evKey != "" {
			_ = dr.Evidence.Delete(ctx, evKey)
		}
		return err
	}
	if s.d.Log != nil {
		s.d.Log.Error("csam match held", "feature", "media", "kind", sub.source, logx.UID(uint32(sub.user)))
	}
	return nil
}

// accountHeld reports whether user has an unreleased CSAM hold.
func (s *Service) accountHeld(ctx context.Context, user core.UserID) (bool, error) {
	var held bool
	err := s.d.DB.Q().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM csam_holds WHERE subject_user = $1 AND account_released_at IS NULL)`,
		int64(user)).Scan(&held)
	return held, err
}

// ---- core.CSAMHolds ----

// Hold returns the moderator view of media's hold: metadata only.
func (s *Service) Hold(ctx context.Context, m uuid.UUID) (core.CSAMHold, error) {
	if s.d.DB == nil {
		return core.CSAMHold{}, gate.ErrNotFound
	}
	var (
		h       core.CSAMHold
		mi      []byte
		url     *string
		subject *int64
		post    *uuid.UUID
		ref     *string
		rel     *time.Time
	)
	err := s.d.DB.Q().QueryRow(ctx, `SELECT h.match_info, h.source_url, h.subject_user, m.source_post, h.held_at, h.retain_until,
		h.reported_at, h.report_ref, h.account_released_at
		FROM csam_holds h JOIN media m ON m.id = h.media_id WHERE h.media_id = $1`, m).
		Scan(&mi, &url, &subject, &post, &h.HeldAt, &h.RetainUntil, &h.ReportedAt, &ref, &rel)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.CSAMHold{}, gate.ErrNotFound
	}
	if err != nil {
		return core.CSAMHold{}, err
	}
	h.Media = m
	if err := json.Unmarshal(mi, &h.MatchInfo); err != nil {
		return core.CSAMHold{}, err
	}
	if url != nil {
		h.SourceURL = *url
	}
	if subject != nil {
		h.SubjectUser = core.UserID(*subject) //nolint:gosec // user ids < 2^32 (DB CHECK)
	}
	if post != nil {
		h.SourcePost = *post
	}
	if ref != nil {
		h.ReportRef = *ref
	}
	h.AccountHeld = rel == nil && h.SubjectUser != 0
	return h, nil
}

var reportRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// Report records the CyberTipline report reference of media's hold, once.
func (s *Service) Report(ctx context.Context, tx store.Q, m uuid.UUID, reportRef string) error {
	if !reportRefRE.MatchString(reportRef) {
		return gate.NewProblem(gate.StatusBadRequest, "bad_request", "report_ref")
	}
	var reported bool
	err := tx.QueryRow(ctx, `SELECT reported_at IS NOT NULL FROM csam_holds WHERE media_id = $1 FOR UPDATE`, m).Scan(&reported)
	if errors.Is(err, pgx.ErrNoRows) {
		return gate.ErrNotFound
	}
	if err != nil {
		return err
	}
	if reported {
		return gate.NewProblem(gate.StatusConflict, "csam_already_reported")
	}
	// Preservation runs one year from the report at least (REPORT Act 2024).
	now := s.now()
	_, err = tx.Exec(ctx, `UPDATE csam_holds SET report_ref = $2, reported_at = $3, retain_until = GREATEST(retain_until, $4)
		WHERE media_id = $1`, m, reportRef, now, now.Add(Retention))
	return err
}

// ReleaseAccount lifts the upload hold of media's subject (evidence stays).
func (s *Service) ReleaseAccount(ctx context.Context, tx store.Q, m uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE csam_holds SET account_released_at = $2 WHERE media_id = $1 AND account_released_at IS NULL`, m, s.now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM csam_holds WHERE media_id = $1`, m).Scan(&one); errors.Is(err, pgx.ErrNoRows) {
			return gate.ErrNotFound
		} else if err != nil {
			return err
		}
	}
	return nil
}
