package records

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// The co-sign cut-over (records_policy.cosign_since). From that moment every
// Invite, Friendship and Revocation record and every passkey change must carry
// a passkey assertion of one of its parties, and every passkey added after a
// user's first must be co-signed by one of that user's earlier credentials.
// Exports publish the time in keys.json and VerifyExport enforces it. It is
// unset until the operator (INT-1, once every caller of core.Cosign has
// landed) turns enforcement on; records signed before it are P0-era records.

// CosignSince returns the cut-over, nil when enforcement is off.
func (s *Service) CosignSince(ctx context.Context) (*time.Time, error) {
	return s.cosignSince(ctx, s.d.DB.Q())
}

func (s *Service) cosignSince(ctx context.Context, q store.Q) (*time.Time, error) {
	var at *time.Time
	err := q.QueryRow(ctx, "SELECT cosign_since FROM records_policy WHERE id").Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if at != nil {
		u := at.UTC()
		at = &u
	}
	return at, nil
}

// EnforceCosign sets the cut-over to at (CLI / INT-1). It only ever moves
// earlier or is set once: a later time would silently exempt records.
func (s *Service) EnforceCosign(ctx context.Context, at time.Time) error {
	_, err := s.d.DB.Q().Exec(ctx, `INSERT INTO records_policy (id, cosign_since) VALUES (true, $1)
		ON CONFLICT (id) DO UPDATE SET cosign_since = LEAST(COALESCE(records_policy.cosign_since, EXCLUDED.cosign_since),
		EXCLUDED.cosign_since)`, at.UTC())
	return err
}
