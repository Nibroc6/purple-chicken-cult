package dm

import (
	"context"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/urlx"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// connection opens a person ref for v that must be one of v's direct
// connections. A ref that does not open, or names someone v cannot see, is
// ErrNotFound; someone v sees but is not connected to is the Problem
// not_connected; v themself is recipient_self.
func (s *Service) connection(ctx context.Context, v core.UserID, ref gate.Ref, sr *core.Search) (core.UserID, error) {
	u, ok := s.d.Refs.OpenUser(v, ref)
	if !ok {
		return 0, gate.ErrNotFound
	}
	if u == v {
		return 0, gate.NewProblem(gate.StatusUnprocessable, "recipient_self", "to")
	}
	if m, err := s.d.Users.Get(ctx, u); err != nil || m.Status != core.StatusActive {
		return 0, gate.ErrNotFound
	}
	if s.connected(v, u) {
		return u, nil
	}
	if *sr == nil {
		got, err := s.d.Vis.SearchFor(ctx, v)
		if err != nil {
			return 0, err
		}
		*sr = got
	}
	if !s.d.Vis.CanSeeUser(*sr, u) {
		return 0, gate.ErrNotFound
	}
	return 0, gate.NewProblem(gate.StatusConflict, "not_connected", "to")
}

// Direct opens the 1:1 conversation with one of the viewer's direct
// connections, creating it on first use (a cleared one comes back).
func (s *Service) Direct(ctx context.Context, to gate.Ref) (gate.ConversationView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.ConversationView{}, err
	}
	var sr core.Search
	u, err := s.connection(ctx, v.ID, to, &sr)
	if err != nil {
		return gate.ConversationView{}, err
	}
	lo, hi := min(v.ID, u), max(v.ID, u)
	now := s.now()
	var id uuid.UUID
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM dm_conversations WHERE kind = 0 AND pair_lo = $1 AND pair_hi = $2`,
			int64(lo), int64(hi)).Scan(&id)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE dm_members SET hidden = false WHERE conversation_id = $1 AND user_id = $2`,
				id, int64(v.ID))
			return err
		}
		if !isNoRows(err) {
			return err
		}
		id = s.d.IDs.New()
		if _, err := tx.Exec(ctx, `INSERT INTO dm_conversations (id, kind, pair_lo, pair_hi, created_by, created_at, last_message_at)
			VALUES ($1, 0, $2, $3, $4, $5, $5) ON CONFLICT DO NOTHING`, id, int64(lo), int64(hi), int64(v.ID), now); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM dm_conversations WHERE kind = 0 AND pair_lo = $1 AND pair_hi = $2`,
			int64(lo), int64(hi)).Scan(&id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO dm_members (conversation_id, user_id, state, joined_at) VALUES ($1, $2, 0, $4), ($1, $3, 0, $4)
			ON CONFLICT DO NOTHING`, id, int64(v.ID), int64(u), now)
		return err
	})
	if err != nil {
		return gate.ConversationView{}, err
	}
	return s.Get(ctx, s.d.Refs.Conv(v.ID, id))
}

// cleanTitle normalizes a group title ("" = untitled); Problem title_too_long.
func cleanTitle(t string) (string, error) {
	if utf8.RuneCountInString(t) > MaxTitle {
		return "", gate.NewProblem(gate.StatusUnprocessable, "title_too_long", "title")
	}
	return urlx.CleanText(norm.NFC.String(t), MaxTitle), nil
}

// groupAllowed refuses group creation and invitations on probation (§8).
func (s *Service) groupAllowed(ctx context.Context, v core.UserID) error {
	m, err := s.d.Users.Get(ctx, v)
	if err != nil {
		return err
	}
	if m.OnProbation(s.now()) {
		return gate.NewProblem(gate.StatusConflict, "probation")
	}
	return nil
}

// invitees resolves people refs to direct connections of v, without repeats.
func (s *Service) invitees(ctx context.Context, v core.UserID, refs []gate.Ref) ([]core.UserID, error) {
	var (
		sr   core.Search
		out  []core.UserID
		seen = map[core.UserID]bool{}
	)
	for _, r := range refs {
		u, err := s.connection(ctx, v, r, &sr)
		if err != nil {
			return nil, err
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out, nil
}

// CreateGroup starts a group chat with a title (optional) and invites the
// given direct connections of the viewer. Problems: members_empty,
// too_many_members, title_too_long, not_connected, probation.
func (s *Service) CreateGroup(ctx context.Context, title string, members []gate.Ref) (gate.ConversationView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.ConversationView{}, err
	}
	us, err := s.invitees(ctx, v.ID, members)
	if err != nil {
		return gate.ConversationView{}, err
	}
	if len(us) == 0 {
		return gate.ConversationView{}, gate.NewProblem(gate.StatusUnprocessable, "members_empty", "members")
	}
	if len(us)+1 > MaxMembers {
		return gate.ConversationView{}, gate.NewProblem(gate.StatusUnprocessable, "too_many_members", "members")
	}
	if title, err = cleanTitle(title); err != nil {
		return gate.ConversationView{}, err
	}
	if err := s.groupAllowed(ctx, v.ID); err != nil {
		return gate.ConversationView{}, err
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.ConversationView{}, err
	}
	now := s.now()
	id := s.d.IDs.New()
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO dm_conversations (id, kind, title, created_by, created_at, last_message_at)
			VALUES ($1, 1, $2, $3, $4, $4)`, id, titlePtr, int64(v.ID), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dm_members (conversation_id, user_id, state, admin, joined_at)
			VALUES ($1, $2, 0, true, $3)`, id, int64(v.ID), now); err != nil {
			return err
		}
		if err := s.insert(ctx, tx, id, msgRow{id: s.d.IDs.New(), sender: v.ID, kind: msgEvent, event: EventCreated, at: now}); err != nil {
			return err
		}
		return s.invite(ctx, tx, id, v.ID, me.Handle, us)
	})
	if err != nil {
		return gate.ConversationView{}, err
	}
	for _, u := range us {
		s.announceTo(ctx, id, u)
	}
	return s.Get(ctx, s.d.Refs.Conv(v.ID, id))
}

// invite writes invitations from by to us in tx and pushes them.
func (s *Service) invite(ctx context.Context, tx store.Q, c uuid.UUID, by core.UserID, byHandle string, us []core.UserID) error {
	now := s.now()
	for _, u := range us {
		if _, err := tx.Exec(ctx, `INSERT INTO dm_members (conversation_id, user_id, state, invited_by, joined_at)
			VALUES ($1, $2, 1, $3, $4)`, c, int64(u), int64(by), now); err != nil {
			return err
		}
		if s.d.Graph.Blocks(u, by) {
			continue
		}
		if err := s.push(ctx, tx, u, c, "group_invite", byHandle); err != nil {
			return err
		}
	}
	return nil
}

// Invite invites more of the viewer's direct connections to a group they are
// a member of (people already in it or invited are skipped). Problems:
// not_joined, not_group, too_many_members, not_connected, probation.
func (s *Service) Invite(ctx context.Context, ref gate.Ref, people []gate.Ref) (gate.ConversationView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.ConversationView{}, err
	}
	if _, _, _, err := s.access(ctx, s.d.DB.Q(), v.ID, ref, true, false); err != nil {
		return gate.ConversationView{}, err
	}
	us, err := s.invitees(ctx, v.ID, people)
	if err != nil {
		return gate.ConversationView{}, err
	}
	if err := s.groupAllowed(ctx, v.ID); err != nil {
		return gate.ConversationView{}, err
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.ConversationView{}, err
	}
	var (
		c     conv
		added []core.UserID
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var ms []member
		c, ms, _, err = s.access(ctx, tx, v.ID, ref, true, true)
		if err != nil {
			return err
		}
		if c.kind != kindGroup {
			return gate.NewProblem(gate.StatusConflict, "not_group")
		}
		added = added[:0]
		for _, u := range us {
			if _, in := find(ms, u); !in {
				added = append(added, u)
			}
		}
		if len(ms)+len(added) > MaxMembers {
			return gate.NewProblem(gate.StatusUnprocessable, "too_many_members", "people")
		}
		return s.invite(ctx, tx, c.id, v.ID, me.Handle, added)
	})
	if err != nil {
		return gate.ConversationView{}, err
	}
	for _, u := range added {
		s.announceTo(ctx, c.id, u)
	}
	return s.Get(ctx, ref)
}

// Join accepts an invitation (the members see "@x joined").
func (s *Service) Join(ctx context.Context, ref gate.Ref) (gate.ConversationView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.ConversationView{}, err
	}
	var (
		c  conv
		ms []member
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var me member
		c, ms, me, err = s.access(ctx, tx, v.ID, ref, false, true)
		if err != nil || me.state == stateMember {
			return err
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE dm_members SET state = 0, joined_at = $3 WHERE conversation_id = $1 AND user_id = $2`,
			c.id, int64(v.ID), now); err != nil {
			return err
		}
		return s.insert(ctx, tx, c.id, msgRow{id: s.d.IDs.New(), sender: v.ID, kind: msgEvent, event: EventJoined,
			subject: v.ID, at: now})
	})
	if err != nil {
		return gate.ConversationView{}, err
	}
	s.announce(ctx, c, ms)
	return s.Get(ctx, ref)
}

// Leave leaves a group (or declines an invitation): the members see "@x
// left"; the earliest remaining member becomes admin when the last admin
// leaves; a group nobody is left in is deleted. For a 1:1 conversation it
// clears the history for the viewer and takes it off their list until a new
// message arrives (the other person sees no change).
func (s *Service) Leave(ctx context.Context, ref gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	var (
		c  conv
		ms []member
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var me member
		c, ms, me, err = s.access(ctx, tx, v.ID, ref, false, true)
		if err != nil {
			return err
		}
		if c.kind == kindDirect {
			_, err := tx.Exec(ctx, `UPDATE dm_members SET hidden = true,
					cleared_up_to = (SELECT id FROM dm_messages WHERE conversation_id = $1 ORDER BY id DESC LIMIT 1),
					read_up_to = (SELECT id FROM dm_messages WHERE conversation_id = $1 ORDER BY id DESC LIMIT 1)
				WHERE conversation_id = $1 AND user_id = $2`, c.id, int64(v.ID))
			return err
		}
		return s.removeMember(ctx, tx, c, ms, me, v.ID, EventLeft)
	})
	if err != nil {
		return err
	}
	s.announce(ctx, c, ms)
	return nil
}

// removeMember deletes m from group c in tx, writes the event (members only),
// hands the admin role on and deletes a group without members.
func (s *Service) removeMember(ctx context.Context, tx store.Q, c conv, ms []member, m member, by core.UserID, event string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM dm_members WHERE conversation_id = $1 AND user_id = $2`, c.id, int64(m.user)); err != nil {
		return err
	}
	var left []member
	for _, x := range joined(ms) {
		if x.user != m.user {
			left = append(left, x)
		}
	}
	if len(left) == 0 {
		_, err := tx.Exec(ctx, `DELETE FROM dm_conversations WHERE id = $1`, c.id)
		return err
	}
	if m.state == stateMember {
		if err := s.insert(ctx, tx, c.id, msgRow{id: s.d.IDs.New(), sender: by, kind: msgEvent, event: event,
			subject: m.user, at: s.now()}); err != nil {
			return err
		}
	}
	hasAdmin := false
	for _, x := range left {
		hasAdmin = hasAdmin || x.admin
	}
	if !hasAdmin {
		_, err := tx.Exec(ctx, `UPDATE dm_members SET admin = true WHERE conversation_id = $1 AND user_id = $2`,
			c.id, int64(left[0].user))
		return err
	}
	return nil
}

// Remove takes a member or invitee out of a group (admins only). Problems:
// not_group, not_admin, remove_self (use Leave).
func (s *Service) Remove(ctx context.Context, ref, person gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	u, ok := s.d.Refs.OpenUser(v.ID, person)
	if !ok {
		return gate.ErrNotFound
	}
	var (
		c  conv
		ms []member
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var me member
		c, ms, me, err = s.access(ctx, tx, v.ID, ref, true, true)
		if err != nil {
			return err
		}
		target, in := find(ms, u)
		switch {
		case !in:
			return gate.ErrNotFound
		case c.kind != kindGroup:
			return gate.NewProblem(gate.StatusConflict, "not_group")
		case u == v.ID:
			return gate.NewProblem(gate.StatusUnprocessable, "remove_self")
		case !me.admin:
			return gate.NewProblem(gate.StatusConflict, "not_admin")
		}
		return s.removeMember(ctx, tx, c, ms, target, v.ID, EventRemoved)
	})
	if err != nil {
		return err
	}
	s.announce(ctx, c, ms)
	return nil
}

// Rename sets a group's title ("" = untitled); any member may. Problems:
// not_group, title_too_long.
func (s *Service) Rename(ctx context.Context, ref gate.Ref, title string) (gate.ConversationView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.ConversationView{}, err
	}
	if title, err = cleanTitle(title); err != nil {
		return gate.ConversationView{}, err
	}
	var (
		c  conv
		ms []member
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		c, ms, _, err = s.access(ctx, tx, v.ID, ref, true, true)
		if err != nil {
			return err
		}
		if c.kind != kindGroup {
			return gate.NewProblem(gate.StatusConflict, "not_group")
		}
		if c.title == title {
			return nil
		}
		var t *string
		if title != "" {
			t = &title
		}
		if _, err := tx.Exec(ctx, `UPDATE dm_conversations SET title = $2 WHERE id = $1`, c.id, t); err != nil {
			return err
		}
		return s.insert(ctx, tx, c.id, msgRow{id: s.d.IDs.New(), sender: v.ID, kind: msgEvent, event: EventRenamed, at: s.now()})
	})
	if err != nil {
		return gate.ConversationView{}, err
	}
	s.announce(ctx, c, ms)
	return s.Get(ctx, ref)
}

// Mute turns pushes and the unread badge off (on) or back on for the viewer.
func (s *Service) Mute(ctx context.Context, ref gate.Ref, on bool) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	c, _, _, err := s.access(ctx, s.d.DB.Q(), v.ID, ref, true, false)
	if err != nil {
		return err
	}
	if _, err := s.d.DB.Q().Exec(ctx, `UPDATE dm_members SET muted = $3 WHERE conversation_id = $1 AND user_id = $2`,
		c.id, int64(v.ID), on); err != nil {
		return err
	}
	s.announceTo(ctx, c.id, v.ID)
	return nil
}
