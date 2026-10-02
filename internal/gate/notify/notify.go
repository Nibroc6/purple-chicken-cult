// Package notify owns in-app notifications and the SSE hub (ARCHITECTURE §10.6);
// P1 adds Web Push. It implements core.Notifier.
//
// Owner: P0-W10 → P1-W05. Scaffold stub: core.Notifier delegates to
// coreref.RecordingNotifier (memory); the handler-facing API returns
// gate.ErrNotImplemented.
package notify

import (
	"context"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the notifications module.
type Service struct {
	d   *core.Deps
	ref *coreref.RecordingNotifier
}

var _ core.Notifier = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, ref: &coreref.RecordingNotifier{D: d}} }

// Init registers notify.flush and the SessionEnded subscription in W10.
func (s *Service) Init(context.Context) error { return nil }

// StreamEvent is one SSE event: hello{unread}, notification{unread},
// handshake{hs,state}, stepup{} (P1).
type StreamEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// ---- handler-facing API ----

// List returns the viewer's notifications.
func (s *Service) List(ctx context.Context, cursor string) (gate.Page[gate.NotificationView], error) {
	return gate.Page[gate.NotificationView]{}, s.todo(ctx)
}

// MarkRead marks notifications read up to upTo.
func (s *Service) MarkRead(ctx context.Context, upTo gate.Ref) error { return s.todo(ctx) }

// UnreadCount returns the viewer's unread count.
func (s *Service) UnreadCount(ctx context.Context) (int, error) { return 0, s.todo(ctx) }

// Stream subscribes the viewer's session to SSE events; call the returned func to stop.
func (s *Service) Stream(ctx context.Context) (<-chan StreamEvent, func(), error) {
	return nil, func() {}, s.todo(ctx)
}

// MutePost mutes reference notifications for a post.
func (s *Service) MutePost(ctx context.Context, p gate.Ref, on bool) error { return s.todo(ctx) }

// PushKey returns the VAPID public key when push is enabled (P1).
func (s *Service) PushKey(ctx context.Context) (string, bool) {
	if s.d.Cfg == nil || !s.d.Cfg.PushEnabled() {
		return "", false
	}
	return s.d.Cfg.Push.VAPIDPublic, true
}

// Subscribe stores a push subscription (P1; fails closed without VAPID).
func (s *Service) Subscribe(ctx context.Context, sub gate.PushSub) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	if s.d.Cfg == nil || !s.d.Cfg.PushEnabled() {
		return gate.Disabled("push")
	}
	return gate.ErrNotImplemented
}

// Unsubscribe removes a push subscription (P1).
func (s *Service) Unsubscribe(ctx context.Context, endpoint string) error { return s.todo(ctx) }

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// ---- core.Notifier (reference delegation) ----

// Notify persists a notification (batched on GroupKey; dropped if the recipient blocked the subject).
func (s *Service) Notify(ctx context.Context, tx store.Q, e core.Event) error {
	return s.ref.Notify(ctx, tx, e)
}

// Live sends an ephemeral SSE event.
func (s *Service) Live(to core.UserID, topic string, data any) { s.ref.Live(to, topic, data) }
