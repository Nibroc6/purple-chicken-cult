package account

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Manual recovery (§10.3, security #14): two distinct moderators, each with a
// fresh step-up; a 24 h hold during which every live session of the account
// shows a banner with Cancel and friends are told; then the second moderator
// alone can fetch the one-time enrollment link.

// Recovery timings.
const (
	HoldFor = 24 * time.Hour // approval → release
	OpenFor = 24 * time.Hour // request → second approval, else expired
	LinkFor = 24 * time.Hour // the banner stays this long after release (enrollment token lifetime)
)

// recovery_requests.state values.
const (
	stateOpen      int16 = 0
	stateHolding   int16 = 1
	stateReleased  int16 = 2
	stateCancelled int16 = 3
	stateExpired   int16 = 4
)

const (
	kindManual        int16 = 0
	roleModerator     int16 = 0
	purposeRecovery   uint8 = 1
	modReasonRecovery       = "other"
)

// sharedModItem is the moderator-scoped ref viewer shared by every moderator,
// so the first moderator can hand a request ID to the second one.
const sharedModItem core.UserID = 0

func (s *Service) freshModerator(ctx context.Context) (core.Viewer, error) {
	v, err := core.RequireModerator(ctx)
	if err != nil {
		return v, err
	}
	return core.RequireFresh(ctx, s.d.Clock.Now())
}

func recoveryProblem(code string) error { return gate.NewProblem(gate.StatusConflict, code) }

// RecoveryRequest opens a manual recovery for subject (a moderator-scoped user
// ref minted for this moderator); it needs a fresh step-up. It returns the
// request ID, a ref every moderator can open. Problems recovery_self,
// recovery_exists.
func (s *Service) RecoveryRequest(ctx context.Context, subject gate.Ref) (gate.Ref, error) {
	v, err := s.freshModerator(ctx)
	if err != nil {
		return "", err
	}
	target, ok := s.d.Refs.OpenModUser(v.ID, subject)
	if !ok {
		return "", gate.ErrNotFound
	}
	m, err := s.d.Users.Get(ctx, target)
	if err != nil {
		return "", err
	}
	if m.Status == core.StatusDeleted {
		return "", gate.ErrNotFound
	}
	if target == v.ID {
		return "", recoveryProblem("recovery_self")
	}
	id := s.d.IDs.New()
	now := s.d.Clock.Now()
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_requests (id, user_id, kind, state, created_at)
			VALUES ($1, $2, $3, $4, $5)`, id, int64(target), kindManual, stateOpen, now); err != nil {
			if store.IsUniqueViolation(err) {
				return recoveryProblem("recovery_exists")
			}
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO recovery_approvals (request_id, approver_id, role, at, auth_at)
			VALUES ($1, $2, $3, $4, $5)`, id, int64(v.ID), roleModerator, now, v.AuthAt)
		return err
	})
	if err != nil {
		return "", err
	}
	return s.d.Refs.ModItem(sharedModItem, id), nil
}

type recoveryRow struct {
	id            uuid.UUID
	user          core.UserID
	state         int16
	createdAt     time.Time
	releaseAt     *time.Time
	tokenIssued   bool
	first, second core.UserID
	firstAuth     *time.Time
	approvals     int
}

func loadRecovery(ctx context.Context, tx store.Q, id uuid.UUID) (recoveryRow, error) {
	r := recoveryRow{id: id}
	var user int64
	err := tx.QueryRow(ctx, `SELECT user_id, state, created_at, release_at, enrollment_token IS NOT NULL
		FROM recovery_requests WHERE id = $1 AND kind = $2 FOR UPDATE`, id, kindManual).
		Scan(&user, &r.state, &r.createdAt, &r.releaseAt, &r.tokenIssued)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, gate.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.user = core.UserID(user)
	rows, err := tx.Query(ctx, `SELECT approver_id, auth_at FROM recovery_approvals
		WHERE request_id = $1 AND role = $2 ORDER BY at, approver_id`, id, roleModerator)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var a int64
		var at *time.Time
		if err := rows.Scan(&a, &at); err != nil {
			return r, err
		}
		switch r.approvals {
		case 0:
			r.first, r.firstAuth = core.UserID(a), at
		case 1:
			r.second = core.UserID(a)
		}
		r.approvals++
	}
	return r, rows.Err()
}

// RecoveryApprove is the second moderator's approval (≠ the first, fresh
// step-up). It starts the 24 h hold, records the two-person action and tells
// the account's friends. Problems second_moderator_required, recovery_self,
// recovery_not_open, recovery_expired.
func (s *Service) RecoveryApprove(ctx context.Context, id gate.Ref) error {
	v, err := s.freshModerator(ctx)
	if err != nil {
		return err
	}
	rid, ok := s.d.Refs.OpenModItem(sharedModItem, id)
	if !ok {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var (
		subject core.UserID
		expired bool
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		expired = false
		r, err := loadRecovery(ctx, tx, rid)
		if err != nil {
			return err
		}
		if r.state != stateOpen {
			return recoveryProblem("recovery_not_open")
		}
		if now.Sub(r.createdAt) > OpenFor {
			expired = true
			_, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", rid, stateExpired)
			return err
		}
		if r.first == v.ID {
			return recoveryProblem("second_moderator_required")
		}
		if r.user == v.ID {
			return recoveryProblem("recovery_self")
		}
		subject = r.user
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_approvals (request_id, approver_id, role, at, auth_at)
			VALUES ($1, $2, $3, $4, $5)`, rid, int64(v.ID), roleModerator, now, v.AuthAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2, release_at = $3 WHERE id = $1",
			rid, stateHolding, now.Add(HoldFor)); err != nil {
			return err
		}
		secondAuth, err := json.Marshal(map[string]any{"at": now.UTC(), "auth_at": v.AuthAt.UTC(),
			"first_auth_at": r.firstAuth})
		if err != nil {
			return err
		}
		// mod_actions keeps both moderators of the two-person action (plan §4.7).
		if _, err := tx.Exec(ctx, `INSERT INTO mod_actions (id, moderator_id, second_moderator, state, action, target_user,
			reason, second_auth, created_at, done_at) VALUES ($1, $2, $3, 1, 'recovery_approve', $4, $5, $6, $7, $8)`,
			s.d.IDs.New(), int64(r.first), int64(v.ID), int64(r.user), modReasonRecovery, secondAuth, r.createdAt, now); err != nil {
			return err
		}
		for _, f := range s.d.Graph.Friends(r.user) {
			if err := s.d.Notify.Notify(ctx, tx, core.Event{To: f.To, Kind: notifyFriendSecurity, SubjectUser: r.user,
				Data: map[string]any{"reason": "recovery_pending"}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if expired {
		return recoveryProblem("recovery_expired")
	}
	s.d.Notify.Live(subject, "recovery", map[string]any{"state": "holding"})
	return nil
}

// RecoveryLink returns the one-time enrollment URL to the second moderator
// (fresh step-up) once the hold has elapsed. Problems recovery_held,
// recovery_link_issued.
func (s *Service) RecoveryLink(ctx context.Context, id gate.Ref) (string, error) {
	v, err := s.freshModerator(ctx)
	if err != nil {
		return "", err
	}
	rid, ok := s.d.Refs.OpenModItem(sharedModItem, id)
	if !ok {
		return "", gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var token string
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		r, err := loadRecovery(ctx, tx, rid)
		if err != nil {
			return err
		}
		if r.approvals < 2 || r.second != v.ID {
			if r.first == v.ID && r.state == stateOpen {
				return recoveryProblem("recovery_held")
			}
			return gate.ErrNotFound
		}
		switch {
		case r.state == stateCancelled || r.state == stateExpired:
			return gate.ErrNotFound
		case r.tokenIssued:
			return recoveryProblem("recovery_link_issued")
		case r.releaseAt == nil || now.Before(*r.releaseAt):
			return recoveryProblem("recovery_held")
		}
		tok, err := s.d.Passkeys.IssueEnrollment(ctx, tx, r.user, purposeRecovery, now)
		if err != nil {
			return err
		}
		h := sha256.Sum256([]byte(tok))
		if _, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2, enrollment_token = $3 WHERE id = $1",
			rid, stateReleased, h[:]); err != nil {
			return err
		}
		token = tok
		return nil
	})
	if err != nil {
		return "", err
	}
	return s.d.Cfg.Origin + "/enroll#" + token, nil
}

// RecoveryCancel cancels a pending recovery of the viewer's own account from
// any of its live sessions; a link already issued stops working. Idempotent.
func (s *Service) RecoveryCancel(ctx context.Context) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	var cancelled bool
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		cancelled = false
		rows, err := tx.Query(ctx, `UPDATE recovery_requests SET state = $2
			WHERE user_id = $1 AND (state IN (0, 1) OR (state = 2 AND release_at > $3))
			RETURNING enrollment_token`, int64(v.ID), stateCancelled, now.Add(-LinkFor))
		if err != nil {
			return err
		}
		var tokens [][]byte
		for rows.Next() {
			var h []byte
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return err
			}
			cancelled = true
			if h != nil {
				tokens = append(tokens, h)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, h := range tokens {
			if err := cancelEnrollment(ctx, tx, h, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if cancelled {
		s.d.Notify.Live(v.ID, "recovery", map[string]any{"state": "cancelled"})
	}
	return nil
}

// cancelEnrollment voids an issued recovery enrollment token.
//
// Local adapter: enrollment_tokens belongs to gate/auth (P0-W01), whose
// EnrollBegin/EnrollFinish refuse a token with cancelled_at set (plan §3.2).
// docs/contract-requests/p0-w11-2.md asks for core.Passkeys.CancelEnrollment.
func cancelEnrollment(ctx context.Context, tx store.Q, tokenHash []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE enrollment_tokens SET cancelled_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND cancelled_at IS NULL`, tokenHash, now)
	return err
}

// RecoveryPending returns the release time of a pending recovery of the
// viewer's own account (the banner), or nil.
func (s *Service) RecoveryPending(ctx context.Context) (*time.Time, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	var at time.Time
	err = s.d.DB.Q().QueryRow(ctx, `SELECT release_at FROM recovery_requests
		WHERE user_id = $1 AND release_at IS NOT NULL AND (state = 1 OR (state = 2 AND release_at > $2))
		ORDER BY created_at DESC LIMIT 1`, int64(v.ID), s.d.Clock.Now().Add(-LinkFor)).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at = at.UTC()
	return &at, nil
}

// releaseDue is the recovery.release job: holding requests whose hold has
// elapsed become released (the second moderator may fetch the link) and open
// requests never approved expire. It returns how many rows changed.
func (s *Service) releaseDue(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	a, err := s.d.DB.Q().Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE state = $3 AND release_at <= $1",
		now, stateReleased, stateHolding)
	if err != nil {
		return 0, err
	}
	b, err := s.d.DB.Q().Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE state = $3 AND created_at <= $1",
		now.Add(-OpenFor), stateExpired, stateOpen)
	if err != nil {
		return 0, err
	}
	return int(a.RowsAffected() + b.RowsAffected()), nil
}
