package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/account"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
)

type env struct {
	t   *testing.T
	a   *wire.App
	d   *core.Deps
	w   *wiretest.TestWorld
	rec *coreref.RecordingNotifier
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	a := wiretest.NewApp(t)
	d := a.Deps()
	rec := &coreref.RecordingNotifier{D: d}
	d.Notify = rec
	return &env{t: t, a: a, d: d, w: wiretest.World(t, a), rec: rec, ctx: context.Background()}
}

func (e *env) now() time.Time { return e.d.Clock.Now() }

// as is a signed-in session whose last passkey verification was at authAt.
func (e *env) as(id core.UserID, authAt time.Time) context.Context {
	m, err := e.a.Users.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return core.WithViewer(e.ctx, core.Viewer{ID: id, Kind: core.SessUser, Role: m.Role, AuthAt: authAt})
}

func (e *env) fresh(id core.UserID) context.Context { return e.as(id, e.now()) }

func (e *env) stale(id core.UserID) context.Context {
	return e.as(id, e.now().Add(-core.FreshFor-time.Second))
}

func (e *env) advance(d time.Duration) {
	e.t.Helper()
	if err := e.a.Hooks().AdvanceClock(d); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Q().Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Q().QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) moderator(handle string) core.UserID {
	id := e.w.User(handle)
	e.exec("UPDATE users SET role = 1 WHERE id = $1", int64(id))
	e.a.Users.Invalidate(id)
	return id
}

func wantProblem(t *testing.T, err error, code string) {
	t.Helper()
	var p *gate.Problem
	if !errors.As(err, &p) || p.Code != code {
		t.Fatalf("err = %v, want Problem %s", err, code)
	}
}

func ptr[T any](v T) *T { return &v }

func raw(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

// Acceptance: settings reject translate_to not in languages and rank_preset
// top; plus the rest of the settings validation.
func TestUpdateSettingsValidation(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	ctx := e.fresh(alice)

	cases := []struct {
		name string
		p    gate.SettingsPatch
		code string
	}{
		{"translate_to not in languages", gate.SettingsPatch{TranslateTo: ptr("de")}, "translate_to_not_in_languages"},
		{"translate_to with other languages", gate.SettingsPatch{Languages: &[]string{"fr", "es"}, TranslateTo: ptr("de")}, "translate_to_not_in_languages"},
		{"preset top (Phase 1)", gate.SettingsPatch{RankPreset: ptr("top")}, "preset_unavailable"},
		{"preset unknown", gate.SettingsPatch{RankPreset: ptr("chronological")}, "preset_unavailable"},
		{"locale not BCP-47", gate.SettingsPatch{UILocale: ptr("english please")}, "locale_invalid"},
		{"language not BCP-47", gate.SettingsPatch{Languages: &[]string{"en", "xx-!!"}}, "lang_invalid"},
		{"no languages", gate.SettingsPatch{Languages: &[]string{}}, "lang_invalid"},
		{"und language", gate.SettingsPatch{Languages: &[]string{"und"}}, "lang_invalid"},
		{"unknown format", gate.SettingsPatch{UIPrefs: raw(`{"format":"grid"}`)}, "ui_prefs_invalid"},
		{"unknown pref", gate.SettingsPatch{UIPrefs: raw(`{"theme":"dark"}`)}, "ui_prefs_invalid"},
		{"bad density", gate.SettingsPatch{UIPrefs: raw(`{"density":"huge"}`)}, "ui_prefs_invalid"},
		{"bad a11y", gate.SettingsPatch{UIPrefs: raw(`{"a11y":{"text_scale":"giant"}}`)}, "ui_prefs_invalid"},
		{"a11y type", gate.SettingsPatch{UIPrefs: raw(`{"a11y":{"reduced_motion":"yes"}}`)}, "ui_prefs_invalid"},
		{"prefs not an object", gate.SettingsPatch{UIPrefs: raw(`["cards"]`)}, "ui_prefs_invalid"},
		{"reserved glyph", gate.SettingsPatch{DisplayName: ptr("Alice ✓")}, "display_name_invalid"},
		{"bidi override", gate.SettingsPatch{DisplayName: ptr("Alice" + string(rune(0x202e)) + "ecila")}, "display_name_invalid"},
		{"zero width", gate.SettingsPatch{DisplayName: ptr("Al" + string(rune(0x200b)) + "ice")}, "display_name_invalid"},
		{"newline", gate.SettingsPatch{DisplayName: ptr("Al\nice")}, "display_name_invalid"},
		{"too long", gate.SettingsPatch{DisplayName: ptr(strings.Repeat("é", 51))}, "display_name_invalid"},
	}
	for _, c := range cases {
		_, err := e.a.Account.UpdateSettings(ctx, c.p)
		var p *gate.Problem
		if !errors.As(err, &p) || p.Code != c.code {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.code)
		}
	}
	me, err := e.a.Account.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if me.Settings.RankPreset != "balanced" || me.Settings.TranslateTo != "" || string(me.Settings.UIPrefs) != "{}" {
		t.Fatalf("rejected patches changed settings: %+v", me.Settings)
	}

	sv, err := e.a.Account.UpdateSettings(ctx, gate.SettingsPatch{
		DisplayName: ptr("  Alice Ⅱ  "), UILocale: ptr("pt-br"), Languages: &[]string{"en", "fr-ca", "en"},
		TranslateTo: ptr("fr-CA"), Autotranslate: ptr(true), RankPreset: ptr("latest"), NameInChains: ptr(true),
		Suggestible: ptr(false), NamedInLikes: ptr(false), SlipperyDefault: ptr(true),
		UIPrefs: raw(`{"format":"compact","density":"compact","a11y":{"contrast":"high","text_scale":"large","reduced_motion":true,"dyslexia_font":false}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sv.UILocale != "pt-BR" || strings.Join(sv.Languages, ",") != "en,fr-CA" || sv.TranslateTo != "fr-CA" ||
		!sv.Autotranslate || sv.RankPreset != "latest" || !sv.NameInChains || sv.Suggestible || sv.NamedInLikes || !sv.SlipperyDefault {
		t.Fatalf("settings %+v", sv)
	}
	var prefs map[string]any
	if err := json.Unmarshal(sv.UIPrefs, &prefs); err != nil || prefs["format"] != "compact" {
		t.Fatalf("prefs %s %v", sv.UIPrefs, err)
	}
	me, err = e.a.Account.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if me.DisplayName != "Alice Ⅱ" || me.Settings.UILocale != "pt-BR" || !sameJSON(t, me.Settings.UIPrefs, sv.UIPrefs) {
		t.Fatalf("me %+v", me)
	}
	m, _ := e.a.Users.Get(e.ctx, alice)
	if m.Preset != core.PresetLatest || m.DisplayName != "Alice Ⅱ" || !m.NameInChains {
		t.Fatalf("users cache not invalidated: %+v", m)
	}
	// Dropping the translate_to language clears it; an empty translate_to clears it too.
	sv, err = e.a.Account.UpdateSettings(ctx, gate.SettingsPatch{Languages: &[]string{"en"}})
	if err != nil || sv.TranslateTo != "" {
		t.Fatalf("translate_to kept: %+v %v", sv, err)
	}
	if _, err := e.a.Account.UpdateSettings(ctx, gate.SettingsPatch{TranslateTo: ptr("en")}); err != nil {
		t.Fatal(err)
	}
	if sv, err = e.a.Account.UpdateSettings(ctx, gate.SettingsPatch{TranslateTo: ptr("")}); err != nil || sv.TranslateTo != "" {
		t.Fatalf("clear translate_to: %+v %v", sv, err)
	}
	// Settings need a signed-in user.
	if _, err := e.a.Account.UpdateSettings(e.ctx, gate.SettingsPatch{}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous: %v", err)
	}
}

// Acceptance: export/delete without fresh auth ⇒ step_up_required.
func TestExportAndDeleteNeedFreshAuth(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	var buf bytes.Buffer
	if err := e.a.Account.Export(e.stale(alice), &buf); !errors.Is(err, gate.ErrStepUp) || buf.Len() != 0 {
		t.Fatalf("stale export: %v", err)
	}
	if err := e.a.Account.Delete(e.stale(alice), "alice"); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := e.a.Account.Delete(e.as(alice, time.Time{}), "alice"); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("never verified: %v", err)
	}
	if err := e.a.Account.Export(e.ctx, &buf); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous export: %v", err)
	}
	if err := e.a.Account.Export(e.fresh(alice), &buf); err != nil || buf.Len() == 0 {
		t.Fatalf("fresh export: %v", err)
	}
	m, _ := e.a.Users.Get(e.ctx, alice)
	if m.Status != core.StatusActive {
		t.Fatal("stale delete changed the account")
	}
}

// Acceptance: deleted account: posts tombstoned, edges revoked, invitees show
// the deleted label, handle not reusable.
func TestDeleteAccount(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	bob := e.w.User("bob", coretest.InvitedBy(alice))
	carol := e.w.User("carol")
	for _, f := range []core.UserID{bob, carol} {
		if err := e.d.Edges.Create(e.ctx, nil, alice, f, core.LevelFriend, []byte("h")); err != nil {
			t.Fatal(err)
		}
		e.d.Graph.AddEdge(alice, f, core.LevelFriend, e.now())
	}
	p1 := e.w.Post("alice", "p1", "first post")
	e.w.Post("alice", "p2", "second post")
	e.w.Post("bob", "b1", "bob's post")
	var deletedPosts []core.PostID
	core.Subscribe(e.d.Bus, func(ev core.PostDeleted) { deletedPosts = append(deletedPosts, ev.ID) })
	var accountDeleted []core.UserID
	core.Subscribe(e.d.Bus, func(ev core.AccountDeleted) { accountDeleted = append(accountDeleted, ev.ID) })
	tok, err := e.a.Auth.IssueSession(e.ctx, nil, core.SessUser, alice, e.now())
	if err != nil || tok == "" {
		t.Fatal(err)
	}

	wantProblem(t, e.a.Account.Delete(e.fresh(alice), "bob"), "confirm_mismatch")
	if err := e.a.Account.Delete(e.fresh(alice), " @Alice "); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE user_id = $1", int64(alice)); n != 0 {
		t.Fatalf("%d sessions left", n)
	}
	if _, err := e.a.Account.Me(e.fresh(alice)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("me after delete: %v", err)
	}
	if err := e.a.Account.Delete(e.fresh(alice), "alice"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}

	// Posts tombstoned: body and salt gone, a signed Tombstone each, index updated.
	if n := e.count("SELECT count(*) FROM posts WHERE author_id = $1 AND (deleted_at IS NULL OR body IS NOT NULL OR body_salt IS NOT NULL)", int64(alice)); n != 0 {
		t.Fatalf("%d live posts left", n)
	}
	if n := e.count("SELECT count(*) FROM posts WHERE author_id = $1 AND deleted_at IS NULL", int64(bob)); n != 1 {
		t.Fatal("bob's post was touched")
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1 AND author_id = $2", int16(core.RecTombstone), int64(alice)); n != 2 {
		t.Fatalf("%d tombstones", n)
	}
	if m, ok := e.d.Posts.Meta(e.ctx, p1); ok && !m.Deleted {
		t.Fatal("post index still has the post")
	}
	if len(deletedPosts) != 2 || len(accountDeleted) != 1 || accountDeleted[0] != alice {
		t.Fatalf("events: posts %v account %v", deletedPosts, accountDeleted)
	}
	// Edges revoked.
	if n, _, _ := e.d.Edges.Count(e.ctx, alice); n != 0 {
		t.Fatalf("%d edges left", n)
	}
	if _, ok := e.d.Graph.Level(alice, bob); ok || e.d.Graph.Degree(alice) != 0 {
		t.Fatal("graph still has alice's edges")
	}
	// Friends were told.
	for _, f := range []core.UserID{bob, carol} {
		evs := e.rec.For(f)
		if len(evs) != 1 || evs[0].Kind != "friend_security" || evs[0].SubjectUser != alice {
			t.Fatalf("notifications of %d: %+v", f, evs)
		}
	}
	// Invitees show the deleted label.
	if l, err := e.a.Users.InviterLabel(e.ctx, bob); err != nil || l.Kind != "deleted" || l.Handle != "" {
		t.Fatalf("bob's inviter label %+v %v", l, err)
	}
	// Handle not reusable.
	if e.a.Users.HandleAvailable(e.ctx, "alice") {
		t.Fatal("handle available again")
	}
	if _, err := e.a.Users.Create(e.ctx, e.d.DB.Q(), core.NewUser{Handle: "Alice", Genesis: true}); err == nil {
		t.Fatal("handle reused")
	} else {
		wantProblem(t, err, "handle_taken")
	}
	// Moderator-only stub.
	var invitees []int64
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT invitees FROM account_stubs WHERE user_id = $1", int64(alice)).Scan(&invitees); err != nil {
		t.Fatal(err)
	}
	if len(invitees) != 1 || invitees[0] != int64(bob) {
		t.Fatalf("stub invitees %v", invitees)
	}
	m, _ := e.a.Users.Get(e.ctx, alice)
	if m.Status != core.StatusDeleted || m.DisplayName != "" {
		t.Fatalf("user row %+v", m)
	}
	// The job is idempotent.
	if err := e.a.Deps().Jobs.Enqueue(e.ctx, nil, "account.delete", map[string]int64{"user": int64(alice)}, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM signed_records WHERE kind = $1", int16(core.RecTombstone)); n != 2 {
		t.Fatalf("tombstones after rerun: %d", n)
	}
	// The job never touches a live account.
	if err := e.a.Deps().Jobs.Enqueue(e.ctx, nil, "account.delete", map[string]int64{"user": int64(carol)}, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	if m, _ := e.a.Users.Get(e.ctx, carol); m.Status != core.StatusActive {
		t.Fatal("carol deleted")
	}
}

// Acceptance: recovery requires two different moderators with fresh
// step-ups, the link is only available after 24 h, and a cancel from the
// account's live session voids it.
func TestManualRecovery(t *testing.T) {
	e := newEnv(t)
	mod1, mod2 := e.moderator("mod_one"), e.moderator("mod_two")
	alice := e.w.User("alice")
	friend := e.w.User("friend")
	e.d.Graph.AddEdge(alice, friend, core.LevelFriend, e.now())
	member := e.w.User("member")
	subject := e.d.Refs.ModUser(mod1, alice)

	// Members do not see moderator surfaces.
	if _, err := e.a.Account.RecoveryRequest(e.fresh(member), e.d.Refs.ModUser(member, alice)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("member request: %v", err)
	}
	// Moderator 1 needs a fresh step-up, and a ref minted for them.
	if _, err := e.a.Account.RecoveryRequest(e.stale(mod1), subject); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale request: %v", err)
	}
	if _, err := e.a.Account.RecoveryRequest(e.fresh(mod2), subject); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("foreign ref: %v", err)
	}
	if _, err := e.a.Account.RecoveryRequest(e.fresh(mod1), e.d.Refs.ModUser(mod1, mod1)); err == nil {
		t.Fatal("self recovery")
	} else {
		wantProblem(t, err, "recovery_self")
	}
	id, err := e.a.Account.RecoveryRequest(e.fresh(mod1), subject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Account.RecoveryRequest(e.fresh(mod1), subject); err == nil {
		t.Fatal("second open request")
	} else {
		wantProblem(t, err, "recovery_exists")
	}
	if at, err := e.a.Account.RecoveryPending(e.fresh(alice)); err != nil || at != nil {
		t.Fatalf("banner before approval: %v %v", at, err)
	}
	// The same moderator cannot approve twice; moderator 2 must be fresh.
	wantProblem(t, e.a.Account.RecoveryApprove(e.fresh(mod1), id), "second_moderator_required")
	if err := e.a.Account.RecoveryApprove(e.stale(mod2), id); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale approve: %v", err)
	}
	if err := e.a.Account.RecoveryApprove(e.fresh(member), id); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("member approve: %v", err)
	}
	if err := e.a.Account.RecoveryApprove(e.fresh(mod2), "garbage"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod1), id); err == nil {
		t.Fatal("link before approval")
	} else {
		wantProblem(t, err, "recovery_held")
	}
	if err := e.a.Account.RecoveryApprove(e.fresh(mod2), id); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.a.Account.RecoveryApprove(e.fresh(mod2), id), "recovery_not_open")
	// Both moderators are recorded; friends are told; the account sees the banner.
	var first, second int64
	var secondAuth []byte
	if err := e.d.DB.Q().QueryRow(e.ctx, `SELECT moderator_id, second_moderator, second_auth FROM mod_actions
		WHERE action = 'recovery_approve' AND target_user = $1`, int64(alice)).Scan(&first, &second, &secondAuth); err != nil {
		t.Fatal(err)
	}
	if first != int64(mod1) || second != int64(mod2) || !bytes.Contains(secondAuth, []byte("auth_at")) {
		t.Fatalf("mod action %d %d %s", first, second, secondAuth)
	}
	if evs := e.rec.For(friend); len(evs) != 1 || evs[0].Kind != "friend_security" {
		t.Fatalf("friend notifications %+v", evs)
	}
	at, err := e.a.Account.RecoveryPending(e.fresh(alice))
	if err != nil || at == nil || !at.Equal(e.now().Add(24*time.Hour)) {
		t.Fatalf("banner %v %v", at, err)
	}
	// The link waits for the 24 h hold and is for moderator 2 only.
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id); err == nil {
		t.Fatal("link during the hold")
	} else {
		wantProblem(t, err, "recovery_held")
	}
	e.advance(24*time.Hour - time.Minute)
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id); err == nil {
		t.Fatal("link a minute early")
	} else {
		wantProblem(t, err, "recovery_held")
	}
	if _, err := e.a.Auth.IssueSession(e.ctx, nil, core.SessUser, alice, e.now()); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	// Release ends every session of the account.
	if n := e.count("SELECT count(*) FROM sessions WHERE user_id = $1", int64(alice)); n != 0 {
		t.Fatalf("%d sessions survive the release", n)
	}
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod1), id); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("moderator 1 link: %v", err)
	}
	if _, err := e.a.Account.RecoveryLink(e.stale(mod2), id); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale link: %v", err)
	}
	u, err := e.a.Account.RecoveryLink(e.fresh(mod2), id)
	if err != nil || !strings.HasPrefix(u, e.d.Cfg.Origin+"/enroll#") || len(u) < len(e.d.Cfg.Origin)+20 {
		t.Fatalf("link %q %v", u, err)
	}
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id); err == nil {
		t.Fatal("second link")
	} else {
		wantProblem(t, err, "recovery_link_issued")
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1 AND purpose = 1 AND cancelled_at IS NULL", int64(alice)); n != 1 {
		t.Fatalf("%d live recovery tokens", n)
	}
	// A cancel from the account's live session voids the issued link.
	if err := e.a.Account.RecoveryCancel(e.as(alice, time.Time{})); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1 AND cancelled_at IS NOT NULL", int64(alice)); n != 1 {
		t.Fatal("token not cancelled")
	}
	if at, _ := e.a.Account.RecoveryPending(e.fresh(alice)); at != nil {
		t.Fatal("banner after cancel")
	}

	// A cancel during the hold voids the request: no link, ever.
	id2, err := e.a.Account.RecoveryRequest(e.fresh(mod2), e.d.Refs.ModUser(mod2, alice))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.a.Account.RecoveryApprove(e.fresh(mod1), id2); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Account.RecoveryCancel(e.as(alice, time.Time{})); err != nil {
		t.Fatal(err)
	}
	e.advance(25 * time.Hour)
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod1), id2); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("link after cancel: %v", err)
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1", int64(alice)); n != 1 {
		t.Fatalf("token issued after cancel: %d", n)
	}

	// An unapproved request expires after 24 h.
	id3, err := e.a.Account.RecoveryRequest(e.fresh(mod1), subject)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(25 * time.Hour)
	wantProblem(t, e.a.Account.RecoveryApprove(e.fresh(mod2), id3), "recovery_expired")
	wantProblem(t, e.a.Account.RecoveryApprove(e.fresh(mod2), id3), "recovery_not_open")
	// Cancelling with nothing pending is a no-op.
	if err := e.a.Account.RecoveryCancel(e.fresh(alice)); err != nil {
		t.Fatal(err)
	}
	// Regression: no live push on an SSE topic outside the closed
	// /api/events list (hello, notification, handshake, stepup).
	for _, ev := range e.rec.LiveEv {
		switch ev.Topic {
		case "hello", "notification", "handshake", "stepup":
		default:
			t.Fatalf("undeclared SSE topic %q", ev.Topic)
		}
	}
}

// approved opens a manual recovery of user by mod1 and approves it by mod2.
func (e *env) approved(mod1, mod2, user core.UserID) gate.Ref {
	e.t.Helper()
	id, err := e.a.Account.RecoveryRequest(e.fresh(mod1), e.d.Refs.ModUser(mod1, user))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.a.Account.RecoveryApprove(e.fresh(mod2), id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// Regression: the link is only issued while the account can still see the
// banner and cancel (release_at + LinkFor), and the token dies with that window.
func TestRecoveryLinkWindow(t *testing.T) {
	e := newEnv(t)
	mod1, mod2 := e.moderator("mod_one"), e.moderator("mod_two")
	alice := e.w.User("alice")

	// Fetched 23 h after release: the token expires at release_at + 24 h,
	// exactly when the banner and the cancel window close.
	id := e.approved(mod1, mod2, alice)
	releaseAt := e.now().Add(account.HoldFor)
	e.advance(account.HoldFor + 23*time.Hour)
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	if at, err := e.a.Account.RecoveryPending(e.fresh(alice)); err != nil || at == nil {
		t.Fatalf("banner 23 h after release: %v %v", at, err)
	}
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id); err != nil {
		t.Fatal(err)
	}
	var expires time.Time
	if err := e.d.DB.Q().QueryRow(e.ctx, "SELECT expires_at FROM enrollment_tokens WHERE user_id = $1 AND purpose = 1",
		int64(alice)).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(releaseAt.Add(account.LinkFor)) {
		t.Fatalf("token expires %v, want %v", expires, releaseAt.Add(account.LinkFor))
	}
	e.advance(time.Hour)
	if at, _ := e.a.Account.RecoveryPending(e.fresh(alice)); at != nil {
		t.Fatal("banner after the window")
	}

	// Fetched after the window: refused, the request expires, no token.
	id2 := e.approved(mod1, mod2, alice)
	e.advance(account.HoldFor + account.LinkFor + time.Minute)
	_, err := e.a.Account.RecoveryLink(e.fresh(mod2), id2)
	wantProblem(t, err, "recovery_expired")
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id2); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("link after expiry: %v", err)
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1", int64(alice)); n != 1 {
		t.Fatalf("%d tokens, want 1", n)
	}

	// The release job expires a released request whose link was never fetched.
	e.approved(mod1, mod2, alice)
	e.advance(account.HoldFor + time.Minute)
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM recovery_requests WHERE user_id = $1 AND state = 2 AND enrollment_token IS NULL", int64(alice)); n != 1 {
		t.Fatalf("%d released", n)
	}
	e.advance(account.LinkFor)
	if err := e.a.DrainJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM recovery_requests WHERE user_id = $1 AND state = 2 AND enrollment_token IS NULL", int64(alice)); n != 0 {
		t.Fatalf("%d released requests without a link outlive the window", n)
	}
}

// Regression: deleting the account ends every live recovery, a released one
// included, voids an issued token, and no recovery step works on a deleted
// account afterwards.
func TestDeleteEndsRecovery(t *testing.T) {
	e := newEnv(t)
	mod1, mod2 := e.moderator("mod_one"), e.moderator("mod_two")

	// Released with the link out: deletion voids the token.
	alice := e.w.User("alice")
	id := e.approved(mod1, mod2, alice)
	e.advance(account.HoldFor)
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Account.Delete(e.fresh(alice), "alice"); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1 AND cancelled_at IS NULL", int64(alice)); n != 0 {
		t.Fatalf("%d live recovery tokens after delete", n)
	}
	if n := e.count("SELECT count(*) FROM recovery_requests WHERE user_id = $1 AND state <> 3", int64(alice)); n != 0 {
		t.Fatalf("%d recoveries not cancelled", n)
	}

	// Released without a link: after deletion moderator 2 gets nothing.
	bob := e.w.User("bob")
	id2 := e.approved(mod1, mod2, bob)
	e.advance(account.HoldFor)
	if err := e.a.Account.Delete(e.fresh(bob), "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Account.RecoveryLink(e.fresh(mod2), id2); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("link on a deleted account: %v", err)
	}
	if n := e.count("SELECT count(*) FROM enrollment_tokens WHERE user_id = $1", int64(bob)); n != 0 {
		t.Fatalf("%d tokens issued for a deleted account", n)
	}

	// The status is read inside the transaction, not from the users cache: a
	// deletion the cache has not seen yet still blocks request, approve and link.
	carol := e.w.User("carol")
	if _, err := e.a.Users.Get(e.ctx, carol); err != nil {
		t.Fatal(err)
	}
	id3, err := e.a.Account.RecoveryRequest(e.fresh(mod1), e.d.Refs.ModUser(mod1, carol))
	if err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE users SET status = $2 WHERE id = $1", int64(carol), int16(core.StatusDeleted))
	if err := e.a.Account.RecoveryApprove(e.fresh(mod2), id3); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("approve on a deleted account: %v", err)
	}
	e.exec("UPDATE recovery_requests SET state = 3 WHERE user_id = $1", int64(carol))
	if _, err := e.a.Account.RecoveryRequest(e.fresh(mod1), e.d.Refs.ModUser(mod1, carol)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("request on a deleted account: %v", err)
	}
}

func TestMeAndRecoveryNeedUser(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	bob := e.w.User("bob", coretest.InvitedBy(alice))
	me, err := e.a.Account.Me(e.fresh(bob))
	if err != nil || me.Handle != "bob" || me.Inviter.Kind != "user" || me.Inviter.Handle != "alice" || me.Role != "member" ||
		me.Settings.RankPreset != "balanced" || len(me.Settings.Languages) != 1 || string(me.Settings.UIPrefs) != "{}" {
		t.Fatalf("me %+v %v", me, err)
	}
	if id, ok := e.d.Refs.OpenUser(bob, me.Ref); !ok || id != bob {
		t.Fatal("me ref does not open for bob")
	}
	if _, err := e.a.Account.Me(e.ctx); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous me: %v", err)
	}
	if _, err := e.a.Account.RecoveryPending(e.ctx); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous pending: %v", err)
	}
	if err := e.a.Account.RecoveryCancel(e.ctx); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("anonymous cancel: %v", err)
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}
