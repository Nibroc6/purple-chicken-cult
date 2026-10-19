package notify_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/notify"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/posts"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush/webpushtest"
)

type pushEnv struct {
	*env
	svc *webpushtest.Service
	pub string
}

// newPushEnv is newEnv with VAPID keys: the real VAPID sender delivers to a
// local fake push service (loopback endpoints: PCC_ENV=test with test hooks).
func newPushEnv(t *testing.T) *pushEnv {
	t.Helper()
	pub, priv, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	a := wiretest.NewApp(t, wiretest.WithEnv("PCC_VAPID_PUBLIC", pub), wiretest.WithEnv("PCC_VAPID_PRIVATE", priv),
		wiretest.WithEnv("PCC_VAPID_SUBJECT", "mailto:ops@pcc.invalid"))
	e := &env{t: t, a: a, d: a.Deps(), n: a.Notify, w: wiretest.World(t, a), ctx: t.Context()}
	for _, u := range []string{"alice", "bob", "carol", "dave", "xavier"} {
		e.w.User(u)
	}
	e.w.Friends("alice", "bob")
	e.w.Friends("bob", "carol")
	e.w.Friends("carol", "dave")
	if err := a.Users.Reload(e.ctx); err != nil {
		t.Fatal(err)
	}
	return &pushEnv{env: e, svc: webpushtest.New(t), pub: pub}
}

// subscribe registers a fresh fake-service subscription for h in session s.
func (e *pushEnv) subscribe(h string, s [32]byte, hide bool) webpush.Subscription {
	e.t.Helper()
	sub := e.svc.Subscribe()
	e.subscribeAs(h, s, sub, hide)
	return sub
}

func (e *pushEnv) subscribeAs(h string, s [32]byte, sub webpush.Subscription, hide bool) {
	e.t.Helper()
	in := gate.PushSub{Endpoint: sub.Endpoint, HideContent: hide}
	in.Keys.P256dh, in.Keys.Auth = sub.P256dh, sub.Auth
	if err := e.n.Subscribe(e.asSession(h, s), in); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pushEnv) drain() {
	e.t.Helper()
	if err := e.a.DrainJobs(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pushEnv) messages(sub webpush.Subscription) []webpush.Message {
	e.t.Helper()
	var out []webpush.Message
	for _, r := range e.svc.For(sub.Endpoint) {
		if r.DecodeErr != nil {
			e.t.Fatalf("undecryptable push: %v", r.DecodeErr)
		}
		m, err := r.Message()
		if err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (e *pushEnv) subs(h string) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT count(*) FROM push_subscriptions WHERE user_id = $1", int64(e.id(h))).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *pushEnv) setLocale(h, loc string) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, "UPDATE users SET ui_locale = $2 WHERE id = $1", int64(e.id(h)), loc); err != nil {
		e.t.Fatal(err)
	}
	e.a.Users.Invalidate(e.id(h))
}

func (e *pushEnv) jobs() int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT count(*) FROM jobs WHERE kind = $1 AND done_at IS NULL", notify.JobDeliver).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestPushDisabledWithoutVAPID(t *testing.T) {
	e := newEnv(t) // no VAPID keys (D20)
	if key, ok := e.n.PushKey(e.ctx); ok || key != "" {
		t.Fatalf("push key without VAPID: %q", key)
	}
	in := gate.PushSub{Endpoint: "http://127.0.0.1:9/push/1"}
	in.Keys.P256dh, in.Keys.Auth = strings.Repeat("A", 87), strings.Repeat("A", 22)
	var dis *gate.DisabledError
	if err := e.n.Subscribe(e.as("alice"), in); !errors.As(err, &dis) || dis.Feature != "push" {
		t.Fatalf("subscribe without VAPID: %v", err)
	}
	if err := e.n.Subscribe(e.ctx, in); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous subscribe: %v", err)
	}
	// Unsubscribing still works (cleanup), and nothing is ever queued.
	if err := e.n.Unsubscribe(e.as("alice"), in.Endpoint); err != nil {
		t.Fatal(err)
	}
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	var n int
	_ = e.d.DB.Q().QueryRow(e.ctx, "SELECT count(*) FROM jobs WHERE kind = $1", notify.JobDeliver).Scan(&n)
	if n != 0 {
		t.Fatalf("%d push jobs with push disabled", n)
	}
}

func TestPushKeyAndEncryptedDelivery(t *testing.T) {
	e := newPushEnv(t)
	if key, ok := e.n.PushKey(e.ctx); !ok || key != e.pub {
		t.Fatalf("push key %q %v", key, ok)
	}
	sub := e.subscribe("alice", session(), false)
	p1 := e.w.Post("alice", "p1", "hello")
	body := "Secret garden plans @alice"
	if _, err := e.a.Posts.Create(e.as("bob"), posts.Draft{Body: body, Lang: "en",
		Parents: []posts.ParentIn{{Ref: e.post("bob", p1), Kind: "ref"}}}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	got := e.messages(sub)
	if len(got) != 2 {
		t.Fatalf("pushes: %+v", got)
	}
	titles := []string{got[0].Title, got[1].Title}
	want := map[string]bool{"@bob mentioned you": true, "1 new post references yours": true}
	for _, ti := range titles {
		if !want[ti] {
			t.Errorf("unexpected text %q (want %v)", ti, want)
		}
	}
	for _, r := range e.svc.For(sub.Endpoint) {
		if strings.Contains(string(r.Plaintext), "Secret") || strings.Contains(string(r.Plaintext), "garden") {
			t.Fatalf("post body in a push payload: %s", r.Plaintext)
		}
		if r.BodyLen != webpush.RecordSize || r.TTL != int(notify.PushTTL.Seconds()) {
			t.Errorf("record %d bytes, ttl %d", r.BodyLen, r.TTL)
		}
		if err := webpushtest.VerifyVAPID(r.Auth, e.pub, e.svc.URL(), time.Now()); err != nil {
			t.Error(err)
		}
		var raw map[string]any
		_ = json.Unmarshal(r.Plaintext, &raw)
		for k := range raw {
			if k != "v" && k != "title" && k != "tag" && k != "url" {
				t.Errorf("unexpected payload field %q", k)
			}
		}
		if raw["url"] != notify.NotificationsURL {
			t.Errorf("url %v", raw["url"])
		}
	}
	// A read notification is not pushed later.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("carol")})
	if _, err := e.d.DB.Q().Exec(e.ctx, "UPDATE notifications SET read_at = now() WHERE user_id = $1", int64(e.id("alice"))); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if n := len(e.messages(sub)); n != 2 {
		t.Fatalf("read notification pushed: %d", n)
	}
}

func TestPushTextInRecipientLocaleEnXA(t *testing.T) {
	e := newPushEnv(t)
	e.setLocale("alice", "en-XA")
	sub := e.subscribe("alice", session(), false)
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	e.drain()
	got := e.messages(sub)
	if len(got) != 1 {
		t.Fatalf("pushes %+v", got)
	}
	title := got[0].Title
	if !strings.HasPrefix(title, "[@bob ") || !strings.HasSuffix(title, "]") || !strings.Contains(title, "šêñŧ ýôû à ƒŕîêñð ŕêǫûêšŧ") {
		t.Fatalf("en-XA text from the catalog: %q", title)
	}
	// Another locale without a catalog falls back to en.
	e.setLocale("alice", "fi")
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindRequestAccepted, SubjectUser: e.id("bob")})
	e.drain()
	if got := e.messages(sub); len(got) != 2 || got[1].Title != "@bob accepted your friend request" {
		t.Fatalf("fi falls back to en: %+v", got)
	}
}

func TestHideContentSendsOnlyTheGenericText(t *testing.T) {
	e := newPushEnv(t)
	hidden := e.subscribe("alice", session(), true)
	shown := e.subscribe("alice", session(), false)
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	e.drain()
	h := e.svc.For(hidden.Endpoint)
	if len(h) != 1 || string(h[0].Plaintext) != `{"v":1,"title":"New activity","tag":"activity","url":"/notifications"}` {
		t.Fatalf("hidden payload: %+v", h)
	}
	if s := e.messages(shown); len(s) != 1 || s[0].Title != "@bob sent you a friend request" {
		t.Fatalf("shown payload: %+v", s)
	}
	if st, err := e.n.SubscriptionState(e.as("alice"), hidden.Endpoint); err != nil || !st.Subscribed || !st.HideContent {
		t.Fatalf("state %+v %v", st, err)
	}
	// Re-subscribing flips the option in place.
	e.subscribeAs("alice", session(), hidden, false)
	if st, _ := e.n.SubscriptionState(e.as("alice"), hidden.Endpoint); !st.Subscribed || st.HideContent {
		t.Fatalf("state after re-subscribe %+v", st)
	}
	if e.subs("alice") != 2 {
		t.Fatalf("subs %d", e.subs("alice"))
	}
}

func TestLogoutRemovesThatSessionsSubscription(t *testing.T) {
	e := newPushEnv(t)
	s1, s2 := session(), session()
	gone := e.subscribe("alice", s1, false)
	kept := e.subscribe("alice", s2, false)
	e.d.Bus.Publish(core.SessionEnded{User: e.id("alice"), Session: s1})
	if st, _ := e.n.SubscriptionState(e.as("alice"), gone.Endpoint); st.Subscribed {
		t.Fatal("ended session's subscription kept")
	}
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	e.drain()
	if len(e.svc.For(gone.Endpoint)) != 0 || len(e.svc.For(kept.Endpoint)) != 1 {
		t.Fatalf("after logout: gone=%d kept=%d", len(e.svc.For(gone.Endpoint)), len(e.svc.For(kept.Endpoint)))
	}
	// Explicit unsubscribe, and the account's deletion, remove the rest.
	if err := e.n.Unsubscribe(e.as("alice"), kept.Endpoint); err != nil || e.subs("alice") != 0 {
		t.Fatalf("unsubscribe: %v %d", err, e.subs("alice"))
	}
	e.subscribe("alice", s2, false)
	e.d.Bus.Publish(core.AccountDeleted{ID: e.id("alice")})
	if e.subs("alice") != 0 {
		t.Fatal("deleted account keeps subscriptions")
	}
	// No subscription, no job.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindMention, SubjectUser: e.id("bob")})
	if e.jobs() != 0 {
		t.Fatal("push job queued for a recipient without subscriptions")
	}
}

func TestResubscribingAnotherAccountsEndpointMovesIt(t *testing.T) {
	e := newPushEnv(t)
	sub := e.subscribe("alice", session(), false)
	// Someone else on the same browser subscribes the same endpoint.
	e.subscribeAs("carol", session(), sub, false)
	if e.subs("alice") != 0 || e.subs("carol") != 1 {
		t.Fatalf("subs alice=%d carol=%d", e.subs("alice"), e.subs("carol"))
	}
	if st, _ := e.n.SubscriptionState(e.as("alice"), sub.Endpoint); st.Subscribed {
		t.Fatal("previous owner still sees the endpoint as hers")
	}
	// Alice unsubscribing it does not touch carol's.
	if err := e.n.Unsubscribe(e.as("alice"), sub.Endpoint); err != nil || e.subs("carol") != 1 {
		t.Fatalf("foreign unsubscribe: %v", err)
	}
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	e.notify(core.Event{To: e.id("carol"), Kind: notify.KindFriendRequest, SubjectUser: e.id("dave")})
	e.drain()
	got := e.messages(sub)
	if len(got) != 1 || got[0].Title != "@dave sent you a friend request" {
		t.Fatalf("endpoint received %+v", got)
	}
}

func TestGoneAndFailingEndpoints(t *testing.T) {
	e := newPushEnv(t)
	gone := e.subscribe("alice", session(), false)
	e.svc.Answer(gone.Endpoint, http.StatusGone)
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	e.drain()
	if e.subs("alice") != 0 {
		t.Fatal("410 endpoint not dropped")
	}
	// A failing endpoint is retried a bounded number of times, then dropped
	// after MaxFailures consecutive failures.
	flaky := e.subscribe("alice", session(), false)
	e.svc.Answer(flaky.Endpoint, http.StatusInternalServerError)
	for i := range notify.MaxFailures {
		e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob"), Data: map[string]any{"i": i}})
		e.drain()
		e.advance(10 * time.Minute)
		e.drain()
	}
	if e.subs("alice") != 0 {
		t.Fatal("persistently failing endpoint kept")
	}
	// Recovery resets the counter.
	ok := e.subscribe("alice", session(), false)
	e.svc.Answer(ok.Endpoint, http.StatusServiceUnavailable)
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("carol")})
	e.drain()
	e.svc.Answer(ok.Endpoint, 0)
	e.advance(2 * time.Minute)
	e.drain()
	if got := e.messages(ok); len(got) != 1 || e.subs("alice") != 1 {
		t.Fatalf("retry after a transient failure: %+v", got)
	}
	var failures int
	_ = e.d.DB.Q().QueryRow(e.ctx, "SELECT failures FROM push_subscriptions WHERE endpoint = $1", ok.Endpoint).Scan(&failures)
	if failures != 0 {
		t.Fatalf("failures %d after success", failures)
	}
}

func TestSubjectsOutOfHorizonAreNotPushed(t *testing.T) {
	e := newPushEnv(t)
	e.w.Friends("alice", "xavier")
	sub := e.subscribe("alice", session(), false)
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("xavier")})
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendRequest, SubjectUser: e.id("bob")})
	e.unfriend("alice", "bob") // bob leaves alice's horizon before delivery
	e.drain()
	if got := e.messages(sub); len(got) != 1 || got[0].Title != "@xavier sent you a friend request" {
		t.Fatalf("pushes %+v", got)
	}
	// An optional person who is out of reach gives the anonymous text.
	e.notify(core.Event{To: e.id("alice"), Kind: notify.KindFriendSecurity, SubjectUser: e.id("dave")})
	e.drain()
	got := e.messages(sub)
	if len(got) != 2 || got[1].Title != "A friend changed their sign-in keys" {
		t.Fatalf("anonymous variant: %+v", got)
	}
}

func TestBatchedGroupPushesWithItsHourlyPing(t *testing.T) {
	e := newPushEnv(t)
	sub := e.subscribe("alice", session(), false)
	p1 := e.w.Post("alice", "p1", "hello")
	ev := core.Event{To: e.id("alice"), Kind: notify.KindPostReferenced, GroupKey: "post_referenced:" + p1.String(),
		SubjectUser: e.id("bob"), SubjectPost: p1}
	e.notify(ev)
	e.drain()
	e.advance(time.Minute)
	e.notify(ev) // later events of the group wait for the hourly ping
	e.notify(ev)
	e.drain()
	if got := e.messages(sub); len(got) != 1 || got[0].Title != "1 new post references yours" {
		t.Fatalf("first push: %+v", got)
	}
	e.advance(notify.BatchPingEvery)
	if err := e.n.Flush(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.drain()
	got := e.messages(sub)
	if len(got) != 2 || got[1].Title != "3 new posts reference yours" || got[1].Tag != got[0].Tag {
		t.Fatalf("hourly push: %+v", got)
	}
}

func TestSubscribeValidation(t *testing.T) {
	e := newPushEnv(t)
	good := e.svc.Subscribe()
	cases := []struct {
		ep, p256, auth, code string
	}{
		{"https://10.0.0.1/push", good.P256dh, good.Auth, "push_endpoint_invalid"},
		{"http://push.example/x", good.P256dh, good.Auth, "push_endpoint_invalid"},
		{"", good.P256dh, good.Auth, "push_endpoint_invalid"},
		{good.Endpoint, "AAAA", good.Auth, "push_keys_invalid"},
		{good.Endpoint, good.P256dh, "", "push_keys_invalid"},
	}
	for _, c := range cases {
		in := gate.PushSub{Endpoint: c.ep}
		in.Keys.P256dh, in.Keys.Auth = c.p256, c.auth
		var p *gate.Problem
		if err := e.n.Subscribe(e.as("alice"), in); !errors.As(err, &p) || p.Code != c.code {
			t.Errorf("%+v: %v", c, err)
		}
	}
	// At most MaxSubscriptions per account; the oldest goes.
	var first webpush.Subscription
	for i := range notify.MaxSubscriptions + 2 {
		s := e.subscribe("alice", session(), false)
		if i == 0 {
			first = s
		}
		e.advance(time.Second)
	}
	if n := e.subs("alice"); n != notify.MaxSubscriptions {
		t.Fatalf("subs %d", n)
	}
	if st, _ := e.n.SubscriptionState(e.as("alice"), first.Endpoint); st.Subscribed {
		t.Fatal("oldest subscription kept beyond the cap")
	}
}

