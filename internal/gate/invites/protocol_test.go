package invites_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/invites/hstest"
)

// TestOutcomeTableIsExhaustive checks Score against the §7 table for every
// combination of evidence: Verified needs the ultrasonic layer (plus the QR,
// always present); Accepted needs the jingle (never in quiet mode) or a dual-QR
// round; a broken commit or a report past the server's bound is Retry; dual-QR
// is never Verified; a retry hints at the jingle in quiet mode, the volume
// otherwise.
func TestOutcomeTableIsExhaustive(t *testing.T) {
	for mask := range 1 << 7 {
		bit := func(i int) bool { return mask&(1<<i) != 0 }
		ev := invites.Evidence{Dual: bit(0), Quiet: bit(1), CommitOK: bit(2), InBound: bit(3), UltraOK: bit(4),
			JingleOK: bit(5), Sync: bit(6)}
		want, hint := core.OutcomeRetry, ""
		switch {
		case !ev.CommitOK || !ev.InBound:
		case ev.Dual:
			want = core.OutcomeAccepted
		case ev.UltraOK:
			want = core.OutcomeVerified
		case ev.JingleOK && !ev.Quiet:
			want = core.OutcomeAccepted
		case ev.Quiet:
			hint = invites.HintTryJingle
		default:
			hint = invites.HintVolumeUp
		}
		got, gotHint, channels := invites.Score(ev)
		if got != want || gotHint != hint {
			t.Errorf("%+v: outcome %d hint %q, want %d %q", ev, got, gotHint, want, hint)
		}
		if channels[0] != "qr" {
			t.Errorf("%+v: channels %v do not start with the QR", ev, channels)
		}
		if ev.Dual && got == core.OutcomeVerified {
			t.Errorf("%+v: dual-QR was Verified", ev)
		}
		has := func(c string) bool {
			for _, x := range channels {
				if x == c {
					return true
				}
			}
			return false
		}
		if want == core.OutcomeVerified && !has("ultrasonic") || want == core.OutcomeAccepted && !ev.Dual && !has("jingle") {
			t.Errorf("%+v: channels %v", ev, channels)
		}
		if has("jingle") && ev.Quiet {
			t.Errorf("%+v: jingle recorded in quiet mode", ev)
		}
	}
}

func TestPhotographedQRWithoutEVIsNotFound(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	g := e.guest()
	body := g.dev.Claim(h.payload)
	other := hstest.NewClaimer().Claim(h.payload)
	cases := map[string]invites.ClaimIn{
		"payload only":      {Payload: h.payload},
		"no proof":          {Payload: h.payload, EV: body.EV, SigPub: body.SigPub},
		"proof of another":  {Payload: h.payload, EV: body.EV, SigPub: body.SigPub, Proof: other.Proof},
		"swapped e_v":       {Payload: h.payload, EV: other.EV, SigPub: body.SigPub, Proof: body.Proof},
		"e_v not on curve":  {Payload: h.payload, EV: strings.Repeat("A", 87), SigPub: body.SigPub, Proof: body.Proof},
		"garbage signature": {Payload: h.payload, EV: body.EV, SigPub: body.SigPub, Proof: "AAAA"},
	}
	for name, in := range cases {
		wantNotFound(t, e.s.Claim(g.ctx, in), name)
	}
	if st, _ := e.s.Status(e.as(alice), h.ref); st.State != "open" {
		t.Fatalf("a refused claim changed the handshake: %+v", st)
	}
	e.must(g.claim(h.payload))
}

func TestClaimFailuresAreUniform(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	var (
		id  uuid.UUID
		key []byte
	)
	e.must(e.d.DB.Q().QueryRow(e.ctx, "SELECT id, frame_key FROM handshake_sessions").Scan(&id, &key))
	g := e.guest()

	// A frame older than FrameMaxAge (by the server's clock) or from the future.
	e.advance(time.Duration(invites.FrameMaxAge+1) * invites.FrameEvery)
	wantNotFound(t, g.claim(h.payload), "stale frame")
	wantNotFound(t, g.claim(h.dev.Frame(1000)), "future frame")
	cur := int64(invites.FrameMaxAge + 1)
	// Forged MAC, a frame of another E_i, garbage, the Phase 0 format.
	parts := strings.Split(h.dev.Frame(cur), ".")
	wantNotFound(t, g.claim(strings.Join(append(parts[:4:4], "AAAAAAAAAAAAAA"), ".")), "forged mac")
	other, err := hstest.NewInitiator(b64(key))
	e.must(err)
	other.HS = id
	wantNotFound(t, g.claim(other.Frame(cur)), "another E_i")
	wantNotFound(t, g.claim("garbage"), "garbage")
	wantNotFound(t, g.claim(fmt.Sprintf("pcc1.%s.1.AAAAAAAAAAAAAA", strings.Split(h.payload, ".")[1])), "phase 0 frame")
	// A signed-in session cannot claim an invite.
	wantNotFound(t, e.s.Claim(e.as(alice), claimIn(hstest.NewClaimer().Claim(h.dev.Frame(cur)))), "user session")
	_, err = g.status()
	wantNotFound(t, err, "status before claim")

	// The client-rendered current frame works (the device kept counting).
	e.must(g.claim(h.dev.Frame(cur)))
	// Reused handshake: a second claimer gets nothing.
	g2 := e.guest()
	wantNotFound(t, g2.claim(h.dev.Frame(cur)), "reused handshake")
	_, err = g2.status()
	wantNotFound(t, err, "status of another session")
	_, err = e.s.Frame(e.as(alice), h.ref)
	wantNotFound(t, err, "frame after claim")
	// Someone else's handshake ref.
	bob := e.w.User("bob")
	_, err = e.s.Status(e.as(bob), h.ref)
	wantNotFound(t, err, "foreign status")
	wantNotFound(t, e.s.Confirm(e.as(bob), h.ref), "foreign confirm")
	_, err = e.s.Peer(e.as(bob), h.ref)
	wantNotFound(t, err, "foreign peer")
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func TestJoinCallsNeedTheDeviceSignature(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	g := e.confirmed(h)
	thief := hstest.NewClaimer()
	thief.Claim(h.payload) // knows the frame (photographed), not the key
	for name, sig := range map[string]string{
		"none":          "",
		"garbage":       "AAAA",
		"another call":  g.dev.Call(invites.CallRegisterBegin),
		"another key":   thief.Call(invites.CallStatus),
		"replayed body": g.dev.Call(invites.CallAudioStart),
	} {
		_, err := e.s.JoinStatus(g.ctx, sig)
		wantNotFound(t, err, "status signed by "+name)
	}
	_, err := e.s.AudioStart(g.ctx, thief.Call(invites.CallAudioStart), g.dev.KmacB64())
	wantNotFound(t, err, "audio start signed by another key")
	_, err = e.s.AudioStart(g.ctx, "", g.dev.KmacB64())
	wantNotFound(t, err, "audio start without a signature")
	_, err = e.s.RegisterBegin(g.ctx, "", e.form("nosig"))
	wantNotFound(t, err, "register without a signature")
	_, _, err = e.s.RegisterCosign(g.ctx, "", []byte(`{}`))
	wantNotFound(t, err, "cosign without a signature")
	if _, err := g.status(); err != nil {
		t.Fatal(err)
	}
}

func TestLateReportIsRetryWhateverTheClientSays(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	g := e.confirmed(h)
	st, err := e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	e.must(err)
	if st.Attempt != 1 || !st.PlayAt.Equal(e.d.Clock.Now()) {
		t.Fatalf("audio start %+v", st)
	}
	if s, _ := e.s.Status(e.as(alice), h.ref); !s.Playing {
		t.Fatalf("status %+v: the initiator should be listening", s)
	}
	e.advance(invites.AudioBound + time.Millisecond)
	ev, err := e.s.Peer(e.as(alice), h.ref)
	e.must(err)
	kmac, err := h.dev.Kmac(ev)
	e.must(err)
	u, j := g.dev.Bits(1)
	zero, small := 0, 10
	out, err := e.s.Report(e.as(alice), h.ref, invites.ReportIn{UltraBits: u, JingleBits: j, KmacCommit: hstest.Commit(kmac),
		TUltraMs: &small, TJingleMs: &small, OffsetMs: &zero})
	e.must(err)
	if out.Outcome != "retry" || out.Hint != "" {
		t.Fatalf("late report %+v", out)
	}
	_, err = e.s.Report(e.as(alice), h.ref, invites.ReportIn{UltraBits: u, KmacCommit: hstest.Commit(kmac)})
	wantNotFound(t, err, "second report of one attempt")

	// Client timings only tighten: a layer the client says took too long fails.
	slow := int(invites.LayerBound/time.Millisecond) + 1
	out, err = e.play(h, g, sound{ultra: true, jingle: true, tUltra: &slow})
	e.must(err)
	if out.Outcome != "accepted" {
		t.Fatalf("slow ultrasonic: %+v", out)
	}
	if s, _ := g.status(); s.Outcome != "accepted" || s.Attempt != 2 || s.SignupToken == "" {
		t.Fatalf("join status %+v", s)
	}
}

func TestBitsOfAnotherAttemptOrKeyFail(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	g := e.confirmed(h)
	_, err := e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	e.must(err)
	ev, err := e.s.Peer(e.as(alice), h.ref)
	e.must(err)
	kmac, err := h.dev.Kmac(ev)
	e.must(err)
	u2, _ := g.dev.Bits(2) // the next attempt's bits
	out, err := e.s.Report(e.as(alice), h.ref, invites.ReportIn{UltraBits: u2, KmacCommit: hstest.Commit(kmac)})
	e.must(err)
	if out.Outcome != "retry" || out.Hint != invites.HintVolumeUp {
		t.Fatalf("bits of another attempt %+v", out)
	}
	if s, _ := g.status(); s.Outcome != "retry" || s.Hint != invites.HintVolumeUp || s.Playing {
		t.Fatalf("join status %+v", s)
	}
	// A wrong commit (the devices derived different keys) is Retry too.
	out, err = e.play(h, g, sound{ultra: true, wrongCommit: true})
	e.must(err)
	if out.Outcome != "retry" {
		t.Fatalf("wrong commit %+v", out)
	}
	// kmac is fixed for the handshake once reported.
	_, err = e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), b64(make([]byte, 32)))
	wantNotFound(t, err, "another kmac")
}

func TestQuietModeHintsAtTheJingle(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", true)
	g := e.confirmed(h)
	out, err := e.play(h, g, sound{jingle: true})
	e.must(err)
	if out.Outcome != "retry" || out.Hint != invites.HintTryJingle {
		t.Fatalf("quiet mode, jingle only: %+v", out)
	}
	out, err = e.play(h, g, sound{ultra: true})
	e.must(err)
	if out.Outcome != "verified" {
		t.Fatalf("quiet mode, ultrasonic: %+v", out)
	}
}

func TestSixthAttemptIsNotFound(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	g := e.confirmed(h)
	for i := range invites.MaxAttempts {
		out, err := e.play(h, g, sound{})
		e.must(err)
		if out.Outcome != "retry" {
			t.Fatalf("attempt %d: %+v", i+1, out)
		}
	}
	if s, _ := g.status(); s.AttemptsLeft != 0 || s.Attempt != invites.MaxAttempts {
		t.Fatalf("status %+v", s)
	}
	_, err := e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	wantNotFound(t, err, "sixth attempt")
	_, err = g.begin(e.form("never"))
	wantNotFound(t, err, "register after five retries")
}

func TestDualQRIsNeverVerified(t *testing.T) {
	e := newEnv(t)
	alice := e.enroll("alice")
	h := e.openWith(alice.ctx(), "dual_qr", true) // quiet is meaningless without sound
	if st, _ := e.s.Status(alice.ctx(), h.ref); st.Mode != "dual_qr" || st.Quiet {
		t.Fatalf("status %+v", st)
	}
	g := e.confirmed(h)
	_, err := e.s.AudioStart(g.ctx, g.dev.Call(invites.CallAudioStart), g.dev.KmacB64())
	wantNotFound(t, err, "sound in dual-QR mode")
	ev, err := e.s.Peer(alice.ctx(), h.ref)
	e.must(err)
	kmac, err := h.dev.Kmac(ev)
	e.must(err)
	dual := func(k []byte, n int64) invites.DualIn {
		return invites.DualIn{Payload: hstest.DualFrame(k, h.dev.HS, n), Kmac: g.dev.KmacB64()}
	}
	call := func() string { return g.dev.Call(invites.CallDualFrame) }
	out, err := e.s.DualFrame(g.ctx, call(), dual([]byte("another key"), 0))
	e.must(err)
	if out.Outcome != "retry" {
		t.Fatalf("frame under another key: %+v", out)
	}
	e.advance(10 * time.Second)
	out, err = e.s.DualFrame(g.ctx, call(), dual(kmac, 0))
	e.must(err)
	if out.Outcome != "retry" {
		t.Fatalf("stale dual frame: %+v", out)
	}
	out, err = e.s.DualFrame(g.ctx, call(), dual(kmac, int64(10*time.Second/invites.FrameEvery)))
	e.must(err)
	if out.Outcome != "accepted" {
		t.Fatalf("fresh dual frame: %+v", out)
	}
	_, err = e.s.Report(alice.ctx(), h.ref, invites.ReportIn{KmacCommit: hstest.Commit(kmac)})
	wantNotFound(t, err, "a sound report in dual-QR mode")
	tok, me, err := e.register(alice, h, g, e.form("quietone"))
	e.must(err)
	if tok == "" || me.Handle != "quietone" {
		t.Fatalf("me %+v", me)
	}
	nb, _ := e.a.Users.ByHandle(e.ctx, "quietone")
	if lvl := e.level(alice.id, nb); lvl != core.LevelFriend {
		t.Fatalf("dual-QR invite level %v, want friend", lvl)
	}
}

func TestBystanderGetsADifferentCode(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	bystander, invitee := e.guest(), e.guest()
	e.must(bystander.claim(h.payload))
	first, err := e.s.Status(e.as(alice), h.ref)
	e.must(err)
	// Codes don't match: the inviter cancels and opens a new handshake.
	e.must(e.s.Cancel(e.as(alice), h.ref))
	if st, _ := bystander.status(); st.State != "cancelled" || st.SAS != "" {
		t.Fatalf("bystander status %+v", st)
	}
	_, err = e.s.AudioStart(bystander.ctx, bystander.dev.Call(invites.CallAudioStart), bystander.dev.KmacB64())
	wantNotFound(t, err, "cancelled sound")
	h2 := e.openWith(e.as(alice), "qr_audio", false)
	e.must(invitee.claim(h2.payload))
	second, err := e.s.Status(e.as(alice), h2.ref)
	e.must(err)
	if first.SAS == second.SAS {
		t.Fatalf("both claimers got %s", first.SAS)
	}
}

func TestHandshakeLifecycle(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	h := e.openWith(e.as(alice), "qr_audio", false)
	wantNotFound(t, e.s.Confirm(e.as(alice), h.ref), "confirm before claim")
	// Opening again cancels the first.
	h2 := e.openWith(e.as(alice), "qr_audio", false)
	if st, _ := e.s.Status(e.as(alice), h.ref); st.State != "cancelled" {
		t.Fatalf("first handshake %+v", st)
	}
	wantNotFound(t, e.s.Cancel(e.as(alice), h.ref), "cancel twice")
	// Expiry: an unclaimed handshake lives OpenTTL.
	e.advance(invites.OpenTTL)
	if st, _ := e.s.Status(e.as(alice), h2.ref); st.State != "expired" {
		t.Fatalf("expired handshake %+v", st)
	}
	wantNotFound(t, e.guest().claim(h2.payload), "expired claim")

	// A claimed handshake must finish within ClaimedTTL.
	h3 := e.openWith(e.as(alice), "qr_audio", false)
	g := e.passed(h3, verified, "verified")
	e.advance(invites.ClaimedTTL)
	if st, _ := e.s.Status(e.as(alice), h3.ref); st.State != "expired" {
		t.Fatalf("claimed handshake %+v", st)
	}
	_, err := g.status()
	e.must(err)
	_, err = e.s.RegisterBegin(g.ctx, g.dev.Call(invites.CallRegisterBegin), e.form("late"))
	wantNotFound(t, err, "expired register")
}

func TestOpenProblems(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	_, err := e.s.Open(e.as(alice), "qr", false)
	wantProblem(t, err, "mode_unsupported") // the Phase 0 QR-only handshake is gone
	_, err = e.s.Open(e.ctx, "qr_audio", false)
	wantNotFound(t, err, "anonymous open")

	_, err = e.s.Budget(e.as(alice)) // creates the bucket
	e.must(err)
	e.exec("UPDATE invite_budgets SET tokens = 0, refreshed_at = $2 WHERE user_id = $1", int64(alice), e.d.Clock.Now())
	_, err = e.s.Open(e.as(alice), "qr_audio", false)
	wantProblem(t, err, "no_invites")
	e.advance(invites.TokenEvery)
	_, err = e.s.Open(e.as(alice), "qr_audio", false)
	e.must(err)

	e.exec("UPDATE invite_budgets SET paused_until = $2 WHERE user_id = $1", int64(alice), e.d.Clock.Now().Add(time.Hour))
	_, err = e.s.Open(e.as(alice), "qr_audio", false)
	wantProblem(t, err, "invites_paused")
	e.exec("UPDATE invite_budgets SET paused_until = NULL, revoked = true WHERE user_id = $1", int64(alice))
	_, err = e.s.Open(e.as(alice), "dual_qr", false)
	wantProblem(t, err, "invites_paused")
}

func TestFriendCap(t *testing.T) {
	e := newEnv(t)
	alice := e.w.User("alice")
	e.exec(`INSERT INTO users (id, ext_id, handle, handle_canon, is_genesis, webauthn_user_id)
		SELECT 3500000000 + g, gen_random_uuid(), 'cap' || g, 'cap' || g, true, sha256(g::text::bytea)
		FROM generate_series(1, 1000) g`)
	e.exec(`INSERT INTO friend_requests (from_id, to_id, state, created_at)
		SELECT $1, 3500000000 + g, 0, $2 FROM generate_series(1, 999) g`, int64(alice), e.d.Clock.Now())
	_, err := e.s.Open(e.as(alice), "qr_audio", false)
	e.must(err)
	e.exec("INSERT INTO friend_requests (from_id, to_id, state, created_at) VALUES ($1, 3500001000, 1, $2)",
		int64(alice), e.d.Clock.Now())
	_, err = e.s.Open(e.as(alice), "qr_audio", false)
	wantProblem(t, err, "friend_cap")
}
