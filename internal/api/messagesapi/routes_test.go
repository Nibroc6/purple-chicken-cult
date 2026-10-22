package messagesapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/messagesapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – bob – carol, xavier alone; carol posts c1 (everyone) and
// c2 (friends only).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "messages", messagesapi.Register)
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "xavier"} {
		w.User(u)
	}
	w.Friends("alice", "bob")
	w.Friends("bob", "carol")
	w.Post("carol", "c1", "for everyone")
	w.Post("carol", "c2", "for friends")
	posts := h.App.Deps().Posts
	m, _ := posts.Meta(context.Background(), w.PostID("c2"))
	m.Audience = 1 // friends only (d1)
	posts.Put(m)
	if err := h.App.Users.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &fixture{h: h, w: w}
}

func (f *fixture) person(viewer, who string) string {
	return string(f.h.App.Deps().Refs.User(f.w.ID(viewer), f.w.ID(who)))
}

func (f *fixture) post(viewer, label string) string {
	return string(f.h.App.Deps().Refs.Post(f.w.ID(viewer), f.w.PostID(label)))
}

func (f *fixture) conv(ref, from, to string) string {
	d := f.h.App.Deps()
	id, _ := d.Refs.OpenConv(f.w.ID(from), gate.Ref(ref))
	return string(d.Refs.Conv(f.w.ID(to), id))
}

func (f *fixture) do(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	rec := f.h.Do(method, path, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec.Body.Bytes()
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSignedOutIsNotFound(t *testing.T) {
	f := newFixture(t)
	f.do(t, http.MethodGet, "/api/messages", nil, http.StatusNotFound)
	f.do(t, http.MethodGet, "/api/messages/unread", nil, http.StatusNotFound)
}

func TestDirectMessagesOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	f.do(t, http.MethodPost, "/api/messages/direct", map[string]any{"to": f.person("alice", "xavier")}, http.StatusNotFound)
	f.do(t, http.MethodPost, "/api/messages/direct", map[string]any{"to": f.person("alice", "carol")}, http.StatusConflict)
	c := decode[gate.ConversationView](t, f.do(t, http.MethodPost, "/api/messages/direct",
		map[string]any{"to": f.person("alice", "bob")}, http.StatusOK))
	base := "/api/messages/" + string(c.Ref)
	f.do(t, http.MethodPost, base+"/messages", map[string]any{"body": ""}, http.StatusUnprocessableEntity)
	f.do(t, http.MethodPost, base+"/messages", map[string]any{"body": "hi", "extra": 1}, http.StatusBadRequest)
	f.do(t, http.MethodPost, base+"/messages", map[string]any{"body": "hi bob"}, http.StatusCreated)
	// alice can see c1 through bob but not c2 (friends only): she cannot share it.
	f.do(t, http.MethodPost, base+"/messages", map[string]any{"body": "", "post": f.post("alice", "c2")}, http.StatusNotFound)
	f.do(t, http.MethodPost, base+"/messages", map[string]any{"body": "", "post": f.post("alice", "c1")}, http.StatusCreated)

	f.h.Login("bob")
	if u := decode[messagesapi.UnreadOut](t, f.do(t, http.MethodGet, "/api/messages/unread", nil, http.StatusOK)); u.Unread != 1 {
		t.Fatalf("unread = %d", u.Unread)
	}
	ls := decode[gate.Page[gate.ConversationView]](t, f.do(t, http.MethodGet, "/api/messages", nil, http.StatusOK))
	if len(ls.Items) != 1 || ls.Items[0].Unread != 2 {
		t.Fatalf("bob's list = %+v", ls)
	}
	bc := "/api/messages/" + f.conv(string(c.Ref), "alice", "bob")
	page := decode[gate.MessagesPage](t, f.do(t, http.MethodGet, bc+"/messages", nil, http.StatusOK))
	if len(page.Items) != 2 || page.Items[0].Post == nil || page.Items[0].Post.Post == nil || page.Items[1].Body != "hi bob" {
		t.Fatalf("bob's messages = %+v", page.Items)
	}
	f.do(t, http.MethodPost, bc+"/read", map[string]any{"up_to": string(page.Items[0].Ref)}, http.StatusNoContent)
	f.do(t, http.MethodPut, bc+"/mute", nil, http.StatusNoContent)
	f.do(t, http.MethodDelete, bc+"/mute", nil, http.StatusNoContent)
	// bob shares c2 (he can see it); alice gets the placeholder only.
	f.do(t, http.MethodPost, bc+"/messages", map[string]any{"body": "", "post": f.post("bob", "c2")}, http.StatusCreated)
	// bob cannot delete alice's message.
	f.do(t, http.MethodDelete, bc+"/messages/"+string(page.Items[1].Ref), nil, http.StatusNotFound)

	f.h.Login("alice")
	page = decode[gate.MessagesPage](t, f.do(t, http.MethodGet, base+"/messages", nil, http.StatusOK))
	if page.Items[0].Post == nil || !page.Items[0].Post.Unavailable || page.Items[0].Post.Post != nil {
		t.Fatalf("a friends-only post leaked: %+v", page.Items[0])
	}
	f.do(t, http.MethodDelete, base+"/messages/"+string(page.Items[2].Ref), nil, http.StatusNoContent)
	f.do(t, http.MethodDelete, base, nil, http.StatusNoContent)
	if ls := decode[gate.Page[gate.ConversationView]](t, f.do(t, http.MethodGet, "/api/messages", nil, http.StatusOK)); len(ls.Items) != 0 {
		t.Fatalf("a cleared conversation is still listed: %+v", ls)
	}
	f.do(t, http.MethodGet, "/api/messages/garbage", nil, http.StatusNotFound)
}

func TestGroupsOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.h.Login("bob")
	f.do(t, http.MethodPost, "/api/messages/groups", map[string]any{"members": []string{}}, http.StatusUnprocessableEntity)
	g := decode[gate.ConversationView](t, f.do(t, http.MethodPost, "/api/messages/groups",
		map[string]any{"title": "plans", "members": []string{f.person("bob", "alice"), f.person("bob", "carol")}}, http.StatusOK))
	if g.Kind != "group" || g.Title != "plans" || !g.Admin {
		t.Fatalf("group = %+v", g)
	}
	base := "/api/messages/" + string(g.Ref)
	f.do(t, http.MethodPatch, base, map[string]any{"title": "weekend"}, http.StatusOK)
	f.do(t, http.MethodPost, base+"/members", map[string]any{"people": []string{f.person("bob", "alice")}}, http.StatusOK)

	f.h.Login("alice")
	ac := "/api/messages/" + f.conv(string(g.Ref), "bob", "alice")
	inv := decode[gate.ConversationView](t, f.do(t, http.MethodGet, ac, nil, http.StatusOK))
	if inv.State != "invited" || inv.InvitedBy == nil || len(inv.Members) != 0 {
		t.Fatalf("invitation = %+v", inv)
	}
	f.do(t, http.MethodGet, ac+"/messages", nil, http.StatusConflict)
	f.do(t, http.MethodPost, ac+"/join", nil, http.StatusOK)
	f.do(t, http.MethodPost, ac+"/messages", map[string]any{"body": "count me in"}, http.StatusCreated)
	f.do(t, http.MethodDelete, ac+"/members/"+f.person("alice", "bob"), nil, http.StatusConflict) // not admin

	f.h.Login("bob")
	f.do(t, http.MethodDelete, base+"/members/"+f.person("bob", "carol"), nil, http.StatusNoContent)
	page := decode[gate.MessagesPage](t, f.do(t, http.MethodGet, base+"/messages", nil, http.StatusOK))
	if len(page.Items) == 0 || page.Items[0].Body != "count me in" {
		t.Fatalf("group messages = %+v", page.Items)
	}
}
