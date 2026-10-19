package notify

// Web Push (P1, ARCHITECTURE §10.6; plan §4.7 notify): Notifier v2 fans every
// announced notification out to the recipient's push subscriptions.
//
// Flow: Notify (and notify.flush for a batched group's hourly ping) enqueues one
// push.deliver job in the notifying transaction when the recipient has a
// subscription, so a rolled-back notification is never pushed. The job reads
// the row again and renders it exactly as the list would for the recipient
// right now: a subject they can no longer see, a read row, a deleted or
// suspended recipient sends nothing. The text comes from the server catalogs
// (platform/i18n, ICU) in the recipient's ui_locale; a subscription with
// hide_content gets only the generic text. Payloads never carry post bodies,
// refs or IDs, and every record is padded to the same size (platform/webpush),
// so the push service sees an opaque, fixed-size blob per notification.
//
// Subscriptions belong to an account and a session: re-subscribing an endpoint
// reassigns it to the current account and session (the previous account stops
// receiving at once), SessionEnded (logout, expiry, revocation) deletes the
// session's subscriptions, AccountDeleted deletes the account's, and a push
// service's 404/410 deletes the subscription. Without VAPID keys push is
// disabled (D20): /push/key says {enabled:false} and subscribing is
// feature_disabled.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/i18n"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Push limits.
const (
	// JobDeliver renders one notification and sends it to the recipient's subscriptions.
	JobDeliver = "push.deliver"
	// PushTTL is how long a push service keeps an undelivered message.
	PushTTL = 24 * time.Hour
	// MaxSubscriptions bounds one account's subscriptions; the oldest goes first.
	MaxSubscriptions = 10
	// MaxFailures consecutive transient failures delete a subscription.
	MaxFailures = 5
	// deliverTries bounds the retries of the subscriptions that failed transiently.
	deliverTries = 3
	// sendTimeout bounds one message.
	sendTimeout = 15 * time.Second
	// NotificationsURL is where a click on a push notification leads.
	NotificationsURL = "/notifications"
	// genericTag collapses hidden-content notifications into one on the lock screen.
	genericTag = "activity"
)

// anonKinds have an optional person and a catalog variant without one.
var anonKinds = map[string]bool{KindFriendSecurity: true, KindRecoveryApproval: true}

// PushState is the caller's server-side state of one endpoint.
type PushState struct {
	Subscribed  bool `json:"subscribed"`
	HideContent bool `json:"hide_content"`
}

// deliverJob is the push.deliver payload: the notification row, and on a retry
// the subscriptions still to send to.
type deliverJob struct {
	N    int64    `json:"n"`
	Subs []string `json:"s,omitempty"`
	Try  int      `json:"t,omitempty"`
}

// initPush builds the sender and catalogs and registers push.deliver. Malformed
// VAPID keys refuse to start; missing ones disable push (ErrDisabled in prod,
// the log driver in dev/test, which is never used while PushEnabled is false).
func (s *Service) initPush() error {
	if s.d.Cfg == nil {
		return nil
	}
	cat, err := i18n.ForConfig(s.d.Cfg)
	if err != nil {
		return err
	}
	s.cat = cat
	snd, err := webpush.New(s.d.Cfg)
	switch {
	case errors.Is(err, webpush.ErrDisabled):
		snd = nil
	case err != nil:
		return err
	}
	if s.sender == nil {
		s.sender = snd
	}
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobDeliver, func(ctx context.Context, p json.RawMessage) error {
			var j deliverJob
			if err := json.Unmarshal(p, &j); err != nil {
				return nil // malformed payload: nothing to retry
			}
			return s.deliver(ctx, j)
		})
	}
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.SessionEnded) { s.sessionEnded(e.User, e.Session) })
	}
	return nil
}

// SetSender replaces the push sender (tests: the log driver or a sender bound to
// a local fake push service).
func (s *Service) SetSender(snd webpush.Sender) { s.sender = snd }

// pushOn reports whether push is enabled: VAPID configured (D20) and a sender.
func (s *Service) pushOn() bool {
	return s.sender != nil && s.d.Cfg != nil && s.d.Cfg.PushEnabled()
}

// PushKey returns the VAPID public key when push is enabled.
func (s *Service) PushKey(context.Context) (string, bool) {
	if !s.pushOn() {
		return "", false
	}
	return s.d.Cfg.Push.VAPIDPublic, true
}

// Subscribe stores the caller's push subscription for this session. An
// endpoint already stored (for anyone) is reassigned to the caller and this
// session. Problems: push_endpoint_invalid, push_keys_invalid; feature_disabled
// without VAPID.
func (s *Service) Subscribe(ctx context.Context, sub gate.PushSub) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	if !s.pushOn() {
		return gate.Disabled("push")
	}
	if webpush.CheckEndpoint(sub.Endpoint, webpush.AllowLoopback(s.d.Cfg)) != nil {
		return gate.NewProblem(gate.StatusUnprocessable, "push_endpoint_invalid", "endpoint")
	}
	if webpush.CheckKeys(sub.Keys.P256dh, sub.Keys.Auth) != nil {
		return gate.NewProblem(gate.StatusUnprocessable, "push_keys_invalid", "keys")
	}
	p256dh, _ := webpush.DecodeKey(sub.Keys.P256dh)
	auth, _ := webpush.DecodeKey(sub.Keys.Auth)
	now := s.now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO push_subscriptions (id, user_id, session_hash, endpoint, p256dh, auth,
				hide_content, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT (endpoint) DO UPDATE SET
				id = CASE WHEN push_subscriptions.user_id = EXCLUDED.user_id THEN push_subscriptions.id ELSE EXCLUDED.id END,
				user_id = EXCLUDED.user_id, session_hash = EXCLUDED.session_hash, p256dh = EXCLUDED.p256dh,
				auth = EXCLUDED.auth, hide_content = EXCLUDED.hide_content, updated_at = EXCLUDED.updated_at,
				failures = 0`,
			s.d.IDs.New(), int64(v.ID), v.Session[:], sub.Endpoint, p256dh, auth, sub.HideContent, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND id NOT IN (
				SELECT id FROM push_subscriptions WHERE user_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2)`,
			int64(v.ID), MaxSubscriptions)
		return err
	})
}

// Unsubscribe removes the caller's subscription of endpoint (nothing happens
// for an endpoint the caller does not hold). It works with push disabled too.
func (s *Service) Unsubscribe(ctx context.Context, endpoint string) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	if endpoint == "" || len(endpoint) > webpush.MaxEndpointLen {
		return gate.NewProblem(gate.StatusUnprocessable, "push_endpoint_invalid", "endpoint")
	}
	_, err = s.d.DB.Q().Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`, int64(v.ID), endpoint)
	return err
}

// SubscriptionState reports whether the caller holds endpoint and its
// hide_content flag. Someone else's endpoint looks exactly like an unknown one.
func (s *Service) SubscriptionState(ctx context.Context, endpoint string) (PushState, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return PushState{}, err
	}
	if endpoint == "" || len(endpoint) > webpush.MaxEndpointLen {
		return PushState{}, gate.NewProblem(gate.StatusUnprocessable, "push_endpoint_invalid", "endpoint")
	}
	var st PushState
	err = s.d.DB.Q().QueryRow(ctx, `SELECT hide_content FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`,
		int64(v.ID), endpoint).Scan(&st.HideContent)
	switch {
	case isNoRows(err):
		return PushState{}, nil
	case err != nil:
		return PushState{}, err
	}
	st.Subscribed = true
	return st, nil
}

// queuePush enqueues push.deliver for row id of u in q (the notifying
// transaction) when push is on and u has a subscription.
func (s *Service) queuePush(ctx context.Context, q store.Q, u core.UserID, id int64) error {
	if !s.pushOn() || s.d.Jobs == nil {
		return nil
	}
	q = s.q(q)
	var has bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_subscriptions WHERE user_id = $1)`, int64(u)).Scan(&has); err != nil {
		return err
	}
	if !has {
		return nil
	}
	return s.d.Jobs.Enqueue(ctx, q, JobDeliver, deliverJob{N: id}, time.Time{}, "n:"+strconv.FormatInt(id, 10))
}

type pushSub struct {
	id       uuid.UUID
	endpoint string
	p256dh   []byte
	auth     []byte
	hide     bool
}

// deliver renders row j.N for its recipient now and sends it to each of their
// subscriptions (j.Subs only, on a retry).
func (s *Service) deliver(ctx context.Context, j deliverJob) error {
	if !s.pushOn() {
		return nil
	}
	var (
		r    row
		u    int64
		su   int64
		sp   *uuid.UUID
		data []byte
	)
	err := s.d.DB.Q().QueryRow(ctx, `SELECT user_id, kind, coalesce(subject_user, 0), subject_post, data, count, updated_at,
			read_at IS NOT NULL
		FROM notifications WHERE id = $1`, j.N).Scan(&u, &r.kind, &su, &sp, &data, &r.count, &r.at, &r.read)
	switch {
	case isNoRows(err):
		return nil
	case err != nil:
		return err
	}
	if r.read {
		return nil
	}
	r.id, r.subjUser = j.N, core.UserID(su)
	if sp != nil {
		r.subjPost = *sp
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &r.data)
	}
	to := core.UserID(u)
	m, err := s.d.Users.Get(ctx, to)
	if err != nil || m.Status != 0 {
		return nil
	}
	sr, err := s.d.Vis.SearchFor(ctx, to)
	if err != nil {
		return err
	}
	nv, ok := s.view(ctx, to, sr, r)
	if !ok {
		return nil // the subject is out of the recipient's horizon now
	}
	full, err := s.payload(m.Locale, nv, false)
	if err != nil {
		return err
	}
	hidden, err := s.payload(m.Locale, nv, true)
	if err != nil {
		return err
	}
	subs, err := s.subscriptions(ctx, to, j.Subs)
	if err != nil {
		return err
	}
	var retry []string
	for _, sub := range subs {
		p := full
		if sub.hide {
			p = hidden
		}
		sctx, cancel := context.WithTimeout(ctx, sendTimeout)
		gone, err := s.sender.Send(sctx, webpush.Subscription{
			Endpoint: sub.endpoint, P256dh: b64(sub.p256dh), Auth: b64(sub.auth),
		}, p, PushTTL)
		cancel()
		switch {
		case gone:
			s.metric("gone")
			_, err = s.d.DB.Q().Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, sub.id, u)
		case err != nil:
			s.metric("failed")
			if s.d.Log != nil {
				s.d.Log.Warn("notify: push delivery failed", "job", JobDeliver, "err", err.Error())
			}
			retry = append(retry, sub.id.String())
			var n int
			err = s.d.DB.Q().QueryRow(ctx, `UPDATE push_subscriptions SET failures = failures + 1
				WHERE id = $1 AND user_id = $2 RETURNING failures`, sub.id, u).Scan(&n)
			if isNoRows(err) {
				err = nil
			} else if err == nil && n >= MaxFailures {
				_, err = s.d.DB.Q().Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, sub.id, u)
			}
		default:
			s.metric("ok")
			_, err = s.d.DB.Q().Exec(ctx, `UPDATE push_subscriptions SET last_ok_at = $3, failures = 0
				WHERE id = $1 AND user_id = $2`, sub.id, u, s.now())
		}
		if err != nil {
			return err
		}
	}
	if len(retry) > 0 && j.Try+1 < deliverTries {
		next := deliverJob{N: j.N, Subs: retry, Try: j.Try + 1}
		at := s.now().Add(time.Duration(1<<j.Try) * time.Minute)
		return s.d.Jobs.Enqueue(ctx, nil, JobDeliver, next, at, "")
	}
	return nil
}

func (s *Service) subscriptions(ctx context.Context, u core.UserID, only []string) ([]pushSub, error) {
	var ids []uuid.UUID
	for _, x := range only {
		if id, err := uuid.Parse(x); err == nil {
			ids = append(ids, id)
		}
	}
	if len(only) > 0 && len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT id, endpoint, p256dh, auth, hide_content FROM push_subscriptions
		WHERE user_id = $1 AND (coalesce(cardinality($2::uuid[]), 0) = 0 OR id = ANY($2::uuid[]))
		ORDER BY updated_at, id`, int64(u), ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pushSub
	for rows.Next() {
		var p pushSub
		if err := rows.Scan(&p.id, &p.endpoint, &p.p256dh, &p.auth, &p.hide); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// payload builds the message for nv in locale: the catalog text of its kind
// (only the generic text with hide), no post body, no refs or IDs.
func (s *Service) payload(locale string, nv gate.NotificationView, hide bool) ([]byte, error) {
	if hide {
		title, err := s.text(locale, "push.generic", nil)
		if err != nil {
			return nil, err
		}
		return webpush.Message{Title: title, Tag: genericTag, URL: NotificationsURL}.Marshal()
	}
	key, args := pushKey(nv)
	title, err := s.text(locale, key, args)
	if err != nil {
		return nil, err
	}
	// The tag is the notification's opaque ref, so a batched group's newer push
	// replaces the older one on the device.
	return webpush.Message{Title: title, Tag: string(nv.ID), URL: NotificationsURL}.Marshal()
}

// pushKey is the catalog key and arguments of nv: push.<kind>, the visible
// person's handle as {name} and the batch size as {count}.
func pushKey(nv gate.NotificationView) (string, map[string]any) {
	args := map[string]any{"count": nv.Count}
	key := "push." + nv.Kind
	switch {
	case nv.Person != nil:
		args["name"] = "@" + nv.Person.Handle
	case anonKinds[nv.Kind]:
		key += "_anon"
	}
	return key, args
}

func (s *Service) text(locale, key string, args map[string]any) (string, error) {
	if s.cat == nil {
		c, err := i18n.Default()
		if err != nil {
			return "", err
		}
		s.cat = c
	}
	return s.cat.Format(locale, key, args)
}

func (s *Service) metric(bucket string) {
	if s.d.Metrics != nil {
		s.d.Metrics.Inc("push.delivered", bucket, 1)
	}
}

// sessionEnded deletes the subscriptions of that session (logout, expiry,
// revocation): a signed-out device stops receiving at once.
func (s *Service) sessionEnded(u core.UserID, session [32]byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.d.DB.Q().Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND session_hash = $2`,
		int64(u), session[:]); err != nil && s.d.Log != nil {
		s.d.Log.Warn("notify: push subscriptions of an ended session not removed", "err", err)
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
