package users

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Steward addition (contract request p0-w01-1): the account facts gate/auth
// needs inside its transactions.

var _ core.Accounts = (*Service)(nil)

// Account reads id's WebAuthn user handle, ext_id and status through q,
// locking the row until q's transaction ends when forUpdate is set.
func (s *Service) Account(ctx context.Context, q store.Q, id core.UserID, forUpdate bool) ([]byte, uuid.UUID, uint8, error) {
	sql := "SELECT webauthn_user_id, ext_id, status FROM users WHERE id = $1"
	if forUpdate {
		sql = "SELECT webauthn_user_id, ext_id, status FROM users WHERE id = $1 FOR NO KEY UPDATE"
	}
	var (
		webID  []byte
		ext    uuid.UUID
		status int16
	)
	if err := q.QueryRow(ctx, sql, int64(id)).Scan(&webID, &ext, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, uuid.Nil, 0, gate.ErrNotFound
		}
		return nil, uuid.Nil, 0, err
	}
	return webID, ext, uint8(status), nil
}

// ActivateGenesis turns a pending genesis account active and records the
// accepted terms version and the age attestation (D29). The caller publishes
// UserChanged after commit.
func (s *Service) ActivateGenesis(ctx context.Context, tx store.Q, id core.UserID, tosVersion int, at time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE users SET status = $2, tos_version = $3, tos_accepted_at = $4, age_attested_at = $4
		WHERE id = $1 AND status = $5 AND is_genesis`, int64(id), int16(core.StatusActive), int16(tosVersion), at,
		int16(core.StatusPendingEnrollment))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return gate.ErrNotFound
	}
	return nil
}

// Steward addition (contract request p0-w12-1): suspension on a confirmed
// two-person moderator action.

var _ core.Suspensions = (*Service)(nil)

// Suspend turns the active account id suspended in tx. The caller publishes
// UserChanged after commit.
func (s *Service) Suspend(ctx context.Context, tx store.Q, id core.UserID) error {
	tag, err := tx.Exec(ctx, "UPDATE users SET status = $2 WHERE id = $1 AND status = $3",
		int64(id), int16(core.StatusSuspended), int16(core.StatusActive))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return gate.ErrNotFound
	}
	return nil
}
