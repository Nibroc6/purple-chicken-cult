// Package notify owns in-app notifications and the SSE hub (ARCHITECTURE §10.6);
// P1 adds Web Push (push.go). It implements core.Notifier (v2: push fan-out).
//
// Owner: P0-W10 → P1-W05.
//
// Notify persists one row per event, inside the caller's transaction. Events
// with a GroupKey (post_referenced:<parent>) upsert the recipient's one unread
// row of that group (count+1). Events are DROPPED when the recipient has blocked
// the subject user (D7), when the subject is the recipient, and for
// post_referenced when the recipient muted that post.
//
// Horizon (§2.2): rows keep raw IDs; every read re-checks each subject for the
// recipient right now and encodes it as a ref minted for them. A subject the
// recipient can no longer see is left out of the view (the row is kept); a kind
// whose subject is required (who mentioned you, which post was referenced)
// disappears from the list and from the unread count until it is visible again.
// So a notification never says anything about a person or post outside the
// recipient's current horizon.
//
// Delivery: every open /api/events stream of the recipient gets
// notification{unread} after the notifying transaction commits (Notify has no
// commit hook, so the hub waits until the row is visible with its new count from
// another connection; a rolled-back row is never announced). A batched group
// pings at most once per hour: the first event of a group pings at once, later
// ones are left to notify.flush, which pings groups whose last ping is an hour
// old. Streams close when their session ends (SessionEnded) or the account is
// deleted.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/i18n"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Notification kinds (notifications.kind; the closed list of §10.6).
const (
	KindFriendRequest    = "friend_request"
	KindRequestAccepted  = "request_accepted"
	KindPostReferenced   = "post_referenced"
	KindMention          = "mention"
	KindInviteeAction    = "invitee_action"
	KindFriendSecurity   = "friend_security"
	KindInviteRedeemed   = "invite_redeemed"
	KindRecoveryApproval = "recovery_approval" // P1
	KindAccountRecovery  = "account_recovery"  // a recovery of the recipient's own account
	KindFriendUpgrade    = "friend_upgrade"    // an acquaintance shows their code to become friends
)

// Live topics (ephemeral SSE events).
const (
	TopicHandshake = "handshake"
	TopicStepUp    = "stepup" // P1
	// TopicFriendUpgrade offers the scanner to the other side of a friend+
	// upgrade the moment it opens (an open app shows a toast).
	TopicFriendUpgrade = "friend_upgrade"
)

// SSE event types besides the live topics.
const (
	EventHello        = "hello"
	EventNotification = "notification"
)

// Jobs.
const (
	// JobFlush pings the batched groups whose last ping is an hour old.
	JobFlush = "notify.flush"
	// FlushEvery is the notify.flush period (each group still pings at most hourly).
	FlushEvery = 10 * time.Minute
	// BatchPingEvery is the minimum time between two pings of one batched group.
	BatchPingEvery = time.Hour
)

// Read limits.
const (
	// PageSize is the number of notifications per page.
	PageSize = 30
	// scanBatch rows are read per query while filtering; maxScan bounds one page.
	scanBatch = 100
	maxScan   = 300
	// UnreadCap bounds the unread count (the client shows "99+" from 100 on).
	UnreadCap = 100
)

// need says whether a kind's subject is required for the item to render.
type need uint8

const (
	none need = iota
	optional
	required
)

type kindRule struct {
	person, post need
}

// rules is the closed kind list with what each needs to be shown.
var rules = map[string]kindRule{
	KindFriendRequest:    {person: required},
	KindRequestAccepted:  {person: required},
	KindInviteRedeemed:   {person: required},
	KindMention:          {person: required, post: required},
	KindPostReferenced:   {person: optional, post: required},
	KindFriendSecurity:   {person: optional},
	KindInviteeAction:    {person: optional, post: optional},
	KindRecoveryApproval: {person: optional},
	KindAccountRecovery:  {},
	KindFriendUpgrade:    {person: required},
}

var codeRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Service is the notifications module.
type Service struct {
	d      *core.Deps
	hub    *hub
	sender webpush.Sender // nil: push disabled
	cat    *i18n.Catalog
}

var _ core.Notifier = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service {
	s := &Service{d: d}
	s.hub = newHub(s)
	return s
}

// Init registers notify.flush, push.deliver and the Bus subscriptions
// (SessionEnded closes that session's streams and deletes its push
// subscriptions; AccountDeleted closes the account's streams and deletes its
// notifications, mutes and push subscriptions; SessionStepUp sends stepup{}).
// Malformed VAPID keys refuse to start (D20).
func (s *Service) Init(context.Context) error {
	if err := s.initPush(); err != nil {
		return err
	}
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobFlush, func(ctx context.Context, _ json.RawMessage) error { return s.Flush(ctx) })
		s.d.Jobs.Every(JobFlush, FlushEvery)
	}
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.SessionEnded) { s.hub.closeSession(e.User, e.Session) })
		core.Subscribe(s.d.Bus, func(e core.SessionStepUp) {
			s.hub.sendSession(e.User, e.Session, StreamEvent{Type: TopicStepUp, Data: map[string]any{}})
		})
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.accountDeleted(e.ID) })
	}
	return nil
}

// Reset closes every stream and forgets pending pings (test hooks: the database
// was wiped, so user IDs are reused).
func (s *Service) Reset() { s.hub.reset() }

// StreamEvent is one SSE event: hello{unread}, notification{unread},
// handshake{state}, stepup{} (P1).
type StreamEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (s *Service) q(tx store.Q) store.Q {
	if tx == nil {
		return s.d.DB.Q()
	}
	return tx
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// ---- core.Notifier ----

// Notify persists e in tx (nil: the pool), batching on GroupKey. It drops the
// event when the recipient blocked the subject user (D7), when the recipient is
// the subject, and for post_referenced when the recipient muted the post. An
// unknown kind or non-code data is a caller bug (error).
func (s *Service) Notify(ctx context.Context, tx store.Q, e core.Event) error {
	if e.To == 0 {
		return nil
	}
	if _, ok := rules[e.Kind]; !ok {
		return errors.New("notify: unknown kind " + e.Kind)
	}
	data, err := cleanData(e.Data)
	if err != nil {
		return err
	}
	if e.SubjectUser == e.To {
		return nil
	}
	if e.SubjectUser != 0 && s.d.Graph != nil && s.d.Graph.Blocks(e.To, e.SubjectUser) {
		return nil
	}
	q := s.q(tx)
	if e.Kind == KindPostReferenced && e.SubjectPost != uuid.Nil {
		var muted bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_mutes WHERE user_id = $1 AND post_id = $2)`,
			int64(e.To), e.SubjectPost).Scan(&muted); err != nil {
			return err
		}
		if muted {
			return nil
		}
	}
	var (
		group *string
		subjU *int64
		subjP *uuid.UUID
	)
	if e.GroupKey != "" {
		g := e.GroupKey
		group = &g
	}
	if e.SubjectUser != 0 {
		u := int64(e.SubjectUser)
		subjU = &u
	}
	if e.SubjectPost != uuid.Nil {
		p := e.SubjectPost
		subjP = &p
	}
	var (
		id        int64
		count     int
		inserted  bool
		delivered *time.Time
	)
	now := s.now()
	if err := q.QueryRow(ctx, `INSERT INTO notifications (user_id, kind, group_key, subject_user, subject_post, data, count,
			created_at, updated_at, delivered_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7, $7)
		ON CONFLICT (user_id, group_key) WHERE read_at IS NULL AND group_key IS NOT NULL DO UPDATE SET
			count = notifications.count + 1, updated_at = EXCLUDED.updated_at, subject_user = EXCLUDED.subject_user,
			data = EXCLUDED.data
		RETURNING id, count, xmax = 0, delivered_at`,
		int64(e.To), e.Kind, group, subjU, subjP, data, now).Scan(&id, &count, &inserted, &delivered); err != nil {
		return err
	}
	// A new row pings now; a batched row only when its last ping is an hour old
	// (later events of the group are left to notify.flush).
	pinged := inserted || delivered == nil || !delivered.After(now.Add(-BatchPingEvery))
	if pinged && !inserted {
		if _, err := q.Exec(ctx, `UPDATE notifications SET delivered_at = $2 WHERE id = $1`, id, now); err != nil {
			return err
		}
	}
	if pinged {
		if err := s.queuePush(ctx, q, e.To, id); err != nil {
			return err
		}
		s.hub.afterCommit(e.To, id, count)
	}
	return nil
}

// Live sends an ephemeral event to every open stream of to (topics: handshake,
// stepup, friend_upgrade, message). Nothing is stored; a recipient without a stream misses it.
func (s *Service) Live(to core.UserID, topic string, data any) {
	if to == 0 || (topic != TopicHandshake && topic != TopicStepUp && topic != TopicFriendUpgrade && topic != TopicMessage) {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	s.hub.sendUser(to, StreamEvent{Type: topic, Data: data})
}

// cleanData keeps codes and numbers only (§15.4: no prose in notifications).
func cleanData(in map[string]any) ([]byte, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if !codeRE.MatchString(k) {
			return nil, errors.New("notify: data key is not a code")
		}
		switch x := v.(type) {
		case string:
			if !codeRE.MatchString(x) {
				return nil, errors.New("notify: data value is not a code")
			}
		case bool, int, int32, int64, uint8, uint16, uint32, float64:
		default:
			return nil, errors.New("notify: data value is not a code or a number")
		}
		out[k] = v
	}
	return json.Marshal(out)
}

// ---- handler-facing API ----

// MutePost turns post_referenced notifications for a post the viewer can see on
// (on=false) or off (on=true).
func (s *Service) MutePost(ctx context.Context, p gate.Ref, on bool) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	if _, err := s.visiblePost(ctx, v.ID, p); err != nil {
		return err
	}
	id, _ := s.d.Refs.OpenPost(v.ID, p)
	if on {
		_, err = s.d.DB.Q().Exec(ctx, `INSERT INTO notification_mutes (user_id, post_id) VALUES ($1, $2)
			ON CONFLICT (user_id, post_id) DO NOTHING`, int64(v.ID), id)
		return err
	}
	_, err = s.d.DB.Q().Exec(ctx, `DELETE FROM notification_mutes WHERE user_id = $1 AND post_id = $2`, int64(v.ID), id)
	return err
}

// visiblePost resolves a post ref the viewer can see right now.
func (s *Service) visiblePost(ctx context.Context, v core.UserID, p gate.Ref) (core.PostMeta, error) {
	id, ok := s.d.Refs.OpenPost(v, p)
	if !ok {
		return core.PostMeta{}, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok || m.Deleted {
		return core.PostMeta{}, gate.ErrNotFound
	}
	sr, err := s.d.Vis.SearchFor(ctx, v)
	if err != nil {
		return core.PostMeta{}, err
	}
	if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
		return core.PostMeta{}, gate.ErrNotFound
	}
	return m, nil
}

// accountDeleted closes the account's streams and deletes its own rows (the
// rows other people hold about it stay; reads drop the deleted subject).
func (s *Service) accountDeleted(u core.UserID) {
	s.hub.closeUser(u)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE user_id = $1`, int64(u)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1`, int64(u)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM notification_mutes WHERE user_id = $1`, int64(u))
		return err
	})
	if err != nil && s.d.Log != nil {
		s.d.Log.Warn("notify: rows of a deleted account not removed", "err", err)
	}
}
