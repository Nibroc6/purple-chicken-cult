package sigs

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

type friendship struct {
	A         uuid.UUID `json:"a"`
	B         uuid.UUID `json:"b"`
	Level     string    `json:"level"`
	CreatedAt Time      `json:"created_at"`
	Handshake *struct {
		Mode string `json:"mode"`
	} `json:"handshake,omitempty"`
}

var (
	ua = uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	ub = uuid.MustParse("018f0000-0000-7000-8000-000000000002")
	t0 = time.Date(2026, 1, 2, 3, 4, 5, 678_901_234, time.UTC)
)

func testKey(t testing.TB) *Key {
	k, err := KeyFromSeed(bytes.Repeat([]byte{3}, ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestCanonicalRecordShape(t *testing.T) {
	canon, err := CanonicalRecord("friendship", 1, friendship{A: ua, B: ub, Level: "friend", CreatedAt: T(t0)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"018f0000-0000-7000-8000-000000000001","b":"018f0000-0000-7000-8000-000000000002","created_at":"2026-01-02T03:04:05.678Z","kind":"friendship","level":"friend","schema_version":1}`
	if string(canon) != want {
		t.Fatalf("got  %s\nwant %s", canon, want)
	}
	k, sv, err := RecordFields(canon)
	if err != nil || k != "friendship" || sv != 1 {
		t.Fatalf("fields: %q %d %v", k, sv, err)
	}
}

func TestGrammarRejects(t *testing.T) {
	type withFloat struct {
		X float64 `json:"x"`
	}
	type withStdTime struct {
		At time.Time `json:"at"`
	}
	type withNilPtr struct {
		P *int `json:"p"`
	}
	type withNilSlice struct {
		S []string `json:"s"`
	}
	type withRawBytes struct {
		B []byte `json:"b"`
	}
	type withBig struct {
		N int64 `json:"n"`
	}
	type withOmitScalar struct {
		N int `json:"n,omitempty"`
	}
	cases := map[string]any{
		"float":        withFloat{X: 1.5},
		"time.Time":    withStdTime{At: t0},
		"nil pointer":  withNilPtr{},
		"nil slice":    withNilSlice{},
		"raw []byte":   withRawBytes{B: []byte{1}},
		"big int":      withBig{N: MaxSafeInt + 1},
		"omit scalar":  withOmitScalar{N: 1},
		"nil":          nil,
		"map nil val":  map[string]any{"x": nil},
		"map float":    map[string]any{"x": 2.5},
		"invalid utf8": map[string]string{"x": string([]byte{0xff})},
	}
	for name, v := range cases {
		if _, err := Canonical(v); !errors.Is(err, ErrGrammar) {
			t.Errorf("%s: want ErrGrammar, got %v", name, err)
		}
	}
}

func TestGrammarAccepts(t *testing.T) {
	type rec struct {
		ID    uuid.UUID `json:"id"`
		At    Time      `json:"at"`
		Data  Bytes     `json:"data"`
		Opt   *Time     `json:"opt,omitempty"`
		List  []string  `json:"list,omitempty"`
		Count int       `json:"count"`
	}
	b, err := Canonical(rec{ID: ua, At: T(t0), Data: Bytes{0xfb, 0xff}, Count: -3})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"data":"-_8"`)) {
		t.Fatalf("bytes must be base64url nopad: %s", b)
	}
	if bytes.Contains(b, []byte("null")) || bytes.Contains(b, []byte("opt")) {
		t.Fatalf("optional fields must be omitted: %s", b)
	}
}

func TestSignVerifyEnvelope(t *testing.T) {
	k := testKey(t)
	canon, err := CanonicalRecord("friendship", 1, friendship{A: ua, B: ub, Level: "friend", CreatedAt: T(t0)})
	if err != nil {
		t.Fatal(err)
	}
	env, err := NewEnvelope(k, canon)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelope(env, k.Pub); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Round-trip through JSON (as in records.jsonl) keeps the exact bytes.
	line, _ := json.Marshal(env)
	var back Envelope
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelope(back, k.Pub); err != nil {
		t.Fatalf("verify after round trip: %v", err)
	}

	tampered := env
	tampered.Body = bytes.Replace(env.Body, []byte(`"friend"`), []byte(`"frienx"`), 1)
	if err := VerifyEnvelope(tampered, k.Pub); !errors.Is(err, ErrBadSig) {
		t.Errorf("tampered body: want ErrBadSig, got %v", err)
	}
	kind := env
	kind.Kind = "revocation"
	if err := VerifyEnvelope(kind, k.Pub); !errors.Is(err, ErrKindMismatch) {
		t.Errorf("kind swap: want ErrKindMismatch, got %v", err)
	}
	keyID := env
	keyID.KeyID = "srv-other"
	if err := VerifyEnvelope(keyID, k.Pub); !errors.Is(err, ErrBadSig) {
		t.Errorf("key_id swap: want ErrBadSig, got %v", err)
	}
	nonCanon := env
	nonCanon.Body = append([]byte(" "), env.Body...)
	if err := VerifyEnvelope(nonCanon, k.Pub); !errors.Is(err, ErrNotCanonical) {
		t.Errorf("non-canonical: want ErrNotCanonical, got %v", err)
	}
	other, _ := KeyFromSeed(bytes.Repeat([]byte{4}, ed25519.SeedSize))
	if err := VerifyEnvelope(env, other.Pub); !errors.Is(err, ErrBadSig) {
		t.Errorf("wrong key: want ErrBadSig, got %v", err)
	}
}

func TestDomainSeparation(t *testing.T) {
	k := testKey(t)
	canon := []byte(`{"kind":"x","schema_version":1}`)
	sig := k.Sign(canon)
	if ed25519.Verify(k.Pub, canon, sig) {
		t.Fatal("signature must not verify over the bare body")
	}
	if !Verify(k.Pub, k.ID, canon, sig) {
		t.Fatal("signature must verify over the domain-separated message")
	}
}

func TestChallengeDiffersByKind(t *testing.T) {
	c := []byte(`{"nonce":"a"}`)
	if Challenge("invite", c) == Challenge("friendship", c) {
		t.Fatal("challenge must bind the kind")
	}
}

func TestKeyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "keys", "server.key")
	k, err := CreateServerKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateServerKey(p); err == nil {
		t.Fatal("must refuse to overwrite an existing key")
	}
	l, err := LoadServerKey(p)
	if err != nil || l.ID != k.ID || !l.Pub.Equal(k.Pub) {
		t.Fatalf("reload mismatch: %v", err)
	}
}

func TestTimeLayout(t *testing.T) {
	var tm Time
	if err := json.Unmarshal([]byte(`"2026-01-02T03:04:05.678Z"`), &tm); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"2026-01-02T03:04:05Z"`, `"2026-01-02T03:04:05.678+01:00"`, `"2026-01-02T03:04:05.6789Z"`} {
		if err := json.Unmarshal([]byte(bad), &tm); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}

func TestPropertyCanonicalIsFixedPoint(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := rapid.MapOf(rapid.StringMatching(`[a-z_]{1,8}`), rapid.Int64Range(-MaxSafeInt, MaxSafeInt)).Draw(t, "m")
		b, err := Canonical(m)
		if err != nil {
			t.Fatal(err)
		}
		b2, err := finish(b)
		if err != nil || !bytes.Equal(b, b2) {
			t.Fatal("not a fixed point")
		}
		if _, err := Canonical(json.RawMessage(b)); err == nil {
			t.Fatal("custom encoders must be rejected by the grammar")
		}
		_ = base64.RawURLEncoding
	})
}
