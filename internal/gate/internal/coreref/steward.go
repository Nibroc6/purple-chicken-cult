package coreref

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Reference implementations of the steward additions from contract requests
// p0-w11-1 (DisplayNames, RecordExporter), p0-w11-2 (PostAdmin, Enrollments) and
// p0-w02-1 (MentionIndex on MemPosts, RequestStates) and p0-w01-1 (Accounts on
// MemUsers).

// DisplayNameFunc adapts a function to core.DisplayNames.
type DisplayNameFunc func(name string) (string, bool)

var _ core.DisplayNames = DisplayNameFunc(nil)

// CleanDisplayName calls f.
func (f DisplayNameFunc) CleanDisplayName(name string) (string, bool) { return f(name) }

var _ core.RecordExporter = (*ServerSigner)(nil)

// ExportRecords returns records.jsonl (one sigs.Envelope per record user is a
// party to; Report records only to their author) and keys.json for the one
// in-memory server key.
func (s *ServerSigner) ExportRecords(_ context.Context, _ store.Q, user core.UserID) ([]byte, []byte, error) {
	recs := s.RecordsOf(user)
	sort.Slice(recs, func(i, j int) bool { return bytes.Compare(recs[i].Hash, recs[j].Hash) < 0 })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		if r.Kind == core.RecReport && r.Author != user {
			continue
		}
		kind, sv, err := sigs.RecordFields(r.Body)
		if err != nil {
			return nil, nil, err
		}
		env := sigs.Envelope{Kind: kind, SchemaVersion: sv, KeyID: r.KeyID, Body: json.RawMessage(r.Body),
			Sig: base64.RawURLEncoding.EncodeToString(r.Sig)}
		if err := enc.Encode(env); err != nil {
			return nil, nil, err
		}
	}
	kv := gate.KeyView{KeyID: s.Key.ID, Alg: "ed25519", PublicKey: base64.RawURLEncoding.EncodeToString(s.Key.Pub),
		Fingerprint: sigs.Fingerprint(s.Key.Pub), ValidFrom: s.Key.From.UTC()}
	if !s.Key.Until.IsZero() {
		u := s.Key.Until.UTC()
		kv.ValidUntil = &u
	}
	keys, err := json.MarshalIndent(map[string][]gate.KeyView{"server": {kv}}, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), keys, nil
}

// MemPostAdmin tombstones every window post of an author through D.Signer
// (the DB-backed implementation is gate/posts). Like the real one it leaves
// PostIndex.Delete to the caller.
type MemPostAdmin struct {
	D *core.Deps

	mu      sync.Mutex
	deleted map[core.PostID]bool
}

var _ core.PostAdmin = (*MemPostAdmin)(nil)

// DeleteAllBy signs a Tombstone for every live window post of author.
func (p *MemPostAdmin) DeleteAllBy(ctx context.Context, tx store.Q, author core.UserID) ([]core.PostID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.deleted == nil {
		p.deleted = map[core.PostID]bool{}
	}
	var by uuid.UUID
	if p.D.Users != nil {
		if m, err := p.D.Users.Get(ctx, author); err == nil {
			by = m.ExtID
		}
	}
	now := p.D.Clock.Now()
	var out []core.PostID
	for _, id := range p.D.Posts.Recent(author) {
		if p.deleted[id] {
			continue
		}
		if p.D.Signer != nil {
			body := memTombstone{Target: id, By: by, At: sigs.T(now)}
			if _, err := p.D.Signer.Sign(ctx, tx, core.RecTombstone, author, body, []core.UserID{author}); err != nil {
				return nil, err
			}
		}
		p.deleted[id] = true
		out = append(out, id)
	}
	return out, nil
}

// MemEnrollments records cancelled enrollment token hashes.
type MemEnrollments struct {
	mu        sync.Mutex
	Cancelled [][]byte
}

var _ core.Enrollments = (*MemEnrollments)(nil)

// CancelEnrollment records tokenHash as cancelled.
func (e *MemEnrollments) CancelEnrollment(_ context.Context, _ store.Q, tokenHash []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Cancelled = append(e.Cancelled, append([]byte(nil), tokenHash...))
	return nil
}

type memTombstone struct {
	Target uuid.UUID `json:"target"`
	By     uuid.UUID `json:"by"`
	At     sigs.Time `json:"at"`
}

// MemRequests is the reference core.RequestStates (contract request p0-w02-1):
// a map from (v, u) to the state v sees ("outgoing", "incoming" or "").
type MemRequests struct {
	mu sync.RWMutex
	m  map[[2]core.UserID]string
}

var _ core.RequestStates = (*MemRequests)(nil)

// Set records the state v sees towards u ("" clears it).
func (r *MemRequests) Set(v, u core.UserID, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[[2]core.UserID]string{}
	}
	if state == "" {
		delete(r.m, [2]core.UserID{v, u})
		return
	}
	r.m[[2]core.UserID{v, u}] = state
}

// RequestState returns the state v sees towards u.
func (r *MemRequests) RequestState(_ context.Context, v, u core.UserID) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m[[2]core.UserID{v, u}]
}

var _ core.Accounts = (*MemUsers)(nil)

// Account returns id's WebAuthn user handle (sha256("webauthn:"+handle), as
// coretest.World writes it), ext_id and status. forUpdate is a no-op here.
func (u *MemUsers) Account(_ context.Context, _ store.Q, id core.UserID, _ bool) ([]byte, uuid.UUID, uint8, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	m, ok := u.byID[id]
	if !ok {
		return nil, uuid.Nil, 0, gate.ErrNotFound
	}
	wid := sha256.Sum256([]byte("webauthn:" + m.Handle))
	return wid[:], m.ExtID, m.Status, nil
}

// ActivateGenesis turns a pending genesis account active.
func (u *MemUsers) ActivateGenesis(_ context.Context, _ store.Q, id core.UserID, _ int, _ time.Time) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	m, ok := u.byID[id]
	if !ok || !m.Genesis || m.Status != core.StatusPendingEnrollment {
		return gate.ErrNotFound
	}
	m.Status = core.StatusActive
	u.byID[id] = m
	return nil
}
