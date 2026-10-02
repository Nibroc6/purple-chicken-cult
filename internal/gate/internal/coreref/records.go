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
func (s *ServerSigner) Sign(_ context.Context, _ store.Q, kind core.RecordKind, author core.UserID, body any, parties []core.UserID, a ...core.Assertion) ([]byte, error) {
	canon, err := sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, body)
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

// Draft returns the canonical bytes of body with "nonce" and "issued_at"
// injected, and the passkey challenge over them.
func (s *ServerSigner) Draft(kind core.RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) ([]byte, [32]byte, error) {
	return DraftRecord(kind, body, nonce, issuedAt)
}

// DraftRecord is the shared co-sign draft construction (P1, §3).
func DraftRecord(kind core.RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) ([]byte, [32]byte, error) {
	base, err := sigs.Canonical(body)
	if err != nil {
		return nil, [32]byte{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(base))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, [32]byte{}, sigs.ErrGrammar
	}
	m["nonce"] = nonce.String()
	ts, err := sigs.T(issuedAt).MarshalJSON()
	if err != nil {
		return nil, [32]byte{}, err
	}
	var tsStr string
	if err := json.Unmarshal(ts, &tsStr); err != nil {
		return nil, [32]byte{}, err
	}
	m["issued_at"] = tsStr
	canon, err := sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, m)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return canon, sigs.Challenge(kind.String(), canon), nil
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
