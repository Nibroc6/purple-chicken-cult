package invites_test

import (
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
)

// connect opens an invite code by a and lets b's signed-in browser claim it.
func (e *env) connect(a, b *member, mode string) (*hs, *guest) {
	e.t.Helper()
	h := e.openWith(a.ctx(), mode, false)
	p := e.peerOf(b)
	e.must(p.claim(h.payload))
	e.must(e.s.Confirm(h.ctx, h.ref))
	return h, p
}

// signBoth co-signs a passed connect (or upgrade) from both sides.
func (e *env) signBoth(a, b *member, h *hs, p *guest) {
	e.t.Helper()
	e.must(e.signs(a, invites.PurposeFriendUpgrade, h.ref))
	st, err := p.status()
	e.must(err)
	e.must(e.signs(b, invites.PurposeFriendUpgrade, st.HS))
}

func TestVerifiedConnectMakesFriendPlusWithoutAnInvite(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	before, err := e.s.Budget(alice.ctx())
	e.must(err)
	// A request bob sent earlier is settled by the handshake.
	e.exec("INSERT INTO friend_requests (from_id, to_id, state, created_at) VALUES ($1, $2, 0, $3)",
		int64(bob.id), int64(alice.id), e.d.Clock.Now())
	var added []core.EdgeAdded
	core.Subscribe(e.d.Bus, func(ev core.EdgeAdded) { added = append(added, ev) })

	h, p := e.connect(alice, bob, "qr_audio")
	st, err := p.status()
	e.must(err)
	if st.Kind != "connect" || st.State != "confirmed" || st.HS == "" || st.SignupToken != "" {
		t.Fatalf("peer status %+v", st)
	}
	if s, _ := e.s.Status(alice.ctx(), h.ref); s.Kind != "connect" {
		t.Fatalf("initiator status %+v", s)
	}
	_, err = e.s.RegisterBegin(p.ctx, p.dev.Call(invites.CallRegisterBegin), e.form("nope"))
	wantNotFound(t, err, "registration on a connect")
	out, err := e.play(h, p, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("outcome %+v", out)
	}
	e.must(e.signs(alice, invites.PurposeFriendUpgrade, h.ref))
	if _, ok := e.d.Graph.Level(alice.id, bob.id); ok {
		t.Fatal("connected before the second signature")
	}
	st, err = p.status()
	e.must(err)
	if st.Sign != "needed" || !st.PeerSigned {
		t.Fatalf("peer status %+v", st)
	}
	e.must(e.signs(bob, invites.PurposeFriendUpgrade, st.HS))
	if e.level(alice.id, bob.id) != core.LevelFriendPlus {
		t.Fatal("a Verified connect must be friend+")
	}
	if len(added) != 1 || added[0].A != alice.id || added[0].B != bob.id || added[0].Level != core.LevelFriendPlus {
		t.Fatalf("EdgeAdded %+v", added)
	}
	var (
		level string
		n     int
	)
	e.must(e.d.DB.Q().QueryRow(e.ctx, `SELECT convert_from(r.body, 'UTF8')::jsonb->>'level', jsonb_array_length(r.assertions) FROM edges x
		JOIN signed_records r ON r.hash = x.record_hash WHERE x.user_id = $1 AND x.friend_id = $2`,
		int64(alice.id), int64(bob.id)).Scan(&level, &n))
	if level != "friend+" || n != 2 {
		t.Fatalf("record level %q with %d assertions", level, n)
	}
	if s, _ := e.s.Status(alice.ctx(), h.ref); s.State != "done" {
		t.Fatalf("final status %+v", s)
	}
	after, err := e.s.Budget(alice.ctx())
	e.must(err)
	if after.Available != before.Available {
		t.Fatalf("a connect spent an invite: %d → %d", before.Available, after.Available)
	}
	if n := e.count("SELECT count(*) FROM invites WHERE inviter_id = $1", int64(alice.id)); n != 0 {
		t.Fatalf("%d invite records", n)
	}
	if n := e.count("SELECT count(*) FROM friend_requests WHERE state IN (0, 1)"); n != 0 {
		t.Fatalf("%d open requests left", n)
	}
	// Friends+ now: scanning again is already_friends.
	h2 := e.openWith(alice.ctx(), "qr_audio", false)
	wantProblem(t, e.peerOf(bob).claim(h2.payload), "already_friends")
}

func TestAcceptedConnectIsAPlainFriend(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.enroll("alice"), e.enroll("bob")
	h, p := e.connect(alice, bob, "qr_audio")
	out, err := e.play(h, p, sound{jingle: true})
	e.must(err)
	if out.Outcome != "accepted" {
		t.Fatalf("outcome %+v", out)
	}
	e.signBoth(alice, bob, h, p)
	if e.level(alice.id, bob.id) != core.LevelFriend {
		t.Fatal("an Accepted connect must be a plain friend")
	}
	// Plain friends scanning the code again get an upgrade (sound only).
	e.advance(time.Second) // the session's latest claim is the new one
	h2, p2 := e.connect(alice, bob, "qr_audio")
	if st, _ := p2.status(); st.Kind != "upgrade" {
		t.Fatalf("second scan %+v", st)
	}
	out, err = e.play(h2, p2, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("outcome %+v", out)
	}
	e.signBoth(alice, bob, h2, p2)
	if e.level(alice.id, bob.id) != core.LevelFriendPlus {
		t.Fatal("the upgrade did not promote the edge")
	}
}

func TestConnectNeedsTheRightPeople(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.enroll("alice"), e.enroll("bob"), e.enroll("carol")
	// Not oneself.
	h := e.openWith(alice.ctx(), "qr_audio", false)
	wantNotFound(t, e.peerOf(alice).claim(h.payload), "own code")
	// Not across a block, either way.
	e.d.Graph.SetBlock(bob.id, alice.id, true)
	wantNotFound(t, e.peerOf(bob).claim(h.payload), "blocked")
	e.d.Graph.SetBlock(bob.id, alice.id, false)
	e.d.Graph.SetBlock(alice.id, bob.id, true)
	wantNotFound(t, e.peerOf(bob).claim(h.payload), "blocking")
	e.d.Graph.SetBlock(alice.id, bob.id, false)
	// Plain friends cannot upgrade without sound.
	e.friend(alice.id, carol.id)
	hd := e.openWith(alice.ctx(), "dual_qr", false)
	wantNotFound(t, e.peerOf(carol).claim(hd.payload), "dual-QR upgrade")

	// A code opened on probation still connects; a block before the second
	// signature writes nothing.
	dave := e.join(alice, "dave", verified, "verified")
	if !e.onProbation(dave.id) {
		t.Fatal("the new account is on probation")
	}
	h3, p3 := e.connect(dave, bob, "qr_audio")
	out, err := e.play(h3, p3, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("outcome %+v", out)
	}
	e.must(e.signs(dave, invites.PurposeFriendUpgrade, h3.ref))
	st, err := p3.status()
	e.must(err)
	e.d.Graph.SetBlock(bob.id, dave.id, true)
	wantNotFound(t, e.signs(bob, invites.PurposeFriendUpgrade, st.HS), "connect after a block")
	e.d.Graph.SetBlock(bob.id, dave.id, false)

	e.advance(time.Second)
	h4, p4 := e.connect(dave, bob, "qr_audio")
	out, err = e.play(h4, p4, verified)
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("outcome %+v", out)
	}
	e.signBoth(dave, bob, h4, p4)
	if e.level(dave.id, bob.id) != core.LevelFriendPlus {
		t.Fatal("probation code did not connect")
	}
	// The friend+ edge outside dave's subtree counts toward probation.
	b, err := e.s.Budget(dave.ctx())
	e.must(err)
	if b.Probation == nil || b.Probation.NeedOutsideFriends != 1 {
		t.Fatalf("probation after the connect %+v", b.Probation)
	}
}
