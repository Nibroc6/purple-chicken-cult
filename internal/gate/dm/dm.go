// Package dm owns direct messages: 1:1 conversations, group chats and posts
// shared into them. It implements core.Messages for moderation.
//
// Who may message whom (the graph horizon, ARCHITECTURE §2.2):
//   - A direct conversation needs a direct edge (acquaintance or friend). An
//     unfriend or a block (which revokes the edge) keeps the history readable
//     but stops new messages both ways ("not_connected"), the same answer
//     for either cause, so a block is never revealed.
//   - A group chat is created by one member, who invites people they are
//     directly connected to; any member may invite their own connections, up
//     to MaxMembers (pending invitations count). An invitee sees only who
//     invited them, the title and the member count, until they join; members
//     see an invitee only once they joined. People on probation (§8) can
//     message their connections but cannot create groups or invite to them.
//   - A member's own blocks hide the blocked person's messages from them (in
//     groups, where no edge is needed); the blocked person sees no difference.
//
// A shared post is stored by ID and checked for every viewer at every read,
// against their own horizon: someone who could not already see the post (its
// audience, distance, a deletion) gets an "unavailable" placeholder and
// nothing else about it. Sharing needs the sender to see the post.
//
// Messages are private between members: no signed record, no moderator access
// except a message someone reported (the report keeps a copy of its text).
// Retention: a message lasts until its sender deletes it (the body is erased
// at once, the row reads "deleted"), a moderator removes it, or the sender's
// account is deleted (its messages are erased). Clearing a 1:1 conversation
// hides it for the viewer; dm.purge erases messages once every member has
// cleared past them, and a group without members is deleted. There are no
// read receipts: the read position is only used for the viewer's own unread
// counts.
//
// Delivery: after commit, every member's open streams get
// message{conversation, unread} (core.Notifier.Live); a push ("new message from
// @alice", never the text) goes out when a conversation turns unread for a
// member who did not mute it.
//
// Owner: direct messages (contract request dm-1).
package dm

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

// Sizes.
const (
	// MaxBody bounds a message (code points).
	MaxBody = 2000
	// MaxTitle bounds a group title (code points).
	MaxTitle = 80
	// MaxMembers bounds a group, invitations included.
	MaxMembers = 32
	// PageSize messages per page; ConvPageSize conversations per page.
	PageSize     = 50
	ConvPageSize = 30
	// ExcerptMax bounds the last-message excerpt in the list (code points).
	ExcerptMax = 80
	// UnreadCap bounds the unread conversation count (the badge shows 99+).
	UnreadCap = 100
)

// Conversation kinds (dm_conversations.kind).
const (
	kindDirect int16 = 0
	kindGroup  int16 = 1
)

// Member states (dm_members.state).
const (
	stateMember  int16 = 0
	stateInvited int16 = 1
)

// Message kinds (dm_messages.kind).
const (
	msgText  int16 = 0
	msgEvent int16 = 1
)

// Group events (dm_messages.event).
const (
	EventCreated = "created"
	EventJoined  = "joined"
	EventLeft    = "left"
	EventRemoved = "removed"
	EventRenamed = "renamed"
)

// Write kinds charged by gate/limits.
const (
	WriteMessage core.WriteKind = "message"
	WriteGroup   core.WriteKind = "message_group"
)

// Jobs.
const (
	// JobPurge erases messages every member cleared and deletes empty groups.
	JobPurge = "dm.purge"
	// PurgeEvery is the dm.purge period.
	PurgeEvery = time.Hour
)

// Live topic of a new message (notify.TopicMessage).
const topicMessage = "message"

// Service is the direct-messages module.
type Service struct {
	d *core.Deps
}

var _ core.Messages = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers dm.purge and the AccountDeleted subscription.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error { return s.Purge(ctx) })
		s.d.Jobs.Every(JobPurge, PurgeEvery)
	}
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.accountDeleted(e.ID) })
	}
	return nil
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// ---- rows ----

type conv struct {
	id     uuid.UUID
	kind   int16
	title  string
	lastAt time.Time
}

type member struct {
	user      core.UserID
	state     int16
	admin     bool
	invitedBy core.UserID
	joinedAt  time.Time
	readUpTo  uuid.UUID // uuid.Nil = none
	cleared   uuid.UUID // uuid.Nil = none
	muted     bool
	hidden    bool
}

// since is the newest message the member has read or cleared.
func (m member) since() uuid.UUID {
	if lessID(m.readUpTo, m.cleared) {
		return m.cleared
	}
	return m.readUpTo
}

// lessID orders UUIDv7 message IDs (byte order is time order).
func lessID(a, b uuid.UUID) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func nullID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func idOf(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

func (s *Service) loadConv(ctx context.Context, q store.Q, id uuid.UUID, lock bool) (conv, bool, error) {
	sql := `SELECT id, kind, coalesce(title, ''), last_message_at FROM dm_conversations WHERE id = $1`
	if lock {
		sql = `SELECT id, kind, coalesce(title, ''), last_message_at FROM dm_conversations WHERE id = $1 FOR UPDATE`
	}
	var c conv
	err := q.QueryRow(ctx, sql, id).Scan(&c.id, &c.kind, &c.title, &c.lastAt)
	if isNoRows(err) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *Service) loadMembers(ctx context.Context, q store.Q, id uuid.UUID) ([]member, error) {
	rows, err := q.Query(ctx, `SELECT user_id, state, admin, coalesce(invited_by, 0), joined_at, read_up_to, cleared_up_to,
			muted, hidden
		FROM dm_members WHERE conversation_id = $1 ORDER BY joined_at, user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []member
	for rows.Next() {
		var (
			m         member
			u, by     int64
			read, clr *uuid.UUID
		)
		if err := rows.Scan(&u, &m.state, &m.admin, &by, &m.joinedAt, &read, &clr, &m.muted, &m.hidden); err != nil {
			return nil, err
		}
		m.user, m.invitedBy, m.readUpTo, m.cleared = core.UserID(u), core.UserID(by), idOf(read), idOf(clr)
		out = append(out, m)
	}
	return out, rows.Err()
}

func find(ms []member, u core.UserID) (member, bool) {
	for _, m := range ms {
		if m.user == u {
			return m, true
		}
	}
	return member{}, false
}

// joined returns the members who joined (state member).
func joined(ms []member) []member {
	out := make([]member, 0, len(ms))
	for _, m := range ms {
		if m.state == stateMember {
			out = append(out, m)
		}
	}
	return out
}

// access opens a conversation ref for v: the conversation, its members and
// v's own membership. ErrNotFound unless v is a member or invitee; with
// needJoined, an invitee gets the Problem not_joined.
func (s *Service) access(ctx context.Context, q store.Q, v core.UserID, ref gate.Ref, needJoined, lock bool) (conv, []member, member, error) {
	id, ok := s.d.Refs.OpenConv(v, ref)
	if !ok {
		return conv{}, nil, member{}, gate.ErrNotFound
	}
	c, ok, err := s.loadConv(ctx, q, id, lock)
	if err != nil || !ok {
		return conv{}, nil, member{}, errOr(err, gate.ErrNotFound)
	}
	ms, err := s.loadMembers(ctx, q, id)
	if err != nil {
		return conv{}, nil, member{}, err
	}
	me, ok := find(ms, v)
	if !ok {
		return conv{}, nil, member{}, gate.ErrNotFound
	}
	if needJoined && me.state != stateMember {
		return conv{}, nil, member{}, gate.NewProblem(gate.StatusConflict, "not_joined")
	}
	return c, ms, me, nil
}

func errOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// connected reports whether a and b share a direct edge (acquaintance or friend).
func (s *Service) connected(a, b core.UserID) bool {
	_, ok := s.d.Graph.Level(a, b)
	return ok
}

// relation is v's own edge with u as an API name.
func (s *Service) relation(v, u core.UserID) string {
	lvl, ok := s.d.Graph.Level(v, u)
	switch {
	case !ok:
		return ""
	case lvl == core.LevelFriendPlus:
		return "friend_plus"
	}
	return "friend"
}

// blocked is the set of people v has blocked (their messages are hidden from v).
func (s *Service) blocked(v core.UserID) []int64 {
	ex := s.d.Graph.Exclusions(v)
	if ex.Blocked == nil || ex.Blocked.IsEmpty() {
		return []int64{}
	}
	out := make([]int64, 0, ex.Blocked.GetCardinality())
	it := ex.Blocked.Iterator()
	for it.HasNext() {
		out = append(out, int64(it.Next()))
	}
	return out
}
