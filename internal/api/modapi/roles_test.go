package modapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// The admin roles routes, validated against the fragment: members and
// moderators get the uniform 404; an admin lists staff, grants and revokes a
// moderator with a fresh step-up, and every change is in the moderation log.
func TestRolesRoutes(t *testing.T) {
	f := newModFixture(t)
	ctx := context.Background()
	d := f.h.App.Deps()
	if _, err := d.DB.Q().Exec(ctx, "UPDATE users SET role = 2 WHERE id = $1", int64(f.w.ID("alice"))); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"carol", "mo1"} {
		f.h.Login(h)
		f.do(t, http.MethodGet, "/api/mod/roles", nil, http.StatusNotFound)
		f.do(t, http.MethodPost, "/api/mod/roles", map[string]string{"handle": "carol"}, http.StatusNotFound)
		f.do(t, http.MethodPost, "/api/mod/roles/"+f.person(h, "mo2")+"/revoke", nil, http.StatusNotFound)
	}

	f.h.Login("alice")
	st := decode[gate.Staff](t, f.do(t, http.MethodGet, "/api/mod/roles", nil, http.StatusOK))
	if len(st.Members) != 3 || st.Members[0].Handle != "alice" || !st.Members[0].Me || len(st.Changes) != 0 {
		t.Fatalf("staff %+v", st)
	}
	f.do(t, http.MethodPost, "/api/mod/roles", map[string]any{"handle": "carol", "role": "admin"}, http.StatusBadRequest)
	b := f.do(t, http.MethodPost, "/api/mod/roles", map[string]string{"handle": "nobody"}, http.StatusUnprocessableEntity)
	if !strings.Contains(string(b), "handle_unknown") {
		t.Fatalf("%s", b)
	}
	f.do(t, http.MethodPost, "/api/mod/roles", map[string]string{"handle": "mo1"}, http.StatusConflict)
	f.do(t, http.MethodPost, "/api/mod/roles", map[string]string{"handle": "carol"}, http.StatusNoContent)
	f.do(t, http.MethodPost, "/api/mod/roles/"+f.person("alice", "alice")+"/revoke", nil, http.StatusConflict)
	f.do(t, http.MethodPost, "/api/mod/roles/"+f.person("alice", "mo1")+"/revoke", nil, http.StatusNoContent)
	st = decode[gate.Staff](t, f.do(t, http.MethodGet, "/api/mod/roles", nil, http.StatusOK))
	if len(st.Members) != 3 || st.Members[1].Handle != "carol" || st.Members[1].Since == nil || len(st.Changes) != 2 {
		t.Fatalf("staff after %+v", st)
	}

	// A stale session is asked to step up.
	f.advance(t, time.Hour)
	b = f.do(t, http.MethodPost, "/api/mod/roles/"+f.person("alice", "mo2")+"/revoke", nil, http.StatusUnauthorized)
	if !strings.Contains(string(b), "step_up_required") {
		t.Fatalf("%s", b)
	}
	var n int
	if err := d.DB.Q().QueryRow(ctx, `SELECT count(*) FROM mod_actions WHERE state = 1 AND reason = 'role_change'
		AND action IN ('grant_moderator', 'revoke_moderator')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("logged %d %v", n, err)
	}
}
