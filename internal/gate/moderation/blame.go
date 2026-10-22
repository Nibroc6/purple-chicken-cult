package moderation

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// KindInviteeAction is the notification an inviter gets after a confirmed
// action on their invitee (§10.4). Its data is {action, reason}, codes only.
const KindInviteeAction = "invitee_action"

// blamePost is what the inviter could see of a removed post when it was removed.
type blamePost struct {
	id       core.PostID
	excerpt  string
	lang     string
	at       time.Time
	audience int16
}

// blameFor returns the removed post as the author's inviter could see it right
// now (before removal), or nil when the inviter could not see it, the author
// has no live inviter, or the reason is csam (that content is never kept).
func (s *Service) blameFor(ctx context.Context, m core.PostMeta, reason string) (*blamePost, error) {
	if reason == string(core.ReasonCSAM) {
		return nil, nil
	}
	author, err := s.d.Users.Get(ctx, m.Author)
	if err != nil || author.InvitedBy == 0 {
		return nil, nil //nolint:nilerr // no inviter, no blame entry
	}
	sr, err := s.d.Vis.SearchFor(ctx, author.InvitedBy)
	if err != nil {
		return nil, err
	}
	if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
		return nil, nil
	}
	body := []rune(s.d.Posts.Bodies(ctx, []core.PostID{m.ID})[m.ID])
	if len(body) > ExcerptMax {
		body = append(body[:ExcerptMax-1], '…')
	}
	return &blamePost{id: m.ID, excerpt: string(body), lang: m.Lang, at: m.CreatedAt, audience: int16(m.Audience)}, nil
}

// blame writes the inviter's blame_inbox entry and the invitee_action
// notification for a confirmed action on invitee (in tx). Nothing happens when
// the invitee has no live inviter (genesis, or the inviter's account is gone).
func (s *Service) blame(ctx context.Context, tx store.Q, invitee core.UserID, action, reason string, p *blamePost, now time.Time) error {
	m, err := s.d.Users.Get(ctx, invitee)
	if err != nil || m.InvitedBy == 0 {
		return nil //nolint:nilerr // no inviter, no blame entry
	}
	inv, err := s.d.Users.Get(ctx, m.InvitedBy)
	if err != nil || inv.Status != core.StatusActive {
		return nil //nolint:nilerr // the inviter's account is gone
	}
	var (
		post          *uuid.UUID
		excerpt, lang *string
		postAt        *time.Time
		audience      *int16
		subjectPost   core.PostID
	)
	if p != nil {
		post, excerpt, lang, postAt, audience = &p.id, &p.excerpt, &p.lang, &p.at, &p.audience
		subjectPost = p.id
	}
	if _, err := tx.Exec(ctx, `INSERT INTO blame_inbox (id, inviter_id, invitee_id, action, reason, post_id, post_excerpt,
		post_lang, post_created_at, post_audience, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		s.d.IDs.New(), int64(m.InvitedBy), int64(invitee), action, reason, post, excerpt, lang, postAt, audience, now); err != nil {
		return err
	}
	return s.d.Notify.Notify(ctx, tx, core.Event{To: m.InvitedBy, Kind: KindInviteeAction, SubjectUser: invitee,
		SubjectPost: subjectPost, Data: map[string]any{"action": action, "reason": reason}})
}

// BlameInbox lists the viewer's blame-inbox entries, newest first (§10.4): what
// a moderator did to one of their invitees and why, the post only if they could
// see it when it was removed (and still see its author). Listing marks the
// returned entries read; each item says whether it was read before.
func (s *Service) BlameInbox(ctx context.Context, cursor string) (gate.Page[gate.BlameItem], error) {
	out := gate.Page[gate.BlameItem]{Items: []gate.BlameItem{}}
	v, err := core.RequireUser(ctx)
	if err != nil {
		return out, err
	}
	offset, ok := s.openCursor(v.ID, "blame", cursor)
	if !ok {
		return out, gate.ErrNotFound
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT id, invitee_id, action, reason, post_id, post_excerpt, post_lang,
			post_created_at, post_audience, created_at, read_at IS NOT NULL
		FROM blame_inbox WHERE inviter_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`,
		int64(v.ID), BlamePage+1, offset)
	if err != nil {
		return out, err
	}
	type row struct {
		id       uuid.UUID
		invitee  int64
		action   string
		reason   string
		post     *uuid.UUID
		excerpt  *string
		lang     *string
		postAt   *time.Time
		audience *int16
		at       time.Time
		read     bool
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.invitee, &r.action, &r.reason, &r.post, &r.excerpt, &r.lang, &r.postAt,
			&r.audience, &r.at, &r.read); err != nil {
			rows.Close()
			return out, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(list) > BlamePage {
		list = list[:BlamePage]
		out.Next = s.cursorString(v.ID, "blame", offset+BlamePage)
	}
	var sr core.Search
	unread := make([]uuid.UUID, 0, len(list))
	for _, r := range list {
		invitee := core.UserID(r.invitee) //nolint:gosec // users.id < 2^32 (DB CHECK)
		it := gate.BlameItem{ID: s.d.Refs.Report(v.ID, r.id), Action: r.action, Reason: r.reason, At: r.at, Read: r.read}
		if m, err := s.d.Users.Get(ctx, invitee); err == nil {
			it.InviteeHandle = m.Handle
		}
		if r.post != nil && r.excerpt != nil && r.postAt != nil {
			if sr == nil {
				if sr, err = s.d.Vis.SearchFor(ctx, v.ID); err != nil {
					return out, err
				}
			}
			if av, err := s.d.Views.Author(ctx, sr, invitee); err == nil {
				lang, aud := "und", core.AudConnected
				if r.lang != nil && *r.lang != "" {
					lang = *r.lang
				}
				if r.audience != nil {
					aud = core.Audience(*r.audience) //nolint:gosec // posts.audience is 0..10
				}
				it.Post = &gate.PostView{Ref: s.d.Refs.Post(v.ID, *r.post), Author: av, Tier: av.Chain.Dist,
					Body: *r.excerpt, Lang: lang, CreatedAt: *r.postAt, Audience: aud.String(),
					RefPolicy: core.PolNobody.String()}
			}
		}
		if !r.read {
			unread = append(unread, r.id)
		}
		out.Items = append(out.Items, it)
	}
	if len(unread) > 0 {
		if _, err := s.d.DB.Q().Exec(ctx, "UPDATE blame_inbox SET read_at = $2 WHERE id = ANY($1) AND read_at IS NULL",
			unread, s.d.Clock.Now()); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Log lists the public moderation log: confirmed removals, suspensions and
// role changes (roles.go) as (day, action, reason, count) per subject, newest day first. A subject is
// named (ref and handle) only to viewers who have that person in their horizon
// right now; everyone else sees a pseudonym, HMAC(PseudonymKey, user)[:6].
func (s *Service) Log(ctx context.Context, cursor string) (gate.Page[gate.LogItem], error) {
	out := gate.Page[gate.LogItem]{Items: []gate.LogItem{}}
	v, err := core.RequireUser(ctx)
	if err != nil {
		return out, err
	}
	offset, ok := s.openCursor(v.ID, "log", cursor)
	if !ok {
		return out, gate.ErrNotFound
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT to_char((done_at AT TIME ZONE 'UTC')::date, 'YYYY-MM-DD') AS day, action, reason,
			target_user, count(*)
		FROM mod_actions WHERE state = $1 AND action IN ('remove_post', 'suspend_user', 'grant_moderator', 'revoke_moderator',
			'grant_admin') AND done_at IS NOT NULL
		GROUP BY 1, action, reason, target_user
		ORDER BY 1 DESC, action, reason, target_user LIMIT $2 OFFSET $3`, actionDone, LogPage+1, offset)
	if err != nil {
		return out, err
	}
	type row struct {
		item gate.LogItem
		user *int64
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.item.Day, &r.item.Action, &r.item.Reason, &r.user, &r.item.Count); err != nil {
			rows.Close()
			return out, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(list) > LogPage {
		list = list[:LogPage]
		out.Next = s.cursorString(v.ID, "log", offset+LogPage)
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return out, err
	}
	for _, r := range list {
		it := r.item
		if r.user != nil {
			u := core.UserID(*r.user) //nolint:gosec // users.id < 2^32 (DB CHECK)
			it.Subject = &gate.LogSubject{Pseudonym: s.pseudonym(u)}
			if s.d.Vis.CanSeeUser(sr, u) {
				if m, err := s.d.Users.Get(ctx, u); err == nil {
					it.Subject = &gate.LogSubject{Ref: s.d.Refs.User(v.ID, u), Handle: m.Handle}
				}
			}
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}
