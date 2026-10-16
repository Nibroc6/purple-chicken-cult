package invites_test

import (
	"crypto/sha256"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
)

func TestBudgetAccruesToTheCap(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	carol := e.w.User("carol", invitedBy(alice))
	b, err := e.s.Budget(e.as(carol))
	e.must(err)
	if b.Available != 1 || b.Cap != invites.DefaultCap {
		t.Fatalf("first budget %+v", b)
	}
	e.exec("UPDATE invite_budgets SET tokens = 0.5, refreshed_at = $2 WHERE user_id = $1", int64(carol), e.d.Clock.Now())
	b, err = e.s.Budget(e.as(carol))
	e.must(err)
	if b.Available != 0 || b.NextAt == nil || !b.NextAt.Equal(e.d.Clock.Now().Add(12*time.Hour)) {
		t.Fatalf("half a token %+v", b)
	}
	e.advance(30 * 24 * time.Hour)
	b, err = e.s.Budget(e.as(carol))
	e.must(err)
	if b.Available != invites.DefaultCap || b.NextAt != nil {
		t.Fatalf("budget never exceeds the cap: %+v", b)
	}
}

// friend makes a and b friends at lvl the way the module that owns edges does.
func (e *env) friendAt(a, b core.UserID, lvl core.EdgeLevel) {
	e.t.Helper()
	h := sha256.Sum256([]byte{byte(a), byte(a >> 8), byte(b), byte(b >> 8)})
	e.must(e.d.Edges.Create(e.ctx, nil, a, b, lvl, h[:]))
	now := e.d.Clock.Now()
	e.d.Graph.AddEdge(a, b, lvl, now)
	e.d.Bus.Publish(core.EdgeAdded{A: a, B: b, Level: lvl, At: now})
}

func (e *env) friend(a, b core.UserID) { e.friendAt(a, b, core.LevelFriend) }

func (e *env) onProbation(u core.UserID) bool {
	e.t.Helper()
	m, err := e.d.Users.Get(e.ctx, u)
	e.must(err)
	return m.OnProbation(e.d.Clock.Now())
}

func TestPhase1ProbationNeedsFriendPlusEdges(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	x, y, z := e.w.User("xavier"), e.w.User("yara"), e.w.User("zoe")
	cleared := 0
	core.Subscribe(e.d.Bus, func(core.ProbationCleared) { cleared++ })
	nb := e.join(alice, "newbie", verified, "verified").id // alice: one friend+ edge outside the subtree

	// An invitee of newbie is inside its subtree and never counts.
	e.exec(`INSERT INTO users (id, ext_id, handle, handle_canon, invited_by, webauthn_user_id, probation_until)
		VALUES (3600000000, $1, 'kid', 'kid', $2, '\x01', $3)`, uuid.New(), int64(nb), e.d.Clock.Now().Add(time.Hour))
	e.friendAt(nb, 3600000000, core.LevelFriendPlus)
	e.friend(nb, x) // plain friends do not count under the Phase 1 rule
	e.friend(nb, y)
	e.friendAt(nb, z, core.LevelFriendPlus)
	e.advance(invites.ProbationForP0)
	if n, _ := e.s.EvaluateAll(e.ctx); n != 0 || !e.onProbation(nb) {
		t.Fatal("cleared after the Phase 0 time")
	}
	e.advance(invites.ProbationFor - invites.ProbationForP0)
	if n, _ := e.s.EvaluateAll(e.ctx); n != 0 || !e.onProbation(nb) {
		t.Fatal("cleared with two friend+ edges outside the subtree")
	}
	b, err := e.s.Budget(e.as(nb))
	e.must(err)
	if b.Probation == nil || b.Probation.NeedOutsideFriends != 1 || !b.Probation.FriendPlus {
		t.Fatalf("probation view %+v", b.Probation)
	}
	// The third friend+ edge clears it at once (EdgeAdded hook).
	e.exec("DELETE FROM edges WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)", int64(nb), int64(x))
	e.d.Graph.RemoveEdge(nb, x)
	e.friendAt(nb, x, core.LevelFriendPlus)
	if e.onProbation(nb) || cleared != 1 {
		t.Fatal("the third friend+ edge did not clear probation")
	}
	b, err = e.s.Budget(e.as(nb))
	e.must(err)
	if b.Available != 1 || b.Probation != nil || b.Cap != invites.DefaultCap {
		t.Fatalf("budget after clearance %+v", b)
	}
	// Monotonic: losing friends does not put the account back on probation.
	e.d.Graph.RemoveEdge(nb, x)
	if n, _ := e.s.EvaluateAll(e.ctx); n != 0 || e.onProbation(nb) {
		t.Fatal("probation came back")
	}
}

func TestMidProbationAccountsFinishUnderThePhase0Rule(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	old := e.w.User("old", invitedBy(alice))
	x, y := e.w.User("xavier"), e.w.User("yara")
	// An account that was mid-probation at the cut-over (rule 0, 7 days).
	e.exec("UPDATE users SET probation_until = $2, probation_rule = 0 WHERE id = $1", int64(old),
		e.d.Clock.Now().Add(invites.ProbationForP0))
	e.d.Bus.Publish(core.UserChanged{ID: old})
	e.friend(old, alice)
	e.friend(old, x)
	e.friend(old, y) // three plain edges outside the own subtree
	b, err := e.s.Budget(e.as(old))
	e.must(err)
	if b.Probation == nil || b.Probation.NeedOutsideFriends != 0 || b.Probation.FriendPlus {
		t.Fatalf("phase 0 probation view %+v", b.Probation)
	}
	e.advance(invites.ProbationForP0)
	n, err := e.s.EvaluateAll(e.ctx)
	e.must(err)
	if n != 1 || e.onProbation(old) {
		t.Fatal("the Phase 0 rule did not clear after 7 days and 3 edges")
	}
}

func TestResetPutsUsersBackOnProbation(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	carol := e.w.User("carol", invitedBy(alice))
	until := e.d.Clock.Now().Add(30 * 24 * time.Hour)
	e.must(e.d.Probation.Reset(e.ctx, nil, []core.UserID{alice, carol}, until))
	if e.onProbation(alice) {
		t.Fatal("genesis accounts have no probation")
	}
	if !e.onProbation(carol) {
		t.Fatal("reset did not apply")
	}
	_, err := e.s.Open(e.as(carol), "qr_audio", false)
	wantProblem(t, err, "on_probation")
	b, err := e.s.Budget(e.as(carol))
	e.must(err)
	if b.Probation == nil || !b.Probation.FriendPlus {
		t.Fatalf("a reset probation runs under the Phase 1 rule: %+v", b.Probation)
	}
}

func TestDiversityDecaySlowsInvitesInsideTheInviterSubtree(t *testing.T) {
	e := newEnv(t)
	root := e.w.User("root")
	inside := e.w.User("inside", invitedBy(root))
	sib1, sib2 := e.w.User("sib1", invitedBy(root)), e.w.User("sib2", invitedBy(root))
	diverse := e.w.User("diverse", invitedBy(root))
	out1, out2 := e.w.User("out1"), e.w.User("out2")
	// inside's edges all stay in root's subtree; diverse has half of them outside.
	e.friend(inside, root)
	e.friend(inside, sib1)
	e.friend(inside, sib2)
	e.friend(diverse, root)
	e.friend(diverse, sib1)
	e.friend(diverse, out1)
	e.friend(diverse, out2)
	for _, u := range []core.UserID{root, inside, diverse} {
		e.exec(`INSERT INTO invite_budgets (user_id, tokens, cap, refreshed_at) VALUES ($1, 0, 3, $2)
			ON CONFLICT (user_id) DO UPDATE SET tokens = 0, refreshed_at = $2`, int64(u), e.d.Clock.Now())
	}
	n, err := e.s.EvaluateDiversity(e.ctx)
	e.must(err)
	if n != 1 {
		t.Fatalf("changed %d multipliers, want 1 (inside)", n)
	}
	div := func(u core.UserID) float64 {
		var d float64
		e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT diversity FROM invite_budgets WHERE user_id = $1", int64(u)).Scan(&d))
		return d
	}
	if d := div(inside); math.Abs(d-invites.DiversityFloor) > 1e-6 {
		t.Fatalf("inside diversity %v", d)
	}
	if div(diverse) != 1 || div(root) != 1 {
		t.Fatal("diverse and genesis accounts keep the full rate")
	}
	// One day: diverse earns a token, inside a quarter of one; the countdown says so.
	e.advance(invites.TokenEvery)
	bi, err := e.s.Budget(e.as(inside))
	e.must(err)
	bd, err := e.s.Budget(e.as(diverse))
	e.must(err)
	if bd.Available != 1 || bi.Available != 0 || bi.NextAt == nil ||
		!bi.NextAt.Equal(e.d.Clock.Now().Add(3*invites.TokenEvery)) {
		t.Fatalf("inside %+v, diverse %+v", bi, bd)
	}
	// New outside edges lift the multiplier; the accrual so far is kept at the old rate.
	e.friend(inside, out1)
	e.friend(inside, out2)
	e.friend(inside, e.w.User("out3"))
	_, err = e.s.EvaluateDiversity(e.ctx)
	e.must(err)
	if div(inside) != 1 {
		t.Fatalf("inside diversity after outside edges %v", div(inside))
	}
	var tokens float64
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT tokens FROM invite_budgets WHERE user_id = $1", int64(inside)).Scan(&tokens))
	if math.Abs(tokens-0.25) > 1e-6 {
		t.Fatalf("materialized tokens %v, want 0.25", tokens)
	}
}
