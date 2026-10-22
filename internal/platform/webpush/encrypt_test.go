package webpush_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush/webpushtest"
)

// TestEncryptedFixedSizeRecords: a message reaches the (fake) push service
// encrypted for the subscription (RFC 8291), padded to the fixed record size
// whatever its length, with a valid VAPID JWT for the endpoint's origin.
func TestEncryptedFixedSizeRecords(t *testing.T) {
	pub, priv, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	svc := webpushtest.New(t)
	v, err := webpush.NewVAPID(pub, priv, "mailto:ops@pcc.invalid", true)
	if err != nil {
		t.Fatal(err)
	}
	sub := svc.Subscribe()
	short := []byte(`{"v":1,"title":"a","url":"/notifications"}`)
	long := []byte(`{"v":1,"title":"` + strings.Repeat("é", 400) + `","url":"/notifications"}`)
	for _, p := range [][]byte{short, long} {
		if gone, err := v.Send(context.Background(), sub, p, 2*time.Hour); gone || err != nil {
			t.Fatalf("send: gone=%v err=%v", gone, err)
		}
	}
	got := svc.For(sub.Endpoint)
	if len(got) != 2 {
		t.Fatalf("received %d", len(got))
	}
	for i, want := range [][]byte{short, long} {
		r := got[i]
		if r.DecodeErr != nil || !bytes.Equal(r.Plaintext, want) {
			t.Fatalf("message %d: %v %q", i, r.DecodeErr, r.Plaintext)
		}
		if r.BodyLen != webpush.RecordSize {
			t.Errorf("message %d: body %d bytes, want the fixed %d", i, r.BodyLen, webpush.RecordSize)
		}
		if r.TTL != 7200 || r.Urgency != "normal" {
			t.Errorf("headers: ttl=%d urgency=%q", r.TTL, r.Urgency)
		}
		if err := webpushtest.VerifyVAPID(r.Auth, pub, svc.URL(), time.Now()); err != nil {
			t.Errorf("vapid: %v", err)
		}
		if err := webpushtest.VerifyVAPID(r.Auth, pub, "https://other.example", time.Now()); err == nil {
			t.Error("aud must be the endpoint origin")
		}
	}
	// A 410 endpoint is gone and records nothing.
	other := svc.Subscribe()
	svc.Answer(other.Endpoint, http.StatusGone)
	if gone, err := v.Send(context.Background(), other, short, time.Hour); !gone || err != nil {
		t.Fatalf("410: gone=%v err=%v", gone, err)
	}
	if len(svc.For(other.Endpoint)) != 0 {
		t.Error("a 410 endpoint recorded a message")
	}
}
