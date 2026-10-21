package invites_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
)

// upgrade opens a friend+ upgrade by a with b and lets b's signed-in browser claim it.
func (e *env) upgrade(a, b *member) (*hs, *guest) {
	e.t.Helper()
	hv, err := e.s.OpenUpgrade(a.ctx(), a.viewer(), b.id)
	e.must(err)
	h := e.started(a.ctx(), hv)
	p := e.peerOf(b)
	e.must(p.claim(h.payload))
	e.must(e.s.Confirm(h.ctx, h.ref))
	return h, p
}

func TestAcceptedInviteThenVerifiedUpgradeMakesFriendPlus(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	nb := e.join(alice, "newbie", sound{jingle: true}, "accepted")
	if e.level(alice.id, nb.id) != core.LevelFriend {
		t.Fatal("an Accepted invite must be a plain friend")
	}
	var upgraded []core.EdgeUpgraded
	core.Subscribe(e.d.Bus, func(ev core.EdgeUpgraded) { upgraded = append(upgraded, ev) })

	h, p := e.upgrade(alice, nb)
	st, err := p.status()
	e.must(err)
	if st.Kind != "upgrade" || st.State != "confirmed" || st.HS == "" || st.SignupToken != "" {
		t.Fatalf("peer status %+v", st)
	}
	// Only Verified upgrades: a jingle-only attempt is a retry.
	out, err := e.play(h, p, sound{jingle: true})
	e.must(err)
	if out.Outcome != "retry" || out.Hint != invites.HintVolumeUp {
		t.Fatalf("accepted upgrade attempt %+v", out)
	}
	if o, _ := e.s.Result(e.ctx, e.hsID(h)); o != core.OutcomeRetry {
		t.Fatalf("result %v", o)
	}
	out, err = e.play(h, p, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("verified upgrade attempt %+v", out)
	}
	// Both friends co-sign the friend+ Friendship; the second signature writes it.
	if s, _ := e.s.Status(alice.ctx(), h.ref); s.Sign != "needed" {
		t.Fatalf("initiator status %+v", s)
	}
	e.must(e.signs(alice, invites.PurposeFriendUpgrade, h.ref))
	if s, _ := e.s.Status(alice.ctx(), h.ref); s.Sign != "waiting" || s.PeerSigned {
		t.Fatalf("initiator status after signing %+v", s)
	}
	if e.level(alice.id, nb.id) != core.LevelFriend {
		t.Fatal("upgraded before the second signature")
	}
	st, err = p.status()
	e.must(err)
	if st.Sign != "needed" || !st.PeerSigned {
		t.Fatalf("peer status %+v", st)
	}
	wantNotFound(t, e.signs(nb, invites.PurposeInviteInviter, st.HS), "the peer signs an invite purpose")
	e.must(e.signs(nb, invites.PurposeFriendUpgrade, st.HS))
	if e.level(alice.id, nb.id) != core.LevelFriendPlus {
		t.Fatal("the upgrade did not promote the edge")
	}
	if len(upgraded) != 1 || upgraded[0].A != alice.id || upgraded[0].B != nb.id {
		t.Fatalf("EdgeUpgraded %+v", upgraded)
	}
	var (
		level string
		n     int
	)
	e.must(e.d.DB.Q().QueryRow(e.ctx, `SELECT convert_from(r.body, 'UTF8')::jsonb->>'level', jsonb_array_length(r.assertions) FROM edges x
		JOIN signed_records r ON r.hash = x.record_hash WHERE x.user_id = $1 AND x.friend_id = $2`,
		int64(alice.id), int64(nb.id)).Scan(&level, &n))
	if level != "friend+" || n != 2 {
		t.Fatalf("upgrade record level %q with %d assertions", level, n)
	}
	if o, _ := e.s.Result(e.ctx, e.hsID(h)); o != core.OutcomeVerified {
		t.Fatalf("result %v", o)
	}
	if s, _ := e.s.Status(alice.ctx(), h.ref); s.State != "done" {
		t.Fatalf("final status %+v", s)
	}
	// Already friend+: no second upgrade.
	_, err = e.s.OpenUpgrade(alice.ctx(), alice.viewer(), nb.id)
	wantNotFound(t, err, "upgrade of a friend+ edge")
	// The friend+ edge counts toward the invitee's probation.
	b, err := e.s.Budget(nb.ctx())
	e.must(err)
	if b.Probation == nil || b.Probation.NeedOutsideFriends != 2 {
		t.Fatalf("probation after the upgrade %+v", b.Probation)
	}
}

func (e *env) hsID(h *hs) uuid.UUID {
	e.t.Helper()
	id, ok := e.d.Refs.OpenHandshake(viewerID(e, h), h.ref)
	if !ok {
		e.t.Fatal("bad ref")
	}
	return id
}

func viewerID(e *env, h *hs) core.UserID {
	v, ok := core.ViewerFrom(h.ctx)
	if !ok {
		e.t.Fatal("no viewer")
	}
	return v.ID
}

func TestUpgradeNeedsTheRightPeople(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.enroll("alice"), e.enroll("bob"), e.enroll("carol")
	e.friend(alice.id, bob.id)
	_, err := e.s.OpenUpgrade(alice.ctx(), alice.viewer(), carol.id)
	wantNotFound(t, err, "upgrade with a stranger")
	_, err = e.s.OpenUpgrade(alice.ctx(), alice.viewer(), alice.id)
	wantNotFound(t, err, "upgrade with oneself")
	hv, err := e.s.OpenUpgrade(alice.ctx(), alice.viewer(), bob.id)
	e.must(err)
	h := e.started(alice.ctx(), hv)
	// Only bob's own signed-in session can claim it.
	wantNotFound(t, e.peerOf(carol).claim(h.payload), "another user")
	wantNotFound(t, e.guest().claim(h.payload), "a guest session")
	p := e.peerOf(bob)
	e.must(p.claim(h.payload))
	_, err = e.s.RegisterBegin(p.ctx, p.dev.Call(invites.CallRegisterBegin), e.form("nope"))
	wantNotFound(t, err, "registration on an upgrade")
	// Unfriended before the second signature: nothing is written.
	e.must(e.s.Confirm(alice.ctx(), h.ref))
	out, err := e.play(h, p, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("outcome %+v", out)
	}
	e.must(e.signs(alice, invites.PurposeFriendUpgrade, h.ref))
	st, err := p.status()
	e.must(err)
	e.d.Graph.RemoveEdge(alice.id, bob.id)
	wantNotFound(t, e.signs(bob, invites.PurposeFriendUpgrade, st.HS), "upgrade after an unfriend")
}

func TestRecoveryHandshake(t *testing.T) {
	e := newEnv(t)
	guardian, account := e.enroll("guardian"), e.enroll("account")
	e.friend(guardian.id, account.id)
	_, err := e.s.OpenRecovery(e.ctx, guardian.id, account.id)
	wantNotFound(t, err, "a plain friend is no guardian")
	e.exec("UPDATE edges SET level = 2 WHERE user_id IN ($1, $2)", int64(guardian.id), int64(account.id))
	e.d.Graph.AddEdge(guardian.id, account.id, core.LevelFriendPlus, e.d.Clock.Now())
	hv, err := e.s.OpenRecovery(e.ctx, guardian.id, account.id)
	e.must(err)
	h := e.started(guardian.ctx(), hv)
	g := e.guest()
	// Not through the invite claim, only through ClaimRecovery.
	wantNotFound(t, g.claim(h.payload), "recovery through /join/claim")
	raw, err := json.Marshal(g.dev.Claim(h.payload))
	e.must(err)
	_, err = e.s.ClaimRecovery(e.ctx, g.sess, `{"payload":"`+h.payload+`"}`)
	wantNotFound(t, err, "recovery claim without the device keys")
	got, err := e.s.ClaimRecovery(e.ctx, g.sess, string(raw))
	e.must(err)
	if got != account.id {
		t.Fatalf("claimed account %d, want %d", got, account.id)
	}
	st, err := g.status()
	e.must(err)
	if st.Kind != "recovery" || st.State != "claimed" || st.SAS == "" {
		t.Fatalf("status %+v", st)
	}
	e.must(e.s.Confirm(h.ctx, h.ref))
	out, err := e.play(h, g, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("outcome %+v", out)
	}
	if o, err := e.s.Result(e.ctx, e.hsID(h)); err != nil || o != core.OutcomeVerified {
		t.Fatalf("result %v %v", o, err)
	}
	if st, _ := g.status(); st.State != "done" || st.Outcome != "verified" {
		t.Fatalf("final status %+v", st)
	}
	if _, err := e.s.Result(e.ctx, uuid.UUID{1}); err == nil {
		t.Fatal("result of an unknown handshake")
	}
}
