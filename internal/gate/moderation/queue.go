package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// reportRow is one open report as the queue reads it.
type reportRow struct {
	id      uuid.UUID
	kind    int16
	post    *uuid.UUID
	user    *int64
	reason  string
	at      time.Time
	state   int16
	sameTgt int
	pending bool
}

func (r reportRow) postID() core.PostID {
	if r.post == nil {
		return uuid.Nil
	}
	return *r.post
}

func (r reportRow) userID() core.UserID {
	if r.user == nil {
		return 0
	}
	return core.UserID(*r.user) //nolint:gosec // users.id < 2^32 (DB CHECK)
}

// Queue lists the open reports for moderators: csam first, then illegal, then
// everything else, oldest first within each.
// Every item carries the subject (the post's author, the person, or the
// reported invitee's inviter) as a moderator-scoped ref with its handle, the
// subject's inviter label and probation flag, the reported content (never for
// csam), how many open reports share the target, and whether a two-person
// action awaits a second moderator.
func (s *Service) Queue(ctx context.Context, cursor string) (gate.Page[gate.ModItem], error) {
	out := gate.Page[gate.ModItem]{Items: []gate.ModItem{}}
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return out, err
	}
	offset, ok := s.openCursor(v.ID, "queue", cursor)
	if !ok {
		return out, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	rows, err := s.d.DB.Q().Query(ctx, `SELECT r.id, r.target_kind, r.target_post, r.target_user, r.reason, r.created_at,
			count(*) OVER (PARTITION BY r.target_kind, r.target_post, r.target_user),
			EXISTS (SELECT 1 FROM mod_actions a WHERE a.report_id = r.id AND a.state = $1 AND a.created_at > $2)
		FROM reports r WHERE r.state = $3
		ORDER BY CASE r.reason WHEN 'csam' THEN 0 WHEN 'illegal' THEN 1 ELSE 2 END, r.created_at, r.id
		LIMIT $4 OFFSET $5`,
		actionPending, now.Add(-SecondWithin), reportOpen, QueuePage+1, offset)
	if err != nil {
		return out, err
	}
	var list []reportRow
	for rows.Next() {
		var r reportRow
		if err := rows.Scan(&r.id, &r.kind, &r.post, &r.user, &r.reason, &r.at, &r.sameTgt, &r.pending); err != nil {
			rows.Close()
			return out, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(list) > QueuePage {
		list = list[:QueuePage]
		out.Next = s.cursorString(v.ID, "queue", offset+QueuePage)
	}
	for _, r := range list {
		item, err := s.item(ctx, v, r, now)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

var kindNames = map[int16]string{0: KindPost, 1: KindPerson, 2: KindInviter, 3: KindPreview}

func (s *Service) item(ctx context.Context, v core.Viewer, r reportRow, now time.Time) (gate.ModItem, error) {
	it := gate.ModItem{
		ID: s.d.Refs.ModItem(v.ID, r.id), Source: "report", Priority: 2, Reason: r.reason, At: r.at,
		AwaitingSecond: r.pending, Summary: map[string]int{"reports": r.sameTgt, "kind_" + kindNames[r.kind]: 1},
	}
	switch r.reason {
	case "csam":
		it.Priority = 0
	case "illegal":
		it.Priority = 1
	}
	subject, meta, hasPost := s.subjectOf(ctx, r)
	if subject != 0 {
		m, err := s.d.Users.Get(ctx, subject)
		if err == nil {
			it.Subject = &gate.ChainHop{Ref: s.d.Refs.ModUser(v.ID, subject), Handle: m.Handle}
			it.SubjectProbation = m.OnProbation(now)
			if lbl, err := s.d.Users.InviterLabel(ctx, subject); err == nil {
				it.SubjectInviter = &lbl
			}
		}
	}
	if hasPost && !meta.Deleted && r.reason != "csam" {
		pv, err := s.modPost(ctx, v, meta)
		if err != nil {
			return it, err
		}
		it.Post = pv
	}
	return it, nil
}

// subjectOf returns the user a report is about: the post's author, the person,
// or (inviter reports) the reported invitee's inviter.
func (s *Service) subjectOf(ctx context.Context, r reportRow) (core.UserID, core.PostMeta, bool) {
	switch r.kind {
	case 0, 3:
		m, ok := s.d.Posts.Meta(ctx, r.postID())
		if !ok {
			return 0, m, false
		}
		return m.Author, m, true
	case 2:
		m, err := s.d.Users.Get(ctx, r.userID())
		if err != nil {
			return 0, core.PostMeta{}, false
		}
		return m.InvitedBy, core.PostMeta{}, false
	}
	return r.userID(), core.PostMeta{}, false
}

// modPost renders reported content for a moderator: the body and the author as
// moderator-scoped refs, and nothing of the graph (no chain, no likes, no context).
func (s *Service) modPost(ctx context.Context, v core.Viewer, m core.PostMeta) (*gate.PostView, error) {
	a, err := s.d.Users.Get(ctx, m.Author)
	if err != nil {
		return nil, nil //nolint:nilerr // an unknown author renders no content
	}
	lbl, err := s.d.Users.InviterLabel(ctx, m.Author)
	if err != nil {
		lbl = gate.InviterLabel{Kind: "deleted"}
	}
	body := s.d.Posts.Bodies(ctx, []core.PostID{m.ID})[m.ID]
	pv := &gate.PostView{
		Ref: s.d.Refs.Post(v.ID, m.ID),
		Author: gate.AuthorView{Ref: s.d.Refs.ModUser(v.ID, m.Author), Handle: a.Handle, DisplayName: a.DisplayName,
			Inviter: lbl},
		Body: body, Lang: m.Lang, CreatedAt: m.CreatedAt, EditedAt: m.EditedAt, Audience: m.Audience.String(),
		RefPolicy: m.RefPolicy.String(), Mine: m.Author == v.ID,
	}
	if pv.Lang == "" {
		pv.Lang = "und"
	}
	return pv, nil
}

// lockReport locks the report behind a moderator-scoped item ref.
func (s *Service) lockReport(ctx context.Context, tx store.Q, v core.Viewer, item gate.Ref) (reportRow, error) {
	var r reportRow
	id, ok := s.d.Refs.OpenModItem(v.ID, item)
	if !ok {
		return r, gate.ErrNotFound
	}
	err := tx.QueryRow(ctx, `SELECT id, target_kind, target_post, target_user, reason, created_at, state
		FROM reports WHERE id = $1 FOR UPDATE`, id).Scan(&r.id, &r.kind, &r.post, &r.user, &r.reason, &r.at, &r.state)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, gate.ErrNotFound
	}
	return r, err
}

// after collects what runs once the transaction has committed.
type after struct {
	removed   core.PostID
	author    core.UserID
	suspended core.UserID
}

func (s *Service) publish(a after) {
	if a.removed != uuid.Nil {
		s.d.Posts.Delete(a.removed)
		s.d.Bus.Publish(core.PostDeleted{ID: a.removed, Author: a.author})
	}
	if a.suspended != 0 {
		s.d.Bus.Publish(core.UserChanged{ID: a.suspended})
	}
}

// Resolve acts on a queue item. action is remove_post | suspend_user | dismiss
// and reason a moderation reason (closed enums: action_invalid,
// reason_invalid). remove_post (post and preview reports only) and dismiss take
// one moderator and close every open report on the same target; suspend_user
// needs a fresh step-up and only creates a pending two-person action that a
// second moderator confirms within 24 h (Confirm). Problems: item_resolved,
// action_pending, post_gone, no_subject.
func (s *Service) Resolve(ctx context.Context, item gate.Ref, action, reason string) error {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, item); !ok {
		return gate.ErrNotFound
	}
	if action != ActionRemovePost && action != ActionSuspendUser && action != ActionDismiss {
		return gate.NewProblem(gate.StatusUnprocessable, "action_invalid", "action")
	}
	if !validReason(core.ModReasons, reason) {
		return gate.NewProblem(gate.StatusUnprocessable, "reason_invalid", "reason")
	}
	if action == ActionSuspendUser {
		if v, err = s.freshModerator(ctx); err != nil {
			return err
		}
	}
	now := s.d.Clock.Now()
	var post after
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		post = after{}
		r, err := s.lockReport(ctx, tx, v, item)
		if err != nil {
			return err
		}
		if r.state != reportOpen {
			return gate.NewProblem(gate.StatusConflict, "item_resolved")
		}
		var pending int
		err = tx.QueryRow(ctx, "SELECT 1 FROM mod_actions WHERE report_id = $1 AND state = $2 AND created_at > $3",
			r.id, actionPending, now.Add(-SecondWithin)).Scan(&pending)
		if err == nil {
			return gate.NewProblem(gate.StatusConflict, "action_pending")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// A pending action past its deadline is expired before a new one starts.
		if _, err := tx.Exec(ctx, "UPDATE mod_actions SET state = $1 WHERE report_id = $2 AND state = $3",
			actionExpired, r.id, actionPending); err != nil {
			return err
		}
		subject, meta, hasPost := s.subjectOf(ctx, r)
		switch action {
		case ActionDismiss:
			if err := s.insertAction(ctx, tx, v, r, action, reason, subject, actionDone, now); err != nil {
				return err
			}
			return s.closeReports(ctx, tx, v, r, reportDismissed, now)
		case ActionSuspendUser:
			if subject == 0 || subject == v.ID {
				return gate.NewProblem(gate.StatusConflict, "no_subject")
			}
			return s.insertAction(ctx, tx, v, r, action, reason, subject, actionPending, now)
		default: // remove_post
			if !hasPost {
				return gate.NewProblem(gate.StatusUnprocessable, "action_invalid", "action")
			}
			blame, err := s.blameFor(ctx, meta, reason)
			if err != nil {
				return err
			}
			author, ok, err := s.d.PostRemover.RemovePost(ctx, tx, meta.ID, v.ID)
			if err != nil {
				return err
			}
			if !ok {
				return gate.NewProblem(gate.StatusConflict, "post_gone")
			}
			if err := s.insertAction(ctx, tx, v, r, action, reason, author, actionDone, now); err != nil {
				return err
			}
			if err := s.closeReports(ctx, tx, v, r, reportActioned, now); err != nil {
				return err
			}
			post = after{removed: meta.ID, author: author}
			return s.blame(ctx, tx, author, action, reason, blame, now)
		}
	})
	if err != nil {
		return err
	}
	s.publish(post)
	return nil
}

// Confirm is the second moderator's confirmation of a pending two-person
// action: a different moderator than the first, with a fresh step-up, within
// 24 h. It suspends the subject, closes every open report on the target and
// writes the blame entry and the invitee_action notification. Problems:
// second_moderator_required, action_expired, subject_inactive.
func (s *Service) Confirm(ctx context.Context, item gate.Ref) error {
	v, err := s.freshModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, item); !ok {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var (
		done    after
		expired bool
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		done, expired = after{}, false
		r, err := s.lockReport(ctx, tx, v, item)
		if err != nil {
			return err
		}
		var (
			aid     uuid.UUID
			first   int64
			action  string
			reason  string
			subject *int64
			created time.Time
		)
		err = tx.QueryRow(ctx, `SELECT id, moderator_id, action, reason, target_user, created_at FROM mod_actions
			WHERE report_id = $1 AND state = $2 FOR UPDATE`, r.id, actionPending).
			Scan(&aid, &first, &action, &reason, &subject, &created)
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.ErrNotFound // nothing awaits a second moderator
		}
		if err != nil {
			return err
		}
		if !created.Add(SecondWithin).After(now) {
			expired = true
			_, err := tx.Exec(ctx, "UPDATE mod_actions SET state = $1 WHERE id = $2", actionExpired, aid)
			return err
		}
		if core.UserID(first) == v.ID { //nolint:gosec // users.id < 2^32 (DB CHECK)
			return gate.NewProblem(gate.StatusConflict, "second_moderator_required")
		}
		if subject == nil {
			return gate.NewProblem(gate.StatusConflict, "no_subject")
		}
		target := core.UserID(*subject) //nolint:gosec // users.id < 2^32 (DB CHECK)
		if target == v.ID {
			return gate.NewProblem(gate.StatusConflict, "no_subject")
		}
		if err := s.d.Suspensions.Suspend(ctx, tx, target); err != nil {
			if errors.Is(err, gate.ErrNotFound) {
				return gate.NewProblem(gate.StatusConflict, "subject_inactive")
			}
			return err
		}
		// A suspended account has no live sessions: end them (and with them
		// the account's open event streams) when the suspension commits.
		if s.d.Passkeys != nil {
			if err := s.d.Passkeys.RevokeAll(ctx, tx, target); err != nil {
				return err
			}
		}
		second, err := json.Marshal(map[string]time.Time{"at": now, "auth_at": v.AuthAt})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE mod_actions SET state = $2, second_moderator = $3, second_auth = $4, done_at = $5
			WHERE id = $1`, aid, actionDone, int64(v.ID), second, now); err != nil {
			return err
		}
		if err := s.closeReports(ctx, tx, v, r, reportActioned, now); err != nil {
			return err
		}
		done = after{suspended: target}
		return s.blame(ctx, tx, target, action, reason, nil, now)
	})
	if err != nil {
		return err
	}
	if expired {
		return gate.NewProblem(gate.StatusConflict, "action_expired")
	}
	s.publish(done)
	return nil
}

func (s *Service) insertAction(ctx context.Context, tx store.Q, v core.Viewer, r reportRow, action, reason string,
	subject core.UserID, state int16, now time.Time) error {
	var done, firstAuth *time.Time
	if state == actionDone {
		done = &now
	}
	if action == ActionSuspendUser {
		at := v.AuthAt
		firstAuth = &at
	}
	_, err := tx.Exec(ctx, `INSERT INTO mod_actions (id, moderator_id, state, action, target_post, target_user, reason,
		report_id, created_at, done_at, first_auth_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		s.d.IDs.New(), int64(v.ID), state, action, r.post, nullUser(subject), reason, r.id, now, done, firstAuth)
	if err != nil && store.IsUniqueViolation(err) {
		return gate.NewProblem(gate.StatusConflict, "action_pending")
	}
	return err
}

// closeReports closes the report and every other open report on the same target.
func (s *Service) closeReports(ctx context.Context, tx store.Q, v core.Viewer, r reportRow, state int16, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE reports SET state = $1, resolved_by = $2, resolved_at = $3
		WHERE state = $4 AND (id = $5 OR (target_kind = $6 AND target_post IS NOT DISTINCT FROM $7
			AND target_user IS NOT DISTINCT FROM $8))`,
		state, int64(v.ID), now, reportOpen, r.id, r.kind, r.post, r.user)
	return err
}
