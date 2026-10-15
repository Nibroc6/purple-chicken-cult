package posts

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Create creates a post.
func (s *Service) Create(ctx context.Context, d Draft) (gate.PostView, error) {
	vs, err := s.create(ctx, []Draft{d})
	if err != nil {
		return gate.PostView{}, err
	}
	return vs[0], nil
}

// CreateThread creates a continuation thread in one write: parts[0] carries the
// parents, part i+1 continues part i (cont). At most MaxThreadParts parts; every
// part shares parts[0]'s audience, ref policy and no_preview.
func (s *Service) CreateThread(ctx context.Context, parts []Draft) ([]gate.PostView, error) {
	return s.create(ctx, parts)
}

// parentLink is a resolved parent of the first part.
type parentLink struct {
	meta core.PostMeta
	kind core.LinkKind
}

// newPart is one post being created.
type newPart struct {
	meta     core.PostMeta
	body     string
	links    []core.LinkMeta
	mentions []mentionTarget
}

type mentionTarget struct {
	ord    int
	target core.UserID
}

// settings are the per-thread choices.
type settings struct {
	audience  core.Audience
	forced    bool
	policy    core.RefPolicy
	noPreview bool
}

func (s *Service) create(ctx context.Context, drafts []Draft) ([]gate.PostView, error) {
	v, sr, err := s.viewer(ctx)
	if err != nil {
		return nil, err
	}
	// 1. Resolve every parent ref (uniform not-found on any failure).
	resolved := make([][]core.PostMeta, len(drafts))
	for i, d := range drafts {
		for _, p := range d.Parents {
			m, err := s.resolve(ctx, sr, p.Ref)
			if err != nil {
				return nil, err
			}
			resolved[i] = append(resolved[i], m)
		}
	}
	// 2. Problems about the caller's own input.
	for _, d := range drafts {
		if len(d.Media) > 0 {
			return nil, gate.Disabled("media") // P1-W12
		}
	}
	if len(drafts) == 0 {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "body_empty", "body")
	}
	bodies := make([]string, len(drafts))
	langs := make([]string, len(drafts))
	for i, d := range drafts {
		if bodies[i], err = cleanBody(d.Body); err != nil {
			return nil, err
		}
		if langs[i], err = cleanLang(d.Lang); err != nil {
			return nil, err
		}
		if i > 0 && len(d.Parents) > 0 {
			return nil, gate.NewProblem(gate.StatusBadRequest, "bad_request", "parents")
		}
	}
	st, err := s.settings(ctx, v.ID, drafts[0])
	if err != nil {
		return nil, err
	}
	parents, err := s.checkParents(ctx, sr, drafts[0].Parents, resolved[0])
	if err != nil {
		return nil, err
	}
	if len(drafts) > MaxThreadParts {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "too_many_parts", "parts")
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return nil, err
	}

	// 3. Write (the caller's row lock serializes their own posts, so the
	// reference cap holds under concurrency; post_parents_one_cont makes a
	// second continuation fail).
	var parts []newPart
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE", int64(v.ID)); err != nil {
			return err
		}
		now := s.now()
		for _, p := range parents {
			if p.kind != core.LinkRef {
				continue
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM post_parents
				WHERE child_author = $1 AND parent_id = $2 AND kind = 0 AND created_at > $3`,
				int64(v.ID), p.meta.ID, now.Add(-24*time.Hour)).Scan(&n); err != nil {
				return err
			}
			if n >= RefCapPerDay {
				return gate.NewProblem(gate.StatusConflict, "ref_cap", "parents")
			}
		}
		parts = make([]newPart, len(drafts))
		for i := range drafts {
			id := s.d.IDs.New()
			created := now.Add(time.Duration(i) * time.Millisecond)
			m := core.PostMeta{ID: id, Author: v.ID, CreatedAt: created, Audience: st.audience, AudienceForced: st.forced,
				RefPolicy: st.policy, Version: 1, Lang: langs[i]}
			var links []core.LinkMeta
			if i == 0 {
				for _, p := range parents {
					l := core.LinkMeta{Child: id, Parent: p.meta.ID, Kind: p.kind, ChildAuthor: v.ID,
						ParentAuthor: p.meta.Author, CreatedAt: created}
					// A parent author who blocked the child's author never sees
					// the link (shadow, §10.1.1); the block stays invisible.
					l.Shadow = l.Kind == core.LinkRef && p.meta.Author != v.ID && s.d.Graph.Blocks(p.meta.Author, v.ID)
					if p.kind == core.LinkRef {
						m.IsReply = true
					} else {
						m.ContParent = p.meta.ID
					}
					links = append(links, l)
				}
			} else {
				prev := parts[i-1].meta.ID
				m.ContParent = prev
				parts[i-1].meta.ContChild = id
				links = []core.LinkMeta{{Child: id, Parent: prev, Kind: core.LinkCont, ChildAuthor: v.ID,
					ParentAuthor: v.ID, CreatedAt: created}}
			}
			parts[i] = newPart{meta: m, body: bodies[i], links: links, mentions: s.mentionTargets(ctx, sr, v.ID, bodies[i])}
		}
		for i := range parts {
			if err := s.insertPart(ctx, tx, me.ExtID, &parts[i], st); err != nil {
				return err
			}
		}
		return s.notifyCreated(ctx, tx, v.ID, parts)
	})
	if err != nil {
		return nil, err
	}

	// 4. After commit: memory, Bus.
	for _, p := range parents {
		if p.kind == core.LinkCont {
			pm := p.meta
			pm.ContChild = parts[0].meta.ID
			s.d.Posts.Put(pm)
		}
	}
	metas := make([]core.PostMeta, len(parts))
	for i, p := range parts {
		s.d.Posts.Put(p.meta)
		for _, l := range p.links {
			s.d.Posts.PutLink(l)
		}
		metas[i] = p.meta
	}
	for _, p := range parts {
		s.d.Bus.Publish(core.PostCreated{Post: p.meta, Links: p.links})
	}
	return s.views(ctx, sr, metas)
}

// settings resolves audience (probation forces d1, D6), ref policy (default
// from the author's slippery setting) and no_preview.
func (s *Service) settings(ctx context.Context, v core.UserID, d Draft) (settings, error) {
	aud, err := parseAudience(d.Audience)
	if err != nil {
		return settings{}, err
	}
	pol, set, err := parsePolicy(d.RefPolicy)
	if err != nil {
		return settings{}, err
	}
	me, err := s.d.Users.Get(ctx, v)
	if err != nil {
		return settings{}, err
	}
	if !set && me.SlipperyDefault {
		pol = core.PolNobody
	}
	st := settings{audience: aud, policy: pol, noPreview: d.NoPreview}
	if me.OnProbation(s.d.Clock.Now()) {
		st.audience, st.forced = core.AudD1, true
	}
	return st, nil
}

// checkParents evaluates the parent Problems in plan order.
func (s *Service) checkParents(ctx context.Context, sr core.Search, in []ParentIn, ms []core.PostMeta) ([]parentLink, error) {
	if len(in) > MaxParents {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "too_many_parents", "parents")
	}
	out := make([]parentLink, 0, len(in))
	seen := map[core.PostID]bool{}
	conts := 0
	for i, p := range in {
		kind, ok := parseKind(p.Kind)
		if !ok {
			return nil, gate.NewProblem(gate.StatusBadRequest, "bad_request", "kind")
		}
		if seen[ms[i].ID] {
			return nil, gate.NewProblem(gate.StatusUnprocessable, "duplicate_parent", "parents")
		}
		seen[ms[i].ID] = true
		if kind == core.LinkCont {
			conts++
		}
		out = append(out, parentLink{meta: ms[i], kind: kind})
	}
	if conts > 1 {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "cont_multiple", "parents")
	}
	v := sr.Viewer()
	for _, p := range out {
		if p.kind != core.LinkCont {
			continue
		}
		if p.meta.Author != v {
			return nil, gate.NewProblem(gate.StatusUnprocessable, "cont_not_own", "parents")
		}
		if p.meta.ContChild != uuid.Nil {
			return nil, gate.NewProblem(gate.StatusConflict, "cont_taken", "parents")
		}
	}
	for _, p := range out {
		if p.kind == core.LinkRef && !s.d.Vis.CanReference(ctx, sr, p.meta, core.LinkRef) {
			return nil, gate.NewProblem(gate.StatusUnprocessable, "ref_not_allowed", "parents")
		}
	}
	return out, nil
}

// mentionTargets resolves the body's mentions within the author's horizon
// (finite distance from the author, or the author): anything else is stored
// nowhere and renders as plain text for everyone.
func (s *Service) mentionTargets(ctx context.Context, sr core.Search, v core.UserID, body string) []mentionTarget {
	var out []mentionTarget
	for _, m := range scanMentions(body) {
		if len(out) >= MaxMentions || m.Ord > 32767 {
			break
		}
		u, ok := s.d.Users.ByHandle(ctx, m.Handle)
		if !ok {
			continue
		}
		if u != v {
			if _, ok := sr.Dist(u); !ok {
				continue
			}
		}
		if um, err := s.d.Users.Get(ctx, u); err != nil || um.Status != core.StatusActive {
			continue
		}
		out = append(out, mentionTarget{ord: m.Ord, target: u})
	}
	return out
}

// insertPart signs the Post record and writes posts, post_versions,
// post_parents, mentions and the preview job.
func (s *Service) insertPart(ctx context.Context, tx pgx.Tx, ext uuid.UUID, p *newPart, st settings) error {
	salt, err := newSalt()
	if err != nil {
		return err
	}
	m := p.meta
	parents := make([]parentRec, 0, len(p.links))
	for _, l := range p.links {
		parents = append(parents, parentRec{ID: l.Parent, Kind: l.Kind.String()})
	}
	hash, err := s.signPost(ctx, tx, ext, m, salt, p.body, parents, st.noPreview)
	if err != nil {
		return err
	}
	var aud *int16
	if m.Audience != core.AudConnected {
		a := int16(m.Audience)
		aud = &a
	}
	if _, err := tx.Exec(ctx, `INSERT INTO posts (id, author_id, body, body_salt, audience, audience_forced, ref_policy, lang,
		no_preview, version, created_at, record_hash) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, $11)`,
		m.ID, int64(m.Author), p.body, salt, aud, m.AudienceForced, int16(m.RefPolicy), m.Lang, st.noPreview,
		m.CreatedAt, hash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO post_versions (post_id, version, record_hash, created_at)
		VALUES ($1, 1, $2, $3)`, m.ID, hash, m.CreatedAt); err != nil {
		return err
	}
	for i, l := range p.links {
		if _, err := tx.Exec(ctx, `INSERT INTO post_parents (child_id, parent_id, ord, kind, child_author, parent_author,
			shadow, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, l.Child, l.Parent, int16(i), int16(l.Kind), //nolint:gosec // ≤ 3 parents
			int64(l.ChildAuthor), int64(l.ParentAuthor), l.Shadow, l.CreatedAt); err != nil {
			if store.IsUniqueViolation(err) && l.Kind == core.LinkCont {
				return gate.NewProblem(gate.StatusConflict, "cont_taken", "parents")
			}
			return err
		}
	}
	if err := insertMentions(ctx, tx, m.ID, p.mentions); err != nil {
		return err
	}
	return s.enqueuePreview(ctx, tx, m, st.noPreview)
}

func insertMentions(ctx context.Context, tx pgx.Tx, id core.PostID, ms []mentionTarget) error {
	for _, mt := range ms {
		if _, err := tx.Exec(ctx, "INSERT INTO mentions (post_id, ord, target_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
			id, int16(mt.ord), int64(mt.target)); err != nil { //nolint:gosec // ord ≤ 32767 (mentionTargets)
			return err
		}
	}
	return nil
}

// enqueuePreview schedules preview.fetch for this version (gate/previews runs
// it), unless the author turned previews off.
func (s *Service) enqueuePreview(ctx context.Context, tx pgx.Tx, m core.PostMeta, noPreview bool) error {
	if noPreview || s.d.Jobs == nil {
		return nil
	}
	return s.d.Jobs.Enqueue(ctx, tx, JobPreviewFetch, map[string]any{"post": m.ID.String(), "version": m.Version},
		time.Time{}, m.ID.String())
}

// signPost signs the Post record of version m.Version.
func (s *Service) signPost(ctx context.Context, tx pgx.Tx, ext uuid.UUID, m core.PostMeta, salt []byte, body string,
	parents []parentRec, noPreview bool) ([]byte, error) {
	rec := postRecord{ID: m.ID, Version: m.Version, Author: ext, BodyCommit: bodyCommit(salt, body), Lang: m.Lang,
		Media: []string{}, Parents: parents, CreatedAt: sigs.T(m.CreatedAt)}
	if m.Audience != core.AudConnected {
		a := m.Audience.String()
		rec.Audience = &a
	}
	if m.RefPolicy != core.PolAnyone {
		p := m.RefPolicy.String()
		rec.RefPolicy = &p
	}
	if noPreview {
		t := true
		rec.NoPreview = &t
	}
	return s.d.Signer.Sign(ctx, tx, core.RecPost, m.Author, rec, []core.UserID{m.Author})
}

// notifyCreated notifies parent authors (only if they can see the child and
// its author is within NotifyWithin hops of them; never for shadow links) and
// mentioned users (only if they can see the post). The Notifier drops events
// whose recipient blocked the subject user (D7) and batches on GroupKey.
func (s *Service) notifyCreated(ctx context.Context, tx pgx.Tx, v core.UserID, parts []newPart) error {
	if s.d.Notify == nil {
		return nil
	}
	head := parts[0]
	done := map[core.UserID]bool{v: true}
	for _, l := range head.links {
		if l.Kind != core.LinkRef || l.Shadow || l.ParentAuthor == v {
			continue
		}
		psr, err := s.d.Vis.SearchFor(ctx, l.ParentAuthor)
		if err != nil {
			return err
		}
		if _, ok := s.d.Vis.CheckPost(ctx, psr, head.meta); !ok {
			continue
		}
		if d, ok := psr.Dist(v); !ok || d > NotifyWithin {
			continue
		}
		if err := s.d.Notify.Notify(ctx, tx, core.Event{To: l.ParentAuthor, Kind: NotifyReferenced,
			GroupKey: NotifyReferenced + ":" + l.Parent.String(), SubjectUser: v, SubjectPost: l.Parent}); err != nil {
			return err
		}
	}
	for _, p := range parts {
		if err := s.notifyMentions(ctx, tx, v, p.meta, p.mentions, done); err != nil {
			return err
		}
	}
	return nil
}

// notifyMentions notifies each mentioned user once (done tracks who already got one).
func (s *Service) notifyMentions(ctx context.Context, tx pgx.Tx, v core.UserID, m core.PostMeta, ms []mentionTarget,
	done map[core.UserID]bool) error {
	if s.d.Notify == nil {
		return nil
	}
	for _, mt := range ms {
		if done[mt.target] {
			continue
		}
		done[mt.target] = true
		tsr, err := s.d.Vis.SearchFor(ctx, mt.target)
		if err != nil {
			return err
		}
		if _, ok := s.d.Vis.CheckPost(ctx, tsr, m); !ok {
			continue
		}
		if err := s.d.Notify.Notify(ctx, tx, core.Event{To: mt.target, Kind: NotifyMention, SubjectUser: v,
			SubjectPost: m.ID}); err != nil {
			return err
		}
	}
	return nil
}
