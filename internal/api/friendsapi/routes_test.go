package friendsapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/api/apitest"
	"github.com/Nibroc6/purple-chicken-cult/internal/api/friendsapi"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type fixture struct {
	h *apitest.Harness
	w *wiretest.TestWorld
}

// newFixture: alice – carol – bob (alice and bob at distance 2), and x – y in
// another component.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := apitest.Service(t, "friends", friendsapi.Register)
	h.App.Deps().Requests = h.App.Friends
	w := wiretest.World(t, h.App)
	for _, u := range []string{"alice", "bob", "carol", "xavier", "yara"} {
		w.User(u)
	}
	w.Friends("alice", "carol")
	w.Friends("carol", "bob")
	w.Friends("xavier", "yara")
	return &fixture{h: h, w: w}
}

// ref is viewer's ref for target.
func (f *fixture) ref(viewer, target string) string {
	return string(f.h.App.Deps().Refs.User(f.w.ID(viewer), f.w.ID(target)))
}

func (f *fixture) do(t *testing.T, method, path string, want int) []byte {
	t.Helper()
	rec := f.h.Do(method, path, nil)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec.Body.Bytes()
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

type person struct {
	Ref      string `json:"ref"`
	Handle   string `json:"handle"`
	Relation string `json:"relation"`
	Request  string `json:"request"`
	Blocked  bool   `json:"blocked"`
	Muted    bool   `json:"muted"`
}

type requests struct {
	Incoming []person `json:"incoming"`
	Outgoing []person `json:"outgoing"`
}

func TestAnonymousGetsUniformNotFound(t *testing.T) {
	f := newFixture(t)
	r := f.ref("alice", "bob")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/friends"}, {http.MethodGet, "/api/friends/requests"}, {http.MethodGet, "/api/blocks"},
		{http.MethodGet, "/api/suggestions"}, {http.MethodGet, "/api/people/" + r},
		{http.MethodPost, "/api/people/" + r + "/request"}, {http.MethodPut, "/api/people/" + r + "/block"},
	} {
		if rec := f.h.Do(c.method, c.path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != `{"error":"not_found"}`+"\n" {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestRequestAcceptUnfriendFlow(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	b := f.do(t, http.MethodPost, "/api/people/"+f.ref("alice", "bob")+"/request", http.StatusAccepted)
	if string(b) != `{"state":"outgoing"}`+"\n" {
		t.Fatalf("request body %s", b)
	}
	rq := decode[requests](t, f.do(t, http.MethodGet, "/api/friends/requests", http.StatusOK))
	if len(rq.Outgoing) != 1 || rq.Outgoing[0].Handle != "bob" || rq.Outgoing[0].Request != "outgoing" {
		t.Fatalf("alice requests %+v", rq)
	}

	f.h.Login("bob")
	rq = decode[requests](t, f.do(t, http.MethodGet, "/api/friends/requests", http.StatusOK))
	if len(rq.Incoming) != 1 || rq.Incoming[0].Handle != "alice" {
		t.Fatalf("bob requests %+v", rq)
	}
	f.do(t, http.MethodPost, "/api/friends/requests/"+rq.Incoming[0].Ref+"/accept", http.StatusNoContent)
	page := decode[struct {
		Items []person `json:"items"`
	}](t, f.do(t, http.MethodGet, "/api/friends", http.StatusOK))
	if len(page.Items) != 2 {
		t.Fatalf("bob friends %+v", page)
	}
	p := decode[person](t, f.do(t, http.MethodGet, "/api/people/"+f.ref("bob", "alice"), http.StatusOK))
	if p.Relation != "friend" {
		t.Fatalf("person %+v", p)
	}
	f.do(t, http.MethodDelete, "/api/friends/"+f.ref("bob", "alice"), http.StatusNoContent)
	f.do(t, http.MethodDelete, "/api/friends/"+f.ref("bob", "alice"), http.StatusNotFound)
	p = decode[person](t, f.do(t, http.MethodGet, "/api/people/"+f.ref("bob", "alice"), http.StatusOK))
	if p.Relation != "connected" {
		t.Fatalf("after unfriend %+v", p)
	}
}

// The requester's answers are byte-identical whether the target blocked them or not.
func TestRequestToBlockerLooksPending(t *testing.T) {
	bodies := map[bool][2]string{}
	for _, blocked := range []bool{false, true} {
		f := newFixture(t)
		if blocked {
			f.h.Login("bob")
			f.do(t, http.MethodPut, "/api/people/"+f.ref("bob", "alice")+"/block", http.StatusNoContent)
		}
		f.h.Login("alice")
		sent := f.do(t, http.MethodPost, "/api/people/"+f.ref("alice", "bob")+"/request", http.StatusAccepted)
		list := f.do(t, http.MethodGet, "/api/friends/requests", http.StatusOK)
		bodies[blocked] = [2]string{string(sent), string(list)}
		if blocked {
			f.h.Login("bob")
			rq := decode[requests](t, f.do(t, http.MethodGet, "/api/friends/requests", http.StatusOK))
			if len(rq.Incoming) != 0 {
				t.Fatalf("blocker sees %+v", rq)
			}
			bl := decode[struct {
				Items []person `json:"items"`
			}](t, f.do(t, http.MethodGet, "/api/blocks", http.StatusOK))
			if len(bl.Items) != 1 || !bl.Items[0].Blocked || bl.Items[0].Handle != "alice" {
				t.Fatalf("blocks %+v", bl)
			}
			f.do(t, http.MethodDelete, "/api/people/"+bl.Items[0].Ref+"/block", http.StatusNoContent)
		}
	}
	if bodies[false] != bodies[true] {
		t.Fatalf("requester answers differ:\n%v\n%v", bodies[false], bodies[true])
	}
}

func TestOtherComponentIsNotFound(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	nf := `{"error":"not_found"}` + "\n"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/people/" + f.ref("alice", "xavier")},
		{http.MethodGet, "/api/people/" + f.ref("alice", "xavier") + "/posts"},
		{http.MethodPost, "/api/people/" + f.ref("alice", "xavier") + "/request"},
		{http.MethodPut, "/api/people/" + f.ref("alice", "yara") + "/mute"},
		{http.MethodPut, "/api/people/" + f.ref("alice", "yara") + "/block"},
		{http.MethodGet, "/api/people/" + f.ref("bob", "alice")}, // another viewer's ref
		{http.MethodGet, "/api/people/garbage"},
	} {
		if rec := f.h.Do(c.method, c.path, nil); rec.Code != http.StatusNotFound || rec.Body.String() != nf {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestProblemsAndMute(t *testing.T) {
	f := newFixture(t)
	f.h.Login("alice")
	self := "/api/people/" + f.ref("alice", "alice")
	for _, path := range []string{self + "/request", self + "/block", self + "/mute"} {
		method := http.MethodPut
		if path == self+"/request" {
			method = http.MethodPost
		}
		rec := f.h.Do(method, path, nil)
		if rec.Code != http.StatusUnprocessableEntity || decode[struct {
			Error string `json:"error"`
		}](t, rec.Body.Bytes()).Error != "self_not_allowed" {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body)
		}
	}
	rec := f.h.Do(http.MethodPost, "/api/people/"+f.ref("alice", "carol")+"/request", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("already friends = %d %s", rec.Code, rec.Body)
	}
	f.do(t, http.MethodPut, "/api/people/"+f.ref("alice", "bob")+"/mute", http.StatusNoContent)
	p := decode[person](t, f.do(t, http.MethodGet, "/api/people/"+f.ref("alice", "bob"), http.StatusOK))
	if !p.Muted {
		t.Fatalf("person %+v", p)
	}
	f.do(t, http.MethodDelete, "/api/people/"+f.ref("alice", "bob")+"/mute", http.StatusNoContent)
	f.do(t, http.MethodGet, "/api/suggestions", http.StatusOK)
	f.do(t, http.MethodGet, "/api/people/"+f.ref("alice", "bob")+"/posts", http.StatusOK)
	f.do(t, http.MethodDelete, "/api/people/"+f.ref("alice", "bob")+"/request", http.StatusNotFound)
}
