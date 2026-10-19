package webpush

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

func keys(t *testing.T) (string, string) {
	t.Helper()
	pub, priv, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestNewFailsClosed(t *testing.T) {
	pub, priv := keys(t)
	cases := []struct {
		name string
		cfg  config.Config
		want string // vapid | log | disabled | bad
	}{
		{"prod without vapid", config.Config{Env: config.EnvProd}, "disabled"},
		{"prod with half the keys", config.Config{Env: config.EnvProd, Push: config.PushConfig{VAPIDPublic: pub, VAPIDSubject: "mailto:a@b.c"}}, "disabled"},
		{"prod with vapid", config.Config{Env: config.EnvProd, Push: config.PushConfig{VAPIDPublic: pub, VAPIDPrivate: priv, VAPIDSubject: "mailto:a@b.c"}}, "vapid"},
		{"dev without vapid", config.Config{Env: config.EnvDev}, "log"},
		{"test without vapid", config.Config{Env: config.EnvTest}, "log"},
		{"mismatched pair", config.Config{Env: config.EnvProd, Push: config.PushConfig{VAPIDPublic: pub, VAPIDPrivate: strings.Repeat("A", 43), VAPIDSubject: "mailto:a@b.c"}}, "bad"},
		{"garbage key", config.Config{Env: config.EnvDev, Push: config.PushConfig{VAPIDPublic: "!!", VAPIDPrivate: priv, VAPIDSubject: "mailto:a@b.c"}}, "bad"},
	}
	for _, c := range cases {
		s, err := New(&c.cfg)
		got := ""
		switch {
		case errors.Is(err, ErrDisabled):
			got = "disabled"
		case errors.Is(err, ErrBadKeys):
			got = "bad"
		case err != nil:
			got = err.Error()
		default:
			switch s.(type) {
			case *VAPID:
				got = "vapid"
			case *Log:
				got = "log"
			}
		}
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	tc := config.Config{Env: config.EnvTest, TestHooks: true}
	if !AllowLoopback(&tc) {
		t.Error("test env with hooks allows loopback endpoints")
	}
	for _, c := range []config.Config{{Env: config.EnvTest}, {Env: config.EnvDev, TestHooks: true}, {Env: config.EnvProd}} {
		if AllowLoopback(&c) {
			t.Errorf("%s hooks=%v must not allow loopback", c.Env, c.TestHooks)
		}
	}
}

func TestCheckEndpoint(t *testing.T) {
	ok := []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAA",
		"https://web.push.apple.com/QGx",
		"https://wns2-par02p.notify.windows.com:443/w/?token=x",
		"https://8.8.8.8/push",
	}
	for _, u := range ok {
		if err := CheckEndpoint(u, false); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	bad := []string{
		"", "http://fcm.googleapis.com/x", "https://fcm.googleapis.com:8443/x", "https://user:pw@fcm.googleapis.com/x",
		"https://127.0.0.1/x", "https://10.1.2.3/x", "https://[::1]/x", "https://169.254.169.254/latest", "https://localhost/x",
		"https://intranet/x", "https://0x7f000001/x", "https://2130706433/x", "https://a.b/x#frag", "ftp://a.b/x",
		"https://[fe80::1]/x", "https://[::ffff:127.0.0.1]/x", "/relative", "https://a.b/" + strings.Repeat("x", MaxEndpointLen),
		"https://a.b/x y", "http://127.0.0.1:9999/push",
	}
	for _, u := range bad {
		if err := CheckEndpoint(u, false); !errors.Is(err, ErrEndpoint) {
			t.Errorf("%q accepted", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:9999/push", "http://localhost:1234/p", "https://[::1]:5/x"} {
		if err := CheckEndpoint(u, true); err != nil {
			t.Errorf("loopback %s refused in test: %v", u, err)
		}
	}
	if err := CheckEndpoint("http://10.0.0.1:80/x", true); err == nil {
		t.Error("allowing loopback must not allow private addresses")
	}
}

func TestBlockedAddresses(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.169.254", "::1", "fc00::1", "fe80::1", "::ffff:8.8.8.8", "100.64.0.1", "0.0.0.0", "224.0.0.1"} {
		if !blocked(netip.MustParseAddr(a), false) {
			t.Errorf("%s not blocked", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "142.250.0.1", "2a00:1450:4001::1"} {
		if blocked(netip.MustParseAddr(a), false) {
			t.Errorf("%s blocked", a)
		}
	}
	if blocked(netip.MustParseAddr("127.0.0.1"), true) || !blocked(netip.MustParseAddr("10.0.0.1"), true) {
		t.Error("loopback allowance is loopback only")
	}
	v := &VAPID{}
	if err := v.control("tcp", "10.0.0.1:443", nil); !errors.Is(err, ErrBlocked) {
		t.Errorf("control lets a private address through: %v", err)
	}
	if err := v.control("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("control blocks a public address: %v", err)
	}
}

func TestCheckKeys(t *testing.T) {
	pub, _ := keys(t)
	auth := "AAAAAAAAAAAAAAAAAAAAAA" // 16 zero bytes
	if err := CheckKeys(pub, auth); err != nil {
		t.Fatal(err)
	}
	if err := CheckKeys(pub+"==", auth+"=="); err != nil {
		t.Fatalf("padded keys: %v", err)
	}
	notOnCurve := "B" + strings.Repeat("A", 86)
	for _, c := range [][2]string{{"", auth}, {pub, ""}, {pub, "AAAA"}, {notOnCurve, auth}, {pub[:40], auth}, {"***", auth}} {
		if err := CheckKeys(c[0], c[1]); !errors.Is(err, ErrSubKeys) {
			t.Errorf("%q/%q accepted", c[0], c[1])
		}
	}
}

func TestMessageMarshal(t *testing.T) {
	b, err := Message{Title: "x", URL: "/notifications"}.Marshal()
	if err != nil || string(b) != `{"v":1,"title":"x","url":"/notifications"}` {
		t.Fatalf("%s %v", b, err)
	}
	if _, err := (Message{Title: strings.Repeat("x", MaxPayload)}).Marshal(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized message: %v", err)
	}
}

func TestVAPIDStatusesAndNoRedirects(t *testing.T) {
	pub, priv := keys(t)
	var hits, redirected atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/gone":
			w.WriteHeader(http.StatusGone)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		case "/redirect":
			http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
		case "/target":
			redirected.Add(1)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()
	v, err := NewVAPID(pub, priv, "mailto:ops@pcc.invalid", true)
	if err != nil {
		t.Fatal(err)
	}
	sub := func(path string) Subscription {
		return Subscription{Endpoint: srv.URL + path, P256dh: pub, Auth: "AAAAAAAAAAAAAAAAAAAAAA"}
	}
	ctx := context.Background()
	for path, want := range map[string]struct {
		gone bool
		err  bool
	}{"/ok": {}, "/gone": {gone: true}, "/missing": {gone: true}, "/broken": {err: true}, "/redirect": {err: true}} {
		gone, err := v.Send(ctx, sub(path), []byte(`{}`), time.Hour)
		if gone != want.gone || (err != nil) != want.err {
			t.Errorf("%s: gone=%v err=%v", path, gone, err)
		}
	}
	if redirected.Load() != 0 {
		t.Error("a redirect was followed")
	}
	// The same server is refused without the test-only loopback allowance.
	strict, _ := NewVAPID(pub, priv, "mailto:ops@pcc.invalid", false)
	before := hits.Load()
	if _, err := strict.Send(ctx, sub("/ok"), []byte(`{}`), time.Hour); !errors.Is(err, ErrEndpoint) {
		t.Errorf("loopback endpoint in prod: %v", err)
	}
	if hits.Load() != before {
		t.Error("a refused endpoint was contacted")
	}
	if _, err := v.Send(ctx, Subscription{Endpoint: srv.URL + "/ok", P256dh: "x", Auth: "y"}, nil, time.Hour); !errors.Is(err, ErrSubKeys) {
		t.Errorf("bad subscription keys: %v", err)
	}
}

func TestLogDriver(t *testing.T) {
	l := NewLog(2)
	ctx := context.Background()
	for _, ep := range []string{"a", "b", "c"} {
		if _, err := l.Send(ctx, Subscription{Endpoint: ep}, []byte(ep), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	got := l.Messages()
	if len(got) != 2 || got[0].Sub.Endpoint != "b" || string(got[1].Payload) != "c" {
		t.Fatalf("ring: %+v", got)
	}
	l.Gone("g")
	if gone, _ := l.Send(ctx, Subscription{Endpoint: "g"}, nil, 0); !gone {
		t.Error("gone endpoint")
	}
	l.Fail(true)
	if _, err := l.Send(ctx, Subscription{Endpoint: "x"}, nil, 0); err == nil {
		t.Error("fail mode")
	}
	l.Reset()
	if len(l.Messages()) != 0 {
		t.Error("reset")
	}
}
