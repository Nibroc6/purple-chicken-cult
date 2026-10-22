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

// sourceNames are the API names of mod_queue.source.
var sourceNames = map[uint8]string{
	core.ModSrcReport: "report", core.ModSrcSignalCluster: "signal", core.ModSrcCSAM: "csam",
	core.ModSrcStepUpRepeat: "stepup", core.ModSrcInviteCluster: "invite_cluster",
}

// Signal codes a signal item's summary may carry (gate/signals/behavior);
// the first present one is the item's reason.
var signalCodes = []string{"cadence", "similarity"}

// reportRow is the report behind a report item.
type reportRow struct {
	id       uuid.UUID
	kind     int16
	post     *uuid.UUID
	user     *int64
	reason   string
	state    int16
	sameTgt  int
	message  *uuid.UUID // message reports
	evidence string
	at       time.Time
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

// item is one mod_queue row as moderation reads it.
type item struct {
	id        uuid.UUID
	source    uint8
	priority  int16
	subject   core.UserID // stored subject (report items: computed from the report)
	post      core.PostID // stored subject post (csam preview holds)
	media     uuid.UUID   // csam items: the held media
	summary   map[string]int
	at        time.Time
	claimedBy core.UserID
	claimedAt *time.Time
	resolved  bool
	rep       *reportRow // report items
}

// claimedNow reports whether a moderator's claim on it still holds.
func (it item) claimedNow(now time.Time) bool {
	return it.claimedBy != 0 && it.claimedAt != nil && it.claimedAt.Add(ClaimFor).After(now)
}

// reason is the item's reason code: the report's reason, csam, or the signal.
func (it item) reason() string {
	switch it.source {
	case core.ModSrcReport:
		if it.rep != nil {
			return it.rep.reason
		}
	case core.ModSrcCSAM:
		return string(core.ReasonCSAM)
	case core.ModSrcSignalCluster:
		for _, c := range signalCodes {
			if it.summary[c] > 0 {
				return c
			}
		}
		return "signal"
	}
	return sourceNames[it.source]
}

// modReason is the item's reason as a moderation reason (the default for
// actions started without one).
func (it item) modReason() string {
	r := it.reason()
	if validReason(core.ModReasons, r) {
		return r
	}
	return string(core.ReasonBot)
}

// selectItem reads queue items with their report and, for report items, how
// many open reports share the target.
const selectItem = `SELECT q.id, q.source, q.priority, q.subject_user, q.subject_post, q.subject_media, q.summary,
		q.created_at, q.claimed_by, q.claimed_at, q.resolved_at IS NOT NULL,
		r.id, r.target_kind, r.target_post, r.target_user, r.reason, r.state,
		(SELECT count(*) FROM reports o WHERE r.id IS NOT NULL AND o.state = 0 AND o.target_kind = r.target_kind
			AND o.target_post IS NOT DISTINCT FROM r.target_post AND o.target_user IS NOT DISTINCT FROM r.target_user
			AND o.target_message IS NOT DISTINCT FROM r.target_message),
		r.target_message, coalesce(r.evidence, ''), r.created_at
	FROM mod_queue q LEFT JOIN reports r ON r.id = q.report_id `

func scanItem(row pgx.Row) (item, error) {
	var (
		it               item
		src              int16
		subject, claimed *int64
		post, media      *uuid.UUID
		summary          []byte
		rid              *uuid.UUID
		rkind, rstate    *int16
		rpost            *uuid.UUID
		ruser            *int64
		rreason          *string
		same             int
		rmsg             *uuid.UUID
		revidence        string
		rat              *time.Time
	)
	if err := row.Scan(&it.id, &src, &it.priority, &subject, &post, &media, &summary, &it.at, &claimed, &it.claimedAt,
		&it.resolved, &rid, &rkind, &rpost, &ruser, &rreason, &rstate, &same, &rmsg, &revidence, &rat); err != nil {
		return it, err
	}
	it.source = uint8(src) //nolint:gosec // mod_queue.source is 0..4
	if subject != nil {
		it.subject = core.UserID(*subject) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	if post != nil {
		it.post = *post
	}
	if media != nil {
		it.media = *media
	}
	if claimed != nil {
		it.claimedBy = core.UserID(*claimed) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	if err := json.Unmarshal(summary, &it.summary); err != nil || it.summary == nil {
		it.summary = map[string]int{}
	}
	if rid != nil && rkind != nil && rreason != nil && rstate != nil {
		it.rep = &reportRow{id: *rid, kind: *rkind, post: rpost, user: ruser, reason: *rreason, state: *rstate, sameTgt: same,
			message: rmsg, evidence: revidence}
		if rat != nil {
			it.rep.at = *rat
		}
	}
	return it, nil
}

// pending is the two-person action awaiting a second moderator on an item.
type pending struct {
	id        uuid.UUID
	first     core.UserID
	action    string
	reason    string
	subject   core.UserID
	at        time.Time
	reportRef *string
}

const pendingCols = "id, moderator_id, action, reason, target_user, created_at, report_ref"

func scanPending(row pgx.Row) (pending, error) {
	var (
		p       pending
		first   int64
		subject *int64
	)
	if err := row.Scan(&p.id, &first, &p.action, &p.reason, &subject, &p.at, &p.reportRef); err != nil {
		return p, err
	}
	p.first = core.UserID(first) //nolint:gosec // users.id < 2^32 (DB CHECK)
	if subject != nil {
		p.subject = core.UserID(*subject) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	return p, nil
}

// pendingOf returns the live (not yet expired) pending action of an item.
func (s *Service) pendingOf(ctx context.Context, q store.Q, id uuid.UUID, now time.Time) (*pending, error) {
	p, err := scanPending(q.QueryRow(ctx, "SELECT "+pendingCols+` FROM mod_actions
		WHERE item_id = $1 AND state = $2 AND created_at > $3`, id, actionPending, now.Add(-SecondWithin)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Queue lists the open queue items for moderators: by priority (csam 0,
// illegal 1, everything else 2, step-up repeats 3), oldest first within each.
// Every item carries its subject as a moderator-scoped ref with its handle,
// the subject's inviter label and probation flag, the reported content (never
// for csam), how many open reports share the target, the action awaiting a
// second moderator, the claim, and for csam holds the match metadata only.
// Items with a subject also count the confirmed-bad accounts of the subject's
// inviter's subtree (subtree_confirmed, quarantine_k) and the inviter's live
// removed-account marks (inviter_marks).
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
	rows, err := s.d.DB.Q().Query(ctx, selectItem+`WHERE q.resolved_at IS NULL
		ORDER BY q.priority, q.created_at, q.id LIMIT $1 OFFSET $2`, QueuePage+1, offset)
	if err != nil {
		return out, err
	}
	var list []item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		list = append(list, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(list) > QueuePage {
		list = list[:QueuePage]
		out.Next = s.cursorString(v.ID, "queue", offset+QueuePage)
	}
	for _, it := range list {
		view, err := s.itemView(ctx, v, it, now)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, view)
	}
	return out, nil
}

func (s *Service) itemView(ctx context.Context, v core.Viewer, it item, now time.Time) (gate.ModItem, error) {
	view := gate.ModItem{
		ID: s.d.Refs.ModItem(v.ID, it.id), Source: sourceNames[it.source], Priority: int(it.priority),
		Reason: it.reason(), At: it.at, Summary: map[string]int{},
		Claimed: it.claimedNow(now), ClaimedByMe: it.claimedNow(now) && it.claimedBy == v.ID,
	}
	for k, n := range it.summary {
		view.Summary[k] = n
	}
	p, err := s.pendingOf(ctx, s.d.DB.Q(), it.id, now)
	if err != nil {
		return view, err
	}
	if p != nil {
		view.AwaitingSecond = true
		view.Pending = &gate.ModPending{Action: p.action, Reason: p.reason, Mine: p.first == v.ID, At: p.at}
		if p.reportRef != nil {
			view.Pending.ReportRef = *p.reportRef
		}
	}
	subject := it.subject
	var (
		meta    core.PostMeta
		hasPost bool
	)
	if it.rep != nil {
		view.Summary["reports"] = max(it.rep.sameTgt, 1)
		subject, meta, hasPost = s.subjectOf(ctx, *it.rep)
	}
	if subject != 0 {
		if m, err := s.d.Users.Get(ctx, subject); err == nil {
			view.Subject = &gate.ChainHop{Ref: s.d.Refs.ModUser(v.ID, subject), Handle: m.Handle}
			view.SubjectProbation = m.OnProbation(now)
			if lbl, err := s.d.Users.InviterLabel(ctx, subject); err == nil {
				view.SubjectInviter = &lbl
			}
			if m.InvitedBy != 0 {
				n, err := s.confirmedBad(ctx, s.d.DB.Q(), m.InvitedBy)
				if err != nil {
					return view, err
				}
				view.Summary["subtree_confirmed"] = n
				view.Summary["quarantine_k"] = s.quarantineK()
				if k, err := s.liveMarks(ctx, m.InvitedBy); err == nil && k > 0 {
					view.Summary["inviter_marks"] = k
				}
			}
		}
	}
	// Content: reported posts only; never for csam (neither a csam report
	// nor a csam hold), never a media URL.
	if hasPost && !meta.Deleted && it.reason() != string(core.ReasonCSAM) && it.source == core.ModSrcReport {
		pv, err := s.modPost(ctx, v, meta)
		if err != nil {
			return view, err
		}
		view.Post = pv
	}
	if it.rep != nil && it.rep.message != nil && it.source == core.ModSrcReport && it.reason() != string(core.ReasonCSAM) {
		mv := &gate.ModMessageView{Body: it.rep.evidence, At: it.rep.at, Removed: true}
		if s.d.Messages != nil {
			if m, ok := s.d.Messages.MessageByID(ctx, *it.rep.message); ok {
				mv.At, mv.Removed, mv.SharedPost = m.At, m.Deleted, m.SharedPost != uuid.Nil
			}
		}
		view.Message = mv
	}
	if it.source == core.ModSrcCSAM && it.media != uuid.Nil && s.d.CSAM != nil {
		if h, err := s.d.CSAM.Hold(ctx, it.media); err == nil {
			view.CSAM = &gate.CSAMHoldView{MatchInfo: h.MatchInfo, SourceURL: h.SourceURL, HeldAt: h.HeldAt,
				RetainUntil: h.RetainUntil, ReportedAt: h.ReportedAt, ReportRef: h.ReportRef, AccountHeld: h.AccountHeld}
			if view.CSAM.MatchInfo == nil {
				view.CSAM.MatchInfo = map[string]string{}
			}
		} else if !errors.Is(err, gate.ErrNotFound) {
			return view, err
		}
	}
	return view, nil
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

// itemSubject is the account an item is about (0 = none).
func (s *Service) itemSubject(ctx context.Context, it item) (core.UserID, core.PostMeta, bool) {
	if it.rep != nil {
		return s.subjectOf(ctx, *it.rep)
	}
	if it.post != uuid.Nil {
		if m, ok := s.d.Posts.Meta(ctx, it.post); ok {
			return it.subject, m, true
		}
	}
	return it.subject, core.PostMeta{}, false
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

// lockItem locks the queue item behind a moderator-scoped item ref (and its
// report, if any).
func (s *Service) lockItem(ctx context.Context, tx store.Q, v core.Viewer, ref gate.Ref) (item, error) {
	id, ok := s.d.Refs.OpenModItem(v.ID, ref)
	if !ok {
		return item{}, gate.ErrNotFound
	}
	it, err := scanItem(tx.QueryRow(ctx, selectItem+"WHERE q.id = $1 FOR UPDATE OF q", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return it, gate.ErrNotFound
	}
	return it, err
}

// ---- core.ModQueue ----

// Enqueue adds a queue item in the caller's transaction (core.ModQueue). An
// item of a signal source (signal cluster, step-up repeat, invite cluster)
// for a subject that already has an open item of that source updates that
// item instead (its summary, and the more urgent priority).
func (s *Service) Enqueue(ctx context.Context, tx store.Q, it core.ModQueueItem) error {
	if tx == nil {
		tx = s.d.DB.Q()
	}
	summary := it.Summary
	if summary == nil {
		summary = map[string]int{}
	}
	sum, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	prio := int16(min(it.Priority, 3))
	src := int16(it.Source)
	subject, post, media, report := nullUser(it.SubjectUser), nullPost(it.SubjectPost), nullPost(it.Media), nullPost(it.ReportID)
	if it.SubjectUser != 0 && (it.Source == core.ModSrcSignalCluster || it.Source == core.ModSrcStepUpRepeat ||
		it.Source == core.ModSrcInviteCluster) {
		tag, err := tx.Exec(ctx, `UPDATE mod_queue SET summary = $3, priority = LEAST(priority, $4)
			WHERE source = $1 AND subject_user = $2 AND resolved_at IS NULL`, src, subject, sum, prio)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO mod_queue (id, source, priority, subject_user, subject_post, subject_media, report_id,
		summary, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		s.d.IDs.New(), src, prio, subject, post, media, report, sum, s.d.Clock.Now())
	return err
}

// ---- claim ----

// Claim marks a queue item as being handled by the calling moderator for
// ClaimFor (a claim of their own is renewed). Other moderators then cannot
// resolve it or start an action on it until the claim lapses; a second
// moderator's confirmation is never blocked. Problems: item_resolved,
// item_claimed.
func (s *Service) Claim(ctx context.Context, ref gate.Ref) error {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, ref); !ok {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		it, err := s.lockItem(ctx, tx, v, ref)
		if err != nil {
			return err
		}
		if it.resolved {
			return gate.NewProblem(gate.StatusConflict, "item_resolved")
		}
		if it.claimedNow(now) && it.claimedBy != v.ID {
			return gate.NewProblem(gate.StatusConflict, "item_claimed")
		}
		_, err = tx.Exec(ctx, "UPDATE mod_queue SET claimed_by = $2, claimed_at = $3 WHERE id = $1", it.id, int64(v.ID), now)
		return err
	})
}

// ---- resolve ----

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

// startable checks that a new action may start on it: open, not claimed by
// another moderator, no action awaiting a second moderator (a pending action
// past its deadline is expired first).
func (s *Service) startable(ctx context.Context, tx store.Q, v core.Viewer, it item, now time.Time) error {
	if it.resolved || (it.rep != nil && it.rep.state != reportOpen) {
		return gate.NewProblem(gate.StatusConflict, "item_resolved")
	}
	if it.claimedNow(now) && it.claimedBy != v.ID {
		return gate.NewProblem(gate.StatusConflict, "item_claimed")
	}
	p, err := s.pendingOf(ctx, tx, it.id, now)
	if err != nil {
		return err
	}
	if p != nil {
		return gate.NewProblem(gate.StatusConflict, "action_pending")
	}
	_, err = tx.Exec(ctx, "UPDATE mod_actions SET state = $1 WHERE item_id = $2 AND state = $3", actionExpired, it.id, actionPending)
	return err
}

// Resolve acts on a queue item. action is remove_post | remove_message |
// suspend_user | dismiss
// and reason a moderation reason (closed enums: action_invalid,
// reason_invalid). remove_post (items with a post) and dismiss take one
// moderator and close the item (a report item: every open report on the same
// target); suspend_user — and dismissing a csam hold, which lifts the
// account's upload hold — need a fresh step-up and only start a two-person
// action that a second moderator confirms within 24 h (Confirm). Problems:
// item_resolved, item_claimed, action_pending, post_gone, no_subject.
func (s *Service) Resolve(ctx context.Context, ref gate.Ref, action, reason string) error {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.d.Refs.OpenModItem(v.ID, ref); !ok {
		return gate.ErrNotFound
	}
	if action != ActionRemovePost && action != ActionSuspendUser && action != ActionDismiss && action != ActionRemoveMessage {
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
		it, err := s.lockItem(ctx, tx, v, ref)
		if err != nil {
			return err
		}
		if err := s.startable(ctx, tx, v, it, now); err != nil {
			return err
		}
		subject, meta, hasPost := s.itemSubject(ctx, it)
		switch {
		case action == ActionDismiss && it.source == core.ModSrcCSAM:
			// Dismissing a hold lifts the account's upload hold: two moderators.
			if v, err = s.freshModerator(ctx); err != nil {
				return err
			}
			if it.media == uuid.Nil {
				return gate.NewProblem(gate.StatusUnprocessable, "action_invalid", "action")
			}
			return s.insertAction(ctx, tx, v, it, action, reason, subject, actionPending, now, nil)
		case action == ActionDismiss:
			if err := s.insertAction(ctx, tx, v, it, action, reason, subject, actionDone, now, nil); err != nil {
				return err
			}
			return s.closeItem(ctx, tx, v, it, reportDismissed, ActionDismiss, now)
		case action == ActionSuspendUser:
			if subject == 0 || subject == v.ID {
				return gate.NewProblem(gate.StatusConflict, "no_subject")
			}
			return s.insertAction(ctx, tx, v, it, action, reason, subject, actionPending, now, nil)
		case action == ActionRemoveMessage:
			// A message is private to its conversation: no blame-inbox entry
			// (the inviter could never see it), only the log's count.
			if it.rep == nil || it.rep.message == nil || s.d.Messages == nil {
				return gate.NewProblem(gate.StatusUnprocessable, "action_invalid", "action")
			}
			sender, ok, err := s.d.Messages.RemoveMessage(ctx, tx, *it.rep.message)
			if err != nil {
				return err
			}
			if !ok {
				return gate.NewProblem(gate.StatusConflict, "message_gone")
			}
			if err := s.insertAction(ctx, tx, v, it, action, reason, sender, actionDone, now, nil); err != nil {
				return err
			}
			return s.closeItem(ctx, tx, v, it, reportActioned, ActionRemoveMessage, now)
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
			if err := s.insertAction(ctx, tx, v, it, action, reason, author, actionDone, now, nil); err != nil {
				return err
			}
			if err := s.closeItem(ctx, tx, v, it, reportActioned, ActionRemovePost, now); err != nil {
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

func (s *Service) insertAction(ctx context.Context, tx store.Q, v core.Viewer, it item, action, reason string,
	subject core.UserID, state int16, now time.Time, reportRef *string) error {
	var done, firstAuth *time.Time
	if state == actionDone {
		done = &now
	} else {
		at := v.AuthAt
		firstAuth = &at
	}
	var (
		report *uuid.UUID
		post   *uuid.UUID
	)
	if it.rep != nil {
		report, post = &it.rep.id, it.rep.post
	} else if it.post != uuid.Nil {
		post = &it.post
	}
	_, err := tx.Exec(ctx, `INSERT INTO mod_actions (id, moderator_id, state, action, target_post, target_user, reason,
		report_id, item_id, report_ref, created_at, done_at, first_auth_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		s.d.IDs.New(), int64(v.ID), state, action, post, nullUser(subject), reason, report, it.id, reportRef, now, done, firstAuth)
	if err != nil && store.IsUniqueViolation(err) {
		return gate.NewProblem(gate.StatusConflict, "action_pending")
	}
	return err
}

// closeItem resolves the item; a report item closes its report and every
// other open report on the same target, with their items.
func (s *Service) closeItem(ctx context.Context, tx store.Q, v core.Viewer, it item, state int16, resolution string, now time.Time) error {
	if _, err := tx.Exec(ctx, "UPDATE mod_queue SET resolved_at = $2, resolution = $3 WHERE id = $1 AND resolved_at IS NULL",
		it.id, now, resolution); err != nil {
		return err
	}
	if it.rep == nil {
		return nil
	}
	r := it.rep
	_, err := tx.Exec(ctx, `WITH closed AS (
			UPDATE reports SET state = $1, resolved_by = $2, resolved_at = $3
			WHERE state = $4 AND (id = $5 OR (target_kind = $6 AND target_post IS NOT DISTINCT FROM $7
				AND target_user IS NOT DISTINCT FROM $8 AND target_message IS NOT DISTINCT FROM $10))
			RETURNING id)
		UPDATE mod_queue SET resolved_at = $3, resolution = $9
		WHERE resolved_at IS NULL AND report_id IN (SELECT id FROM closed)`,
		state, int64(v.ID), now, reportOpen, r.id, r.kind, r.post, r.user, resolution, r.message)
	return err
}
