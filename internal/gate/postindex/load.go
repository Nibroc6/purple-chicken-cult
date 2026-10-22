package postindex

import (
	"context"
	"errors"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Post metadata queries. IsReply: the post has a ref parent; ContParent /
// ContChild: its cont links (post_parents_one_cont makes the child unique).
const (
	metaByIDSQL = `SELECT p.id, p.author_id, p.created_at, p.audience, p.audience_forced, p.ref_policy, p.version,
	p.edited_at, p.lang, p.deleted_at IS NOT NULL,
	EXISTS (SELECT 1 FROM post_parents r WHERE r.child_id = p.id AND r.kind = 0),
	(SELECT c.parent_id FROM post_parents c WHERE c.child_id = p.id AND c.kind = 1 LIMIT 1),
	(SELECT c.child_id FROM post_parents c WHERE c.parent_id = p.id AND c.kind = 1 LIMIT 1)
	FROM posts p WHERE p.id = ANY($1::uuid[])`
	metaWindowSQL = `SELECT p.id, p.author_id, p.created_at, p.audience, p.audience_forced, p.ref_policy, p.version,
	p.edited_at, p.lang, p.deleted_at IS NOT NULL,
	EXISTS (SELECT 1 FROM post_parents r WHERE r.child_id = p.id AND r.kind = 0),
	(SELECT c.parent_id FROM post_parents c WHERE c.child_id = p.id AND c.kind = 1 LIMIT 1),
	(SELECT c.child_id FROM post_parents c WHERE c.parent_id = p.id AND c.kind = 1 LIMIT 1)
	FROM posts p WHERE p.deleted_at IS NULL AND p.created_at >= $1`
	// metaByAuthor*SQL: one author's live posts after a position (posts_author_time).
	metaByAuthorSQL = `SELECT p.id, p.author_id, p.created_at, p.audience, p.audience_forced, p.ref_policy, p.version,
	p.edited_at, p.lang, p.deleted_at IS NOT NULL,
	EXISTS (SELECT 1 FROM post_parents r WHERE r.child_id = p.id AND r.kind = 0),
	(SELECT c.parent_id FROM post_parents c WHERE c.child_id = p.id AND c.kind = 1 LIMIT 1),
	(SELECT c.child_id FROM post_parents c WHERE c.parent_id = p.id AND c.kind = 1 LIMIT 1)
	FROM posts p WHERE p.author_id = $1 AND p.deleted_at IS NULL`
	byAuthorNewestSQL = metaByAuthorSQL + `
	AND ($2::timestamptz IS NULL OR p.created_at < $2 OR (p.created_at = $2 AND p.id > $3::uuid))
	ORDER BY p.created_at DESC, p.id ASC LIMIT $4`
	byAuthorOldestSQL = metaByAuthorSQL + `
	AND ($2::timestamptz IS NULL OR p.created_at > $2 OR (p.created_at = $2 AND p.id < $3::uuid))
	ORDER BY p.created_at ASC, p.id DESC LIMIT $4`
	childrenSQL = `SELECT child_id, parent_id, kind, child_author, parent_author, detached, shadow, created_at
	FROM post_parents WHERE parent_id = $1 ORDER BY created_at DESC, child_id`
	parentsSQL = `SELECT child_id, parent_id, kind, child_author, parent_author, detached, shadow, created_at
	FROM post_parents WHERE child_id = $1 ORDER BY ord`
	// windowLinksSQL: every link touching a window post (complete parent and
	// children lists of the window, which metaWindowSQL defines).
	windowLinksSQL = `WITH w AS (SELECT id FROM posts WHERE deleted_at IS NULL AND created_at >= $1)
	SELECT child_id, parent_id, kind, child_author, parent_author, detached, shadow, created_at
	FROM post_parents WHERE child_id IN (SELECT id FROM w) OR parent_id IN (SELECT id FROM w)
	ORDER BY child_id, ord`
	windowMentionsSQL = `SELECT m.post_id, m.ord, m.target_id FROM mentions m JOIN posts p ON p.id = m.post_id
	WHERE p.deleted_at IS NULL AND p.created_at >= $1`
)

func loadMetas(ctx context.Context, q store.Q, sql string, args ...any) ([]core.PostMeta, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.PostMeta
	for rows.Next() {
		var (
			m                core.PostMeta
			author           int64
			aud              *int16
			pol              int16
			version          int32
			edited           *time.Time
			contPar, contKid *core.PostID
			deleted, isReply bool
			forced           bool
			lang             string
			created          time.Time
			id               core.PostID
		)
		if err := rows.Scan(&id, &author, &created, &aud, &forced, &pol, &version, &edited, &lang, &deleted,
			&isReply, &contPar, &contKid); err != nil {
			return nil, err
		}
		m = core.PostMeta{ID: id, Author: core.UserID(author), CreatedAt: created.UTC(), //nolint:gosec // users.id < 2^32 (DB CHECK)
			AudienceForced: forced, RefPolicy: core.RefPolicy(pol), Version: int(version), Lang: lang, //nolint:gosec // small enum
			Deleted: deleted, IsReply: isReply}
		if aud != nil {
			m.Audience = core.Audience(*aud) //nolint:gosec // DB CHECK (1, 2, 3, 10)
		}
		if edited != nil {
			e := edited.UTC()
			m.EditedAt = &e
		}
		if contPar != nil {
			m.ContParent = *contPar
		}
		if contKid != nil {
			m.ContChild = *contKid
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func loadLinks(ctx context.Context, q store.Q, byParent bool, id core.PostID) ([]core.LinkMeta, error) {
	sql := parentsSQL
	if byParent {
		sql = childrenSQL
	}
	return queryLinks(ctx, q, sql, id)
}

func loadWindowLinks(ctx context.Context, q store.Q, start time.Time) ([]core.LinkMeta, error) {
	return queryLinks(ctx, q, windowLinksSQL, start)
}

type mentionRow struct {
	post   core.PostID
	ord    int
	target core.UserID
}

func loadWindowMentions(ctx context.Context, q store.Q, start time.Time) ([]mentionRow, error) {
	rows, err := q.Query(ctx, windowMentionsSQL, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mentionRow
	for rows.Next() {
		var (
			m      mentionRow
			ord    int16
			target int64
		)
		if err := rows.Scan(&m.post, &ord, &target); err != nil {
			return nil, err
		}
		m.ord, m.target = int(ord), core.UserID(target) //nolint:gosec // users.id < 2^32 (DB CHECK)
		out = append(out, m)
	}
	return out, rows.Err()
}

func queryLinks(ctx context.Context, q store.Q, sql string, arg any) ([]core.LinkMeta, error) {
	rows, err := q.Query(ctx, sql, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.LinkMeta{}
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

func uuidStrings(ids []core.PostID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// ctxAlive is false for errors caused by a cancelled request (not worth logging).
func ctxAlive(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
