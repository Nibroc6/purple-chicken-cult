// Package webpush sends Web Push messages (RFC 8291, VAPID). The "log" driver
// (dev/test without VAPID keys) records payloads in an in-memory ring readable by
// tests. In prod without VAPID keys New returns ErrDisabled and push reports
// {enabled:false} (fail closed).
//
// Owner: P1-W05 (the scaffold ships both drivers).
package webpush

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	wp "github.com/SherClockHolmes/webpush-go"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Subscription is a browser push subscription.
type Subscription struct {
	Endpoint string
	P256dh   string // base64url
	Auth     string // base64url
}

// Sender delivers one encrypted push message. gone reports that the endpoint is
// permanently invalid (404/410) and must be deleted.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte, ttl time.Duration) (gone bool, err error)
}

// ErrDisabled is returned when push is not configured in prod.
var ErrDisabled = errors.New("webpush: disabled (VAPID keys missing)")

// New returns the VAPID sender when configured, the log sender in dev/test, and
// ErrDisabled otherwise.
func New(c *config.Config) (Sender, error) {
	if c.PushEnabled() {
		return &VAPID{pub: c.Push.VAPIDPublic, priv: c.Push.VAPIDPrivate, sub: c.Push.VAPIDSubject}, nil
	}
	if c.DevLike() {
		return NewLog(256), nil
	}
	return nil, ErrDisabled
}

// VAPID sends through push services with VAPID authentication.
type VAPID struct{ pub, priv, sub string }

// Send encrypts and posts payload.
func (v *VAPID) Send(ctx context.Context, s Subscription, payload []byte, ttl time.Duration) (bool, error) {
	resp, err := wp.SendNotificationWithContext(ctx, payload, &wp.Subscription{
		Endpoint: s.Endpoint, Keys: wp.Keys{P256dh: s.P256dh, Auth: s.Auth},
	}, &wp.Options{
		Subscriber: v.sub, VAPIDPublicKey: v.pub, VAPIDPrivateKey: v.priv, TTL: int(ttl.Seconds()),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return true, nil
	case resp.StatusCode >= 300:
		return false, errors.New("webpush: push service rejected the message")
	}
	return false, nil
}

// Sent is one message recorded by the log driver.
type Sent struct {
	Sub     Subscription
	Payload []byte
	TTL     time.Duration
}

// Log records messages instead of sending them.
type Log struct {
	mu   sync.Mutex
	ring []Sent
	max  int
}

// NewLog returns a log sender keeping the last max messages.
func NewLog(max int) *Log { return &Log{max: max} }

// Send records the message.
func (l *Log) Send(_ context.Context, s Subscription, payload []byte, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = append(l.ring, Sent{Sub: s, Payload: append([]byte(nil), payload...), TTL: ttl})
	if len(l.ring) > l.max {
		l.ring = l.ring[len(l.ring)-l.max:]
	}
	return false, nil
}

// Messages returns a copy of the recorded messages.
func (l *Log) Messages() []Sent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Sent(nil), l.ring...)
}
