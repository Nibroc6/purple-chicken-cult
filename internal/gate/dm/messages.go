package dm

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

type msgRow struct {
	id      uuid.UUID
	sender  core.UserID
	kind    int16
	body    string
	post    uuid.UUID
	event   string
	subject core.UserID
	at      time.Time
	deleted bool
}

// messageRows returns up to n messages of c that me can see, newest first,
// older than before (uuid.Nil: from the newest): not cleared by me, not from
// someone I blocked.
func (s *Service) messageRows(ctx context.Context, q store.Q, c uuid.UUID, me member, before uuid.UUID, n int,
	blocked []int64) ([]msgRow, error) {
	rows, err := q.Query(ctx, `SELECT id, sender_id, kind, coalesce(body, ''), shared_post, coalesce(event, ''),
			coalesce(subject_user, 0), created_at, deleted_at IS NOT NULL
		FROM dm_messages
		WHERE conversation_id = $1 AND ($2::uuid IS NULL OR id > $2) AND ($3::uuid IS NULL OR id < $3)
			AND NOT (sender_id = ANY($4))
		ORDER BY id DESC LIMIT $5`, c, nullID(me.cleared), nullID(before), blocked, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []msgRow
	for rows.Next() {
		var (
			r       msgRow
			snd, sj int64
			post    *uuid.UUID
		)
		if err := rows.Scan(&r.id, &snd, &r.kind, &r.body, &post, &r.event, &sj, &r.at, &r.deleted); err != nil {
			return nil, err
		}
		r.sender, r.subject, r.post = core.UserID(snd), core.UserID(sj), idOf(post)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Messages returns a page of the conversation's messages, newest first; the
// cursor is the ref of the oldest message of the previous page. Shared posts
// are rendered from the viewer's own horizon (unavailable when they cannot
// see them).
func (s *Service) Messages(ctx context.Context, ref gate.Ref, cursor string) (gate.MessagesPage, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.MessagesPage{}, err
	}
	q := s.d.DB.Q()
	c, ms, me, err := s.access(ctx, q, v.ID, ref, true, false)
	if err != nil {
		return gate.MessagesPage{}, err
	}
	before := uuid.Nil
	if cursor != "" {
		id, ok := s.d.Refs.OpenMessage(v.ID, gate.Ref(cursor))
		if !ok {
			return gate.MessagesPage{}, gate.ErrNotFound
		}
		before = id
	}
	rows, err := s.messageRows(ctx, q, c.id, me, before, PageSize+1, s.blocked(v.ID))
	if err != nil {
		return gate.MessagesPage{}, err
	}
	page := gate.MessagesPage{Items: []gate.MessageView{}}
	if len(rows) > PageSize {
		rows = rows[:PageSize]
		page.Next = string(s.d.Refs.Message(v.ID, rows[len(rows)-1].id))
	}
	views, err := s.messageViews(ctx, v.ID, c, ms, rows)
	if err != nil {
		return gate.MessagesPage{}, err
	}
	page.Items = views
	return page, nil
}

// messageViews renders rows for v.
func (s *Service) messageViews(ctx context.Context, v core.UserID, c conv, ms []member, rows []msgRow) ([]gate.MessageView, error) {
	pp := s.people(ctx, v)
	ids := make([]core.UserID, 0, 2*len(rows))
	var posts []core.PostID
	for _, r := range rows {
		ids = append(ids, r.sender, r.subject)
		if r.post != uuid.Nil && !r.deleted {
			posts = append(posts, r.post)
		}
	}
	pp.load(ids)
	shared, err := s.sharedPosts(ctx, v, posts)
	if err != nil {
		return nil, err
	}
	admins := map[core.UserID]bool{}
	for _, m := range ms {
		if m.admin && c.kind == kindGroup {
			admins[m.user] = true
		}
	}
	out := make([]gate.MessageView, 0, len(rows))
	for _, r := range rows {
		mv := gate.MessageView{Ref: s.d.Refs.Message(v, r.id), Kind: "text", Mine: r.sender == v, At: r.at}
		if !mv.Mine {
			mv.From = pp.member(r.sender, admins[r.sender])
		}
		switch {
		case r.kind == msgEvent:
			mv.Kind, mv.Event = "event", r.event
			if r.subject != 0 && r.subject != v {
				mv.Subject = pp.member(r.subject, false)
			}
		case r.deleted:
			mv.Kind = "deleted"
		default:
			mv.Body = r.body
			if r.post != uuid.Nil {
				if pv, ok := shared[r.post]; ok {
					mv.Post = &gate.SharedPostView{Post: &pv}
				} else {
					mv.Post = &gate.SharedPostView{Unavailable: true}
				}
			}
		}
		out = append(out, mv)
	}
	return out, nil
}

// sharedPosts renders the posts v can see right now; the others are left out
// (they render as unavailable). The reader's search runs only when needed.
func (s *Service) sharedPosts(ctx context.Context, v core.UserID, ids []core.PostID) (map[core.PostID]gate.PostView, error) {
	out := map[core.PostID]gate.PostView{}
	if len(ids) == 0 {
		return out, nil
	}
	metas := s.d.Posts.MetaMany(ctx, ids)
	var live []core.PostMeta
	for _, id := range ids {
		if m, ok := metas[id]; ok && !m.Deleted {
			live = append(live, m)
		}
	}
	if len(live) == 0 {
		return out, nil
	}
	sr, err := s.d.Vis.SearchFor(ctx, v)
	if err != nil {
		return nil, err
	}
	checked := s.d.Vis.CheckPosts(ctx, sr, live)
	if len(checked) == 0 {
		return out, nil
	}
	views, err := s.d.Views.Posts(ctx, sr, checked, core.ViewOpts{WithBody: true})
	if err != nil {
		return nil, err
	}
	byRef := make(map[gate.Ref]core.PostID, len(checked))
	for _, c := range checked {
		byRef[s.d.Refs.Post(v, c.Meta().ID)] = c.Meta().ID
	}
	for _, pv := range views {
		if id, ok := byRef[pv.Ref]; ok {
			out[id] = pv
		}
	}
	return out, nil
}

// cleanBody normalizes a message (NFC, trailing space and leading blank lines
// dropped); Problems body_empty (unless empty is allowed), body_too_long.
func cleanBody(body string, allowEmpty bool) (string, error) {
	if strings.IndexByte(body, 0) >= 0 || !utf8.ValidString(body) {
		return "", gate.NewProblem(gate.StatusBadRequest, "bad_request", "body")
	}
	b := strings.TrimRightFunc(norm.NFC.String(body), unicode.IsSpace)
	b = strings.TrimLeft(b, "\r\n")
	if strings.TrimSpace(b) == "" {
		if allowEmpty {
			return "", nil
		}
		return "", gate.NewProblem(gate.StatusUnprocessable, "body_empty", "body")
	}
	if utf8.RuneCountInString(b) > MaxBody {
		return "", gate.NewProblem(gate.StatusUnprocessable, "body_too_long", "body")
	}
	return b, nil
}

// Draft is one message to send: a body, a shared post, or both.
type Draft struct {
	Body string
	Post gate.Ref // optional
}

// Send posts a message into a conversation the viewer joined. A shared post
// must be one the viewer can see right now. Problems: not_joined,
// not_connected (a 1:1 conversation whose other person is no longer a direct
// connection), body_empty, body_too_long.
func (s *Service) Send(ctx context.Context, ref gate.Ref, d Draft) (gate.MessageView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.MessageView{}, err
	}
	var post core.PostID
	if d.Post != "" {
		if post, err = s.visiblePost(ctx, v.ID, d.Post); err != nil {
			return gate.MessageView{}, err
		}
	}
	if _, _, _, err := s.access(ctx, s.d.DB.Q(), v.ID, ref, true, false); err != nil {
		return gate.MessageView{}, err
	}
	body, err := cleanBody(d.Body, post != uuid.Nil)
	if err != nil {
		return gate.MessageView{}, err
	}
	var (
		row msgRow
		c   conv
		ms  []member
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		c, ms, _, err = s.access(ctx, tx, v.ID, ref, true, true)
		if err != nil {
			return err
		}
		if c.kind == kindDirect {
			if o := s.other(ms, v.ID); o == 0 || !s.connected(v.ID, o) {
				return gate.NewProblem(gate.StatusConflict, "not_connected")
			}
		}
		row = msgRow{id: s.d.IDs.New(), sender: v.ID, kind: msgText, body: body, post: post, at: s.now()}
		if err := s.insert(ctx, tx, c.id, row); err != nil {
			return err
		}
		return s.pushNew(ctx, tx, c, ms, row)
	})
	if err != nil {
		return gate.MessageView{}, err
	}
	s.announce(ctx, c, ms)
	views, err := s.messageViews(ctx, v.ID, c, ms, []msgRow{row})
	if err != nil || len(views) == 0 {
		return gate.MessageView{}, errOr(err, gate.ErrNotFound)
	}
	return views[0], nil
}

// visiblePost opens a post ref the viewer can see right now.
func (s *Service) visiblePost(ctx context.Context, v core.UserID, ref gate.Ref) (core.PostID, error) {
	id, ok := s.d.Refs.OpenPost(v, ref)
	if !ok {
		return uuid.Nil, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok || m.Deleted {
		return uuid.Nil, gate.ErrNotFound
	}
	sr, err := s.d.Vis.SearchFor(ctx, v)
	if err != nil {
		return uuid.Nil, err
	}
	if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
		return uuid.Nil, gate.ErrNotFound
	}
	return id, nil
}

// insert writes a message, moves the conversation up the members' lists
// (a cleared 1:1 conversation comes back) and marks it read for its sender.
func (s *Service) insert(ctx context.Context, tx store.Q, c uuid.UUID, r msgRow) error {
	var (
		body, event *string
		subject     *int64
	)
	if r.kind == msgText && r.body != "" {
		body = &r.body
	}
	if r.kind == msgEvent {
		event = &r.event
		if r.subject != 0 {
			sj := int64(r.subject)
			subject = &sj
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dm_messages (id, conversation_id, sender_id, kind, body, shared_post, event,
			subject_user, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		r.id, c, int64(r.sender), r.kind, body, nullID(r.post), event, subject, r.at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE dm_conversations SET last_message_at = GREATEST(last_message_at, $2) WHERE id = $1`,
		c, r.at); err != nil {
		return err
	}
	if r.kind == msgText {
		if _, err := tx.Exec(ctx, `UPDATE dm_members SET hidden = false WHERE conversation_id = $1 AND hidden`, c); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE dm_members SET read_up_to = $3 WHERE conversation_id = $1 AND user_id = $2`,
		c, int64(r.sender), r.id)
	return err
}

// pushNew queues a push for every member the new message turns the
// conversation unread for (not muted, not blocking the sender).
func (s *Service) pushNew(ctx context.Context, tx store.Q, c conv, ms []member, r msgRow) error {
	if s.d.MessagePush == nil {
		return nil
	}
	sender, err := s.d.Users.Get(ctx, r.sender)
	if err != nil {
		return err
	}
	key := "message"
	switch {
	case r.post != uuid.Nil && c.kind == kindDirect:
		key = "message_post"
	case c.kind == kindGroup:
		key = "message_group"
	}
	for _, m := range joined(ms) {
		if m.user == r.sender || m.muted || s.d.Graph.Blocks(m.user, r.sender) {
			continue
		}
		n, err := s.unread(ctx, tx, c.id, m, s.blocked(m.user))
		if err != nil {
			return err
		}
		if n != 1 {
			continue // already unread: one push per unread stretch
		}
		if err := s.push(ctx, tx, m.user, c.id, key, sender.Handle); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) push(ctx context.Context, tx store.Q, to core.UserID, c uuid.UUID, key, handle string) error {
	if s.d.MessagePush == nil {
		return nil
	}
	ref := string(s.d.Refs.Conv(to, c))
	return s.d.MessagePush.PushText(ctx, tx, to, core.PushText{Key: key, Args: map[string]any{"name": "@" + handle},
		Tag: "dm:" + ref, URL: "/messages/" + ref})
}

// announce sends message{conversation, unread} to the open streams of every
// member and invitee (after commit).
func (s *Service) announce(ctx context.Context, c conv, ms []member) {
	if s.d.Notify == nil {
		return
	}
	for _, m := range ms {
		s.announceTo(ctx, c.id, m.user)
	}
}

func (s *Service) announceTo(ctx context.Context, c uuid.UUID, u core.UserID) {
	if s.d.Notify == nil || u == 0 {
		return
	}
	n, err := s.unreadTotal(ctx, u)
	if err != nil {
		return
	}
	s.d.Notify.Live(u, topicMessage, map[string]any{"conversation": string(s.d.Refs.Conv(u, c)), "unread": n})
}

// Delete erases one of the viewer's own messages (the row reads "deleted").
func (s *Service) Delete(ctx context.Context, ref, msg gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	id, ok := s.d.Refs.OpenMessage(v.ID, msg)
	if !ok {
		return gate.ErrNotFound
	}
	c, ms, _, err := s.access(ctx, s.d.DB.Q(), v.ID, ref, true, false)
	if err != nil {
		return err
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE dm_messages SET body = NULL, shared_post = NULL, deleted_at = $4
		WHERE id = $1 AND conversation_id = $2 AND sender_id = $3 AND kind = 0`, id, c.id, int64(v.ID), s.now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return gate.ErrNotFound
	}
	s.announce(ctx, c, ms)
	return nil
}

// MarkRead moves the viewer's read position up to message upTo (never back).
func (s *Service) MarkRead(ctx context.Context, ref, upTo gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	id, ok := s.d.Refs.OpenMessage(v.ID, upTo)
	if !ok {
		return gate.ErrNotFound
	}
	c, _, _, err := s.access(ctx, s.d.DB.Q(), v.ID, ref, true, false)
	if err != nil {
		return err
	}
	if _, err := s.d.DB.Q().Exec(ctx, `UPDATE dm_members SET read_up_to = $3
		WHERE conversation_id = $1 AND user_id = $2 AND (read_up_to IS NULL OR read_up_to < $3)
			AND EXISTS (SELECT 1 FROM dm_messages WHERE id = $3 AND conversation_id = $1)`, c.id, int64(v.ID), id); err != nil {
		return err
	}
	s.announceTo(ctx, c.id, v.ID) // the viewer's other tabs
	return nil
}
