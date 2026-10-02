// Package moderation owns reports, the moderator queue, two-person actions, the
// blame inbox, the public moderation log and CSP report aggregation
// (ARCHITECTURE §8, §10.4). P1 adds core.ModQueue, quarantine and signals.
//
// Owner: P0-W12 → P1-W06. Scaffold stub: GrantModerator works (CLI bootstrap);
// the rest returns gate.ErrNotImplemented. Report always succeeds (invalid refs
// are dropped silently by contract).
package moderation

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the moderation module.
type Service struct {
	d     *core.Deps
	queue *coreref.MemModQueue
}

var _ core.ModQueue = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, queue: &coreref.MemModQueue{}} }

// Init does nothing yet.
func (s *Service) Init(context.Context) error { return nil }

// Report files a report (kind: post|person|inviter|preview). Always nil for a
// signed-in reporter: invalid refs are dropped silently.
func (s *Service) Report(ctx context.Context, kind string, target gate.Ref, reason, note string) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	ok := false
	for _, r := range core.ReportReasons {
		if string(r) == reason {
			ok = true
		}
	}
	if !ok {
		return gate.NewProblem(gate.StatusUnprocessable, "reason_invalid", "reason")
	}
	return nil
}

// Queue lists the moderator queue.
func (s *Service) Queue(ctx context.Context, cursor string) (gate.Page[gate.ModItem], error) {
	return gate.Page[gate.ModItem]{}, s.mod(ctx)
}

// Resolve acts on a queue item (two-person actions await a second moderator).
func (s *Service) Resolve(ctx context.Context, item gate.Ref, action, reason string) error {
	return s.mod(ctx)
}

// Confirm is the second moderator's confirmation (RequireFresh).
func (s *Service) Confirm(ctx context.Context, item gate.Ref) error { return s.mod(ctx) }

// BlameInbox lists the viewer's blame-inbox entries.
func (s *Service) BlameInbox(ctx context.Context, cursor string) (gate.Page[gate.BlameItem], error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return gate.Page[gate.BlameItem]{}, err
	}
	return gate.Page[gate.BlameItem]{}, gate.ErrNotImplemented
}

// Log lists the public moderation log (pseudonyms outside the viewer's horizon).
func (s *Service) Log(ctx context.Context, cursor string) (gate.Page[gate.LogItem], error) {
	if _, err := core.RequireUser(ctx); err != nil {
		return gate.Page[gate.LogItem]{}, err
	}
	return gate.Page[gate.LogItem]{}, gate.ErrNotImplemented
}

// CSPReport counts a CSP violation per directive (aggregate only).
func (s *Service) CSPReport(ctx context.Context, directive string) error { return nil }

// GrantModerator makes handle a moderator (CLI). by must be an existing admin;
// bootstrap makes handle the first admin and works only when no admin exists.
func (s *Service) GrantModerator(ctx context.Context, handle, by string, bootstrap bool) error {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	by = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(by), "@"))
	return s.d.DB.TxSerializable(ctx, func(tx pgx.Tx) error {
		var admins int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role = 2").Scan(&admins); err != nil {
			return err
		}
		role := int16(core.RoleModerator)
		if bootstrap {
			if admins > 0 {
				return errors.New("moderation: an admin already exists; use --by")
			}
			role = int16(core.RoleAdmin)
		} else {
			var byRole int16
			err := tx.QueryRow(ctx, "SELECT role FROM users WHERE handle_canon = $1 AND status = 0", by).Scan(&byRole)
			if err != nil || byRole != int16(core.RoleAdmin) {
				return errors.New("moderation: --by must name an active admin")
			}
		}
		var id int64
		err := tx.QueryRow(ctx, "UPDATE users SET role = GREATEST(role, $2) WHERE handle_canon = $1 RETURNING id",
			handle, role).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.ErrNotFound
		}
		if err != nil {
			return err
		}
		s.d.Bus.Publish(core.UserChanged{ID: core.UserID(id)})
		return nil
	})
}

func (s *Service) mod(ctx context.Context) error {
	if _, err := core.RequireModerator(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// ---- core.ModQueue (P1-W06) ----

// Enqueue adds a moderation queue item.
func (s *Service) Enqueue(ctx context.Context, tx store.Q, item core.ModQueueItem) error {
	return s.queue.Enqueue(ctx, tx, item)
}
