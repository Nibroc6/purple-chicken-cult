package coretest_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
)

// The Phase 1 references (SCAFFOLD-1) behave as their contracts say, so work
// packages can test against them before the real implementations land.

func TestReferenceMedia(t *testing.T) {
	m := coretest.NewMem(t)
	w := coretest.NewWorld(t, m.D, nil)
	a, b := w.User("a"), w.User("b")
	w.Friends("a", "b")
	ctx := context.Background()

	if ids, err := m.D.Media.Attach(ctx, nil, a, nil); err != nil || ids != nil {
		t.Fatalf("attaching nothing: %v %v", ids, err)
	}
	id, ref := m.Media.Put(a, 640, 480, "a cat")
	if _, err := m.D.Media.Attach(ctx, nil, b, []gate.Ref{ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("another user's media: %v", err)
	}
	_, bref := m.Media.Put(b, 1, 1, "")
	if _, err := m.D.Media.Attach(ctx, nil, a, []gate.Ref{bref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("a ref minted for b used by a: %v", err)
	}
	if _, err := m.D.Media.Attach(ctx, nil, a, []gate.Ref{ref, ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("duplicate ref: %v", err)
	}
	ids, err := m.D.Media.Attach(ctx, nil, a, []gate.Ref{ref})
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("attach: %v %v", ids, err)
	}
	if _, err := m.D.Media.Attach(ctx, nil, a, []gate.Ref{ref}); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("attached twice: %v", err)
	}
	five := make([]gate.Ref, 5)
	for i := range five {
		_, five[i] = m.Media.Put(a, 1, 1, "")
	}
	var p *gate.Problem
	if _, err := m.D.Media.Attach(ctx, nil, a, five); !errors.As(err, &p) {
		t.Fatalf("five media: %v", err)
	}

	s, err := m.D.Vis.SearchFor(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := m.D.Media.Views(ctx, s, ids)
	if err != nil || len(vs) != 1 || vs[0].W != 640 || vs[0].Alt != "a cat" {
		t.Fatalf("views: %+v %v", vs, err)
	}
	if got, ok := m.D.Refs.OpenMedia(b, vs[0].Ref); !ok || got != id {
		t.Fatal("view ref must be minted for the searching viewer")
	}

	var img bytes.Buffer
	if err := png.Encode(&img, image.NewGray(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	pid := coretest.NewPostID()
	mid, err := m.D.Media.Ingest(ctx, img.Bytes(), pid, "https://example.com/a.png")
	if it, ok := m.Media.Item(mid); err != nil || !ok || it.Owner != 0 || it.W != 3 || it.SourcePost != pid {
		t.Fatalf("ingest: %+v %v", it, err)
	}
	if _, err := m.D.Media.Ingest(ctx, []byte("<html>"), pid, ""); !errors.As(err, &p) || p.Code != "media_type" {
		t.Fatalf("ingest non-image: %v", err)
	}

	// Without a configured pipeline the reference fails closed.
	off := &coreref.MemMedia{}
	if _, err := off.Attach(ctx, nil, a, []gate.Ref{ref}); !errors.Is(err, gate.ErrDisabled) {
		t.Fatalf("disabled attach: %v", err)
	}
	if _, err := off.Ingest(ctx, img.Bytes(), pid, ""); !errors.Is(err, gate.ErrDisabled) {
		t.Fatalf("disabled ingest: %v", err)
	}
	if ids, err := off.Attach(ctx, nil, a, nil); err != nil || ids != nil {
		t.Fatalf("disabled, attaching nothing: %v %v", ids, err)
	}
}

func TestReferenceHandshakes(t *testing.T) {
	m := coretest.NewMem(t)
	w := coretest.NewWorld(t, m.D, nil)
	a, b, c, x := w.User("a"), w.User("b"), w.User("c"), w.User("x")
	w.Friends("a", "b")
	w.FriendsPlus("a", "c")
	ctx := context.Background()
	va := core.Viewer{ID: a, Kind: core.SessUser}

	if _, err := m.D.Handshakes.OpenUpgrade(ctx, va, x); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("upgrade with a stranger: %v", err)
	}
	if _, err := m.D.Handshakes.OpenUpgrade(ctx, va, c); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("upgrade of a friend+ edge: %v", err)
	}
	hv, err := m.D.Handshakes.OpenUpgrade(ctx, va, b)
	if err != nil || hv.HS == "" {
		t.Fatalf("upgrade: %+v %v", hv, err)
	}
	up, ok := m.D.Refs.OpenHandshake(a, hv.HS)
	if !ok {
		t.Fatal("handshake ref must be minted for the opener")
	}
	if o, err := m.D.Handshakes.Result(ctx, up); err != nil || o != core.OutcomeNone {
		t.Fatalf("fresh result: %v %v", o, err)
	}
	if err := m.Handshakes.Complete(up, core.OutcomeVerified); err != nil {
		t.Fatal(err)
	}
	if o, _ := m.D.Handshakes.Result(ctx, up); o != core.OutcomeVerified {
		t.Fatalf("completed result: %v", o)
	}

	if _, err := m.D.Handshakes.OpenRecovery(ctx, b, a); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("a plain friend cannot be a guardian: %v", err)
	}
	rv, err := m.D.Handshakes.OpenRecovery(ctx, c, a)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := m.D.Refs.OpenHandshake(c, rv.HS)
	hs, _ := m.Handshakes.Get(rid)
	var sess [32]byte
	if _, err := m.D.Handshakes.ClaimRecovery(ctx, sess, "wrong"); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("wrong payload: %v", err)
	}
	if got, err := m.D.Handshakes.ClaimRecovery(ctx, sess, hs.Payload); err != nil || got != a {
		t.Fatalf("claim: %v %v", got, err)
	}
	if _, err := m.D.Handshakes.ClaimRecovery(ctx, sess, hs.Payload); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("second claim: %v", err)
	}
	if o, _ := m.D.Handshakes.Result(ctx, rid); o != core.OutcomeVerified {
		t.Fatalf("recovery result: %v", o)
	}

	rv2, _ := m.D.Handshakes.OpenRecovery(ctx, c, a)
	rid2, _ := m.D.Refs.OpenHandshake(c, rv2.HS)
	hs2, _ := m.Handshakes.Get(rid2)
	m.Clock.Advance(coreref.HandshakeTTL)
	if _, err := m.D.Handshakes.ClaimRecovery(ctx, sess, hs2.Payload); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("expired claim: %v", err)
	}
}

func TestReferenceCosign(t *testing.T) {
	m := coretest.NewMem(t)
	w := coretest.NewWorld(t, m.D, nil)
	a, b := w.User("a"), w.User("b")
	ctx := context.Background()
	pk := m.D.Passkeys.(*coretest.FakePasskeys)
	va, vb := core.Viewer{ID: a, Kind: core.SessUser}, core.Viewer{ID: b, Kind: core.SessUser}
	body := map[string]any{"from": "a", "to": "b"}

	begin := func(v core.Viewer, purpose string) (gate.Ref, []byte) {
		t.Helper()
		in, err := m.D.Cosign.BeginIntent(ctx, v, purpose, "")
		if err != nil || len(in.Options) == 0 || len(in.Record) == 0 {
			t.Fatalf("begin: %+v %v", in, err)
		}
		id, ok := m.D.Refs.OpenHandshake(v.ID, in.ID)
		if !ok {
			t.Fatal("intent ref must be minted for the viewer")
		}
		return in.ID, pk.Assert(v.ID, purpose, id[:])
	}

	intent, asr := begin(va, "friend_request")
	if _, _, err := m.D.Cosign.Require(ctx, nil, vb, "friend_request", core.RecFriendship, body, intent, asr); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("another user's intent: %v", err)
	}
	if _, _, err := m.D.Cosign.Require(ctx, nil, va, "unfriend", core.RecFriendship, body, intent, asr); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("other purpose: %v", err)
	}
	got, draft, err := m.D.Cosign.Require(ctx, nil, va, "friend_request", core.RecFriendship, body, intent, asr)
	if err != nil || got.User != a || !bytes.Contains(draft, []byte(`"nonce"`)) || !bytes.Contains(draft, []byte(`"issued_at"`)) {
		t.Fatalf("require: %+v %s %v", got, draft, err)
	}
	if _, _, err := m.D.Cosign.Require(ctx, nil, va, "friend_request", core.RecFriendship, body, intent, asr); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("replayed intent: %v", err)
	}

	intent, asr = begin(va, "block")
	if _, _, err := m.D.Cosign.Require(ctx, nil, va, "block", core.RecFriendship, body, intent, pk.Assert(a, "block", []byte("other"))); err == nil {
		t.Fatal("assertion over another challenge accepted")
	}
	if _, _, err := m.D.Cosign.Require(ctx, nil, va, "block", core.RecFriendship, body, intent, asr); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("intent reused after a failed assertion: %v", err)
	}

	intent, asr = begin(va, "block")
	m.Clock.Advance(coreref.CosignTTL + time.Second)
	if _, _, err := m.D.Cosign.Require(ctx, nil, va, "block", core.RecFriendship, body, intent, asr); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("expired intent: %v", err)
	}
}

func TestReferenceStepUpAndModQueue(t *testing.T) {
	m := coretest.NewMem(t)
	ctx := context.Background()
	var got []core.SessionStepUp
	core.Subscribe(m.D.Bus, func(e core.SessionStepUp) { got = append(got, e) })
	sess := [32]byte{1}
	if err := m.D.StepUp.Require(ctx, 7, sess, "webdriver"); err != nil {
		t.Fatal(err)
	}
	if m.StepUp.Required[sess] != "webdriver" || len(got) != 1 || got[0].User != 7 {
		t.Fatalf("step-up: %v %v", m.StepUp.Required, got)
	}
	if err := m.D.ModQueue.Enqueue(ctx, nil, core.ModQueueItem{Source: core.ModSrcCSAM, Priority: 0, SubjectUser: 7}); err != nil {
		t.Fatal(err)
	}
	if len(m.ModQueue.Items) != 1 || m.ModQueue.Items[0].Source != core.ModSrcCSAM {
		t.Fatalf("queue: %+v", m.ModQueue.Items)
	}
}
