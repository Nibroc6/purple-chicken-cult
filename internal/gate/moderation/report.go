package moderation

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/urlx"
)

// reportBody is the signed Report record (§3: reporter, target, reason, at).
type reportBody struct {
	Reporter   uuid.UUID `json:"reporter"`
	TargetKind string    `json:"target_kind"`
	Target     uuid.UUID `json:"target"` // post ID, or the reported person's (for inviter: the invitee's) ext_id
	Reason     string    `json:"reason"`
	At         sigs.Time `json:"at"`
}

// target is a resolved report target.
type target struct {
	kind int16
	post core.PostID // post and preview reports
	user core.UserID // person reports; for inviter reports the INVITEE
	ext  uuid.UUID   // record target
	// subject is who the report is about: the post's author, the person, or
	// (inviter reports) the invitee's inviter.
	subject core.UserID
}

// Report files a report. kind is post | person | inviter | preview; for
// inviter the ref is the INVITEE's (the inviter is never addressable, §3).
// Problems about the caller's own input: kind_invalid, reason_invalid,
// note_too_long. A target the reporter cannot see (or garbage, another viewer's
// ref, the reporter themself) is dropped silently: the answer is always the
// same nil, so a report reveals nothing about its target.
func (s *Service) Report(ctx context.Context, kind string, ref gate.Ref, reason, note string) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	tk, ok := targetKinds[kind]
	if !ok {
		return gate.NewProblem(gate.StatusUnprocessable, "kind_invalid", "target.kind")
	}
	if !validReason(core.ReportReasons, reason) {
		return gate.NewProblem(gate.StatusUnprocessable, "reason_invalid", "reason")
	}
	if utf8.RuneCountInString(note) > NoteMax {
		return gate.NewProblem(gate.StatusUnprocessable, "note_too_long", "note")
	}
	note = urlx.CleanText(note, NoteMax)
	t, ok := s.resolveTarget(ctx, v, tk, ref)
	if !ok {
		return nil
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// One open report per reporter and target: a repeat changes nothing.
		var dup int
		err := tx.QueryRow(ctx, `SELECT 1 FROM reports WHERE reporter_id = $1 AND state = $2 AND target_kind = $3
			AND target_post IS NOT DISTINCT FROM $4 AND target_user IS NOT DISTINCT FROM $5 LIMIT 1`,
			int64(v.ID), reportOpen, t.kind, nullPost(t.post), nullUser(t.user)).Scan(&dup)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		id := s.d.IDs.New()
		var notePtr *string
		if note != "" {
			notePtr = &note
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reports (id, reporter_id, target_kind, target_post, target_user, reason, note, created_at, state)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, int64(v.ID), t.kind, nullPost(t.post), nullUser(t.user),
			reason, notePtr, now, reportOpen); err != nil {
			return err
		}
		body := reportBody{Reporter: me.ExtID, TargetKind: kind, Target: t.ext, Reason: reason, At: sigs.T(now)}
		if _, err := s.d.Signer.Sign(ctx, tx, core.RecReport, v.ID, body, []core.UserID{v.ID}); err != nil {
			return err
		}
		// The report's queue item: csam first, then illegal, then the rest.
		prio := uint8(2)
		switch reason {
		case string(core.ReasonCSAM):
			prio = 0
		case string(core.ReasonIllegal):
			prio = 1
		}
		return s.Enqueue(ctx, tx, core.ModQueueItem{Source: core.ModSrcReport, Priority: prio, SubjectUser: t.subject,
			SubjectPost: t.post, ReportID: id, Summary: map[string]int{"kind_" + kind: 1}})
	})
}

// resolveTarget opens ref for v and checks v can see the target right now.
func (s *Service) resolveTarget(ctx context.Context, v core.Viewer, kind int16, ref gate.Ref) (target, bool) {
	t := target{kind: kind}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return t, false
	}
	switch kind {
	case 0, 3: // post, preview
		pid, ok := s.d.Refs.OpenPost(v.ID, ref)
		if !ok {
			return t, false
		}
		m, ok := s.d.Posts.Meta(ctx, pid)
		if !ok || m.Author == v.ID {
			return t, false
		}
		if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
			return t, false
		}
		t.post, t.ext, t.subject = pid, pid, m.Author
		return t, true
	default: // person, inviter (via the invitee)
		u, ok := s.d.Refs.OpenUser(v.ID, ref)
		if !ok || u == v.ID || !s.d.Vis.CanSeeUser(sr, u) {
			return t, false
		}
		m, err := s.d.Users.Get(ctx, u)
		if err != nil {
			return t, false
		}
		if kind == 2 && (m.InvitedBy == 0 || m.InvitedBy == v.ID) {
			return t, false // genesis has no inviter; nobody reports themself
		}
		t.user, t.ext, t.subject = u, m.ExtID, u
		if kind == 2 {
			t.subject = m.InvitedBy
		}
		return t, true
	}
}

func nullPost(p core.PostID) *uuid.UUID {
	if p == uuid.Nil {
		return nil
	}
	return &p
}

func nullUser(u core.UserID) *int64 {
	if u == 0 {
		return nil
	}
	x := int64(u)
	return &x
}
