package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// row is one stored notification.
type row struct {
	id       int64
	kind     string
	subjUser core.UserID
	subjPost core.PostID
	data     map[string]any
	count    int
	at       time.Time
	read     bool
}

// pos is a list position: (updated_at, id) of the last row scanned.
type pos struct {
	at time.Time
	id int64
}

// List returns one page of the viewer's notifications, newest first. Items whose
// required subject the viewer cannot see right now are skipped (plan §4.7).
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.NotificationView], error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.Page[gate.NotificationView]{}, err
	}
	var after *pos
	if cursor != "" {
		p, ok := s.openCursor(v.ID, cursor)
		if !ok {
			return gate.Page[gate.NotificationView]{}, gate.ErrNotFound
		}
		after = &p
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return gate.Page[gate.NotificationView]{}, err
	}
	out := gate.Page[gate.NotificationView]{Items: []gate.NotificationView{}}
	scanned := 0
	for scanned < maxScan {
		rows, err := s.rows(ctx, v.ID, after, false, scanBatch)
		if err != nil {
			return out, err
		}
		for i, r := range rows {
			scanned++
			after = &pos{at: r.at, id: r.id}
			if nv, ok := s.view(ctx, v.ID, sr, r); ok {
				out.Items = append(out.Items, nv)
			}
			if len(out.Items) == PageSize || scanned == maxScan {
				if i < len(rows)-1 || len(rows) == scanBatch {
					out.Next = s.cursorString(v.ID, *after)
				}
				return out, nil
			}
		}
		if len(rows) < scanBatch {
			return out, nil
		}
	}
	return out, nil
}

// UnreadCount is the number of unread items the viewer would see (at most UnreadCap).
func (s *Service) UnreadCount(ctx context.Context) (int, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return 0, err
	}
	return s.unread(ctx, v.ID)
}

func (s *Service) unread(ctx context.Context, u core.UserID) (int, error) {
	rows, err := s.rows(ctx, u, nil, true, UnreadCap)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, u)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if _, ok := s.view(ctx, u, sr, r); ok {
			n++
		}
	}
	return n, nil
}

// MarkRead marks every notification of the viewer up to (and including) upTo
// read: the rows last updated no later than it. A batched row that grew after
// the viewer saw upTo stays unread. Every open stream of the viewer gets the
// new unread count.
func (s *Service) MarkRead(ctx context.Context, upTo gate.Ref) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	id, ok := s.d.Refs.OpenNotif(v.ID, upTo)
	if !ok {
		return gate.ErrNotFound
	}
	var at time.Time
	if err := s.d.DB.Q().QueryRow(ctx, `SELECT updated_at FROM notifications WHERE id = $1 AND user_id = $2`,
		id, int64(v.ID)).Scan(&at); err != nil {
		if isNoRows(err) {
			return gate.ErrNotFound
		}
		return err
	}
	if _, err := s.d.DB.Q().Exec(ctx, `UPDATE notifications SET read_at = $4
		WHERE user_id = $1 AND read_at IS NULL AND (updated_at, id) <= ($2, $3)`, int64(v.ID), at, id, s.now()); err != nil {
		return err
	}
	s.hub.pushUnread(ctx, v.ID)
	return nil
}

// rows reads up to limit rows of u after p, newest first.
func (s *Service) rows(ctx context.Context, u core.UserID, p *pos, unreadOnly bool, limit int) ([]row, error) {
	at, id := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), int64(1<<62)
	if p != nil {
		at, id = p.at, p.id
	}
	rs, err := s.d.DB.Q().Query(ctx, `SELECT id, kind, coalesce(subject_user, 0), subject_post, data, count, updated_at,
			read_at IS NOT NULL
		FROM notifications
		WHERE user_id = $1 AND (updated_at, id) < ($2, $3) AND (NOT $4 OR read_at IS NULL)
		ORDER BY updated_at DESC, id DESC LIMIT $5`, int64(u), at, id, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var (
			r    row
			su   int64
			sp   *uuid.UUID
			data []byte
		)
		if err := rs.Scan(&r.id, &r.kind, &su, &sp, &data, &r.count, &r.at, &r.read); err != nil {
			return nil, err
		}
		r.subjUser = core.UserID(su)
		if sp != nil {
			r.subjPost = *sp
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &r.data); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// view renders r for viewer v (search sr), or false when it must not be shown:
// unknown kind, or a required subject v cannot see right now.
func (s *Service) view(ctx context.Context, v core.UserID, sr core.Search, r row) (gate.NotificationView, bool) {
	rule, ok := rules[r.kind]
	if !ok {
		return gate.NotificationView{}, false
	}
	nv := gate.NotificationView{ID: s.d.Refs.Notif(v, r.id), Kind: r.kind, Count: r.count, At: r.at, Read: r.read}
	if len(r.data) > 0 {
		nv.Data = r.data
	}
	if rule.person != none && r.subjUser != 0 {
		nv.Person = s.person(ctx, v, sr, r.subjUser)
	}
	if rule.person == required && nv.Person == nil {
		return gate.NotificationView{}, false
	}
	if rule.post != none && r.subjPost != uuid.Nil {
		if m, ok := s.d.Posts.Meta(ctx, r.subjPost); ok && !m.Deleted {
			if _, ok := s.d.Vis.CheckPost(ctx, sr, m); ok {
				ref := s.d.Refs.Post(v, r.subjPost)
				nv.Post = &ref
			}
		}
	}
	if rule.post == required && nv.Post == nil {
		return gate.NotificationView{}, false
	}
	return nv, true
}

// person is u as a chain hop for v, or nil unless v can see u right now.
func (s *Service) person(ctx context.Context, v core.UserID, sr core.Search, u core.UserID) *gate.ChainHop {
	if !s.d.Vis.CanSeeUser(sr, u) {
		return nil
	}
	m, err := s.d.Users.Get(ctx, u)
	if err != nil || m.Status != 0 {
		return nil
	}
	return &gate.ChainHop{Ref: s.d.Refs.User(v, u), Handle: m.Handle}
}

// ---- cursors: base64url(updated_at µs ‖ id ‖ HMAC16) bound to the viewer ----

const macLen = 16

func (s *Service) mac(v core.UserID, payload []byte) []byte {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	m.Write([]byte("pcc/notify/v1\x00cursor\x00"))
	m.Write(b[:])
	m.Write(payload)
	return m.Sum(nil)[:macLen]
}

func (s *Service) cursorString(v core.UserID, p pos) string {
	payload := make([]byte, 0, 16)
	payload = binary.BigEndian.AppendUint64(payload, uint64(p.at.UnixMicro()))
	payload = binary.BigEndian.AppendUint64(payload, uint64(p.id))
	return base64.RawURLEncoding.EncodeToString(append(payload, s.mac(v, payload)...))
}

func (s *Service) openCursor(v core.UserID, str string) (pos, bool) {
	if len(str) != base64.RawURLEncoding.EncodedLen(16+macLen) {
		return pos{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(str)
	if err != nil || len(raw) != 16+macLen || !hmac.Equal(raw[16:], s.mac(v, raw[:16])) {
		return pos{}, false
	}
	return pos{at: time.UnixMicro(int64(binary.BigEndian.Uint64(raw[:8]))).UTC(),
		id: int64(binary.BigEndian.Uint64(raw[8:16]))}, true
}
