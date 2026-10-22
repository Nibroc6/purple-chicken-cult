package engage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// MarkSeen records posts the viewer saw (~1 s at ≥ 50 % visible, §4.3). At most
// SeenBatch refs count; refs that do not open, name no post or a post the
// viewer cannot see are dropped silently, so garbage writes nothing and the
// answer is the same 204 either way. A post seen again moves to the top of the
// recently-seen list. First-time sightings feed feed.impressions_by_degree.
func (s *Service) MarkSeen(ctx context.Context, ps []gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	if len(ps) > SeenBatch {
		ps = ps[:SeenBatch]
	}
	var ids []core.PostID
	dup := map[core.PostID]bool{}
	for _, r := range ps {
		if id, ok := s.d.Refs.OpenPost(v.ID, r); ok && !dup[id] {
			dup[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return err
	}
	metas := s.d.Posts.MetaMany(ctx, ids)
	tier := map[core.PostID]int{}
	keep := make([]string, 0, len(ids))
	for _, id := range ids {
		m, ok := metas[id]
		if !ok {
			continue
		}
		if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
			continue
		}
		d, _ := sr.Dist(m.Author)
		tier[id] = d
		keep = append(keep, id.String())
	}
	if len(keep) == 0 {
		return nil
	}
	rows, err := s.d.DB.Q().Query(ctx, `INSERT INTO seen (user_id, post_id, at)
		SELECT $1, p, $3 FROM unnest($2::uuid[]) AS p
		ON CONFLICT (user_id, post_id) DO UPDATE SET at = EXCLUDED.at
		RETURNING post_id, (xmax = 0)`, int64(v.ID), keep, s.now())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id    core.PostID
			fresh bool
		)
		if err := rows.Scan(&id, &fresh); err != nil {
			return err
		}
		if fresh && s.d.Metrics != nil && tier[id] >= 1 {
			s.d.Metrics.Inc(MetricImpressions, degreeBucket(tier[id]), 1)
		}
	}
	return rows.Err()
}

// degreeBucket is the closed bucket of a tier ("1" … "6", "7+").
func degreeBucket(k int) string {
	if k >= 7 {
		return "7+"
	}
	return strconv.Itoa(k)
}

// RecentlySeen lists the posts the viewer saw in the last 14 days, most
// recently seen first, that the viewer can still see (§4.3 "recover posts you
// scrolled past quickly").
func (s *Service) RecentlySeen(ctx context.Context, cursor string) (gate.Page[gate.PostView], error) {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	after, err := s.openCursor(v.ID, cursorSeen, cursor)
	if err != nil {
		return gate.Page[gate.PostView]{}, err
	}
	since := s.now().Add(-SeenRetention)
	return s.listPage(ctx, sr, cursorSeen, after, func(cur *seenKey, n int) ([]seenKey, error) {
		return s.seenAfter(ctx, v.ID, since, cur, n)
	})
}

// listPage builds one page of a viewer's own post list (recently seen, saves)
// from next, which returns up to n keys after cur in list order.
func (s *Service) listPage(ctx context.Context, sr core.Search, dom string, after *seenKey,
	next func(cur *seenKey, n int) ([]seenKey, error)) (gate.Page[gate.PostView], error) {
	page := gate.Page[gate.PostView]{Items: []gate.PostView{}}
	var (
		checked []core.Checked
		more    bool
		err     error
	)
	const batchSize = 2 * SeenPageSize
	// Posts the viewer can no longer see are skipped, so a page may need more
	// than one batch; after five, the page is cut short and continues later.
	cur := after
scan:
	for batch := 0; batch < 5; batch++ {
		keys, err := next(cur, batchSize)
		if err != nil {
			return gate.Page[gate.PostView]{}, err
		}
		ids := make([]core.PostID, len(keys))
		for i, k := range keys {
			ids[i] = k.post
		}
		metas := s.d.Posts.MetaMany(ctx, ids)
		for i := range keys {
			cur = &keys[i]
			m, ok := metas[keys[i].post]
			if !ok {
				continue
			}
			if c, ok := s.d.Vis.CheckPost(ctx, sr, m); ok {
				checked = append(checked, c)
				if len(checked) == SeenPageSize {
					more = i < len(keys)-1 || len(keys) == batchSize
					break scan
				}
			}
		}
		more = len(keys) == batchSize
		if !more {
			break
		}
	}
	if len(checked) > 0 {
		if page.Items, err = s.d.Views.Posts(ctx, sr, checked, core.ViewOpts{WithBody: true, WithContext: true}); err != nil {
			return gate.Page[gate.PostView]{}, err
		}
	}
	if more && cur != nil {
		page.Next = s.cursor(sr.Viewer(), dom, *cur)
	}
	return page, nil
}

// seenKey is a position in a list ordered by (at desc, post_id desc).
type seenKey struct {
	at   time.Time
	post core.PostID
}

func (s *Service) seenAfter(ctx context.Context, v core.UserID, since time.Time, after *seenKey, n int) ([]seenKey, error) {
	q := `SELECT post_id, at FROM seen WHERE user_id = $1 AND at >= $2
		ORDER BY at DESC, post_id DESC LIMIT $3`
	args := []any{int64(v), since, n}
	if after != nil {
		q = `SELECT post_id, at FROM seen WHERE user_id = $1 AND at >= $2 AND (at, post_id) < ($4, $5)
			ORDER BY at DESC, post_id DESC LIMIT $3`
		args = append(args, after.at, after.post)
	}
	return scanKeys(s.d.DB.Q().Query(ctx, q, args...))
}

// scanKeys reads (post_id, at) rows.
func scanKeys(rows pgx.Rows, err error) ([]seenKey, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []seenKey
	for rows.Next() {
		var k seenKey
		if err := rows.Scan(&k.post, &k.at); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Cursor domains: a cursor of one list never opens in another.
const (
	cursorSeen   = "pcc/seen-cursor/v1"
	cursorSaves  = "pcc/saves-cursor/v1"
	cursorHidden = "pcc/hidden-cursor/v1"
)

// Cursors are "<µs>.<post ref>.<mac>": the post travels as the viewer's own
// ref (never a raw ID) and the MAC binds both to the viewer and the list, so
// another viewer's cursor, or a forged one, fails like a bad ref.
func (s *Service) cursorMAC(v core.UserID, dom, payload string) string {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	m.Write([]byte(dom + "\x00"))
	m.Write(b[:])
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

func (s *Service) cursor(v core.UserID, dom string, k seenKey) string {
	payload := strconv.FormatInt(k.at.UnixMicro(), 10) + "." + string(s.d.Refs.Post(v, k.post))
	return payload + "." + s.cursorMAC(v, dom, payload)
}

func (s *Service) openCursor(v core.UserID, dom, c string) (*seenKey, error) {
	if c == "" {
		return nil, nil
	}
	i := strings.LastIndexByte(c, '.')
	if i < 0 || !hmac.Equal([]byte(c[i+1:]), []byte(s.cursorMAC(v, dom, c[:i]))) {
		return nil, gate.ErrNotFound
	}
	micros, ref, ok := strings.Cut(c[:i], ".")
	if !ok {
		return nil, gate.ErrNotFound
	}
	us, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	id, ok := s.d.Refs.OpenPost(v, gate.Ref(ref))
	if !ok {
		return nil, gate.ErrNotFound
	}
	return &seenKey{at: time.UnixMicro(us), post: id}, nil
}

// ---- core.Engagement ----

// SeenSet returns the posts v saw at or after since.
func (s *Service) SeenSet(ctx context.Context, v core.UserID, since time.Time) map[core.PostID]struct{} {
	out := map[core.PostID]struct{}{}
	rows, err := s.d.DB.Q().Query(ctx, "SELECT post_id FROM seen WHERE user_id = $1 AND at >= $2", int64(v), since)
	if err != nil {
		s.logErr("engage.seen_load", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id core.PostID
		if err := rows.Scan(&id); err != nil {
			s.logErr("engage.seen_load", err)
			return out
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		s.logErr("engage.seen_load", err)
	}
	return out
}

// PurgeSeen deletes seen state older than SeenRetention (job seen.purge).
func (s *Service) PurgeSeen(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM seen WHERE at < $1", s.now().Add(-SeenRetention))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
