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
	if target == v.ID {
		return "", gate.NewProblem(gate.StatusConflict, "recovery_self")
	}
	id := s.d.IDs.New()
	now := s.d.Clock.Now()
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// Status under a share lock: a concurrent Delete (users row FOR
		// UPDATE, then cancels every live request) serializes with us.
		if err := subjectLive(ctx, tx, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_requests (id, user_id, kind, state, created_at)
			VALUES ($1, $2, $3, $4, $5)`, id, int64(target), kindManual, stateOpen, now); err != nil {
			if store.IsUniqueViolation(err) {
				return gate.NewProblem(gate.StatusConflict, "recovery_exists")
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
	revokeOld     bool
	first, second core.UserID
	firstAuth     *time.Time
	approvals     int
}

// subjectLive share-locks the subject's users row and answers ErrNotFound when
// the account is gone or deleted. Delete holds that row FOR UPDATE while it
// cancels every live recovery, so no recovery step can race a deletion.
func subjectLive(ctx context.Context, tx store.Q, user core.UserID) error {
	var status int16
	err := tx.QueryRow(ctx, "SELECT status FROM users WHERE id = $1 FOR SHARE", int64(user)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return gate.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == int16(core.StatusDeleted) {
		return gate.ErrNotFound
	}
	return nil
}

// loadRecovery locks a manual recovery request after share-locking its
// subject's users row (the lock order Delete uses) and answers ErrNotFound
// when the subject account has been deleted.
func loadRecovery(ctx context.Context, tx store.Q, id uuid.UUID) (recoveryRow, error) {
	r := recoveryRow{id: id}
	var user int64
	err := tx.QueryRow(ctx, "SELECT user_id FROM recovery_requests WHERE id = $1 AND kind = $2", id, kindManual).Scan(&user)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, gate.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err := subjectLive(ctx, tx, core.UserID(user)); err != nil {
		return r, err
	}
	err = tx.QueryRow(ctx, `SELECT user_id, state, created_at, release_at, enrollment_token IS NOT NULL, revoke_old
		FROM recovery_requests WHERE id = $1 AND kind = $2 FOR UPDATE`, id, kindManual).
		Scan(&user, &r.state, &r.createdAt, &r.releaseAt, &r.tokenIssued, &r.revokeOld)
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
	var expired bool
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		expired = false
		r, err := loadRecovery(ctx, tx, rid)
		if err != nil {
			return err
		}
		if r.state != stateOpen {
			return gate.NewProblem(gate.StatusConflict, "recovery_not_open")
		}
		if now.Sub(r.createdAt) > OpenFor {
			expired = true
			_, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", rid, stateExpired)
			return err
		}
		if r.first == v.ID {
			return gate.NewProblem(gate.StatusConflict, "second_moderator_required")
		}
		if r.user == v.ID {
			return gate.NewProblem(gate.StatusConflict, "recovery_self")
		}
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
		return gate.NewProblem(gate.StatusConflict, "recovery_expired")
	}
	// No live push: "recovery" is not in the closed GET /api/events list. The
	// account's sessions pick the banner up from GET /api/me/recovery (polled,
	// and refetched on focus and reconnect).
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
	var (
		token   string
		expired bool
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		token, expired = "", false
		r, err := loadRecovery(ctx, tx, rid)
		if err != nil {
			return err
		}
		if r.approvals < 2 || r.second != v.ID {
			if r.first == v.ID && r.state == stateOpen {
				return gate.NewProblem(gate.StatusConflict, "recovery_held")
			}
			return gate.ErrNotFound
		}
		switch {
		case r.state == stateCancelled || r.state == stateExpired:
			return gate.ErrNotFound
		case r.tokenIssued:
			return gate.NewProblem(gate.StatusConflict, "recovery_link_issued")
		case r.releaseAt == nil || now.Before(*r.releaseAt):
			return gate.NewProblem(gate.StatusConflict, "recovery_held")
		case !now.Before(r.releaseAt.Add(LinkFor)):
			// The banner and the account's cancel window close at
			// release_at + LinkFor; a link fetched later would be invisible
			// to the owner, so the request expires instead.
			expired = true
			_, err := tx.Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE id = $1", rid, stateExpired)
			return err
		}
		if r.state == stateHolding && r.revokeOld {
			// Released now (the job had not run yet): every session ends.
			if err := s.d.Passkeys.RevokeAll(ctx, tx, r.user); err != nil {
				return err
			}
		}
		// Usable from release_at for EnrollTokenTTL (= LinkFor): the token dies
		// exactly when the banner and the account's cancel window close.
		tok, err := s.d.Passkeys.IssueEnrollment(ctx, tx, r.user, purposeRecovery, *r.releaseAt)
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
	if expired {
		return "", gate.NewProblem(gate.StatusConflict, "recovery_expired")
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
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		_, err := s.cancelRecoveries(ctx, tx, v.ID, now)
		return err
	})
}

// cancelRecoveries cancels every live recovery of user (open, holding, or
// released within the link window) and voids any enrollment token already
// issued for them. Used by RecoveryCancel and Delete. It reports whether
// anything was cancelled.
func (s *Service) cancelRecoveries(ctx context.Context, tx store.Q, user core.UserID, now time.Time) (bool, error) {
	rows, err := tx.Query(ctx, `UPDATE recovery_requests SET state = $2
		WHERE user_id = $1 AND (state IN (0, 1) OR (state = 2 AND release_at > $3))
		RETURNING enrollment_token`, int64(user), stateCancelled, now.Add(-LinkFor))
	if err != nil {
		return false, err
	}
	cancelled := false
	var tokens [][]byte
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return false, err
		}
		cancelled = true
		if h != nil {
			tokens = append(tokens, h)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, h := range tokens {
		if err := s.d.Enrollments.CancelEnrollment(ctx, tx, h); err != nil {
			return false, err
		}
	}
	return cancelled, nil
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
// requests never approved expire. On release every session of the account
// ends (revoke_old, security #14). It returns how many rows changed.
func (s *Service) releaseDue(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	released := 0
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		released = 0
		rows, err := tx.Query(ctx, `UPDATE recovery_requests SET state = $2 WHERE state = $3 AND release_at <= $1
			RETURNING user_id, revoke_old`, now, stateReleased, stateHolding)
		if err != nil {
			return err
		}
		var revoke []core.UserID
		for rows.Next() {
			var u int64
			var old bool
			if err := rows.Scan(&u, &old); err != nil {
				rows.Close()
				return err
			}
			released++
			if old {
				revoke = append(revoke, core.UserID(u))
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, u := range revoke {
			if err := s.d.Passkeys.RevokeAll(ctx, tx, u); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	b, err := s.d.DB.Q().Exec(ctx, "UPDATE recovery_requests SET state = $2 WHERE state = $3 AND created_at <= $1",
		now.Add(-OpenFor), stateExpired, stateOpen)
	if err != nil {
		return 0, err
	}
	// A released request whose link was never fetched expires with the window.
	c, err := s.d.DB.Q().Exec(ctx, `UPDATE recovery_requests SET state = $2
		WHERE state = $3 AND enrollment_token IS NULL AND release_at <= $1`,
		now.Add(-LinkFor), stateExpired, stateReleased)
	if err != nil {
		return 0, err
	}
	return released + int(b.RowsAffected()) + int(c.RowsAffected()), nil
}
