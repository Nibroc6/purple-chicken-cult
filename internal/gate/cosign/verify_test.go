package cosign_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/auth/authtest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

func (e *env) export(u core.UserID) []byte {
	e.t.Helper()
	var buf bytes.Buffer
	e.must(e.a.Account.Export(coretest.AsUser(e.ctx, u, e.d.Clock.Now()), &buf))
	return buf.Bytes()
}

func (e *env) verify(z []byte) gate.VerifyReport {
	e.t.Helper()
	rep, err := records.VerifyExport(bytes.NewReader(z), int64(len(z)), nil)
	e.must(err)
	return rep
}

func unzip(t *testing.T, z []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

func rezip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func codes(rep gate.VerifyReport) map[string]int {
	out := map[string]int{}
	for _, f := range rep.Failures {
		out[f.Code]++
	}
	return out
}

// cosignedFriendship has alice co-sign a Friendship with bob (and writes the edge).
func (e *env) cosignedFriendship(alice, bob *client) []byte {
	e.t.Helper()
	in := e.begin(alice, bob)
	hash, err := e.require(alice, in, mustGet(e, alice.auth, in.Options))
	e.must(err)
	e.must(e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctx, "INSERT INTO record_parties (user_id, record_hash) VALUES ($1, $2)", int64(bob.id), hash)
		return err
	}))
	return hash
}

// Acceptance: a forged edge without an assertion is detected by VerifyExport
// once co-signing is enforced; a co-signed one verifies; a tampered
// assertion is bad_assertion.
func TestVerifyExportDetectsForgedEdge(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.enroll("alice"), e.enroll("bob"), e.enroll("carol")
	e.cosignedFriendship(alice, bob)
	// The co-signed record verifies offline for both parties, cut-over or not.
	for _, u := range []*client{alice, bob} {
		if rep := e.verify(e.export(u.id)); len(rep.Failures) != 0 {
			t.Fatalf("co-signed export of %d: %+v", u.id, rep)
		}
	}
	e.must(e.a.Records.EnforceCosign(e.ctx, e.d.Clock.Now().Add(-time.Hour)))
	if rep := e.verify(e.export(alice.id)); len(rep.Failures) != 0 {
		t.Fatalf("co-signed export after cut-over: %+v", rep)
	}

	// An insider holds the server key and the custodial keys, so the forged
	// Friendship carries valid signatures — but no passkey assertion.
	a, c := e.ext(alice.id), e.ext(carol.id)
	if bytes.Compare(a[:], c[:]) > 0 {
		a, c = c, a
	}
	_, err := e.d.Signer.Sign(e.ctx, nil, core.RecFriendship, alice.id,
		friendship{A: a, B: c, Level: "friend", CreatedAt: sigs.T(e.d.Clock.Now())}, []core.UserID{alice.id, carol.id})
	e.must(err)
	rep := e.verify(e.export(carol.id))
	if codes(rep)[records.FailMissingAssertion] != 1 {
		t.Fatalf("forged edge not detected: %+v", rep)
	}

	// An assertion copied from the genuine record onto the forged one does
	// not verify for it.
	files := unzip(t, e.export(alice.id))
	ls := bytes.Split(bytes.TrimSpace(files[records.FileRecords]), []byte("\n"))
	genuine, forged := -1, -1
	envs := make([]sigs.Envelope, len(ls))
	for i, l := range ls {
		e.must(json.Unmarshal(l, &envs[i]))
		if envs[i].Kind == "friendship" {
			if len(envs[i].Assertions) > 0 {
				genuine = i
			} else {
				forged = i
			}
		}
	}
	if genuine < 0 || forged < 0 {
		t.Fatalf("records %s", files[records.FileRecords])
	}
	envs[forged].Assertions = envs[genuine].Assertions
	// …and a flipped signature byte invalidates the genuine assertion.
	var as []map[string]any
	raw, _ := json.Marshal(envs[genuine].Assertions)
	e.must(json.Unmarshal(raw, &as))
	sig, _ := as[0]["signature"].(string)
	as[0]["signature"] = flip(sig)
	b, _ := json.Marshal(as)
	e.must(json.Unmarshal(b, &envs[genuine].Assertions))
	var out bytes.Buffer
	for _, env := range envs {
		l, _ := json.Marshal(env)
		out.Write(l)
		out.WriteByte('\n')
	}
	files[records.FileRecords] = out.Bytes()
	rep = e.verify(rezip(t, files))
	if codes(rep)[records.FailBadAssertion] != 2 {
		t.Fatalf("tampered assertions: %+v", rep)
	}
}

func flip(s string) string {
	b := []byte(s)
	i := len(b) / 2
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

type passkeyChange struct {
	User         uuid.UUID  `json:"user"`
	Op           string     `json:"op"`
	CredentialID sigs.Bytes `json:"credential_id"`
	PublicKey    sigs.Bytes `json:"public_key"`
	Alg          int        `json:"alg"`
	At           sigs.Time  `json:"at"`
}

func coseP256(t *testing.T) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: b[1:33], -3: b[33:65]})
	if err != nil {
		t.Fatal(err)
	}
	return cose
}

// Acceptance: a passkey add not co-signed by an existing credential fails
// verification; one co-signed by the user's first credential verifies.
func TestPasskeyChangeChain(t *testing.T) {
	e := newEnv(t)
	e.must(e.a.Records.EnforceCosign(e.ctx, e.d.Clock.Now()))
	alice := e.enroll("alice")
	if rep := e.verify(e.export(alice.id)); len(rep.Failures) != 0 {
		t.Fatalf("first credential: %+v", rep)
	}
	// A second credential added with the alice's first one co-signing.
	newID := bytes.Repeat([]byte{9}, 16)
	body := passkeyChange{User: e.ext(alice.id), Op: "add", CredentialID: newID, PublicKey: coseP256(t), Alg: -7,
		At: sigs.T(e.d.Clock.Now())}
	e.s.RegisterPurpose("test_passkey_add", core.RecPasskeyChange,
		func(context.Context, core.Viewer, gate.Ref) (any, error) { return body, nil })
	in, err := e.s.BeginIntent(alice.ctx(), alice.viewer(), "test_passkey_add", "")
	e.must(err)
	asr := mustGet(e, alice.auth, in.Options)
	e.must(e.d.DB.Tx(e.ctx, func(tx pgx.Tx) error {
		a, _, err := e.s.Require(alice.ctx(), tx, alice.viewer(), "test_passkey_add", core.RecPasskeyChange, body, in.ID, asr)
		if err != nil {
			return err
		}
		_, err = e.d.Signer.Sign(e.ctx, tx, core.RecPasskeyChange, alice.id, body, []core.UserID{alice.id}, a)
		return err
	}))
	if rep := e.verify(e.export(alice.id)); len(rep.Failures) != 0 {
		t.Fatalf("co-signed add: %+v", rep)
	}

	// The P0 add flow (no co-sign) after the cut-over breaks the chain.
	e.advance(time.Minute)
	e.must(e.addPasskey(alice))
	rep := e.verify(e.export(alice.id))
	if codes(rep)[records.FailBrokenChain] != 1 {
		t.Fatalf("unsigned add: %+v", rep)
	}
}

// addPasskey runs the P0 add-passkey flow (fresh session; no co-sign).
func (e *env) addPasskey(c *client) error {
	e.t.Helper()
	opts, err := e.a.Auth.AddPasskeyBegin(c.ctx())
	if err != nil {
		return err
	}
	other := authtest.New(e.d.Cfg.Origin)
	resp, err := other.Create(opts)
	if err != nil {
		return err
	}
	return e.a.Auth.AddPasskeyFinish(c.ctx(), resp, "second")
}
