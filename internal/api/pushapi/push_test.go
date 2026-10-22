package pushapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/api"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/authapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/pushapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/limits"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/webpush/webpushtest"
)

func harness(t *testing.T) *apitest.Harness {
	t.Helper()
	return apitest.Service(t, "push", func(r chi.Router, d *api.Deps) {
		pushapi.Register(r, d)
		authapi.Register(r, d)
	})
}

// enable turns push on as a deploy with VAPID keys would, delivering to a
// local fake push service.
func enable(t *testing.T, h *apitest.Harness) (*webpushtest.Service, string) {
	t.Helper()
	pub, priv, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	h.App.Config().Push = config.PushConfig{VAPIDPublic: pub, VAPIDPrivate: priv, VAPIDSubject: "mailto:ops@pcc.invalid"}
	v, err := webpush.NewVAPID(pub, priv, "mailto:ops@pcc.invalid", true)
	if err != nil {
		t.Fatal(err)
	}
	h.App.Notify.SetSender(v)
	return webpushtest.New(t), pub
}

func do(t *testing.T, h *apitest.Harness, method, path string, body any, want int) []byte {
	t.Helper()
	rec := h.Do(method, path, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec.Body.Bytes()
}

func sub(s webpush.Subscription, hide bool) map[string]any {
	return map[string]any{"endpoint": s.Endpoint, "keys": map[string]string{"p256dh": s.P256dh, "auth": s.Auth}, "hide_content": hide}
}

func state(t *testing.T, h *apitest.Harness, endpoint string) map[string]bool {
	t.Helper()
	var st map[string]bool
	if err := json.Unmarshal(do(t, h, http.MethodGet, "/api/push/subscriptions?endpoint="+url.QueryEscape(endpoint), nil, http.StatusOK), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDisabledWithoutVAPID(t *testing.T) {
	h := harness(t)
	if got := string(do(t, h, http.MethodGet, "/api/push/key", nil, http.StatusOK)); got != `{"enabled":false}`+"\n" && got != `{"enabled":false}` {
		t.Fatalf("key = %q", got)
	}
	h.Login("alice")
	s := webpushtest.New(t).Subscribe()
	body := do(t, h, http.MethodPost, "/api/push/subscriptions", sub(s, false), http.StatusConflict)
	if string(body) != `{"error":"feature_disabled","feature":"push"}`+"\n" && string(body) != `{"error":"feature_disabled","feature":"push"}` {
		t.Fatalf("subscribe = %s", body)
	}
	do(t, h, http.MethodDelete, "/api/push/subscriptions", map[string]string{"endpoint": s.Endpoint}, http.StatusNoContent)
}

func TestSubscribeStateLogoutAndCharging(t *testing.T) {
	h := harness(t)
	svc, pub := enable(t, h)
	var key pushapi.KeyOut
	_ = json.Unmarshal(do(t, h, http.MethodGet, "/api/push/key", nil, http.StatusOK), &key)
	if !key.Enabled || key.Key != pub {
		t.Fatalf("key %+v", key)
	}
	s := svc.Subscribe()
	do(t, h, http.MethodPost, "/api/push/subscriptions", sub(s, false), http.StatusNotFound) // anonymous
	do(t, h, http.MethodGet, "/api/push/subscriptions?endpoint=x", nil, http.StatusNotFound)
	h.Login("alice")
	if st := state(t, h, s.Endpoint); st["subscribed"] {
		t.Fatalf("before: %+v", st)
	}
	writes := func() int { return h.Charges()[limits.RefillBucket(limits.WriteSettings)] }
	before := writes()
	do(t, h, http.MethodPost, "/api/push/subscriptions", sub(s, true), http.StatusNoContent)
	if writes() != before+1 {
		t.Fatal("subscribing is a settings write")
	}
	if st := state(t, h, s.Endpoint); !st["subscribed"] || !st["hide_content"] {
		t.Fatalf("after: %+v", st)
	}
	// Problems about the caller's own input.
	bad := sub(s, false)
	bad["endpoint"] = "https://10.0.0.1/x"
	if b := do(t, h, http.MethodPost, "/api/push/subscriptions", bad, http.StatusUnprocessableEntity); !json.Valid(b) {
		t.Fatal(string(b))
	}
	bad = sub(s, false)
	bad["keys"] = map[string]string{"p256dh": "AAAA", "auth": s.Auth}
	do(t, h, http.MethodPost, "/api/push/subscriptions", bad, http.StatusUnprocessableEntity)
	extra := sub(s, false)
	extra["x"] = 1
	do(t, h, http.MethodPost, "/api/push/subscriptions", extra, http.StatusBadRequest)
	// Logout removes this session's subscription.
	if rec := h.Raw(http.MethodPost, "/api/auth/logout", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	h.Login("alice")
	if st := state(t, h, s.Endpoint); st["subscribed"] {
		t.Fatal("subscription survived logout")
	}
}

func TestEndpointMovesToTheNewAccount(t *testing.T) {
	h := harness(t)
	svc, _ := enable(t, h)
	s := svc.Subscribe()
	h.Login("alice")
	do(t, h, http.MethodPost, "/api/push/subscriptions", sub(s, false), http.StatusNoContent)
	h.Login("bob")
	do(t, h, http.MethodPost, "/api/push/subscriptions", sub(s, false), http.StatusNoContent)
	if !state(t, h, s.Endpoint)["subscribed"] {
		t.Fatal("bob does not hold it")
	}
	h.Login("alice")
	if state(t, h, s.Endpoint)["subscribed"] {
		t.Fatal("alice still holds a moved endpoint")
	}
	do(t, h, http.MethodDelete, "/api/push/subscriptions", map[string]string{"endpoint": s.Endpoint}, http.StatusNoContent)
	h.Login("bob")
	if !state(t, h, s.Endpoint)["subscribed"] {
		t.Fatal("alice's unsubscribe removed bob's subscription")
	}
	do(t, h, http.MethodDelete, "/api/push/subscriptions", map[string]string{"endpoint": s.Endpoint}, http.StatusNoContent)
	if state(t, h, s.Endpoint)["subscribed"] {
		t.Fatal("unsubscribe")
	}
}
