package invites_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
)

func TestVerifiedInviteEndToEnd(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	bob := e.w.User("bob")
	e.friend(bob, alice.id)
	before, err := e.s.Budget(alice.ctx())
	e.must(err)
	if before.Available != invites.GenesisCap || before.Cap != invites.GenesisCap || before.NextAt != nil {
		t.Fatalf("genesis budget %+v", before)
	}

	hv, err := e.s.Open(alice.ctx(), "", false)
	e.must(err)
	if hv.Mode != "qr_audio" || hv.FrameTTLms != 250 || len(hv.FrameKey) != 43 {
		t.Fatalf("handshake view %+v", hv)
	}
	_, err = e.s.Frame(alice.ctx(), hv.HS)
	wantNotFound(t, err, "frame before E_i")
	h := e.started(alice.ctx(), hv)
	wantNotFound(t, e.s.Epub(alice.ctx(), h.ref, h.dev.EI()), "second E_i")
	// The device renders the frames itself from the frame key; the server's frame is the same.
	if h.dev.Frame(h.dev.N) != h.payload || h.dev.N != 0 {
		t.Fatalf("client frame %q, server frame %q", h.dev.Frame(h.dev.N), h.payload)
	}
	if st, _ := e.s.Status(alice.ctx(), h.ref); st.State != "open" || st.SAS != "" || st.Kind != "invite" {
		t.Fatalf("status %+v", st)
	}
	_, err = e.s.Peer(alice.ctx(), h.ref)
	wantNotFound(t, err, "peer before claim")

	g := e.guest()
	e.must(g.claim(h.payload))
	inv, err := e.s.Status(alice.ctx(), h.ref)
	e.must(err)
	mine, err := g.status()
	e.must(err)
	if inv.State != "claimed" || len(inv.SAS) != 6 || mine.SAS != inv.SAS || mine.State != "claimed" {
		t.Fatalf("inviter %+v invitee %+v", inv, mine)
	}
	ev, err := e.s.Peer(alice.ctx(), h.ref)
	e.must(err)
	if ev != g.dev.EV() {
		t.Fatal("E_v not relayed")
	}
	if len(e.rec.LiveEv) == 0 {
		t.Fatal("no live event for the inviter")
	}
	if last := e.rec.LiveEv[len(e.rec.LiveEv)-1]; last.Topic != "handshake" ||
		!reflect.DeepEqual(last.Data, map[string]any{"hs": h.ref, "state": "claimed"}) {
		t.Fatalf("live event %+v, want handshake {hs, state}", last)
	}

	// The sound waits for "codes match"; registration waits for the outcome.
	_, err = e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	wantNotFound(t, err, "sound before confirm")
	e.must(e.s.Confirm(alice.ctx(), h.ref))
	_, err = g.begin(e.form("newbie"))
	wantNotFound(t, err, "register before the outcome")
	out, err := e.play(h, g, verified)
	e.must(err)
	if out.Outcome != "verified" || out.Hint != "" {
		t.Fatalf("report %+v", out)
	}
	mine, err = g.status()
	e.must(err)
	if mine.Outcome != "verified" || mine.SignupToken == "" || mine.Attempt != 1 || mine.AttemptsLeft != 4 {
		t.Fatalf("join status %+v", mine)
	}
	_, err = e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	wantNotFound(t, err, "sound after a passed outcome")

	// Form problems come before the passkey.
	f := e.form("newbie")
	f.TOSVersion = 0
	_, err = g.begin(f)
	wantProblem(t, err, "tos_required")
	f = e.form("newbie")
	f.AgeAttested = false
	_, err = g.begin(f)
	wantProblem(t, err, "age_required")
	_, err = g.begin(e.form("admin"))
	wantProblem(t, err, "handle_invalid")
	_, err = g.begin(e.form("Bob"))
	wantProblem(t, err, "handle_taken")
	f = e.form("newbie")
	f.SignupToken = invites.SignupToken([]byte("not the secret"), []byte(g.dev.EV()))
	_, err = e.s.RegisterBegin(g.ctx, g.dev.Call(invites.CallRegisterBegin), f)
	wantNotFound(t, err, "wrong signup token")

	fin, err := g.finish(e.form("@NewBie"))
	e.must(err)
	var drafts []map[string]any
	e.must(json.Unmarshal(fin.Records, &drafts))
	if len(drafts) != 2 || drafts[0]["kind"] != "invite" || drafts[1]["kind"] != "friendship" ||
		drafts[1]["level"] != "friend+" {
		t.Fatalf("drafts %s", fin.Records)
	}
	if st, _ := e.s.Status(alice.ctx(), h.ref); st.Sign != "needed" {
		t.Fatalf("inviter status %+v, want sign needed", st)
	}
	assertion, err := g.auth.Get(fin.Options)
	e.must(err)
	// The invitee is first: the transaction waits for the inviter's signature.
	_, _, err = g.cosign(assertion)
	wantProblem(t, err, "awaiting_inviter")
	if n := e.count("SELECT count(*) FROM users WHERE handle_canon = 'newbie'"); n != 0 {
		t.Fatal("an account exists before both signatures")
	}
	e.must(e.signs(alice, invites.PurposeInviteInviter, h.ref))
	if st, _ := e.s.Status(alice.ctx(), h.ref); st.Sign != "waiting" {
		t.Fatalf("inviter status after signing %+v", st)
	}
	if st, _ := g.status(); !st.PeerSigned {
		t.Fatalf("join status after the inviter signed %+v", st)
	}
	wantNotFound(t, e.signs(alice, invites.PurposeInviteInviter, h.ref), "signing twice")

	tok, me, err := g.cosign(assertion)
	e.must(err)
	if tok == "" || me.Handle != "newbie" || me.Inviter.Kind != "user" || me.Inviter.Handle != "alice" {
		t.Fatalf("me %+v", me)
	}
	nb, ok := e.a.Users.ByHandle(e.ctx, "newbie")
	if !ok {
		t.Fatal("no account")
	}
	m, err := e.d.Users.Get(e.ctx, nb)
	e.must(err)
	if m.InvitedBy != alice.id || !m.OnProbation(e.d.Clock.Now()) ||
		!m.ProbationUntil.Equal(e.d.Clock.Now().Add(invites.ProbationFor)) || len(m.Languages) != 2 {
		t.Fatalf("meta %+v", m)
	}
	// Verified ⇒ friend+ (fidelity #20).
	if lvl := e.level(alice.id, nb); lvl != core.LevelFriendPlus {
		t.Fatalf("level %v, want friend+", lvl)
	}
	// Both records carry assertions: the Invite the inviter's and the
	// invitee's, the Friendship the invitee's; the handshake field names the
	// channels that passed and the bucketed server timing only.
	var invAs, frAs int
	var hsField string
	e.must(e.d.DB.Q().QueryRow(e.ctx, `SELECT jsonb_array_length(a.assertions), jsonb_array_length(b.assertions),
		i.handshake::text FROM invites i JOIN signed_records a ON a.hash = i.invite_record
		JOIN signed_records b ON b.hash = i.friendship_record WHERE i.inviter_id = $1 AND i.invitee_id = $2`,
		int64(alice.id), int64(nb)).Scan(&invAs, &frAs, &hsField))
	if invAs != 2 || frAs != 1 {
		t.Fatalf("assertions on invite %d, friendship %d", invAs, frAs)
	}
	var info struct {
		Mode     string         `json:"mode"`
		Channels []string       `json:"channels_passed"`
		Timings  map[string]int `json:"timings"`
	}
	e.must(json.Unmarshal([]byte(hsField), &info))
	if info.Mode != "qr_audio" || !reflect.DeepEqual(info.Channels, []string{"qr", "ultrasonic", "jingle"}) ||
		info.Timings["report_ms"] != 0 {
		t.Fatalf("handshake field %s", hsField)
	}
	if n := e.count("SELECT count(*) FROM record_parties WHERE user_id = $1", int64(nb)); n < 3 {
		t.Fatalf("records of the invitee = %d, want invite, friendship and passkey", n)
	}
	if n := e.count("SELECT count(*) FROM sessions WHERE id_hash = $1", g.sess[:]); n != 0 {
		t.Fatal("guest session survived the upgrade")
	}
	var got []core.Event
	for _, ev := range e.rec.Events {
		if ev.Kind == "invite_redeemed" {
			got = append(got, ev)
		}
	}
	if len(got) != 1 || got[0].To != alice.id || got[0].SubjectUser != nb {
		t.Fatalf("notifications %+v", e.rec.Events)
	}
	if e.met.Get("invites.redeemed", "") != 1 {
		t.Fatal("metric not counted")
	}
	after, err := e.s.Budget(alice.ctx())
	e.must(err)
	if after.Available != invites.GenesisCap-1 || after.NextAt == nil ||
		!after.NextAt.Equal(e.d.Clock.Now().Add(invites.TokenEvery)) {
		t.Fatalf("budget after %+v", after)
	}
	if st, _ := e.s.Status(alice.ctx(), h.ref); st.State != "done" || st.SAS != "" {
		t.Fatalf("final status %+v", st)
	}

	// The invitee list shows the probation state to the inviter only.
	list, err := e.s.Invitees(alice.ctx())
	e.must(err)
	if len(list) != 1 || list[0].Person.Handle != "newbie" || !list[0].OnProbation || list[0].ProbationUntil == nil {
		t.Fatalf("invitees %+v", list)
	}
	other, err := e.s.Invitees(e.as(bob))
	e.must(err)
	if len(other) != 0 {
		t.Fatalf("bob sees %+v", other)
	}
	_, err = e.s.Invitees(e.ctx)
	wantNotFound(t, err, "anonymous invitees")

	// The new account is on probation under the Phase 1 rule: no handshakes.
	_, err = e.s.Open(e.as(nb), "qr_audio", false)
	wantProblem(t, err, "on_probation")
	b, err := e.s.Budget(e.as(nb))
	e.must(err)
	if b.Available != 0 || b.Probation == nil || b.Probation.NeedOutsideFriends != 2 || !b.Probation.FriendPlus {
		t.Fatalf("probation budget %+v (the friend+ inviter counts as one outside friend)", b.Probation)
	}
}

func TestAcceptedInviteIsAPlainFriend(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	// Only the jingle got through: Accepted.
	nb := e.join(alice, "plain", sound{jingle: true}, "accepted")
	if lvl := e.level(alice.id, nb.id); lvl != core.LevelFriend {
		t.Fatalf("level %v, want friend", lvl)
	}
	var lvl string
	e.must(e.d.DB.Q().QueryRow(e.ctx, `SELECT convert_from(b.body, 'UTF8')::jsonb->>'level' FROM invites i JOIN signed_records b
		ON b.hash = i.friendship_record WHERE i.invitee_id = $1`, int64(nb.id)).Scan(&lvl))
	if lvl != "friend" {
		t.Fatalf("friendship record level %q", lvl)
	}
	b, err := e.s.Budget(nb.ctx())
	e.must(err)
	if b.Probation == nil || b.Probation.NeedOutsideFriends != 3 {
		t.Fatalf("a plain friend does not count under the Phase 1 rule: %+v", b.Probation)
	}
}

func TestFailedSignupAssertionAbortsEverything(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	h := e.openWith(alice.ctx(), "qr_audio", false)
	g := e.passed(h, verified, "verified")
	fin, err := g.finish(e.form("careful"))
	e.must(err)
	e.must(e.signs(alice, invites.PurposeInviteInviter, h.ref))
	good, err := g.auth.Get(fin.Options)
	e.must(err)

	// A UV-less assertion, then one by another authenticator: both abort the
	// transaction; nothing is created and the ceremony stays usable.
	g.auth.NoUV = true
	bad, err := g.auth.Get(fin.Options)
	e.must(err)
	g.auth.NoUV = false
	_, _, err = g.cosign(bad)
	wantNotFound(t, err, "assertion without UV")
	_, _, err = g.cosign([]byte(`{}`))
	wantNotFound(t, err, "garbage assertion")
	for _, q := range []string{"SELECT count(*) FROM users WHERE handle_canon = 'careful'",
		"SELECT count(*) FROM invites", "SELECT count(*) FROM edges WHERE user_id = " + itoa(alice.id),
		"SELECT count(*) FROM signed_records WHERE kind IN (1, 2)", "SELECT count(*) FROM webauthn_credentials WHERE user_id <> " + itoa(alice.id)} {
		if n := e.count(q); n != 0 {
			t.Fatalf("%s = %d after a failed assertion", q, n)
		}
	}
	_, me, err := g.cosign(good)
	e.must(err)
	if me.Handle != "careful" {
		t.Fatalf("me %+v", me)
	}
	_, _, err = g.cosign(good)
	wantNotFound(t, err, "replayed assertion")
}

func itoa(id core.UserID) string {
	b, _ := json.Marshal(uint32(id))
	return string(b)
}

func TestHandleTakenIsAnsweredTenTimes(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	for i := range invites.HandleTries {
		e.w.User("taken" + string(rune('a'+i)))
	}
	h := e.openWith(e.as(alice), "qr_audio", false)
	g := e.passed(h, verified, "verified")
	for i := range invites.HandleTries {
		_, err := g.begin(e.form("taken" + string(rune('a'+i))))
		wantProblem(t, err, "handle_taken")
	}
	_, err := g.begin(e.form("alice"))
	wantNotFound(t, err, "11th probe")
	_, err = g.begin(e.form("freehandle"))
	wantNotFound(t, err, "after the probes")
}

func TestConcurrentRedeemCreatesOneUser(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	h := e.openWith(alice.ctx(), "qr_audio", false)
	g := e.passed(h, verified, "verified")
	fin, err := g.finish(e.form("racer"))
	e.must(err)
	e.must(e.signs(alice, invites.PurposeInviteInviter, h.ref))
	a, err := g.auth.Get(fin.Options)
	e.must(err)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
		errs []error
	)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := e.s.Redeem(e.ctx, g.sess, a)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if !errors.Is(err, gate.ErrNotFound) {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || len(errs) != 0 {
		t.Fatalf("wins = %d, errors %v", wins, errs)
	}
	if n := e.count("SELECT count(*) FROM users WHERE invited_by = $1", int64(alice.id)); n != 1 {
		t.Fatalf("users = %d", n)
	}
	if n := e.count("SELECT count(*) FROM invites"); n != 1 {
		t.Fatalf("invites = %d", n)
	}
	var tokens float64
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT tokens FROM invite_budgets WHERE user_id = $1", int64(alice.id)).Scan(&tokens))
	if tokens != invites.GenesisCap-1 {
		t.Fatalf("tokens = %v", tokens)
	}
}

func TestRegistrationNeedsItsOwnCeremony(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	h := e.openWith(alice.ctx(), "qr_audio", false)
	g := e.passed(h, verified, "verified")
	_, err := e.s.RegisterFinish(g.ctx, g.dev.Call(invites.CallRegisterFinish), json.RawMessage(`{}`))
	wantNotFound(t, err, "finish before begin")
	wantNotFound(t, e.signs(alice, invites.PurposeInviteInviter, h.ref), "inviter signs before the drafts")
	opts, err := g.begin(e.form("solo"))
	e.must(err)
	raw, err := g.auth.Create(opts)
	e.must(err)
	other := e.guest()
	_, err = e.s.RegisterFinish(other.ctx, other.dev.Call(invites.CallRegisterFinish), raw)
	wantNotFound(t, err, "finish from another session")
	// A new begin discards the drafts and the inviter's signature.
	fin, err := e.s.RegisterFinish(g.ctx, g.dev.Call(invites.CallRegisterFinish), raw)
	e.must(err)
	e.must(e.signs(alice, invites.PurposeInviteInviter, h.ref))
	_, err = g.begin(e.form("solo"))
	e.must(err)
	a, err := g.auth.Get(fin.Options)
	e.must(err)
	_, _, err = g.cosign(a)
	wantNotFound(t, err, "cosign after a new begin")
	if st, _ := e.s.Status(alice.ctx(), h.ref); st.Sign != "" {
		t.Fatalf("inviter status %+v: nothing drafted", st)
	}
}
