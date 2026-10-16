package coreref

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// StoredRecord is a record kept by ServerSigner.
type StoredRecord struct {
	Hash       []byte
	Kind       core.RecordKind
	Author     core.UserID
	KeyID      string
	Body       []byte // exact JCS bytes
	Sig        []byte
	Parties    []core.UserID
	Assertions []core.Assertion
	PurgeAfter *time.Time
}

// ServerSigner signs records with a real Ed25519 server key and keeps them in
// memory (the DB-backed signer is gate/records, P0-W11).
type ServerSigner struct {
	Key *sigs.Key

	mu      sync.RWMutex
	records map[[32]byte]StoredRecord
}

var _ core.Signer = (*ServerSigner)(nil)

// NewServerSigner returns a signer over key.
func NewServerSigner(key *sigs.Key) *ServerSigner {
	return &ServerSigner{Key: key, records: map[[32]byte]StoredRecord{}}
}

// Sign canonicalizes, signs and stores a record.
// A nil body with an assertion that carries a Draft signs the draft's record.
func (s *ServerSigner) Sign(_ context.Context, _ store.Q, kind core.RecordKind, author core.UserID, body any, parties []core.UserID, a ...core.Assertion) ([]byte, error) {
	var canon []byte
	var err error
	if body == nil && len(a) > 0 && a[0].Draft != nil {
		canon, _, _, err = RecordFromDraft(a[0].Draft)
	} else {
		canon, err = sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, body)
	}
	if err != nil {
		return nil, err
	}
	h := sigs.RecordHash(canon)
	rec := StoredRecord{
		Hash: h[:], Kind: kind, Author: author, KeyID: s.Key.ID, Body: canon, Sig: s.Key.Sign(canon),
		Parties: append([]core.UserID(nil), parties...), Assertions: append([]core.Assertion(nil), a...),
	}
	s.mu.Lock()
	s.records[h] = rec
	s.mu.Unlock()
	return h[:], nil
}

// Draft returns the canonical co-sign draft of body and its passkey challenge.
func (s *ServerSigner) Draft(kind core.RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) ([]byte, [32]byte, error) {
	return DraftRecord(kind, body, nonce, issuedAt)
}

// DraftMember is the member a co-sign draft adds to a record body:
// "cosign": {"nonce": <intent id>, "issued_at": <time>}. It is nested so a
// record's own fields (an Invite's issued_at) never collide with it; a body
// that already has the member cannot be drafted.
const DraftMember = "cosign"

// DraftRecord is the shared co-sign draft construction (P1, §3): the canonical
// record of body (kind and schema_version injected) with the DraftMember, and
// the challenge sigs.Challenge(kind, draft). Stripping the member from the
// draft yields exactly sigs.CanonicalRecord of body.
func DraftRecord(kind core.RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) ([]byte, [32]byte, error) {
	if kind.String() == "" || nonce == uuid.Nil {
		return nil, [32]byte{}, sigs.ErrNoRecord
	}
	base, err := sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, body)
	if err != nil {
		return nil, [32]byte{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(base))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, [32]byte{}, sigs.ErrGrammar
	}
	if _, taken := m[DraftMember]; taken {
		return nil, [32]byte{}, sigs.ErrGrammar
	}
	ts, err := sigs.T(issuedAt).MarshalJSON()
	if err != nil {
		return nil, [32]byte{}, err
	}
	var tsStr string
	if err := json.Unmarshal(ts, &tsStr); err != nil {
		return nil, [32]byte{}, err
	}
	m[DraftMember] = map[string]any{"nonce": nonce.String(), "issued_at": tsStr}
	canon, err := sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, m)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return canon, sigs.Challenge(kind.String(), canon), nil
}

// RecordFromDraft returns the record a co-sign draft is about: the draft with
// its DraftMember removed, canonical (sigs.CanonicalRecord of the original
// body), plus the member's nonce and issue time.
func RecordFromDraft(draft []byte) (canon []byte, nonce uuid.UUID, issuedAt time.Time, err error) {
	kind, sv, err := sigs.RecordFields(draft)
	if err != nil {
		return nil, uuid.Nil, time.Time{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(draft))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, uuid.Nil, time.Time{}, sigs.ErrGrammar
	}
	raw, err := json.Marshal(m[DraftMember])
	if err != nil {
		return nil, uuid.Nil, time.Time{}, err
	}
	var mem struct {
		Nonce    uuid.UUID `json:"nonce"`
		IssuedAt sigs.Time `json:"issued_at"`
	}
	if err := json.Unmarshal(raw, &mem); err != nil || mem.Nonce == uuid.Nil {
		return nil, uuid.Nil, time.Time{}, sigs.ErrGrammar
	}
	delete(m, DraftMember)
	canon, err = sigs.CanonicalRecord(kind, sv, m)
	if err != nil {
		return nil, uuid.Nil, time.Time{}, err
	}
	return canon, mem.Nonce, mem.IssuedAt.Time(), nil
}

// SetPurgeAfter marks a record for purging.
func (s *ServerSigner) SetPurgeAfter(_ context.Context, _ store.Q, hash []byte, at time.Time) error {
	k, ok := key32(hash)
	if !ok {
		return gate.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[k]
	if !ok {
		return gate.ErrNotFound
	}
	r.PurgeAfter = &at
	s.records[k] = r
	return nil
}

// Delete removes a record.
func (s *ServerSigner) Delete(_ context.Context, _ store.Q, hash []byte) error {
	k, ok := key32(hash)
	if !ok {
		return gate.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, k)
	return nil
}

// Record returns a stored record (reference helper).
func (s *ServerSigner) Record(hash []byte) (StoredRecord, bool) {
	k, ok := key32(hash)
	if !ok {
		return StoredRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[k]
	return r, ok
}

// RecordsOf returns every record u is a party to (reference helper, export).
func (s *ServerSigner) RecordsOf(u core.UserID) []StoredRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []StoredRecord
	for _, r := range s.records {
		for _, p := range r.Parties {
			if p == u {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func key32(b []byte) ([32]byte, bool) {
	var k [32]byte
	if len(b) != sha256.Size {
		return k, false
	}
	copy(k[:], b)
	return k, true
}
