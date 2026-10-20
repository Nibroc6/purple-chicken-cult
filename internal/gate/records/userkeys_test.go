package records_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Authored records are signed by the author's custodial key and countersigned
// by the server; server-authored ones keep the server key alone. The export
// publishes the user key with its owner and verifies.
func TestAuthoredRecordsUseCustodialKeys(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	if !f.a.Records.UserKeysEnabled() {
		t.Fatal("test config derives a KEK")
	}
	h, err := f.a.Records.Sign(f.ctx, nil, core.RecFriendship, alice,
		friendship{A: f.ext[alice], B: f.ext[bob], Level: "friend", CreatedAt: sigs.T(f.d.Clock.Now())},
		[]core.UserID{alice, bob})
	if err != nil {
		t.Fatal(err)
	}
	var keyID string
	var cosigs []byte
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT key_id, cosigs FROM signed_records WHERE hash = $1", h).
		Scan(&keyID, &cosigs); err != nil {
		t.Fatal(err)
	}
	var owner int64
	var wrapped, pub []byte
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT owner_id, private_key_enc, public_key FROM signing_keys WHERE key_id = $1",
		keyID).Scan(&owner, &wrapped, &pub); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(keyID, records.UserKeyPrefix) || owner != int64(alice) || len(wrapped) < 60 ||
		bytes.Contains(wrapped, pub) || len(cosigs) == 0 {
		t.Fatalf("key %s owner %d wrapped %d cosigs %s", keyID, owner, len(wrapped), cosigs)
	}
	// One key per user, reused.
	again, err := f.a.Records.EnsureUserKey(f.ctx, nil, alice)
	if err != nil || again != keyID {
		t.Fatalf("ensure: %s %v", again, err)
	}
	// A server-authored record stays server-signed.
	hs := f.befriend(alice, f.user("carol"))
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT key_id FROM signed_records WHERE hash = $1", hs).Scan(&keyID); err != nil ||
		!strings.HasPrefix(keyID, "srv-") {
		t.Fatalf("server record key %s %v", keyID, err)
	}

	z := f.export(bob)
	rep := verify(t, z, nil)
	if rep.Valid != rep.Records || rep.Records == 0 {
		t.Fatalf("export: %+v", rep)
	}
	var kf records.KeysFile
	if err := json.Unmarshal(unzip(t, z)[records.FileKeys], &kf); err != nil {
		t.Fatal(err)
	}
	if len(kf.Users) != 1 || kf.Users[0].Owner != f.ext[alice] || kf.Origin != f.d.Cfg.Origin {
		t.Fatalf("keys.json %+v", kf)
	}
	// Pinning covers the server key; the user key is trusted through the
	// countersignature, so a pinned verify still passes ...
	pinned := map[string]string{}
	for _, k := range kf.Server {
		pinned[k.KeyID] = k.Fingerprint
	}
	if rep := verify(t, z, pinned); len(rep.Failures) > 0 {
		t.Fatalf("pinned: %+v", rep)
	}
	// ... and a user key listed under a wrong fingerprint is not trusted.
	kf.Users[0].Fingerprint = strings.Repeat("0", 64)
	files := unzip(t, z)
	files[records.FileKeys], _ = json.Marshal(kf)
	if rep := verify(t, rezip(t, files), nil); rep.Failures[0].Code != records.FailUnknownKey {
		t.Fatalf("bad user fingerprint: %+v", rep)
	}
}

// Acceptance: a wrapped key moved to another user's row does not unwrap, so
// nothing can be signed with it.
func TestWrappedKeyIsBoundToItsOwner(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	ka, err := f.a.Records.EnsureUserKey(f.ctx, nil, alice)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := f.a.Records.EnsureUserKey(f.ctx, nil, bob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.DB.Q().Exec(f.ctx, `UPDATE signing_keys SET private_key_enc =
		(SELECT private_key_enc FROM signing_keys WHERE key_id = $1) WHERE key_id = $2`, ka, kb); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, bob, map[string]any{"blocker": f.ext[bob],
		"blocked": f.ext[alice], "at": sigs.T(f.d.Clock.Now())}, []core.UserID{bob}); err == nil {
		t.Fatal("signed with alice's key moved to bob's row")
	}
	// Moving alice's row to bob (owner_id) does not help either.
	if _, err := f.d.DB.Q().Exec(f.ctx, "DELETE FROM signing_keys WHERE key_id = $1", kb); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.DB.Q().Exec(f.ctx, "UPDATE signing_keys SET owner_id = $2 WHERE key_id = $1", ka, int64(bob)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, bob, map[string]any{"blocker": f.ext[bob],
		"blocked": f.ext[alice], "at": sigs.T(f.d.Clock.Now())}, []core.UserID{bob}); err == nil {
		t.Fatal("signed with a key re-owned to bob")
	}
}

// Deleting an account destroys its wrapped key but keeps the public half.
func TestAccountDeletionRetiresTheUserKey(t *testing.T) {
	f := newFixture(t)
	alice := f.user("alice")
	k, err := f.a.Records.EnsureUserKey(f.ctx, nil, alice)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Bus.Publish(core.AccountDeleted{ID: alice})
	var wrapped, pub []byte
	var retired bool
	if err := f.d.DB.Q().QueryRow(f.ctx, `SELECT private_key_enc, public_key, valid_until IS NOT NULL
		FROM signing_keys WHERE key_id = $1`, k).Scan(&wrapped, &pub, &retired); err != nil {
		t.Fatal(err)
	}
	if wrapped != nil || len(pub) != 32 || !retired {
		t.Fatalf("after deletion: wrapped=%x retired=%v", wrapped, retired)
	}
}
