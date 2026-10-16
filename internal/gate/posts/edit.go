package posts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Edit creates a new version of the caller's own post: same id, version + 1, a
// fresh salt and a new Post record; parents are immutable. Mentions are rebuilt
// (newly mentioned users are notified) and previews re-fetched. lang "" keeps
// the current language.
func (s *Service) Edit(ctx context.Context, p gate.Ref, body, lang string) (gate.PostView, error) {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return gate.PostView{}, err
	}
	m, err := s.resolveOwn(ctx, sr, p)
	if err != nil {
		return gate.PostView{}, err
	}
	b, err := cleanBody(body)
	if err != nil {
		return gate.PostView{}, err
	}
	l := m.Lang
	if lang != "" {
		if l, err = cleanLang(lang); err != nil {
			return gate.PostView{}, err
		}
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.PostView{}, err
	}
	var next core.PostMeta
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var (
			version   int32
			noPreview bool
		)
		if err := tx.QueryRow(ctx, `SELECT version, no_preview FROM posts
			WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL FOR UPDATE`, m.ID, int64(v.ID)).
			Scan(&version, &noPreview); err != nil {
			return notFoundOr(err)
		}
		now := s.now()
		next = m
		next.Version, next.Lang, next.EditedAt = int(version)+1, l, &now
		parents, err := s.parentRecs(ctx, tx, m.ID)
		if err != nil {
			return err
		}
		salt, err := newSalt()
		if err != nil {
			return err
		}
		// The record keeps the original created_at; the version row says when.
		hash, err := s.signPost(ctx, tx, me.ExtID, next, salt, b, parents, noPreview)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET body = $2, body_salt = $3, lang = $4, version = $5, edited_at = $6,
			record_hash = $7 WHERE id = $1`, m.ID, b, salt, l, next.Version, now, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO post_versions (post_id, version, record_hash, created_at)
			VALUES ($1, $2, $3, $4)`, m.ID, next.Version, hash, now); err != nil {
			return err
		}
		before, err := mentionedIn(ctx, tx, m.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM mentions WHERE post_id = $1", m.ID); err != nil {
			return err
		}
		ms := s.mentionTargets(ctx, sr, v.ID, b)
		if err := insertMentions(ctx, tx, m.ID, ms); err != nil {
			return err
		}
		before[v.ID] = true
		if err := s.notifyMentions(ctx, tx, v.ID, next, ms, before); err != nil {
			return err
		}
		if !noPreview && s.d.Jobs != nil {
			return s.d.Jobs.Enqueue(ctx, tx, JobPreviewFetch, map[string]any{"post": m.ID.String(), "version": next.Version},
				time.Time{}, m.ID.String())
		}
		return nil
	})
	if err != nil {
		return gate.PostView{}, err
	}
	s.d.Posts.Put(next)
	s.d.Bus.Publish(core.PostEdited{ID: next.ID, Version: next.Version})
	vs, err := s.views(ctx, sr, []core.PostMeta{next})
	if err != nil {
		return gate.PostView{}, err
	}
	return vs[0], nil
}

// parentRecs reads a post's parents for its record (in ord order).
func (s *Service) parentRecs(ctx context.Context, tx pgx.Tx, id core.PostID) ([]parentRec, error) {
	rows, err := tx.Query(ctx, "SELECT parent_id, kind FROM post_parents WHERE child_id = $1 ORDER BY ord", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []parentRec{}
	for rows.Next() {
		var (
			pid  uuid.UUID
			kind int16
		)
		if err := rows.Scan(&pid, &kind); err != nil {
			return nil, err
		}
		out = append(out, parentRec{ID: pid, Kind: core.LinkKind(kind).String()}) //nolint:gosec // DB CHECK (0, 1)
	}
	return out, rows.Err()
}

// mentionedIn returns the users a post mentions now.
func mentionedIn(ctx context.Context, tx pgx.Tx, id core.PostID) (map[core.UserID]bool, error) {
	rows, err := tx.Query(ctx, "SELECT target_id FROM mentions WHERE post_id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.UserID]bool{}
	for rows.Next() {
		var u int64
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out[core.UserID(u)] = true //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	return out, rows.Err()
}

// Delete tombstones the caller's own post: a signed Tombstone record, then body
// and salt hard-deleted together (security #25). Children stay; for everyone
// else they render as roots (a deleted parent looks like an invisible one).
func (s *Service) Delete(ctx context.Context, p gate.Ref) error {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return err
	}
	m, err := s.resolveOwn(ctx, sr, p)
	if err != nil {
		return err
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return err
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		ok, err := s.tombstone(ctx, tx, m.ID, v.ID, me.ExtID, s.now())
		if err != nil {
			return err
		}
		if !ok {
			return gate.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.d.Posts.Delete(m.ID)
	s.d.Bus.Publish(core.PostDeleted{ID: m.ID, Author: v.ID})
	return nil
}

// tombstone signs the Tombstone record of a live post and hard-deletes its body,
// salt and mentions in tx. It reports false when the post is not live.
func (s *Service) tombstone(ctx context.Context, tx store.Q, id core.PostID, author core.UserID, by uuid.UUID, now time.Time) (bool, error) {
	var x int
	err := tx.QueryRow(ctx, "SELECT 1 FROM posts WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL FOR UPDATE",
		id, int64(author)).Scan(&x)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := s.d.Signer.Sign(ctx, tx, core.RecTombstone, author, tombstoneBody{Target: id, By: by, At: sigs.T(now)},
		[]core.UserID{author}); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "UPDATE posts SET body = NULL, body_salt = NULL, deleted_at = $2 WHERE id = $1", id, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM mentions WHERE post_id = $1", id); err != nil {
		return false, err
	}
	return true, nil
}

// SetRefPolicy changes who may reference the caller's own post. Switching to
// nobody (slippery) answers offerDetachAll = true when the post has attached
// references the author can see, so the client can offer "detach all".
func (s *Service) SetRefPolicy(ctx context.Context, p gate.Ref, pol string) (bool, error) {
	_, sr, err := s.viewer(ctx)
	if err != nil {
		return false, err
	}
	m, err := s.resolveOwn(ctx, sr, p)
	if err != nil {
		return false, err
	}
	np, set, err := parsePolicy(pol)
	if err != nil {
		return false, err
	}
	if !set {
		return false, gate.NewProblem(gate.StatusUnprocessable, "ref_policy_invalid", "ref_policy")
	}
	if _, err := s.d.DB.Q().Exec(ctx, "UPDATE posts SET ref_policy = $2 WHERE id = $1 AND deleted_at IS NULL",
		m.ID, int16(np)); err != nil {
		return false, err
	}
	next := m
	next.RefPolicy = np
	s.d.Posts.Put(next)
	if np != core.PolNobody {
		return false, nil
	}
	// Only references the author could see under the previous policy count:
	// shadow links are never revealed.
	offer := len(s.d.Vis.VisibleChildren(ctx, sr, m, core.ChildFilter{RefOnly: true, NotSelf: true, Limit: 1})) > 0
	return offer, nil
}

// Detach detaches child from the caller's own post parent. The link must be
// one the caller can see (a shadow link answers like an unknown one);
// continuations are the author's own thread and cannot be detached.
func (s *Service) Detach(ctx context.Context, parent, child gate.Ref) error {
	_, sr, err := s.viewer(ctx)
	if err != nil {
		return err
	}
	pm, err := s.resolveOwn(ctx, sr, parent)
	if err != nil {
		return err
	}
	cm, err := s.resolve(ctx, sr, child)
	if err != nil {
		return err
	}
	// The link must be one the caller can see: a shadow or detached link answers
	// like an unknown child.
	var link *core.LinkMeta
	for _, vl := range s.d.Vis.VisibleParents(ctx, sr, cm) {
		if vl.Link.Parent == pm.ID {
			l := vl.Link
			link = &l
		}
	}
	if link == nil {
		return gate.ErrNotFound
	}
	if link.Kind == core.LinkCont {
		return gate.NewProblem(gate.StatusUnprocessable, "cont_not_detachable", "child")
	}
	ls, err := s.detach(ctx, detachOneSQL, pm.ID, cm.ID)
	if err != nil {
		return err
	}
	s.publishLinks(ls)
	return nil
}

// DetachAll detaches every reference to the caller's own post (continuations
// stay). Shadow links are detached too, so the result is the same for every
// child author.
func (s *Service) DetachAll(ctx context.Context, parent gate.Ref) error {
	_, sr, err := s.viewer(ctx)
	if err != nil {
		return err
	}
	pm, err := s.resolveOwn(ctx, sr, parent)
	if err != nil {
		return err
	}
	ls, err := s.detach(ctx, detachAllSQL, pm.ID)
	if err != nil {
		return err
	}
	s.publishLinks(ls)
	return nil
}

// Detach statements: attached ref links of one parent (and one child).
const (
	detachOneSQL = `UPDATE post_parents SET detached = true
		WHERE kind = 0 AND NOT detached AND parent_id = $1 AND child_id = $2
		RETURNING child_id, parent_id, kind, child_author, parent_author, detached, shadow, created_at`
	detachAllSQL = `UPDATE post_parents SET detached = true
		WHERE kind = 0 AND NOT detached AND parent_id = $1
		RETURNING child_id, parent_id, kind, child_author, parent_author, detached, shadow, created_at`
)

// detach runs a detach statement and returns the links it detached.
func (s *Service) detach(ctx context.Context, sql string, args ...any) ([]core.LinkMeta, error) {
	rows, err := s.d.DB.Q().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.LinkMeta
	for rows.Next() {
		var (
			l          core.LinkMeta
			kind       int16
			cAut, pAut int64
		)
		if err := rows.Scan(&l.Child, &l.Parent, &kind, &cAut, &pAut, &l.Detached, &l.Shadow, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.Kind = core.LinkKind(kind)                                         //nolint:gosec // DB CHECK (0, 1)
		l.ChildAuthor, l.ParentAuthor = core.UserID(cAut), core.UserID(pAut) //nolint:gosec // users.id < 2^32
		l.CreatedAt = l.CreatedAt.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Service) publishLinks(ls []core.LinkMeta) {
	for _, l := range ls {
		s.d.Posts.UpdateLink(l)
		s.d.Bus.Publish(core.LinkChanged{Link: l})
	}
}

func notFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return gate.ErrNotFound
	}
	return err
}
