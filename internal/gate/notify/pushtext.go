package notify

// Pushes that are not notifications (core.MessagePush): direct messages tell
// the recipient's devices "new message from @alice" without a row in the
// notifications list (the Messages page has its own unread count). The same
// rules as push.deliver hold: queued in the sending transaction, rendered in
// the recipient's ui_locale at delivery, the generic text for hide_content,
// never any message or post text, padded to the same size as every push.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// JobDeliverText renders one core.PushText and sends it to the recipient's subscriptions.
const JobDeliverText = "push.text"

// TopicMessage is the live event of a new direct message (gate/dm):
// message{conversation, unread}.
const TopicMessage = "message"

var _ core.MessagePush = (*Service)(nil)

// textJob is the push.text payload.
type textJob struct {
	To   int64          `json:"to"`
	Key  string         `json:"k"`
	Args map[string]any `json:"a,omitempty"`
	Tag  string         `json:"g,omitempty"`
	URL  string         `json:"u,omitempty"`
}

func (s *Service) initPushText() {
	if s.d.Jobs == nil {
		return
	}
	s.d.Jobs.Handle(JobDeliverText, func(ctx context.Context, p json.RawMessage) error {
		var j textJob
		if err := json.Unmarshal(p, &j); err != nil {
			return nil // malformed payload: nothing to retry
		}
		return s.deliverText(ctx, j)
	})
}

// PushText enqueues push.text for to in q when push is on and to has a
// subscription (core.MessagePush).
func (s *Service) PushText(ctx context.Context, q store.Q, to core.UserID, p core.PushText) error {
	if to == 0 || !s.pushOn() || s.d.Jobs == nil || !codeRE.MatchString(p.Key) || !strings.HasPrefix(p.URL, "/") {
		return nil
	}
	q = s.q(q)
	var has bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_subscriptions WHERE user_id = $1)`, int64(to)).Scan(&has); err != nil {
		return err
	}
	if !has {
		return nil
	}
	return s.d.Jobs.Enqueue(ctx, q, JobDeliverText, textJob{To: int64(to), Key: p.Key, Args: p.Args, Tag: p.Tag, URL: p.URL},
		time.Time{}, "")
}

// deliverText renders j for an active recipient and sends it once to each of
// their subscriptions (a chat push that failed is not worth a retry: the next
// message pushes again).
func (s *Service) deliverText(ctx context.Context, j textJob) error {
	if !s.pushOn() {
		return nil
	}
	to := core.UserID(j.To)
	m, err := s.d.Users.Get(ctx, to)
	if err != nil || m.Status != 0 {
		return nil //nolint:nilerr // a missing or inactive recipient gets nothing
	}
	title, err := s.text(m.Locale, "push."+j.Key, j.Args)
	if err != nil {
		return err
	}
	full, err := webpush.Message{Title: title, Tag: j.Tag, URL: j.URL}.Marshal()
	if err != nil {
		return err
	}
	generic, err := s.text(m.Locale, "push.generic", nil)
	if err != nil {
		return err
	}
	hidden, err := webpush.Message{Title: generic, Tag: genericTag, URL: j.URL}.Marshal()
	if err != nil {
		return err
	}
	subs, err := s.subscriptions(ctx, to, nil)
	if err != nil {
		return err
	}
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
			_, err = s.d.DB.Q().Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, sub.id, j.To)
		case err != nil:
			s.metric("failed")
			if s.d.Log != nil {
				s.d.Log.Warn("notify: push delivery failed", "job", JobDeliverText, "err", err.Error())
			}
			err = nil
		default:
			s.metric("ok")
			_, err = s.d.DB.Q().Exec(ctx, `UPDATE push_subscriptions SET last_ok_at = $3, failures = 0
				WHERE id = $1 AND user_id = $2`, sub.id, j.To, s.now())
		}
		if err != nil {
			return err
		}
	}
	return nil
}
