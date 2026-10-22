package dm

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/urlx"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// people caches user rows for one view.
type people struct {
	s    *Service
	ctx  context.Context
	v    core.UserID
	meta map[core.UserID]core.UserMeta
}

func (s *Service) people(ctx context.Context, v core.UserID) *people {
	return &people{s: s, ctx: ctx, v: v, meta: map[core.UserID]core.UserMeta{}}
}

func (p *people) load(ids []core.UserID) {
	var want []core.UserID
	for _, id := range ids {
		if _, ok := p.meta[id]; !ok && id != 0 {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return
	}
	got, err := p.s.d.Users.GetMany(p.ctx, want)
	if err != nil {
		return
	}
	for id, m := range got {
		p.meta[id] = m
	}
}

// member is u as v sees them in a conversation; nil for a deleted or
// suspended account.
func (p *people) member(u core.UserID, admin bool) *gate.ConvMember {
	p.load([]core.UserID{u})
	m, ok := p.meta[u]
	if !ok || m.Status != core.StatusActive {
		return nil
	}
	return &gate.ConvMember{Ref: p.s.d.Refs.User(p.v, u), Handle: m.Handle, DisplayName: m.DisplayName, Admin: admin,
		Relation: p.s.relation(p.v, u)}
}

// unread counts the messages of others in c after v's read position (not
// deleted, not from someone v blocked).
func (s *Service) unread(ctx context.Context, q store.Q, c uuid.UUID, v member, blocked []int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM dm_messages
		WHERE conversation_id = $1 AND kind = 0 AND deleted_at IS NULL AND sender_id <> $2
			AND ($3::uuid IS NULL OR id > $3) AND NOT (sender_id = ANY($4)) LIMIT 100) x`,
		c, int64(v.user), nullID(v.since()), blocked).Scan(&n)
	return n, err
}

// Unread returns how many conversations have unread messages for the viewer
// (muted ones excepted), plus pending group invitations, capped at UnreadCap.
func (s *Service) Unread(ctx context.Context) (int, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return 0, err
	}
	return s.unreadTotal(ctx, v.ID)
}

func (s *Service) unreadTotal(ctx context.Context, v core.UserID) (int, error) {
	var n int
	err := s.d.DB.Q().QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM dm_members mb
		WHERE mb.user_id = $1 AND (mb.state = 1 OR (NOT mb.muted AND EXISTS (SELECT 1 FROM dm_messages m
			WHERE m.conversation_id = mb.conversation_id AND m.kind = 0 AND m.deleted_at IS NULL AND m.sender_id <> $1
				AND NOT (m.sender_id = ANY($2))
				AND m.id > GREATEST(coalesce(mb.read_up_to, '00000000-0000-0000-0000-000000000000'),
					coalesce(mb.cleared_up_to, '00000000-0000-0000-0000-000000000000')))))
		LIMIT $3) x`, int64(v), s.blocked(v), UnreadCap).Scan(&n)
	return n, err
}

// lastMessage is the newest message of c that v can see.
func (s *Service) lastMessage(ctx context.Context, q store.Q, c uuid.UUID, me member, blocked []int64) (*msgRow, error) {
	rows, err := s.messageRows(ctx, q, c, me, uuid.Nil, 1, blocked)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func (s *Service) preview(pp *people, v core.UserID, r *msgRow) *gate.MessagePreview {
	if r == nil {
		return nil
	}
	p := &gate.MessagePreview{At: r.at, FromMe: r.sender == v}
	if !p.FromMe {
		if m := pp.member(r.sender, false); m != nil {
			p.From = m.Handle
		}
	}
	switch {
	case r.kind == msgEvent:
		p.Kind, p.Event = "event", r.event
		// An event's preview names who it is about (who joined, left or was removed).
		if r.subject != 0 {
			p.FromMe, p.From = r.subject == v, ""
			if m := pp.member(r.subject, false); m != nil && !p.FromMe {
				p.From = m.Handle
			}
		}
	case r.deleted:
		p.Kind = "deleted"
	case r.body == "" && r.post != uuid.Nil:
		p.Kind = "post"
	default:
		p.Kind, p.Excerpt = "text", urlx.CleanText(r.body, ExcerptMax)
		if r.post != uuid.Nil {
			p.Kind = "post"
		}
	}
	return p
}

// convView builds c as v sees it.
func (s *Service) convView(ctx context.Context, q store.Q, pp *people, c conv, ms []member, me member) (gate.ConversationView, error) {
	v := me.user
	view := gate.ConversationView{
		Ref: s.d.Refs.Conv(v, c.id), Kind: "direct", State: "member", Title: c.title, Members: []gate.ConvMember{},
		Admin: me.admin, Muted: me.muted, UpdatedAt: c.lastAt,
	}
	if c.kind == kindGroup {
		view.Kind = "group"
	}
	in := joined(ms)
	ids := make([]core.UserID, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.user, m.invitedBy)
	}
	pp.load(ids)
	view.MemberCount = len(in)
	if me.state == stateInvited {
		view.State = "invited"
		view.InvitedBy = pp.member(me.invitedBy, false)
		view.Unread = 1
		return view, nil
	}
	for _, m := range in {
		if m.user == v {
			continue
		}
		if mv := pp.member(m.user, m.admin && c.kind == kindGroup); mv != nil {
			view.Members = append(view.Members, *mv)
		}
	}
	if c.kind == kindDirect {
		other := s.other(ms, v)
		view.CanSend = other != 0 && s.connected(v, other)
	} else {
		view.CanSend = true
	}
	blocked := s.blocked(v)
	n, err := s.unread(ctx, q, c.id, me, blocked)
	if err != nil {
		return view, err
	}
	view.Unread = n
	last, err := s.lastMessage(ctx, q, c.id, me, blocked)
	if err != nil {
		return view, err
	}
	view.Last = s.preview(pp, v, last)
	return view, nil
}

// other is the other member of a direct conversation (0 once their account is gone).
func (s *Service) other(ms []member, v core.UserID) core.UserID {
	for _, m := range ms {
		if m.user != v {
			return m.user
		}
	}
	return 0
}

// Get returns one conversation as the viewer sees it.
func (s *Service) Get(ctx context.Context, ref gate.Ref) (gate.ConversationView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.ConversationView{}, err
	}
	q := s.d.DB.Q()
	c, ms, me, err := s.access(ctx, q, v.ID, ref, false, false)
	if err != nil {
		return gate.ConversationView{}, err
	}
	return s.convView(ctx, q, s.people(ctx, v.ID), c, ms, me)
}

// List returns the viewer's conversations and invitations, most recently
// active first (a cleared 1:1 conversation is left out until a new message).
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.ConversationView], error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.Page[gate.ConversationView]{}, err
	}
	page := gate.Page[gate.ConversationView]{Items: []gate.ConversationView{}}
	var (
		at    time.Time
		after uuid.UUID
	)
	if cursor != "" {
		micros, ref, ok := strings.Cut(cursor, ".")
		us, err := strconv.ParseInt(micros, 10, 64)
		id, ok2 := s.d.Refs.OpenConv(v.ID, gate.Ref(ref))
		if !ok || err != nil || !ok2 {
			return page, gate.ErrNotFound
		}
		at, after = time.UnixMicro(us).UTC(), id
	}
	q := s.d.DB.Q()
	rows, err := q.Query(ctx, `SELECT c.id FROM dm_members mb JOIN dm_conversations c ON c.id = mb.conversation_id
		WHERE mb.user_id = $1 AND NOT mb.hidden AND ($2::uuid IS NULL OR (c.last_message_at, c.id) < ($3, $2))
		ORDER BY c.last_message_at DESC, c.id DESC LIMIT $4`, int64(v.ID), nullID(after), at, ConvPageSize+1)
	if err != nil {
		return page, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return page, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return page, err
	}
	more := len(ids) > ConvPageSize
	if more {
		ids = ids[:ConvPageSize]
	}
	pp := s.people(ctx, v.ID)
	for _, id := range ids {
		c, ok, err := s.loadConv(ctx, q, id, false)
		if err != nil {
			return page, err
		}
		if !ok {
			continue
		}
		ms, err := s.loadMembers(ctx, q, id)
		if err != nil {
			return page, err
		}
		me, ok := find(ms, v.ID)
		if !ok {
			continue
		}
		view, err := s.convView(ctx, q, pp, c, ms, me)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, view)
		if more {
			page.Next = strconv.FormatInt(c.lastAt.UnixMicro(), 10) + "." + string(view.Ref)
		}
	}
	return page, nil
}
