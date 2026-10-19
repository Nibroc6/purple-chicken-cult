package moderation_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/moderation"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) reload() {
	e.t.Helper()
	e.must(e.a.Users.Reload(e.ctx))
}

// itemOf returns mod's queue item about subject handle (with the given source).
func (e *env) itemOf(mod, source, subject string) gate.ModItem {
	e.t.Helper()
	for _, it := range e.queue(mod) {
		if it.Source == source && it.Subject != nil && it.Subject.Handle == subject {
			return it
		}
	}
	e.t.Fatalf("no %s item about %s", source, subject)
	return gate.ModItem{}
}

// suspend runs a whole two-person suspension of handle (reported by
// reporter) with the P0 fresh step-ups (moderators without passkeys).
func (e *env) suspend(reporter, handle string) {
	e.t.Helper()
	e.report(reporter, "person", e.person(reporter, handle), "bot")
	e.must(e.m.Resolve(e.mod("mo1", true), e.itemOf("mo1", "report", handle).ID, "suspend_user", "bot"))
	e.must(e.m.Confirm(e.mod("mo2", true), e.itemOf("mo2", "report", handle).ID, nil))
}

// hold fakes a CSAM hold of a media item uploaded by owner (gate/media's
// tables) and queues it the way gate/media does.
func (e *env) hold(owner string) uuid.UUID {
	e.t.Helper()
	m := uuid.New()
	now := e.d.Clock.Now()
	e.exec("INSERT INTO media (id, owner_id, status, scan_driver, created_at) VALUES ($1, $2, 3, 'dev', $3)", m, int64(e.id(owner)), now)
	e.exec(`INSERT INTO csam_holds (media_id, evidence_key, match_info, subject_user, held_at, retain_until)
		VALUES ($1, 'ev-key', '{"driver":"dev","match":"known"}', $2, $3, $4)`, m, int64(e.id(owner)), now, now.AddDate(1, 0, 0))
	e.must(e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		return e.m.Enqueue(e.ctx, tx, core.ModQueueItem{Source: core.ModSrcCSAM, Priority: 0, SubjectUser: e.id(owner),
			Media: m, Summary: map[string]int{"csam_match": 1, "upload": 1}})
	}))
	return m
}

// Acceptance (unified queue): reports, csam holds and signals share one
// queue, csam first; a csam item shows the hold's metadata and never content
// or a media URL; a repeated signal for the same subject merges into its open
// item; claims hold off other moderators for an hour.
func TestUnifiedQueueAndClaims(t *testing.T) {
	e := newEnv(t)
	e.report("carol", "post", e.post("carol", "b1"), "spam")
	m := e.hold("bob")
	sig := core.ModQueueItem{Source: core.ModSrcSignalCluster, Priority: 2, SubjectUser: e.id("eve"),
		Summary: map[string]int{"cadence": 1, "posts": 14}}
	e.must(e.m.Enqueue(e.ctx, nil, sig))
	sig.Summary = map[string]int{"cadence": 1, "posts": 20}
	e.must(e.m.Enqueue(e.ctx, nil, sig))

	items := e.queue("mo1")
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Source != "csam" || items[0].Priority != 0 || items[0].Reason != "csam" {
		t.Fatalf("csam first: %+v", items[0])
	}
	c := items[0]
	if c.Post != nil || c.CSAM == nil || c.CSAM.MatchInfo["match"] != "known" || !c.CSAM.AccountHeld ||
		c.Subject == nil || c.Subject.Handle != "bob" {
		t.Fatalf("csam view %+v %+v", c, c.CSAM)
	}
	raw, _ := json.Marshal(c)
	for _, bad := range []string{"ev-key", "/m/", "url\"", m.String()} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("csam item leaks %q: %s", bad, raw)
		}
	}
	signal := e.itemOf("mo1", "signal", "eve")
	if signal.Reason != "cadence" || signal.Summary["posts"] != 20 || signal.Post != nil {
		t.Fatalf("signal %+v", signal)
	}
	if e.count("SELECT count(*) FROM mod_queue WHERE source = 1") != 1 {
		t.Fatal("a repeated signal makes a second item")
	}
	report := e.itemOf("mo1", "report", "bob")
	if report.Post == nil || report.Summary["reports"] != 1 {
		t.Fatalf("report %+v", report)
	}

	// Claims: mo1 claims the report; mo2 can neither claim nor resolve it.
	e.must(e.m.Claim(e.mod("mo1", false), report.ID))
	if it := e.itemOf("mo1", "report", "bob"); !it.ClaimedByMe || !it.Claimed {
		t.Fatalf("claim %+v", it)
	}
	other := e.itemOf("mo2", "report", "bob")
	if other.ClaimedByMe || !other.Claimed {
		t.Fatalf("claimed by another %+v", other)
	}
	problem(t, e.m.Claim(e.mod("mo2", false), other.ID), "item_claimed")
	problem(t, e.m.Resolve(e.mod("mo2", false), other.ID, "dismiss", "other"), "item_claimed")
	if err := e.m.Claim(e.as("carol"), other.ID); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("member claim: %v", err)
	}
	e.must(e.m.Claim(e.mod("mo1", false), report.ID)) // renew
	e.advance(moderation.ClaimFor + time.Minute)
	e.must(e.m.Claim(e.mod("mo2", false), other.ID))
	e.must(e.m.Resolve(e.mod("mo2", false), other.ID, "dismiss", "other"))
	problem(t, e.m.Claim(e.mod("mo2", false), other.ID), "item_resolved")
	if len(e.queue("mo1")) != 2 {
		t.Fatal("dismissed item still open")
	}
}

// ---- passkey moderators ----

// member is a browser session signed in with a passkey.
type member struct {
	e      *env
	cookie string
	auth   *authtest.Authenticator
	id     core.UserID
}

func (m *member) ctx() context.Context {
	m.e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.AddCookie(&http.Cookie{Name: httpx.CookieName(false), Value: m.cookie}) //nolint:gosec // test request
	var got context.Context
	m.e.a.Auth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).
		ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func (m *member) viewer() core.Viewer {
	m.e.t.Helper()
	v, ok := core.ViewerFrom(m.ctx())
	if !ok || v.Kind != core.SessUser || len(v.Credential) == 0 {
		m.e.t.Fatalf("not signed in with a passkey: %+v", v)
	}
	return v
}

// moderator enrolls handle as a genesis account with a passkey and makes it
// a moderator.
func (e *env) moderator(handle string) *member {
	e.t.Helper()
	tok, err := e.a.Auth.GenesisToken(e.ctx, handle)
	e.must(err)
	m := &member{e: e, auth: authtest.New(e.d.Cfg.Origin)}
	ev, err := e.a.Auth.EnrollBegin(m.ctx(), tok)
	e.must(err)
	m.cookie = ev.Token
	resp, err := m.auth.Create(ev.Options)
	e.must(err)
	m.cookie, _, err = e.a.Auth.EnrollFinish(m.ctx(), resp, false, e.d.Cfg.TOSVersion, true)
	e.must(err)
	id, ok := e.a.Users.ByHandle(e.ctx, handle)
	if !ok {
		e.t.Fatal("no user")
	}
	m.id = id
	e.exec("UPDATE users SET role = 1 WHERE id = $1", int64(id))
	e.a.Users.Invalidate(id)
	return m
}

// cosig opens m's mod_action intent for item and signs it with signer's
// credential (m's own unless given).
func (e *env) cosig(m *member, item gate.Ref, signer *member) *moderation.Cosig {
	e.t.Helper()
	in, err := e.a.Cosign.BeginIntent(m.ctx(), m.viewer(), moderation.PurposeModAction, item)
	e.must(err)
	var cred []byte
	if signer != nil {
		cred = signer.auth.Credentials()[0].ID
	} else {
		signer = m
	}
	asr, err := signer.auth.GetWith(in.Options, cred)
	e.must(err)
	return &moderation.Cosig{Intent: in.ID, Assertion: asr}
}

func (e *env) queueOf(m *member) []gate.ModItem {
	e.t.Helper()
	p, err := e.m.Queue(m.ctx(), "")
	e.must(err)
	return p.Items
}

// Acceptance (security #30): the second moderator co-signs the exact
// ModAction record with their own passkey. A passkey session's confirmation
// without the co-signature, or with an assertion made with the first
// moderator's credential, fails and changes nothing; the first moderator
// cannot even draft it. The stored record carries the assertion.
func TestSecondModeratorCosigns(t *testing.T) {
	e := newEnv(t)
	m1, m2 := e.moderator("pk1"), e.moderator("pk2")
	e.report("carol", "person", e.person("carol", "bob"), "bot")
	item := func(m *member) gate.Ref {
		for _, it := range e.queueOf(m) {
			if it.Subject != nil && it.Subject.Handle == "bob" {
				return it.ID
			}
		}
		t.Fatal("no item")
		return ""
	}
	e.must(e.m.Resolve(m1.ctx(), item(m1), "suspend_user", "bot"))
	it2 := item(m2)
	if q := e.queueOf(m2); q[0].Pending == nil || q[0].Pending.Action != "suspend_user" || q[0].Pending.Mine {
		t.Fatalf("pending %+v", q[0].Pending)
	}
	if q := e.queueOf(m1); !q[0].Pending.Mine {
		t.Fatal("the first moderator's own action")
	}

	// The first moderator cannot draft the confirmation.
	if _, err := e.a.Cosign.BeginIntent(m1.ctx(), m1.viewer(), moderation.PurposeModAction, item(m1)); err == nil {
		t.Fatal("the first moderator drafted the confirmation")
	} else {
		problem(t, err, "second_moderator_required")
	}
	// A passkey session without its co-signature: the uniform not-found.
	if err := e.m.Confirm(m2.ctx(), it2, nil); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("unsigned confirmation: %v", err)
	}
	// m2's intent signed with m1's credential: refused.
	if err := e.m.Confirm(m2.ctx(), it2, e.cosig(m2, it2, m1)); err == nil {
		t.Fatal("confirmed with the first moderator's credential")
	}
	// An intent of another purpose or a garbage assertion: refused.
	if err := e.m.Confirm(m2.ctx(), it2, &moderation.Cosig{Intent: "garbage", Assertion: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("confirmed with garbage")
	}
	if m, _ := e.a.Users.Get(e.ctx, e.id("bob")); m.Status != core.StatusActive {
		t.Fatal("a refused confirmation suspended bob")
	}
	if len(e.blame("alice")) != 0 || e.m.Marked(e.ctx, e.id("carol"), e.id("alice")) {
		t.Fatal("blame or mark before confirmation")
	}

	// The real co-signature.
	e.must(e.m.Confirm(m2.ctx(), it2, e.cosig(m2, it2, nil)))
	if m, _ := e.a.Users.Get(e.ctx, e.id("bob")); m.Status != core.StatusSuspended {
		t.Fatal("not suspended")
	}
	var (
		kind      int16
		body      []byte
		asserts   int
		secondRaw []byte
	)
	e.must(e.d.DB.Q().QueryRow(e.ctx, `SELECT r.kind, r.body, COALESCE(jsonb_array_length(r.assertions), 0), a.second_auth
		FROM mod_actions a JOIN signed_records r ON r.hash = decode(a.second_auth->>'record', 'hex')
		WHERE a.action = 'suspend_user' AND a.state = 1`).Scan(&kind, &body, &asserts, &secondRaw))
	if core.RecordKind(kind) != core.RecModAction || asserts != 1 || !strings.Contains(string(secondRaw), `"cosigned": true`) {
		t.Fatalf("record kind %d assertions %d second %s", kind, asserts, secondRaw)
	}
	var rec map[string]any
	e.must(json.Unmarshal(body, &rec))
	pk1, _ := e.a.Users.Get(e.ctx, m1.id)
	pk2, _ := e.a.Users.Get(e.ctx, m2.id)
	bob, _ := e.a.Users.Get(e.ctx, e.id("bob"))
	if rec["kind"] != "mod_action" || rec["action"] != "suspend_user" || rec["by"] != pk1.ExtID.String() ||
		rec["second"] != pk2.ExtID.String() || rec["subject"] != bob.ExtID.String() {
		t.Fatalf("record %v", rec)
	}
	if len(e.blame("alice")) != 1 {
		t.Fatal("no blame after confirmation")
	}
}

// Acceptance (§8): quarantine needs ≥ k confirmed-bad accounts in the
// subtree and two moderators; it puts exactly the subtree back on probation
// and pauses the root's invites for 30 days; a second offense revokes them.
func TestQuarantineAffectsOnlyTheSubtree(t *testing.T) {
	e := newEnv(t)
	root := e.w.User("root", coretest.InvitedBy(e.id("alice")))
	kids := []string{"kid1", "kid2", "kid3", "kid4", "kid5", "kid6", "kid7"}
	for _, k := range kids {
		e.w.User(k, coretest.InvitedBy(root))
	}
	e.w.User("grand", coretest.InvitedBy(e.id("kid1")))
	e.w.User("sib", coretest.InvitedBy(e.id("alice"))) // outside: root's sibling
	for _, k := range append(kids, "root", "grand", "sib") {
		e.w.Friends("carol", k)
	}
	e.reload()

	for _, k := range []string{"kid1", "kid2"} {
		e.suspend("carol", k)
	}
	e.report("carol", "person", e.person("carol", "kid4"), "bot")
	it := e.itemOf("mo1", "report", "kid4")
	if it.Summary["subtree_confirmed"] != 2 || it.Summary["quarantine_k"] != 3 {
		t.Fatalf("summary %+v", it.Summary)
	}
	problem(t, e.m.Quarantine(e.mod("mo1", true), it.ID, ""), "quarantine_threshold")
	e.suspend("carol", "kid3")
	if err := e.m.Quarantine(e.mod("mo1", false), it.ID, ""); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale quarantine: %v", err)
	}
	problem(t, e.m.Quarantine(e.mod("mo1", true), it.ID, "rude"), "reason_invalid")
	e.must(e.m.Quarantine(e.mod("mo1", true), it.ID, ""))
	problem(t, e.m.Quarantine(e.mod("mo2", true), e.itemOf("mo2", "report", "kid4").ID, ""), "action_pending")
	if m, _ := e.a.Users.Get(e.ctx, e.id("kid4")); m.OnProbation(e.d.Clock.Now()) {
		t.Fatal("one moderator quarantined")
	}
	problem(t, e.m.Confirm(e.mod("mo1", true), it.ID, nil), "second_moderator_required")
	e.must(e.m.Confirm(e.mod("mo2", true), e.itemOf("mo2", "report", "kid4").ID, nil))
	e.reload()
	now := e.d.Clock.Now()
	for _, h := range []string{"kid4", "kid5", "grand"} {
		if m, _ := e.a.Users.Get(e.ctx, e.id(h)); !m.OnProbation(now) || !m.ProbationUntil.Equal(now.Add(moderation.QuarantineFor)) {
			t.Fatalf("%s not back on probation: %+v", h, m.ProbationUntil)
		}
	}
	for _, h := range []string{"root", "sib", "alice", "carol", "bob"} {
		if m, _ := e.a.Users.Get(e.ctx, e.id(h)); m.OnProbation(now) {
			t.Fatalf("%s outside the subtree is on probation", h)
		}
	}
	var (
		paused   *time.Time
		revoked  bool
		offenses int
	)
	budget := func() {
		e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT paused_until, revoked, offenses FROM invite_budgets WHERE user_id = $1",
			int64(root)).Scan(&paused, &revoked, &offenses))
	}
	budget()
	if paused == nil || !paused.Equal(now.Add(moderation.QuarantineFor)) || revoked || offenses != 1 {
		t.Fatalf("budget %v %v %d", paused, revoked, offenses)
	}
	if n := e.count("SELECT count(*) FROM invite_budgets WHERE user_id = $1 AND (paused_until IS NOT NULL OR offenses > 0)",
		int64(e.id("sib"))); n != 0 {
		t.Fatal("the sibling's invites changed")
	}
	if n := e.count("SELECT count(*) FROM quarantines WHERE root_inviter = $1 AND confirmed_bad = 3", int64(root)); n != 1 {
		t.Fatal("quarantine row")
	}
	// Nothing is told to anyone (no notification beyond the suspensions' blame).
	if n := e.count("SELECT count(*) FROM notifications WHERE user_id = $1", int64(root)); n != 3 {
		t.Fatalf("root notifications %d", n)
	}

	// Within 30 days: quarantine_active; the old suspensions no longer count.
	problem(t, e.m.Quarantine(e.mod("mo1", true), e.itemOf("mo1", "report", "kid4").ID, ""), "quarantine_active")
	e.advance(moderation.QuarantineFor + time.Hour)
	problem(t, e.m.Quarantine(e.mod("mo1", true), e.itemOf("mo1", "report", "kid4").ID, ""), "quarantine_threshold")
	for _, k := range []string{"kid5", "kid6", "kid7"} {
		e.suspend("carol", k)
	}
	e.must(e.m.Quarantine(e.mod("mo1", true), e.itemOf("mo1", "report", "kid4").ID, "ban_evasion"))
	e.must(e.m.Confirm(e.mod("mo2", true), e.itemOf("mo2", "report", "kid4").ID, nil))
	budget()
	if !revoked || offenses != 2 {
		t.Fatalf("second offense: revoked %v offenses %d", revoked, offenses)
	}
}

// Acceptance (fidelity #19): a confirmed suspension marks the inviter for
// 90 days, visible only to the removed account's component at removal time;
// not to the inviter, not to someone who joins later, never before the
// second moderator confirmed.
func TestRemovedAccountMark(t *testing.T) {
	e := newEnv(t)
	e.w.User("dave")
	e.w.User("ivy", coretest.InvitedBy(e.id("alice")))
	e.w.Friends("carol", "ivy")
	e.reload()
	e.report("carol", "person", e.person("carol", "bob"), "bot")
	e.must(e.m.Resolve(e.mod("mo1", true), e.itemOf("mo1", "report", "bob").ID, "suspend_user", "bot"))
	alice, carol := e.id("alice"), e.id("carol")
	if e.m.Marked(e.ctx, carol, alice) || e.count("SELECT count(*) FROM removed_account_marks") != 0 {
		t.Fatal("mark before confirmation")
	}
	e.must(e.m.Confirm(e.mod("mo2", true), e.itemOf("mo2", "report", "bob").ID, nil))
	if !e.m.Marked(e.ctx, carol, alice) {
		t.Fatal("carol had bob in her horizon")
	}
	if e.m.Marked(e.ctx, alice, alice) || e.m.Marked(e.ctx, e.id("xavier"), alice) || e.m.Marked(e.ctx, e.id("frank"), alice) {
		t.Fatal("the mark reaches the inviter or another component")
	}
	// dave joins the component after the removal: he never sees it.
	e.w.Friends("dave", "alice")
	e.reload()
	if e.m.Marked(e.ctx, e.id("dave"), alice) {
		t.Fatal("a later joiner sees the mark")
	}
	// The person view carries it for carol only.
	sr, err := e.d.Vis.SearchFor(e.ctx, carol)
	e.must(err)
	if pv, err := e.d.Views.Person(e.ctx, sr, alice); err != nil || !pv.InvitedRemoved {
		t.Fatalf("carol's view of alice: %+v %v", pv, err)
	}
	sr, err = e.d.Vis.SearchFor(e.ctx, e.id("dave"))
	e.must(err)
	if pv, err := e.d.Views.Person(e.ctx, sr, alice); err != nil || pv.InvitedRemoved {
		t.Fatalf("dave's view of alice: %+v %v", pv, err)
	}
	// Moderators see the count on items about the inviter's invitees.
	e.report("carol", "person", e.person("carol", "ivy"), "spam")
	if it := e.itemOf("mo1", "report", "ivy"); it.Summary["inviter_marks"] != 1 {
		t.Fatalf("moderator summary %+v", it.Summary)
	}
	// 90 days later it is gone, and the expiry job deletes the row.
	e.advance(moderation.MarkFor)
	if e.m.Marked(e.ctx, carol, alice) {
		t.Fatal("mark after 90 days")
	}
	e.must(e.m.Expire(e.ctx))
	if e.count("SELECT count(*) FROM removed_account_marks") != 0 {
		t.Fatal("expired mark kept")
	}
}

// Acceptance (p1-w04-2, fidelity #35): the csam_report action is two-person
// and records the report on the hold only on confirmation; dismissing a hold
// (a false positive) is two-person too and lifts the upload hold.
func TestCSAMReportAndRelease(t *testing.T) {
	e := newEnv(t)
	m := e.hold("bob")
	it := e.itemOf("mo1", "csam", "bob")
	problem(t, e.m.CSAMReport(e.mod("mo1", true), it.ID, "bad ref!"), "bad_request")
	if err := e.m.CSAMReport(e.mod("mo1", false), it.ID, "CT-1234"); !errors.Is(err, gate.ErrStepUp) {
		t.Fatalf("stale: %v", err)
	}
	e.report("carol", "post", e.post("carol", "b1"), "spam")
	problem(t, e.m.CSAMReport(e.mod("mo1", true), e.itemOf("mo1", "report", "bob").ID, "CT-1234"), "action_invalid")
	e.must(e.m.CSAMReport(e.mod("mo1", true), it.ID, "CT-1234"))
	var reported *time.Time
	held := func() {
		e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT reported_at FROM csam_holds WHERE media_id = $1", m).Scan(&reported))
	}
	held()
	if reported != nil {
		t.Fatal("reported by one moderator")
	}
	if p := e.itemOf("mo2", "csam", "bob").Pending; p == nil || p.Action != "csam_report" || p.ReportRef != "CT-1234" {
		t.Fatalf("pending %+v", p)
	}
	problem(t, e.m.Confirm(e.mod("mo1", true), it.ID, nil), "second_moderator_required")
	e.must(e.m.Confirm(e.mod("mo2", true), e.itemOf("mo2", "csam", "bob").ID, nil))
	held()
	if reported == nil || e.count("SELECT count(*) FROM csam_holds WHERE report_ref = 'CT-1234'") != 1 {
		t.Fatal("report not recorded")
	}
	if e.count("SELECT count(*) FROM mod_queue WHERE source = 2 AND resolution = 'csam_report'") != 1 {
		t.Fatal("item not resolved")
	}
	// A second item of the same (reported) hold cannot be reported again.
	e.must(e.m.Enqueue(e.ctx, nil, core.ModQueueItem{Source: core.ModSrcCSAM, SubjectUser: e.id("bob"), Media: m}))
	problem(t, e.m.CSAMReport(e.mod("mo1", true), e.itemOf("mo1", "csam", "bob").ID, "CT-9"), "csam_already_reported")

	// A false positive: dismissing is two-person and lifts the upload hold.
	m2 := e.hold("eve")
	e.must(e.m.Resolve(e.mod("mo1", true), e.itemOf("mo1", "csam", "eve").ID, "dismiss", "other"))
	if n := e.count("SELECT count(*) FROM csam_holds WHERE media_id = $1 AND account_released_at IS NULL", m2); n != 1 {
		t.Fatal("released by one moderator")
	}
	e.must(e.m.Confirm(e.mod("mo2", true), e.itemOf("mo2", "csam", "eve").ID, nil))
	if n := e.count("SELECT count(*) FROM csam_holds WHERE media_id = $1 AND account_released_at IS NOT NULL", m2); n != 1 {
		t.Fatal("upload hold not lifted")
	}
}
