package moderation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Role changes (mod_actions.action, reason role_change). Every grant and
// revocation is a confirmed moderation-log entry: moderator_id is the admin
// who made it (the new admin themselves for `pcc mod grant --bootstrap`),
// target_user the person whose role changed.
const (
	ActionGrantModerator  = "grant_moderator"
	ActionRevokeModerator = "revoke_moderator"
	ActionGrantAdmin      = "grant_admin"
	// ReasonRoleChange is the log reason of every role change.
	ReasonRoleChange = "role_change"
	// RoleChangesShown is how many role changes the roles page lists.
	RoleChangesShown = 50
)

// Admins manage moderators on the roles page; the admin role itself changes
// only through the CLI (`pcc mod grant`), which needs access to the server.
// A single admin session, even freshly stepped up, cannot mint or remove
// admins: there is no second admin to confirm it, and a hijacked admin
// session could otherwise lock out the operator.

func canonHandle(h string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "@"))
}

// requireAdmin returns the viewer if they are an admin; otherwise the uniform
// not-found (the roles page does not exist for anyone else).
func requireAdmin(ctx context.Context) (core.Viewer, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return v, err
	}
	if v.Role != core.RoleAdmin {
		return core.Viewer{}, gate.ErrNotFound
	}
	return v, nil
}

// logRoleChange writes the moderation-log entry of a role change in tx.
// authAt is the admin's step-up time (nil from the CLI).
func (s *Service) logRoleChange(ctx context.Context, tx store.Q, by, subject core.UserID, action string, authAt *time.Time,
	now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO mod_actions (id, moderator_id, state, action, target_user, reason, created_at, done_at,
		first_auth_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)`,
		s.d.IDs.New(), int64(by), actionDone, action, int64(subject), ReasonRoleChange, now, authAt)
	return err
}

// Staff lists the active moderators and admins (admins first, then by
// handle) and the latest RoleChangesShown role changes. Admins only.
func (s *Service) Staff(ctx context.Context) (gate.Staff, error) {
	out := gate.Staff{Members: []gate.StaffMember{}, Changes: []gate.RoleChange{}}
	v, err := requireAdmin(ctx)
	if err != nil {
		return out, err
	}
	type member struct {
		id    core.UserID
		role  core.Role
		since *time.Time
	}
	var members []member
	rows, err := s.d.DB.Q().Query(ctx, `SELECT u.id, u.role, (SELECT max(a.done_at) FROM mod_actions a WHERE a.target_user = u.id
			AND a.state = $1 AND a.action IN ('grant_moderator', 'grant_admin'))
		FROM users u WHERE u.role >= $2 AND u.status = $3 ORDER BY u.role DESC, u.handle_canon`,
		actionDone, int16(core.RoleModerator), int16(core.StatusActive))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var (
			id   int64
			role int16
			m    member
		)
		if err := rows.Scan(&id, &role, &m.since); err != nil {
			rows.Close()
			return out, err
		}
		m.id, m.role = core.UserID(id), core.Role(role) //nolint:gosec // users.id < 2^32 (DB CHECK), users.role is 0..2
		members = append(members, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	type change struct {
		action      string
		subject, by core.UserID
		at          time.Time
	}
	var changes []change
	rows, err = s.d.DB.Q().Query(ctx, `SELECT action, target_user, moderator_id, done_at FROM mod_actions
		WHERE state = $1 AND action IN ('grant_moderator', 'revoke_moderator', 'grant_admin')
		ORDER BY done_at DESC, id LIMIT $2`, actionDone, RoleChangesShown)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var (
			c           change
			subject, by int64
		)
		if err := rows.Scan(&c.action, &subject, &by, &c.at); err != nil {
			rows.Close()
			return out, err
		}
		c.subject, c.by = core.UserID(subject), core.UserID(by) //nolint:gosec // users.id < 2^32 (DB CHECK)
		changes = append(changes, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	ids := make([]core.UserID, 0, len(members)+2*len(changes))
	for _, m := range members {
		ids = append(ids, m.id)
	}
	for _, c := range changes {
		ids = append(ids, c.subject, c.by)
	}
	metas, err := s.d.Users.GetMany(ctx, ids)
	if err != nil {
		return out, err
	}
	for _, m := range members {
		meta, ok := metas[m.id]
		if !ok {
			continue
		}
		out.Members = append(out.Members, gate.StaffMember{Ref: s.d.Refs.User(v.ID, m.id), Handle: meta.Handle,
			DisplayName: meta.DisplayName, Role: m.role.String(), Since: m.since, Me: m.id == v.ID})
	}
	for _, c := range changes {
		out.Changes = append(out.Changes, gate.RoleChange{Action: c.action, Handle: metas[c.subject].Handle,
			ByHandle: metas[c.by].Handle, At: c.at})
	}
	return out, nil
}

// GrantModeratorRole makes the active member with handle a moderator. Admins
// only, with a fresh step-up. Problems: handle_unknown (no active account
// has it), already_staff (already a moderator or admin).
func (s *Service) GrantModeratorRole(ctx context.Context, handle string) error {
	if _, err := requireAdmin(ctx); err != nil {
		return err
	}
	v, err := core.RequireFresh(ctx, s.d.Clock.Now())
	if err != nil {
		return err
	}
	handle = canonHandle(handle)
	now := s.d.Clock.Now()
	var id int64
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var role int16
		err := tx.QueryRow(ctx, "SELECT id, role FROM users WHERE handle_canon = $1 AND status = $2 FOR UPDATE",
			handle, int16(core.StatusActive)).Scan(&id, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.NewProblem(gate.StatusUnprocessable, "handle_unknown", "handle")
		}
		if err != nil {
			return err
		}
		if role != int16(core.RoleMember) {
			return gate.NewProblem(gate.StatusConflict, "already_staff")
		}
		if _, err := tx.Exec(ctx, "UPDATE users SET role = $2 WHERE id = $1", id, int16(core.RoleModerator)); err != nil {
			return err
		}
		at := v.AuthAt
		return s.logRoleChange(ctx, tx, v.ID, core.UserID(id), ActionGrantModerator, &at, now) //nolint:gosec // users.id < 2^32 (DB CHECK)
	})
	if err != nil {
		return err
	}
	s.d.Bus.Publish(core.UserChanged{ID: core.UserID(id)}) //nolint:gosec // users.id < 2^32 (DB CHECK)
	return nil
}

// RevokeModerator makes the moderator ref (the admin's own user ref, from
// Staff) a member again and releases their claims on queue items; two-person
// actions they started still wait for a second moderator. Admins only, with a
// fresh step-up. Problems: admin_role (admins change only through the CLI),
// not_moderator.
func (s *Service) RevokeModerator(ctx context.Context, ref gate.Ref) error {
	v, err := requireAdmin(ctx)
	if err != nil {
		return err
	}
	target, ok := s.d.Refs.OpenUser(v.ID, ref)
	if !ok {
		return gate.ErrNotFound
	}
	if v, err = core.RequireFresh(ctx, s.d.Clock.Now()); err != nil {
		return err
	}
	now := s.d.Clock.Now()
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var role int16
		err := tx.QueryRow(ctx, "SELECT role FROM users WHERE id = $1 FOR UPDATE", int64(target)).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return gate.ErrNotFound
		}
		if err != nil {
			return err
		}
		switch core.Role(role) { //nolint:gosec // users.role is 0..2
		case core.RoleAdmin:
			return gate.NewProblem(gate.StatusConflict, "admin_role")
		case core.RoleModerator:
		default:
			return gate.NewProblem(gate.StatusConflict, "not_moderator")
		}
		if _, err := tx.Exec(ctx, "UPDATE users SET role = $2 WHERE id = $1", int64(target), int16(core.RoleMember)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE mod_queue SET claimed_by = NULL, claimed_at = NULL WHERE claimed_by = $1 AND resolved_at IS NULL",
			int64(target)); err != nil {
			return err
		}
		at := v.AuthAt
		return s.logRoleChange(ctx, tx, v.ID, target, ActionRevokeModerator, &at, now)
	})
	if err != nil {
		return err
	}
	s.d.Bus.Publish(core.UserChanged{ID: target})
	return nil
}
