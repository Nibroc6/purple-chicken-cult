package moderation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/moderation"
)

func (e *env) admin(h string, fresh bool) context.Context {
	at := e.d.Clock.Now()
	if !fresh {
		at = at.Add(-time.Hour)
	}
	return core.WithViewer(e.ctx, core.Viewer{ID: e.id(h), Kind: core.SessUser, Role: core.RoleAdmin, AuthAt: at})
}

func (e *env) roleOf(h string) core.Role {
	return core.Role(e.count("SELECT role FROM users WHERE id = $1", int64(e.id(h)))) //nolint:gosec // 0..2
}

func (e *env) roleLog(action, h string) int {
	return e.count("SELECT count(*) FROM mod_actions WHERE action = $1 AND target_user = $2 AND state = 1 AND reason = 'role_change'",
		action, int64(e.id(h)))
}

// Admins list staff, grant and revoke moderators with a fresh step-up; every
// change is in the moderation log; nobody else sees the page; the admin role
// itself never changes here.
func TestRoles(t *testing.T) {
	e := newEnv(t)
	if err := e.m.GrantModerator(e.ctx, "alice", "", true); err != nil {
		t.Fatal(err)
	}
	if n := e.roleLog(moderation.ActionGrantAdmin, "alice"); n != 1 {
		t.Fatalf("bootstrap logged %d", n)
	}
	// Members and moderators get the uniform 404.
	for _, ctx := range []context.Context{e.as("carol"), e.mod("mo1", true)} {
		if _, err := e.m.Staff(ctx); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("staff: %v", err)
		}
		if err := e.m.GrantModeratorRole(ctx, "carol"); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("grant: %v", err)
		}
		if err := e.m.RevokeModerator(ctx, e.person("mo1", "mo2")); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("revoke: %v", err)
		}
	}
	st, err := e.m.Staff(e.admin("alice", false))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Members) != 3 || st.Members[0].Handle != "alice" || st.Members[0].Role != "admin" || !st.Members[0].Me ||
		st.Members[0].Since == nil || st.Members[1].Handle != "mo1" || st.Members[1].Role != "moderator" || st.Members[1].Since != nil {
		t.Fatalf("members %+v", st.Members)
	}
	if len(st.Changes) != 1 || st.Changes[0].Action != moderation.ActionGrantAdmin || st.Changes[0].ByHandle != "alice" {
		t.Fatalf("changes %+v", st.Changes)
	}

	// Grant: step-up first, then the closed problems.
	if err := e.m.GrantModeratorRole(e.admin("alice", false), "carol"); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale grant: %v", err)
	}
	problem(t, e.m.GrantModeratorRole(e.admin("alice", true), "nobody"), "handle_unknown")
	problem(t, e.m.GrantModeratorRole(e.admin("alice", true), "mo1"), "already_staff")
	problem(t, e.m.GrantModeratorRole(e.admin("alice", true), "alice"), "already_staff")
	e.advance(time.Minute)
	if err := e.m.GrantModeratorRole(e.admin("alice", true), " @Carol"); err != nil {
		t.Fatal(err)
	}
	if e.roleOf("carol") != core.RoleModerator || e.roleLog(moderation.ActionGrantModerator, "carol") != 1 {
		t.Fatal("carol not granted and logged")
	}

	// Revoke: a foreign or garbage ref is not found; admins are CLI only.
	ref := e.person("alice", "mo1")
	if err := e.m.RevokeModerator(e.admin("alice", true), e.person("bob", "mo1")); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("foreign ref: %v", err)
	}
	if err := e.m.RevokeModerator(e.admin("alice", false), ref); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale revoke: %v", err)
	}
	problem(t, e.m.RevokeModerator(e.admin("alice", true), e.person("alice", "alice")), "admin_role")
	problem(t, e.m.RevokeModerator(e.admin("alice", true), e.person("alice", "bob")), "not_moderator")
	e.report("carol", "person", e.person("carol", "bob"), "spam")
	it := e.queue("mo1")[0]
	if err := e.m.Claim(e.mod("mo1", false), it.ID); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	if err := e.m.RevokeModerator(e.admin("alice", true), ref); err != nil {
		t.Fatal(err)
	}
	if e.roleOf("mo1") != core.RoleMember || e.roleLog(moderation.ActionRevokeModerator, "mo1") != 1 {
		t.Fatal("mo1 not revoked and logged")
	}
	if n := e.count("SELECT count(*) FROM mod_queue WHERE claimed_by = $1", int64(e.id("mo1"))); n != 0 {
		t.Fatalf("claims kept: %d", n)
	}
	problem(t, e.m.RevokeModerator(e.admin("alice", true), ref), "not_moderator")

	st, err = e.m.Staff(e.admin("alice", false))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Members) != 3 || st.Members[1].Handle != "carol" || st.Members[2].Handle != "mo2" {
		t.Fatalf("members after %+v", st.Members)
	}
	if len(st.Changes) != 3 || st.Changes[0].Action != moderation.ActionRevokeModerator || st.Changes[0].Handle != "mo1" ||
		st.Changes[1].Action != moderation.ActionGrantModerator || st.Changes[1].Handle != "carol" {
		t.Fatalf("changes after %+v", st.Changes)
	}

	// The public log lists the role changes like any confirmed action.
	page, err := e.m.Log(e.as("xavier"), "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, li := range page.Items {
		if li.Reason == moderation.ReasonRoleChange {
			seen[li.Action] = true
		}
	}
	if !seen[moderation.ActionGrantAdmin] || !seen[moderation.ActionGrantModerator] || !seen[moderation.ActionRevokeModerator] {
		t.Fatalf("log %+v", page.Items)
	}
}
