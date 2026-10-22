package records_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

type blockBody struct {
	Blocker uuid.UUID `json:"blocker"`
	Blocked uuid.UUID `json:"blocked"`
	At      sigs.Time `json:"at"`
}

func (f *fixture) signAs(author core.UserID, body any, kind core.RecordKind, parties ...core.UserID) []byte {
	f.t.Helper()
	h, err := f.a.Records.Sign(f.ctx, nil, kind, author, body, parties)
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func asUser(f *fixture, u core.UserID) context.Context {
	return coretest.AsUser(f.ctx, u, f.d.Clock.Now())
}

// Signer v2: an authored record is signed by the author's custodial key and
// countersigned by the server; the export lists the user key and verifies;
// without the countersign or the user key it fails.
func TestUserKeyAndServerCountersign(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	h := f.signAs(alice, blockBody{Blocker: f.ext[alice], Blocked: f.ext[bob], At: sigs.T(f.d.Clock.Now())},
		core.RecBlock, alice)
	var keyID string
	var cosigs []byte
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT key_id, cosigs FROM signed_records WHERE hash = $1", h).
		Scan(&keyID, &cosigs); err != nil {
		t.Fatal(err)
	}
	if !records.IsUserKeyID(keyID) || !bytes.Contains(cosigs, []byte(`"srv-`)) {
		t.Fatalf("record key %s cosigs %s", keyID, cosigs)
	}
	// One key per user, reused.
	f.signAs(alice, blockBody{Blocker: f.ext[alice], Blocked: f.ext[alice], At: sigs.T(f.d.Clock.Now())}, core.RecBlock, alice)
	var n int
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT count(*) FROM signing_keys WHERE owner_id = $1", int64(alice)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alice keys %d %v", n, err)
	}

	z := f.export(alice)
	if rep := verify(t, z, nil); rep.Records != 2 || rep.Valid != 2 || len(rep.Failures) != 0 {
		t.Fatalf("clean: %+v", rep)
	}
	files := unzip(t, z)
	var kf records.KeysFile
	if err := json.Unmarshal(files[records.FileKeys], &kf); err != nil || len(kf.Users) != 1 ||
		kf.Users[0].KeyID != keyID || kf.Users[0].Owner != f.ext[alice] || kf.CosignSince != nil {
		t.Fatalf("keys.json %s %v", files[records.FileKeys], err)
	}
	mine, err := f.a.Records.MyKeys(asUser(f, alice))
	if err != nil || len(mine) != 1 || mine[0].KeyID != keyID {
		t.Fatalf("my keys %v %v", mine, err)
	}

	edit := func(fn func(*sigs.Envelope)) []byte {
		var out bytes.Buffer
		for _, l := range lines(files[records.FileRecords]) {
			var env sigs.Envelope
			if err := json.Unmarshal(l, &env); err != nil {
				t.Fatal(err)
			}
			fn(&env)
			b, _ := json.Marshal(env)
			out.Write(b)
			out.WriteByte('\n')
		}
		m := map[string][]byte{}
		for k, v := range files {
			m[k] = v
		}
		m[records.FileRecords] = out.Bytes()
		return rezip(t, m)
	}
	rep := verify(t, edit(func(e *sigs.Envelope) { e.Cosigs = nil }), nil)
	if len(rep.Failures) != 2 || rep.Failures[0].Code != records.FailBadCosig {
		t.Fatalf("no countersign: %+v", rep)
	}
	rep = verify(t, edit(func(e *sigs.Envelope) { e.Cosigs[0].Sig = e.Sig }), nil)
	if len(rep.Failures) != 2 || rep.Failures[0].Code != records.FailBadCosig {
		t.Fatalf("user sig as countersign: %+v", rep)
	}
	// keys.json without the user key, or with a key whose id does not match.
	noUsers := map[string][]byte{}
	for k, v := range files {
		noUsers[k] = v
	}
	kf2 := kf
	kf2.Users = nil
	noUsers[records.FileKeys], _ = json.Marshal(kf2)
	if rep := verify(t, rezip(t, noUsers), nil); len(rep.Failures) != 2 || rep.Failures[0].Code != records.FailUnknownKey {
		t.Fatalf("no user key: %+v", rep)
	}
	kf2.Users = append(kf2.Users, kf.Users[0])
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32)).Public().(ed25519.PublicKey)
	kf2.Users[0].PublicKey = base64.RawURLEncoding.EncodeToString(other)
	kf2.Users[0].Fingerprint = sigs.Fingerprint(other)
	noUsers[records.FileKeys], _ = json.Marshal(kf2)
	if rep := verify(t, rezip(t, noUsers), nil); len(rep.Failures) != 2 || rep.Failures[0].Code != records.FailUnknownKey {
		t.Fatalf("substituted user key: %+v", rep)
	}
}

// Acceptance: a wrapped key moved to another user's row fails to unwrap.
func TestWrappedKeyBoundToOwnerAndID(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	body := func(u core.UserID) blockBody {
		return blockBody{Blocker: f.ext[u], Blocked: f.ext[u], At: sigs.T(f.d.Clock.Now())}
	}
	f.signAs(alice, body(alice), core.RecBlock, alice)
	f.signAs(bob, body(bob), core.RecBlock, bob)
	if _, err := f.d.DB.Q().Exec(f.ctx, `UPDATE signing_keys SET private_key_enc =
		(SELECT private_key_enc FROM signing_keys WHERE owner_id = $1) WHERE owner_id = $2`, int64(alice), int64(bob)); err != nil {
		t.Fatal(err)
	}
	advance(t, f.a, time.Millisecond)
	if _, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, bob, body(bob), []core.UserID{bob}); err == nil {
		t.Fatal("bob signed with alice's wrapped key")
	}
	// The AAD binds owner and key id.
	kek := bytes.Repeat([]byte{1}, 32)
	seed := bytes.Repeat([]byte{2}, 32)
	w, err := records.WrapSeed(kek, "usr-1", alice, seed)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := records.UnwrapSeed(kek, "usr-1", alice, w); err != nil || !bytes.Equal(got, seed) {
		t.Fatalf("unwrap: %v", err)
	}
	for name, try := range map[string]func() ([]byte, error){
		"other owner": func() ([]byte, error) { return records.UnwrapSeed(kek, "usr-1", bob, w) },
		"other id":    func() ([]byte, error) { return records.UnwrapSeed(kek, "usr-2", alice, w) },
		"other kek":   func() ([]byte, error) { return records.UnwrapSeed(bytes.Repeat([]byte{9}, 32), "usr-1", alice, w) },
		"truncated":   func() ([]byte, error) { return records.UnwrapSeed(kek, "usr-1", alice, w[:10]) },
	} {
		if _, err := try(); err == nil {
			t.Errorf("%s unwrapped", name)
		}
	}
}

// Acceptance: KEK missing in prod ⇒ startup refused.
func TestKEKRequiredInProd(t *testing.T) {
	f := newFixture(t)
	cfg := *f.d.Cfg
	cfg.Env, cfg.KEK = config.EnvProd, nil
	d := *f.d
	d.Cfg = &cfg
	err := records.New(&d).Init(f.ctx)
	if err == nil || !strings.Contains(err.Error(), "PCC_KEK") {
		t.Fatalf("prod without KEK: %v", err)
	}
	cfg.KEK = []byte("short")
	if err := records.New(&d).Init(f.ctx); err == nil {
		t.Fatal("short KEK accepted")
	}
}

// Sign refuses an assertion that does not belong to the record.
func TestSignRejectsUnboundAssertion(t *testing.T) {
	f := newFixture(t)
	alice := f.user("alice")
	body := blockBody{Blocker: f.ext[alice], Blocked: f.ext[alice], At: sigs.T(f.d.Clock.Now())}
	other := blockBody{Blocker: f.ext[alice], Blocked: uuid.Must(uuid.NewV7()), At: body.At}
	draft, _, err := coreref.DraftRecord(core.RecBlock, other, uuid.Must(uuid.NewV7()), f.d.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]core.Assertion{
		"no draft":    {CredentialID: []byte{1}},
		"other draft": {CredentialID: []byte{1}, Draft: draft},
	} {
		if _, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, alice, body, []core.UserID{alice}, a); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// Acceptance: once co-signing is enforced, a server-signed edge without an
// assertion (an insider's forgery) is reported; records before the cut-over
// are P0-era and still verify.
func TestCutOverFlagsUnattestedEdges(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.user("alice"), f.user("bob"), f.user("carol")
	f.befriend(alice, bob) // P0-era
	advance(t, f.a, time.Hour)
	if err := f.a.Records.EnforceCosign(f.ctx, f.d.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	// A later EnforceCosign never moves the cut-over forward.
	if err := f.a.Records.EnforceCosign(f.ctx, f.d.Clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	since, err := f.a.Records.CosignSince(f.ctx)
	if err != nil || since == nil || !since.Equal(f.d.Clock.Now()) {
		t.Fatalf("since %v %v", since, err)
	}
	advance(t, f.a, time.Minute)
	f.befriend(alice, carol) // forged: no assertion
	rep := verify(t, f.export(alice), nil)
	if rep.Records != 2 || rep.Valid != 1 || len(rep.Failures) != 1 || rep.Failures[0].Code != records.FailMissingAssertion {
		t.Fatalf("forged edge: %+v", rep)
	}
	if rep := verify(t, f.export(bob), nil); len(rep.Failures) != 0 {
		t.Fatalf("P0-era edge: %+v", rep)
	}
}
