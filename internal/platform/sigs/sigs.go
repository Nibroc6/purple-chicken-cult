// Package sigs implements canonical signed records (ARCHITECTURE §3): JCS
// (RFC 8785) canonicalization with an enforced value grammar, domain-separated
// Ed25519 signatures bound to a key ID, record hashes and passkey challenges.
//
// Record value grammar (security #4), enforced by Canonical:
//   - timestamps: strings "YYYY-MM-DDTHH:MM:SS.sssZ" (UTC, exactly millisecond
//     precision) — use type Time; time.Time is rejected
//   - IDs: lowercase UUID strings (uuid.UUID); users are referenced by ext_id UUIDs
//   - integers: |n| ≤ 2^53−1; floats are forbidden
//   - bytes: base64url without padding — use type Bytes; plain []byte is rejected
//   - optional fields: omitted, never null (omitempty on pointer/slice/map fields only)
//   - every record body carries "kind" (string) and "schema_version" (int)
//     inside the canonical bytes (CanonicalRecord injects them)
package sigs

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gowebpki/jcs"
)

// MaxSafeInt is the largest integer magnitude allowed in records (2^53−1).
const MaxSafeInt = 1<<53 - 1

const (
	signDomain      = "pcc-rec-v1\x00"
	challengePrefix = "pcc-v1:"
	timeLayout      = "2006-01-02T15:04:05.000Z"
)

// Errors returned by canonicalization and verification.
var (
	ErrGrammar      = errors.New("sigs: value grammar violation")
	ErrNotCanonical = errors.New("sigs: body is not canonical")
	ErrKindMismatch = errors.New("sigs: envelope kind or schema_version differs from body")
	ErrBadSig       = errors.New("sigs: bad signature")
	ErrNoRecord     = errors.New("sigs: body lacks kind or schema_version")
)

// Time is a record timestamp: UTC, millisecond precision.
type Time time.Time

// T converts t to a record Time (UTC, truncated to milliseconds).
func T(t time.Time) Time { return Time(t.UTC().Truncate(time.Millisecond)) }

// Time returns the underlying time.
func (t Time) Time() time.Time { return time.Time(t) }

// MarshalJSON encodes as "YYYY-MM-DDTHH:MM:SS.sssZ".
func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(t).UTC().Truncate(time.Millisecond).Format(timeLayout) + `"`), nil
}

// UnmarshalJSON accepts only the exact record layout.
func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.Parse(timeLayout, s)
	if err != nil || v.Format(timeLayout) != s {
		return fmt.Errorf("%w: timestamp %q", ErrGrammar, s)
	}
	*t = Time(v)
	return nil
}

// Bytes is a byte string encoded as base64url without padding.
type Bytes []byte

// MarshalJSON encodes as base64url without padding.
func (b Bytes) MarshalJSON() ([]byte, error) {
	return []byte(`"` + base64.RawURLEncoding.EncodeToString(b) + `"`), nil
}

// UnmarshalJSON decodes strict base64url without padding.
func (b *Bytes) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return fmt.Errorf("%w: bytes", ErrGrammar)
	}
	*b = v
	return nil
}

var (
	typTime      = reflect.TypeFor[Time]()
	typStdTime   = reflect.TypeFor[time.Time]()
	typBytes     = reflect.TypeFor[Bytes]()
	typUUID      = reflect.TypeFor[uuid.UUID]()
	typMarshal   = reflect.TypeFor[json.Marshaler]()
	typTextMarsh = reflect.TypeFor[interface{ MarshalText() ([]byte, error) }]()
)

// Canonical checks v against the value grammar, marshals it and returns its JCS
// form. It asserts that the output is a JCS fixed point.
func Canonical(v any) ([]byte, error) {
	if err := checkValue(reflect.ValueOf(v), "$"); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return finish(raw)
}

func finish(raw []byte) ([]byte, error) {
	out, err := jcs.Transform(raw)
	if err != nil {
		return nil, err
	}
	if err := checkJSON(out); err != nil {
		return nil, err
	}
	again, err := jcs.Transform(out)
	if err != nil || !bytes.Equal(again, out) {
		return nil, ErrNotCanonical
	}
	return out, nil
}

// CanonicalRecord canonicalizes body (a JSON object) with "kind" and
// "schema_version" injected inside the canonical bytes.
func CanonicalRecord(kind string, schemaVersion int, body any) ([]byte, error) {
	if kind == "" || schemaVersion <= 0 {
		return nil, ErrNoRecord
	}
	canon, err := Canonical(body)
	if err != nil {
		return nil, err
	}
	m, err := decodeObject(canon)
	if err != nil {
		return nil, err
	}
	if k, ok := m["kind"]; ok && k != kind {
		return nil, ErrKindMismatch
	}
	m["kind"] = kind
	m["schema_version"] = json.Number(fmt.Sprint(schemaVersion))
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return finish(raw)
}

func decodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, fmt.Errorf("%w: record body must be a JSON object", ErrGrammar)
	}
	return m, nil
}

// RecordFields returns the "kind" and "schema_version" stored inside canon.
func RecordFields(canon []byte) (kind string, schemaVersion int, err error) {
	var hdr struct {
		Kind          *string `json:"kind"`
		SchemaVersion *int    `json:"schema_version"`
	}
	if err := json.Unmarshal(canon, &hdr); err != nil {
		return "", 0, err
	}
	if hdr.Kind == nil || hdr.SchemaVersion == nil {
		return "", 0, ErrNoRecord
	}
	return *hdr.Kind, *hdr.SchemaVersion, nil
}

// RecordHash is sha256(canon).
func RecordHash(canon []byte) [32]byte { return sha256.Sum256(canon) }

// SignedMessage is the exact signed message: "pcc-rec-v1\x00" ‖ keyID ‖ 0x00 ‖ canon.
func SignedMessage(keyID string, canon []byte) []byte {
	m := make([]byte, 0, len(signDomain)+len(keyID)+1+len(canon))
	m = append(m, signDomain...)
	m = append(m, keyID...)
	m = append(m, 0)
	return append(m, canon...)
}

// Challenge is the passkey challenge for a record draft: sha256("pcc-v1:" ‖ kind ‖ canon).
// canon must contain a nonce so challenges never repeat.
func Challenge(kind string, canon []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(challengePrefix))
	h.Write([]byte(kind))
	h.Write(canon)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Key is an Ed25519 signing key with its ID and validity window.
type Key struct {
	ID    string
	Priv  ed25519.PrivateKey
	Pub   ed25519.PublicKey
	From  time.Time
	Until time.Time // zero = open-ended
}

// ServerKeyID derives the key ID of a server key from its public key:
// "srv-" ‖ first 16 hex chars of sha256(pub). (The validity window lives in the
// signing_keys table, owned by gate/records.)
func ServerKeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "srv-" + hex.EncodeToString(h[:8])
}

// Fingerprint is hex(sha256(pub)), the value published out-of-band.
func Fingerprint(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:])
}

// KeyFromSeed builds a server Key from a 32-byte Ed25519 seed.
func KeyFromSeed(seed []byte) (*Key, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("sigs: seed must be %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &Key{ID: ServerKeyID(pub), Priv: priv, Pub: pub}, nil
}

// LoadServerKey reads a 32-byte Ed25519 seed file.
func LoadServerKey(path string) (*Key, error) {
	seed, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return KeyFromSeed(seed)
}

// CreateServerKey generates a new seed and writes it (0600) to path, refusing to
// overwrite an existing file. Callable only when cfg.DevLike() (wire enforces).
func CreateServerKey(path string) (*Key, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(seed); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return KeyFromSeed(seed)
}

// Sign signs canon under the domain-separated message bound to k.ID.
func (k *Key) Sign(canon []byte) []byte {
	return ed25519.Sign(k.Priv, SignedMessage(k.ID, canon))
}

// Verify checks sig over SignedMessage(keyID, canon).
func Verify(pub ed25519.PublicKey, keyID string, canon, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, SignedMessage(keyID, canon), sig)
}

// Cosig is an extra (user) signature on a record (Phase 1).
type Cosig struct {
	KeyID string `json:"key_id"`
	Sig   string `json:"sig"`
}

// Envelope is one line of records.jsonl in exports.
type Envelope struct {
	Kind          string            `json:"kind"`
	SchemaVersion int               `json:"schema_version"`
	KeyID         string            `json:"key_id"`
	Body          json.RawMessage   `json:"body"`
	Sig           string            `json:"sig"`
	Cosigs        []Cosig           `json:"cosigs,omitempty"`
	Assertions    []json.RawMessage `json:"assertions,omitempty"`
}

// NewEnvelope signs canon (a record produced by CanonicalRecord) with k.
func NewEnvelope(k *Key, canon []byte) (Envelope, error) {
	kind, sv, err := RecordFields(canon)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Kind: kind, SchemaVersion: sv, KeyID: k.ID,
		Body: json.RawMessage(canon), Sig: base64.RawURLEncoding.EncodeToString(k.Sign(canon)),
	}, nil
}

// VerifyEnvelope verifies the stored bytes as-is: it never re-canonicalizes, it
// rejects a body that is not already canonical, an envelope whose kind or
// schema_version differ from the body's, and a bad signature.
func VerifyEnvelope(e Envelope, pub ed25519.PublicKey) error {
	again, err := jcs.Transform(e.Body)
	if err != nil || !bytes.Equal(again, e.Body) {
		return ErrNotCanonical
	}
	kind, sv, err := RecordFields(e.Body)
	if err != nil {
		return err
	}
	if kind != e.Kind || sv != e.SchemaVersion {
		return ErrKindMismatch
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(e.Sig)
	if err != nil || !Verify(pub, e.KeyID, e.Body, sig) {
		return ErrBadSig
	}
	return nil
}

// ---- grammar ----

func grammarErr(path, why string) error { return fmt.Errorf("%w at %s: %s", ErrGrammar, path, why) }

func checkValue(v reflect.Value, path string) error {
	if !v.IsValid() {
		return grammarErr(path, "null")
	}
	t := v.Type()
	switch t {
	case typTime, typUUID, typBytes:
		if t == typBytes && v.IsNil() {
			return grammarErr(path, "null bytes")
		}
		return nil
	case typStdTime:
		return grammarErr(path, "time.Time (use sigs.Time)")
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return grammarErr(path, "null")
		}
		return checkValue(v.Elem(), path)
	}
	if t.Implements(typMarshal) || t.Implements(typTextMarsh) {
		return grammarErr(path, "unsupported custom encoding "+t.String())
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return grammarErr(path, "float")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n := v.Int(); n > MaxSafeInt || n < -MaxSafeInt {
			return grammarErr(path, "integer out of range")
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if v.Uint() > MaxSafeInt {
			return grammarErr(path, "integer out of range")
		}
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return grammarErr(path, "invalid UTF-8")
		}
	case reflect.Bool:
	case reflect.Slice:
		if v.IsNil() {
			return grammarErr(path, "null slice")
		}
		if t.Elem().Kind() == reflect.Uint8 {
			return grammarErr(path, "[]byte (use sigs.Bytes)")
		}
		fallthrough
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := checkValue(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return grammarErr(path, "null map")
		}
		if t.Key().Kind() != reflect.String {
			return grammarErr(path, "non-string map key")
		}
		it := v.MapRange()
		for it.Next() {
			if err := checkValue(it.Value(), path+"."+it.Key().String()); err != nil {
				return err
			}
		}
	case reflect.Struct:
		return checkStruct(v, path)
	default:
		return grammarErr(path, "unsupported kind "+v.Kind().String())
	}
	return nil
}

func checkStruct(v reflect.Value, path string) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		omit := strings.Contains(","+opts+",", ",omitempty,") || strings.Contains(","+opts+",", ",omitzero,")
		fv := v.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct && tag == "" {
			if err := checkStruct(fv, path); err != nil {
				return err
			}
			continue
		}
		k := f.Type.Kind()
		nillable := k == reflect.Pointer || k == reflect.Slice || k == reflect.Map || k == reflect.Interface
		if omit && !nillable {
			return grammarErr(path+"."+name, "omitempty allowed on pointer/slice/map fields only")
		}
		if nillable && fv.IsNil() {
			if omit {
				continue
			}
			return grammarErr(path+"."+name, "null (use omitempty)")
		}
		if omit && (k == reflect.Slice || k == reflect.Map) && fv.Len() == 0 {
			continue
		}
		if err := checkValue(fv, path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

// checkJSON re-parses canonical output and rejects null, non-integers and
// integers beyond ±(2^53−1) — the grammar applied to the final bytes.
func checkJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	return walkJSON(v, "$")
}

func walkJSON(v any, path string) error {
	switch x := v.(type) {
	case nil:
		return grammarErr(path, "null")
	case json.Number:
		s := string(x)
		if strings.ContainsAny(s, ".eE") {
			return grammarErr(path, "float")
		}
		n, err := x.Int64()
		if err != nil || n > MaxSafeInt || n < -MaxSafeInt {
			return grammarErr(path, "integer out of range")
		}
	case map[string]any:
		for k, e := range x {
			if err := walkJSON(e, path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range x {
			if err := walkJSON(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
