package devseed

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// batch is a set of facts written in one transaction and then applied to the
// in-memory modules. Dev/test only: rows carry placeholder record hashes (no
// signed records are produced), exactly like coretest.World.
type batch struct {
	users  []userRow
	edges  []edgeRow // one per friendship; both directions are written
	blocks [][2]core.UserID
	mutes  [][2]core.UserID
	posts  []postRow
	links  []core.LinkMeta // in parent order per child (ord = position among the child's links)
	likes  []likeRow

	// touched are metas of existing posts changed by this batch (a new
	// continuation child); memory only, the database derives them from links.
	touched []core.PostMeta
}

type userRow struct {
	meta    core.UserMeta
	webauth []byte
}

type edgeRow struct {
	a, b  core.UserID
	level core.EdgeLevel
	since time.Time
}

type postRow struct {
	meta core.PostMeta
	body string
}

type likeRow struct {
	user core.UserID
	post core.PostID
	at   time.Time
}

// placeholderHash is the stand-in record hash of dev data.
func placeholderHash(label string) []byte {
	h := sha256.Sum256([]byte("pcc-devseed:" + label))
	return h[:]
}

// chunkRows bounds the rows of one set-based statement, so a 1M-user graph never
// builds a single huge bind message.
const chunkRows = 20000

// inChunks calls fn over consecutive [lo, hi) windows of n rows.
func inChunks(n int, fn func(lo, hi int) error) error {
	for lo := 0; lo < n; lo += chunkRows {
		if err := fn(lo, min(lo+chunkRows, n)); err != nil {
			return err
		}
	}
	return nil
}

// write inserts every fact in tx with set-based statements (unnest), so a 10k
// user synthetic graph is a handful of round trips.
func (b *batch) write(ctx context.Context, tx pgx.Tx) error {
	if err := inChunks(len(b.users), func(lo, hi int) error { return writeUsers(ctx, tx, b.users[lo:hi]) }); err != nil {
		return fmt.Errorf("devseed: users: %w", err)
	}
	if len(b.users) > 0 {
		if _, err := tx.Exec(ctx,
			"SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST((SELECT max(id) FROM users), 1))"); err != nil {
			return fmt.Errorf("devseed: users: %w", err)
		}
	}
	if err := inChunks(len(b.edges), func(lo, hi int) error { return writeEdges(ctx, tx, b.edges[lo:hi]) }); err != nil {
		return fmt.Errorf("devseed: edges: %w", err)
	}
	if err := b.writeExclusions(ctx, tx); err != nil {
		return fmt.Errorf("devseed: blocks/mutes: %w", err)
	}
	if err := inChunks(len(b.posts), func(lo, hi int) error { return writePosts(ctx, tx, b.posts[lo:hi]) }); err != nil {
		return fmt.Errorf("devseed: posts: %w", err)
	}
	ords := make([]int16, len(b.links))
	next := map[core.PostID]int16{}
	for i, l := range b.links {
		ords[i] = next[l.Child]
		next[l.Child]++
	}
	if err := inChunks(len(b.links), func(lo, hi int) error { return writeLinks(ctx, tx, b.links[lo:hi], ords[lo:hi]) }); err != nil {
		return fmt.Errorf("devseed: post_parents: %w", err)
	}
	if err := inChunks(len(b.likes), func(lo, hi int) error { return writeLikes(ctx, tx, b.likes[lo:hi]) }); err != nil {
		return fmt.Errorf("devseed: likes: %w", err)
	}
	return nil
}

func writeUsers(ctx context.Context, tx pgx.Tx, users []userRow) error {
	n := len(users)
	var (
		ids, invited                               = make([]int64, n), make([]int64, n)
		extIDs                                     = make([]string, n)
		handles, names, locales                    = make([]string, n), make([]string, n), make([]string, n)
		genesis, chains, sugg, named, slip, hasPro = make([]bool, n), make([]bool, n), make([]bool, n), make([]bool, n), make([]bool, n), make([]bool, n)
		cleared                                    = make([]bool, n)
		status, role, preset                       = make([]int16, n), make([]int16, n), make([]int16, n)
		created, probation                         = make([]time.Time, n), make([]time.Time, n)
		webauthn                                   = make([][]byte, n)
	)
	for i, u := range users {
		m := u.meta
		ids[i], invited[i], extIDs[i] = int64(m.ID), int64(m.InvitedBy), m.ExtID.String()
		handles[i], names[i], locales[i] = m.Handle, m.DisplayName, m.Locale
		genesis[i], chains[i], sugg[i], named[i], slip[i] = m.Genesis, m.NameInChains, m.Suggestible, m.NamedInLikes, m.SlipperyDefault
		status[i], role[i], preset[i], created[i], webauthn[i] = int16(m.Status), int16(m.Role), int16(m.Preset), m.CreatedAt, u.webauth
		if m.ProbationUntil != nil {
			hasPro[i], probation[i], cleared[i] = true, *m.ProbationUntil, m.ProbationCleared
		} else {
			probation[i] = m.CreatedAt
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO users (id, ext_id, handle, handle_canon, display_name, invited_by, is_genesis, status, role,
		created_at, probation_until, probation_cleared_at, name_in_chains, suggestible, named_in_likes, slippery_default,
		rank_preset, ui_locale, languages, webauthn_user_id, deleted_at)
		SELECT u.id, u.ext::uuid, u.handle, lower(u.handle), u.name, NULLIF(u.inv, 0), u.gen, u.st, u.role,
			u.created, CASE WHEN u.haspro THEN u.pro END, CASE WHEN u.haspro AND u.cleared THEN u.created END,
			u.chains, u.sugg, u.named, u.slip, u.preset, u.loc, ARRAY[u.loc], u.wa, CASE WHEN u.st = 3 THEN u.created END
		FROM unnest($1::bigint[], $2::text[], $3::text[], $4::text[], $5::bigint[], $6::bool[], $7::smallint[], $8::smallint[],
			$9::timestamptz[], $10::bool[], $11::timestamptz[], $12::bool[], $13::bool[], $14::bool[], $15::bool[], $16::bool[],
			$17::smallint[], $18::text[], $19::bytea[])
			AS u(id, ext, handle, name, inv, gen, st, role, created, haspro, pro, cleared, chains, sugg, named, slip, preset, loc, wa)`,
		ids, extIDs, handles, names, invited, genesis, status, role, created, hasPro, probation, cleared, chains, sugg, named,
		slip, preset, locales, webauthn)
	return err
}

func writeEdges(ctx context.Context, tx pgx.Tx, edges []edgeRow) error {
	n := 2 * len(edges)
	as, bs, lvls, since := make([]int64, 0, n), make([]int64, 0, n), make([]int16, 0, n), make([]time.Time, 0, n)
	hashes := make([][]byte, 0, n)
	for _, e := range edges {
		h := placeholderHash(fmt.Sprintf("edge:%d:%d", min(e.a, e.b), max(e.a, e.b)))
		as, bs = append(as, int64(e.a), int64(e.b)), append(bs, int64(e.b), int64(e.a))
		lvls, since = append(lvls, int16(e.level), int16(e.level)), append(since, e.since, e.since)
		hashes = append(hashes, h, h)
	}
	_, err := tx.Exec(ctx, `INSERT INTO edges (user_id, friend_id, level, since, record_hash)
		SELECT * FROM unnest($1::bigint[], $2::bigint[], $3::smallint[], $4::timestamptz[], $5::bytea[])`,
		as, bs, lvls, since, hashes)
	return err
}

func (b *batch) writeExclusions(ctx context.Context, tx pgx.Tx) error {
	for _, t := range []struct {
		sql  string
		rows [][2]core.UserID
	}{
		{"INSERT INTO blocks (blocker, blocked) SELECT * FROM unnest($1::bigint[], $2::bigint[])", b.blocks},
		{"INSERT INTO mutes (user_id, author_id) SELECT * FROM unnest($1::bigint[], $2::bigint[])", b.mutes},
	} {
		if len(t.rows) == 0 {
			continue
		}
		as, bs := make([]int64, len(t.rows)), make([]int64, len(t.rows))
		for i, r := range t.rows {
			as[i], bs[i] = int64(r[0]), int64(r[1])
		}
		if _, err := tx.Exec(ctx, t.sql, as, bs); err != nil {
			return err
		}
	}
	return nil
}

func writePosts(ctx context.Context, tx pgx.Tx, posts []postRow) error {
	n := len(posts)
	var (
		ids, authors  = make([]string, n), make([]int64, n)
		bodies        = make([]string, n)
		auds, pols    = make([]int16, n), make([]int16, n)
		forced, del   = make([]bool, n), make([]bool, n)
		langs         = make([]string, n)
		created       = make([]time.Time, n)
		hashes, salts = make([][]byte, n), make([][]byte, n)
	)
	for i, p := range posts {
		m := p.meta
		ids[i], authors[i], bodies[i], auds[i], pols[i] = m.ID.String(), int64(m.Author), p.body, int16(m.Audience), int16(m.RefPolicy)
		forced[i], del[i], langs[i], created[i] = m.AudienceForced, m.Deleted, m.Lang, m.CreatedAt
		hashes[i] = placeholderHash("post:" + m.ID.String())
		salts[i] = placeholderHash("salt:" + m.ID.String())[:16]
	}
	if _, err := tx.Exec(ctx, `INSERT INTO posts (id, author_id, body, body_salt, audience, audience_forced, ref_policy, lang,
		version, created_at, deleted_at, record_hash)
		SELECT p.id::uuid, p.author, CASE WHEN p.del THEN NULL ELSE p.body END, CASE WHEN p.del THEN NULL ELSE p.salt END,
			NULLIF(p.aud, 0), p.forced, p.pol, p.lang, 1, p.created, CASE WHEN p.del THEN p.created END, p.hash
		FROM unnest($1::text[], $2::bigint[], $3::text[], $4::bytea[], $5::smallint[], $6::bool[], $7::smallint[], $8::text[],
			$9::timestamptz[], $10::bool[], $11::bytea[])
			AS p(id, author, body, salt, aud, forced, pol, lang, created, del, hash)`,
		ids, authors, bodies, salts, auds, forced, pols, langs, created, del, hashes); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO post_versions (post_id, version, record_hash, created_at)
		SELECT id::uuid, 1, hash, created FROM unnest($1::text[], $2::bytea[], $3::timestamptz[]) AS v(id, hash, created)`,
		ids, hashes, created)
	return err
}

func writeLinks(ctx context.Context, tx pgx.Tx, links []core.LinkMeta, ord []int16) error {
	n := len(links)
	var (
		child, parent = make([]string, n), make([]string, n)
		kind          = make([]int16, n)
		cAuth, pAuth  = make([]int64, n), make([]int64, n)
		det, shadow   = make([]bool, n), make([]bool, n)
		created       = make([]time.Time, n)
	)
	for i, l := range links {
		child[i], parent[i], kind[i] = l.Child.String(), l.Parent.String(), int16(l.Kind)
		cAuth[i], pAuth[i], det[i], shadow[i], created[i] = int64(l.ChildAuthor), int64(l.ParentAuthor), l.Detached, l.Shadow, l.CreatedAt
	}
	_, err := tx.Exec(ctx, `INSERT INTO post_parents (child_id, parent_id, ord, kind, child_author, parent_author, detached,
		shadow, created_at)
		SELECT c::uuid, p::uuid, o, k, ca, pa, d, s, t FROM unnest($1::text[], $2::text[], $3::smallint[], $4::smallint[],
			$5::bigint[], $6::bigint[], $7::bool[], $8::bool[], $9::timestamptz[]) AS l(c, p, o, k, ca, pa, d, s, t)`,
		child, parent, ord, kind, cAuth, pAuth, det, shadow, created)
	return err
}

func writeLikes(ctx context.Context, tx pgx.Tx, likes []likeRow) error {
	us, ps, at := make([]int64, len(likes)), make([]string, len(likes)), make([]time.Time, len(likes))
	for i, l := range likes {
		us[i], ps[i], at[i] = int64(l.user), l.post.String(), l.at
	}
	_, err := tx.Exec(ctx, `INSERT INTO engagements (user_id, post_id, kind, at)
		SELECT u, p::uuid, 1, t FROM unnest($1::bigint[], $2::text[], $3::timestamptz[]) AS e(u, p, t)`, us, ps, at)
	return err
}

// Optional hooks of the in-memory modules. The reference implementations
// (coreref, and the scaffold stubs that delegate to them) are memory-only, so
// the facts are also pushed into them directly; modules that cache their own
// tables reload them.
type (
	reloader interface {
		Reload(ctx context.Context) error
	}
	loader interface {
		Load(ctx context.Context) error
	}
	bodySetter interface {
		SetBody(id core.PostID, body string)
	}
	likeSetter interface {
		SetLike(u core.UserID, p core.PostID, on bool)
	}
	muteSetter interface {
		SetMute(v, author core.UserID, on bool)
	}
	userInvalider interface{ Invalidate(id core.UserID) }
	userPutter    interface{ Put(m core.UserMeta) }
)

// reload asks x to re-read its tables when it can.
func reload(ctx context.Context, x any) error {
	switch r := x.(type) {
	case reloader:
		return r.Reload(ctx)
	case loader:
		return r.Load(ctx)
	}
	return nil
}

// apply pushes the committed facts into memory (after commit, like every owner).
func (s *Service) apply(ctx context.Context, b *batch) error {
	d := s.d
	if err := reload(ctx, d.Users); err != nil {
		return err
	}
	if inv, ok := d.Users.(userInvalider); ok {
		for _, u := range b.users {
			inv.Invalidate(u.meta.ID)
		}
	}
	if up, ok := d.Users.(userPutter); ok {
		for _, u := range b.users {
			up.Put(u.meta)
		}
	}
	if len(b.edges) > 0 || len(b.blocks) > 0 {
		if err := d.Graph.Load(ctx); err != nil {
			return err
		}
	}
	for _, x := range []any{d.Posts, d.Engage, d.Mutes} {
		if x == nil {
			continue
		}
		if err := reload(ctx, x); err != nil {
			return err
		}
	}
	bs, _ := d.Posts.(bodySetter)
	for _, p := range b.posts {
		d.Posts.Put(p.meta)
		if bs != nil && !p.meta.Deleted {
			bs.SetBody(p.meta.ID, p.body)
		}
	}
	for _, m := range b.touched {
		d.Posts.Put(m)
	}
	for _, l := range b.links {
		d.Posts.PutLink(l)
	}
	if ls, ok := d.Engage.(likeSetter); ok {
		for _, l := range b.likes {
			ls.SetLike(l.user, l.post, true)
		}
	}
	if ms, ok := d.Mutes.(muteSetter); ok {
		for _, m := range b.mutes {
			ms.SetMute(m[0], m[1], true)
		}
	}
	return nil
}
