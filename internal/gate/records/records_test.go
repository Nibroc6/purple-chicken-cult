package records_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/records"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/wire/wiretest"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

type friendship struct {
	A         uuid.UUID `json:"a"`
	B         uuid.UUID `json:"b"`
	Level     string    `json:"level"`
	CreatedAt sigs.Time `json:"created_at"`
}

type revocation struct {
	Edge struct {
		A uuid.UUID `json:"a"`
		B uuid.UUID `json:"b"`
	} `json:"edge"`
	By uuid.UUID `json:"by"`
	At sigs.Time `json:"at"`
}

type postRecord struct {
	ID         uuid.UUID  `json:"id"`
	Version    int        `json:"version"`
	Author     uuid.UUID  `json:"author"`
	BodyCommit sigs.Bytes `json:"body_commit"`
	Lang       string     `json:"lang"`
	Media      []string   `json:"media"`
	Parents    []string   `json:"parents"`
	CreatedAt  sigs.Time  `json:"created_at"`
}

type fixture struct {
	t        *testing.T
	a        *wire.App
	d        *core.Deps
	ctx      context.Context
	ext      map[core.UserID]uuid.UUID
	userByID map[string]core.UserID
}

func newFixture(t *testing.T, opts ...wiretest.Option) *fixture {
	t.Helper()
	a := wiretest.NewApp(t, opts...)
	return &fixture{t: t, a: a, d: a.Deps(), ctx: context.Background(), ext: map[core.UserID]uuid.UUID{},
		userByID: map[string]core.UserID{}}
}

func (f *fixture) user(handle string) core.UserID {
	f.t.Helper()
	id, err := f.a.Users.Create(f.ctx, f.d.DB.Q(), core.NewUser{Handle: handle, Genesis: true})
	if err != nil {
		f.t.Fatal(err)
	}
	m, err := f.a.Users.Get(f.ctx, id)
	if err != nil {
		f.t.Fatal(err)
	}
	f.ext[id] = m.ExtID
	f.userByID[handle] = id
	return id
}

// befriend signs a Friendship record and writes both edges rows.
func (f *fixture) befriend(a, b core.UserID) []byte {
	f.t.Helper()
	now := f.d.Clock.Now()
	h, err := f.a.Records.Sign(f.ctx, nil, core.RecFriendship, 0,
		friendship{A: f.ext[a], B: f.ext[b], Level: "friend", CreatedAt: sigs.T(now)}, []core.UserID{a, b})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, p := range [][2]core.UserID{{a, b}, {b, a}} {
		if _, err := f.d.DB.Q().Exec(f.ctx, `INSERT INTO edges (user_id, friend_id, level, since, record_hash)
			VALUES ($1, $2, 1, $3, $4)`, int64(p[0]), int64(p[1]), now, h); err != nil {
			f.t.Fatal(err)
		}
	}
	return h
}

func (f *fixture) export(u core.UserID) []byte {
	f.t.Helper()
	var buf bytes.Buffer
	ctx := coretest.AsUser(f.ctx, u, f.d.Clock.Now())
	if err := f.a.Account.Export(ctx, &buf); err != nil {
		f.t.Fatalf("export: %v", err)
	}
	return buf.Bytes()
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

func verify(t *testing.T, z []byte, pinned map[string]string) gate.VerifyReport {
	t.Helper()
	rep, err := records.VerifyExport(bytes.NewReader(z), int64(len(z)), pinned)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return rep
}

func lines(b []byte) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte{'\n'}) {
		if len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

func lineOfKind(t *testing.T, recs []byte, kind string) (int, sigs.Envelope) {
	t.Helper()
	for i, l := range lines(recs) {
		var env sigs.Envelope
		if err := json.Unmarshal(l, &env); err != nil {
			t.Fatal(err)
		}
		if env.Kind == kind {
			return i, env
		}
	}
	t.Fatalf("no %s record in %s", kind, recs)
	return 0, sigs.Envelope{}
}

func advance(t *testing.T, a *wire.App, d time.Duration) {
	t.Helper()
	if err := a.Hooks().AdvanceClock(d); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: export of two friends contains the byte-identical Friendship
// record and both verify against keys.json (also pinned to the published
// fingerprints).
func TestExportOfFriendsSharesByteIdenticalRecord(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	f.befriend(alice, bob)

	za, zb := f.export(alice), f.export(bob)
	fa, fb := unzip(t, za), unzip(t, zb)
	for _, name := range []string{records.FileRecords, records.FileKeys, records.FilePosts, records.FileCredentials, records.FileManifest} {
		if _, ok := fa[name]; !ok {
			t.Fatalf("export lacks %s", name)
		}
	}
	ia, ea := lineOfKind(t, fa[records.FileRecords], "friendship")
	ib, eb := lineOfKind(t, fb[records.FileRecords], "friendship")
	if !bytes.Equal(lines(fa[records.FileRecords])[ia], lines(fb[records.FileRecords])[ib]) {
		t.Fatalf("friendship lines differ:\n%s\n%s", lines(fa[records.FileRecords])[ia], lines(fb[records.FileRecords])[ib])
	}
	if !bytes.Equal(ea.Body, eb.Body) || ea.Sig != eb.Sig {
		t.Fatal("friendship records differ")
	}

	keys, err := f.a.Records.Keys(f.ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys %v %v", keys, err)
	}
	pinned := map[string]string{keys[0].KeyID: keys[0].Fingerprint}
	for _, z := range [][]byte{za, zb} {
		for _, pin := range []map[string]string{nil, pinned} {
			rep := verify(t, z, pin)
			if rep.Records != 1 || rep.Valid != 1 || len(rep.Failures) != 0 || rep.KeysPinned != (pin != nil) {
				t.Fatalf("report %+v", rep)
			}
		}
	}
	// A pin that does not match the published key rejects every record.
	rep := verify(t, za, map[string]string{keys[0].KeyID: strings.Repeat("0", 64)})
	if rep.Valid != 0 || len(rep.Failures) != 1 || rep.Failures[0].Code != records.FailUnknownKey {
		t.Fatalf("wrong pin: %+v", rep)
	}
}

// Acceptance: tampering one byte, changing the envelope kind, swapping key_id
// or a non-canonical body fails VerifyExport.
func TestVerifyExportRejectsTampering(t *testing.T) {
	dir := t.TempDir()
	f := newFixture(t, wiretest.WithEnv("PCC_SERVER_KEY_FILE", filepath.Join(dir, "server.key")))
	alice, bob, carol := f.user("alice"), f.user("bob"), f.user("carol")
	f.befriend(alice, bob)
	// A second published key, so a swapped key_id names a real key.
	if _, err := f.a.Records.RotateServerKey(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.befriend(alice, carol)
	z := f.export(alice)
	files := unzip(t, z)
	if rep := verify(t, z, nil); rep.Valid != 2 || len(rep.Failures) != 0 {
		t.Fatalf("clean export: %+v", rep)
	}
	recs := lines(files[records.FileRecords])
	var envs []sigs.Envelope
	for _, l := range recs {
		var env sigs.Envelope
		if err := json.Unmarshal(l, &env); err != nil {
			t.Fatal(err)
		}
		envs = append(envs, env)
	}
	if envs[0].KeyID == envs[1].KeyID {
		t.Fatal("records should be signed by different keys")
	}

	mutate := func(name string, fn func([]byte) []byte, want string) {
		t.Run(name, func(t *testing.T) {
			cp := map[string][]byte{}
			for k, v := range files {
				cp[k] = v
			}
			first := fn(append([]byte(nil), recs[0]...))
			cp[records.FileRecords] = append(append(first, '\n'), append(recs[1], '\n')...)
			rep := verify(t, rezip(t, cp), nil)
			if rep.Records != 2 || rep.Valid != 1 || len(rep.Failures) != 1 || rep.Failures[0].Line != 1 || rep.Failures[0].Code != want {
				t.Fatalf("%s: %+v", name, rep)
			}
		})
	}
	reenc := func(env sigs.Envelope) []byte {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(env); err != nil {
			t.Fatal(err)
		}
		return bytes.TrimRight(buf.Bytes(), "\n")
	}
	mutate("one byte of the body", func(_ []byte) []byte {
		env := envs[0]
		body := bytes.Replace(env.Body, []byte(`"friend"`), []byte(`"friene"`), 1)
		if bytes.Equal(body, env.Body) {
			t.Fatal("no byte changed")
		}
		env.Body = body
		return reenc(env)
	}, records.FailBadSig)
	mutate("envelope kind", func(_ []byte) []byte {
		env := envs[0]
		env.Kind = "revocation"
		return reenc(env)
	}, records.FailKindMismatch)
	mutate("swapped key_id", func(_ []byte) []byte {
		env := envs[0]
		env.KeyID = envs[1].KeyID
		return reenc(env)
	}, records.FailBadSig)
	mutate("unknown key_id", func(_ []byte) []byte {
		env := envs[0]
		env.KeyID = "srv-0000000000000000"
		return reenc(env)
	}, records.FailUnknownKey)
	mutate("non-canonical body", func(_ []byte) []byte {
		env := envs[0]
		var m map[string]any
		if err := json.Unmarshal(env.Body, &m); err != nil {
			t.Fatal(err)
		}
		pretty, err := json.MarshalIndent(m, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.WriteString(`{"kind":"friendship","schema_version":1,"key_id":"` + env.KeyID + `","body":`)
		buf.Write(pretty)
		buf.WriteString(`,"sig":"` + env.Sig + `"}`)
		return bytes.ReplaceAll(buf.Bytes(), []byte("\n"), []byte(" "))
	}, records.FailNotCanonical)
	mutate("one byte of the signature", func(_ []byte) []byte {
		env := envs[0]
		b := []byte(env.Sig)
		if b[3] == 'A' {
			b[3] = 'B'
		} else {
			b[3] = 'A'
		}
		env.Sig = string(b)
		return reenc(env)
	}, records.FailBadSig)
}

// Acceptance: the value grammar rejects floats, non-UTC times and nulls.
func TestSignEnforcesValueGrammar(t *testing.T) {
	f := newFixture(t)
	alice := f.user("alice")
	paris := time.FixedZone("CET", 3600)
	bad := map[string]any{
		"float":    map[string]any{"x": 1.5},
		"std time": map[string]any{"t": time.Date(2026, 1, 1, 0, 0, 0, 0, paris)},
		"null":     map[string]any{"n": nil},
		"null pointer": struct {
			P *string `json:"p"`
		}{},
		"big integer": map[string]any{"n": int64(1) << 60},
		"utc std time": struct {
			T time.Time `json:"t"`
		}{T: time.Now().UTC()},
	}
	for name, body := range bad {
		if _, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, alice, body, []core.UserID{alice}); !errors.Is(err, sigs.ErrGrammar) {
			t.Errorf("%s: err = %v, want ErrGrammar", name, err)
		}
	}
	var ts sigs.Time
	for _, s := range []string{`"2026-01-01T01:00:00.000+01:00"`, `"2026-01-01T00:00:00Z"`, `"2026-01-01T00:00:00.000000Z"`} {
		if err := json.Unmarshal([]byte(s), &ts); !errors.Is(err, sigs.ErrGrammar) {
			t.Errorf("time %s accepted: %v", s, err)
		}
	}
	var n int
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT count(*) FROM signed_records").Scan(&n); err != nil || n != 0 {
		t.Fatalf("records stored after grammar errors: %d %v", n, err)
	}
	// A valid body stores once with its parties; re-signing is idempotent.
	body := map[string]any{"blocker": f.ext[alice].String(), "blocked": uuid.Nil.String(), "at": sigs.T(f.d.Clock.Now())}
	h1, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, alice, body, []core.UserID{alice})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := f.a.Records.Sign(f.ctx, nil, core.RecBlock, alice, body, []core.UserID{alice, alice})
	if err != nil || !bytes.Equal(h1, h2) {
		t.Fatalf("re-sign: %v", err)
	}
	var stored []byte
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT body FROM signed_records WHERE hash = $1", h1).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if h := sha256.Sum256(stored); !bytes.Equal(h[:], h1) || !bytes.Contains(stored, []byte(`"kind":"block","schema_version":1`)) {
		t.Fatalf("stored body %s", stored)
	}
	// Delete removes the record and its parties.
	if err := f.a.Records.Delete(f.ctx, nil, h1); err != nil {
		t.Fatal(err)
	}
	if err := f.d.DB.Q().QueryRow(f.ctx, "SELECT count(*) FROM record_parties").Scan(&n); err != nil || n != 0 {
		t.Fatalf("parties left: %d %v", n, err)
	}
}

// Acceptance: a deleted post's exported body_commit cannot be matched without
// the salt (the salt is deleted with the body; the commitment is not the plain
// hash of the body).
func TestDeletedPostCommitNeedsSalt(t *testing.T) {
	f := newFixture(t)
	alice := f.user("alice")
	const body = "a short secret that is easy to guess"
	salt := bytes.Repeat([]byte{7}, 16)
	id := uuid.MustParse("01890000-0000-7000-8000-000000000001")
	now := f.d.Clock.Now()
	h, err := f.a.Records.Sign(f.ctx, nil, core.RecPost, alice, postRecord{ID: id, Version: 1, Author: f.ext[alice],
		BodyCommit: records.BodyCommit(salt, body), Lang: "en", Media: []string{}, Parents: []string{},
		CreatedAt: sigs.T(now)}, []core.UserID{alice})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.DB.Q().Exec(f.ctx, `INSERT INTO posts (id, author_id, body, body_salt, lang, version, created_at, record_hash)
		VALUES ($1, $2, $3, $4, 'en', 1, $5, $6)`, id, int64(alice), body, salt, now, h); err != nil {
		t.Fatal(err)
	}
	z := f.export(alice)
	if rep := verify(t, z, nil); rep.BodiesChecked != 1 || len(rep.Failures) != 0 {
		t.Fatalf("live post: %+v", rep)
	}
	// A wrong body does not open the commitment.
	files := unzip(t, z)
	files[records.FilePosts] = bytes.Replace(files[records.FilePosts], []byte("secret"), []byte("Secret"), 1)
	if rep := verify(t, rezip(t, files), nil); rep.BodiesChecked != 0 || len(rep.Failures) != 1 || rep.Failures[0].Code != records.FailCommitMismatch {
		t.Fatalf("edited body: %+v", rep)
	}

	if _, err := f.d.DB.Q().Exec(f.ctx, "UPDATE posts SET body = NULL, body_salt = NULL, deleted_at = $2 WHERE id = $1", id, now); err != nil {
		t.Fatal(err)
	}
	files = unzip(t, f.export(alice))
	if len(lines(files[records.FilePosts])) != 0 {
		t.Fatalf("deleted post exported: %s", files[records.FilePosts])
	}
	_, env := lineOfKind(t, files[records.FileRecords], "post")
	var pr postRecord
	if err := json.Unmarshal(env.Body, &pr); err != nil {
		t.Fatal(err)
	}
	plain := sha256.Sum256([]byte(body))
	if bytes.Equal(pr.BodyCommit, plain[:]) || bytes.Equal(pr.BodyCommit, records.BodyCommit(nil, body)) {
		t.Fatal("body_commit matches the unsalted body")
	}
	if !bytes.Equal(pr.BodyCommit, records.BodyCommit(salt, body)) {
		t.Fatal("body_commit is not the salted commitment")
	}
	for name, b := range files {
		if bytes.Contains(b, []byte(base64.RawURLEncoding.EncodeToString(salt))) || bytes.Contains(b, []byte(body)) {
			t.Fatalf("%s still holds the salt or the body", name)
		}
	}
}

// Acceptance: AuditEdges flags an edges row whose record is missing or revoked.
func TestAuditEdges(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol, dave := f.user("alice"), f.user("bob"), f.user("carol"), f.user("dave")
	f.befriend(alice, bob)
	if bad, err := f.a.Records.AuditEdges(f.ctx); err != nil || bad != 0 {
		t.Fatalf("clean audit: %d %v", bad, err)
	}
	// Missing record (both rows of the pair are flagged).
	missing := sha256.Sum256([]byte("no such record"))
	for _, p := range [][2]core.UserID{{alice, carol}, {carol, alice}} {
		if _, err := f.d.DB.Q().Exec(f.ctx, `INSERT INTO edges (user_id, friend_id, level, since, record_hash)
			VALUES ($1, $2, 1, now(), $3)`, int64(p[0]), int64(p[1]), missing[:]); err != nil {
			t.Fatal(err)
		}
	}
	if bad, err := f.a.Records.AuditEdges(f.ctx); err != nil || bad != 2 {
		t.Fatalf("missing record: %d %v", bad, err)
	}
	// A record signed for other users does not back this edge.
	h := f.befriend(bob, dave)
	if _, err := f.d.DB.Q().Exec(f.ctx, "UPDATE edges SET record_hash = $1 WHERE user_id = $2 AND friend_id = $3",
		h, int64(alice), int64(carol)); err != nil {
		t.Fatal(err)
	}
	if bad, _ := f.a.Records.AuditEdges(f.ctx); bad != 2 {
		t.Fatalf("foreign record: %d", bad)
	}
	if _, err := f.d.DB.Q().Exec(f.ctx, "DELETE FROM edges WHERE user_id IN ($1, $2) AND friend_id IN ($1, $2)",
		int64(alice), int64(carol)); err != nil {
		t.Fatal(err)
	}
	// Revoked: a Revocation of the pair after the friendship.
	advance(t, f.a, time.Minute)
	var rv revocation
	rv.Edge.A, rv.Edge.B, rv.By, rv.At = f.ext[bob], f.ext[alice], f.ext[alice], sigs.T(f.d.Clock.Now())
	if _, err := f.a.Records.Sign(f.ctx, nil, core.RecRevocation, alice, rv, []core.UserID{alice, bob}); err != nil {
		t.Fatal(err)
	}
	if bad, _ := f.a.Records.AuditEdges(f.ctx); bad != 2 {
		t.Fatalf("revoked by record: %d", bad)
	}
	// Revoked: scheduled for purging (§2.3).
	if err := f.a.Records.SetPurgeAfter(f.ctx, nil, h, f.d.Clock.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if bad, _ := f.a.Records.AuditEdges(f.ctx); bad != 4 {
		t.Fatalf("purging record: %d", bad)
	}
	// The weekly job runs the same audit without failing.
	if err := f.a.DrainJobs(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRotateKeysAndPurge(t *testing.T) {
	dir := t.TempDir()
	f := newFixture(t, wiretest.WithEnv("PCC_SERVER_KEY_FILE", filepath.Join(dir, "keys", "server.key")))
	alice, bob := f.user("alice"), f.user("bob")
	h1 := f.befriend(alice, bob)
	before, _ := f.a.Records.Keys(f.ctx)
	advance(t, f.a, time.Hour)
	line, err := f.a.Records.RotateServerKey(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := f.a.Records.Keys(f.ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys after rotation: %+v %v", keys, err)
	}
	if keys[0].KeyID != before[0].KeyID || keys[0].ValidUntil == nil || !keys[0].ValidUntil.Equal(f.d.Clock.Now()) {
		t.Fatalf("old key window not closed: %+v", keys[0])
	}
	if keys[1].ValidUntil != nil || line != keys[1].KeyID+"  "+keys[1].Fingerprint {
		t.Fatalf("new key %+v, line %q", keys[1], line)
	}
	loaded, err := sigs.LoadServerKey(filepath.Join(dir, "keys", "server.key"))
	if err != nil || loaded.ID != keys[1].KeyID {
		t.Fatalf("key file not replaced: %v", err)
	}
	carol := f.user("carol")
	h2 := f.befriend(alice, carol)
	var k1, k2 string
	_ = f.d.DB.Q().QueryRow(f.ctx, "SELECT key_id FROM signed_records WHERE hash = $1", h1).Scan(&k1)
	_ = f.d.DB.Q().QueryRow(f.ctx, "SELECT key_id FROM signed_records WHERE hash = $1", h2).Scan(&k2)
	if k1 != keys[0].KeyID || k2 != keys[1].KeyID {
		t.Fatalf("signing keys %s %s", k1, k2)
	}
	if rep := verify(t, f.export(alice), nil); rep.Valid != 2 || len(rep.Failures) != 0 {
		t.Fatalf("export across a rotation: %+v", rep)
	}
	pinned := map[string]string{keys[0].KeyID: keys[0].Fingerprint, keys[1].KeyID: keys[1].Fingerprint}
	if rep := verify(t, f.export(alice), pinned); rep.Valid != 2 {
		t.Fatalf("pinned: %+v", rep)
	}

	// Purge removes records past purge_after only.
	if err := f.a.Records.SetPurgeAfter(f.ctx, nil, h1, f.d.Clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Records.SetPurgeAfter(f.ctx, nil, []byte("nope"), f.d.Clock.Now()); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("purge_after on a missing record: %v", err)
	}
	if n, err := f.a.Records.Purge(f.ctx); err != nil || n != 0 {
		t.Fatalf("early purge: %d %v", n, err)
	}
	advance(t, f.a, 2*time.Hour)
	if n, err := f.a.Records.Purge(f.ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
}

// The key window check rejects a record whose time lies outside its key's window.
func TestVerifyExportKeyWindow(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.user("alice"), f.user("bob")
	old := sigs.T(coretest.Epoch.Add(-48 * time.Hour))
	if _, err := f.a.Records.Sign(f.ctx, nil, core.RecFriendship, 0,
		friendship{A: f.ext[alice], B: f.ext[bob], Level: "friend", CreatedAt: old}, []core.UserID{alice, bob}); err != nil {
		t.Fatal(err)
	}
	rep := verify(t, f.export(alice), nil)
	if rep.Valid != 0 || len(rep.Failures) != 1 || rep.Failures[0].Code != records.FailKeyWindow {
		t.Fatalf("report %+v", rep)
	}
	if _, err := records.VerifyExport(bytes.NewReader([]byte("not a zip")), 9, nil); err == nil {
		t.Fatal("garbage accepted")
	}
}
