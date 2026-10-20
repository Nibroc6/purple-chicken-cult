package coreref

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/gif"  // DecodeConfig for preview images
	_ "image/jpeg" // DecodeConfig for preview images
	_ "image/png"  // DecodeConfig for preview images
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// ---- Phase 1 references (SCAFFOLD-1): Media, Handshakes, Cosign ----
//
// MemModQueue and MemStepUp live in misc.go. All of these keep state in memory
// and ignore the caller's transaction, so a rolled-back write is not undone here.

func nowOf(d *core.Deps) time.Time {
	if d != nil && d.Clock != nil {
		return d.Clock.Now()
	}
	return time.Now().UTC()
}

// MaxPostMedia is the per-post attachment cap (plan P1-W12).
const MaxPostMedia = 4

// MemMediaItem is one in-memory media item.
type MemMediaItem struct {
	Owner      core.UserID // 0 = system ingest (preview image)
	W, H       int
	Alt        string
	Ready      bool
	SourcePost core.PostID
	SourceURL  string
}

// MemMedia is the media reference. Without D, or when D.Cfg.MediaEnabled() is
// false, it fails closed: attaching or ingesting anything is
// gate.Disabled("media") (attaching nothing is always fine) and Views renders
// nothing. That is what the running app uses until P1-W04 lands, so no media id
// ever reaches a database row. With D and media enabled (coretest.Mem), it keeps
// items in memory; Put stands in for an upload.
type MemMedia struct {
	D *core.Deps

	mu       sync.Mutex
	items    map[uuid.UUID]*MemMediaItem
	attached map[uuid.UUID]bool
}

var _ core.Media = (*MemMedia)(nil)

func (m *MemMedia) enabled() bool { return m.D != nil && m.D.Cfg != nil && m.D.Cfg.MediaEnabled() }

func (m *MemMedia) put(it MemMediaItem) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items, m.attached = map[uuid.UUID]*MemMediaItem{}, map[uuid.UUID]bool{}
	}
	m.items[id] = &it
	return id
}

// Put stores a ready image of owner (the stand-in for an upload) and returns its
// id and the owner's ref to it. D must be set.
func (m *MemMedia) Put(owner core.UserID, w, h int, alt string) (uuid.UUID, gate.Ref) {
	id := m.put(MemMediaItem{Owner: owner, W: w, H: h, Alt: alt, Ready: true})
	return id, m.D.Refs.Media(owner, id)
}

// Item returns a copy of item id.
func (m *MemMedia) Item(id uuid.UUID) (MemMediaItem, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[id]; ok {
		return *it, true
	}
	return MemMediaItem{}, false
}

// Attach resolves owner's refs to owned, ready, not yet attached media (at most
// MaxPostMedia, no duplicates) and marks them attached. Any unresolvable ref is
// the uniform gate.ErrNotFound and attaches nothing.
func (m *MemMedia) Attach(_ context.Context, _ store.Q, owner core.UserID, refs []gate.Ref) ([]uuid.UUID, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if !m.enabled() {
		return nil, gate.Disabled("media")
	}
	if len(refs) > MaxPostMedia {
		return nil, gate.NewProblem(gate.StatusBadRequest, "bad_request", "media")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uuid.UUID, 0, len(refs))
	seen := map[uuid.UUID]bool{}
	for _, r := range refs {
		id, ok := m.D.Refs.OpenMedia(owner, r)
		if !ok || seen[id] {
			return nil, gate.ErrNotFound
		}
		it, ok := m.items[id]
		if !ok || it.Owner != owner || owner == 0 || !it.Ready || m.attached[id] {
			return nil, gate.ErrNotFound
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range out {
		m.attached[id] = true
	}
	return out, nil
}

// Views renders the ready items among ids, in order, with refs for the searching
// viewer. The caller has already decided the post is visible.
func (m *MemMedia) Views(_ context.Context, s core.Search, ids []uuid.UUID) ([]gate.MediaView, error) {
	if !m.enabled() || len(ids) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]gate.MediaView, 0, len(ids))
	for _, id := range ids {
		if it, ok := m.items[id]; ok && it.Ready {
			out = append(out, gate.MediaView{Ref: m.D.Refs.Media(s.Viewer(), id), W: it.W, H: it.H, Alt: it.Alt})
		}
	}
	return out, nil
}

// Ingest stores a preview image (no scanner in the reference: a real scan is
// P1-W04's). Bytes that are not a PNG, JPEG or GIF are Problem media_type.
func (m *MemMedia) Ingest(_ context.Context, img []byte, sourcePost core.PostID, sourceURL string) (uuid.UUID, error) {
	if !m.enabled() {
		return uuid.Nil, gate.Disabled("media")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		return uuid.Nil, gate.NewProblem(gate.StatusBadRequest, "media_type")
	}
	return m.put(MemMediaItem{W: cfg.Width, H: cfg.Height, Ready: true, SourcePost: sourcePost, SourceURL: sourceURL}), nil
}

// Handshake kinds of the reference (handshake_sessions kinds 1 and 2).
const (
	HSUpgrade  uint8 = 1
	HSRecovery uint8 = 2
)

// HandshakeTTL is how long an unclaimed reference handshake stays open.
const HandshakeTTL = 10 * time.Minute

// MemHandshake is one in-memory non-invite handshake.
type MemHandshake struct {
	Kind    uint8
	A, B    core.UserID // upgrade: opener, peer; recovery: guardian, account
	Payload string      // what the other device scans (the reference has one static payload)
	Expires time.Time
	Claimed bool
	Outcome core.Outcome
}

// MemHandshakes is the handshake reference: friend+ upgrade and recovery
// handshakes kept in memory, without the audio protocol. An upgrade needs a
// plain friend edge; a recovery needs the guardian to be the account's friend+.
// ClaimRecovery with the payload of an open recovery handshake is Verified
// (stands in for a passed SAS + audio check); Complete sets an outcome directly
// (tests). Everything that does not resolve is the uniform gate.ErrNotFound.
type MemHandshakes struct {
	D *core.Deps

	mu sync.Mutex
	hs map[uuid.UUID]*MemHandshake
}

var _ core.Handshakes = (*MemHandshakes)(nil)

func (h *MemHandshakes) open(kind uint8, a, b core.UserID) (gate.HandshakeView, error) {
	var p [16]byte
	if _, err := rand.Read(p[:]); err != nil {
		return gate.HandshakeView{}, err
	}
	id := uuid.Must(uuid.NewV7())
	h.mu.Lock()
	if h.hs == nil {
		h.hs = map[uuid.UUID]*MemHandshake{}
	}
	h.hs[id] = &MemHandshake{Kind: kind, A: a, B: b, Payload: base64.RawURLEncoding.EncodeToString(p[:]),
		Expires: nowOf(h.D).Add(HandshakeTTL)}
	h.mu.Unlock()
	return gate.HandshakeView{HS: h.D.Refs.Handshake(a, id), Mode: "qr", FrameTTLms: 10_000}, nil
}

// OpenUpgrade opens a friend → friend+ handshake between v and a plain friend.
func (h *MemHandshakes) OpenUpgrade(_ context.Context, v core.Viewer, peer core.UserID) (gate.HandshakeView, error) {
	if h.D == nil || v.ID == 0 || peer == 0 || peer == v.ID {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if lvl, ok := h.D.Graph.Level(v.ID, peer); !ok || lvl != core.LevelFriend {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	return h.open(HSUpgrade, v.ID, peer)
}

// OpenRecovery opens a recovery handshake by guardian for account.
func (h *MemHandshakes) OpenRecovery(_ context.Context, guardian, account core.UserID) (gate.HandshakeView, error) {
	if h.D == nil || guardian == 0 || account == 0 || guardian == account {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if lvl, ok := h.D.Graph.Level(guardian, account); !ok || lvl != core.LevelFriendPlus {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	return h.open(HSRecovery, guardian, account)
}

// ClaimRecovery claims the open recovery handshake whose payload matches (once)
// and returns the account being recovered.
func (h *MemHandshakes) ClaimRecovery(_ context.Context, _ [32]byte, payload string) (core.UserID, error) {
	t := nowOf(h.D)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.hs {
		if s.Kind == HSRecovery && !s.Claimed && t.Before(s.Expires) &&
			subtle.ConstantTimeCompare([]byte(s.Payload), []byte(payload)) == 1 {
			s.Claimed, s.Outcome = true, core.OutcomeVerified
			return s.B, nil
		}
	}
	return 0, gate.ErrNotFound
}

// Result returns the outcome of handshake hs.
func (h *MemHandshakes) Result(_ context.Context, hs uuid.UUID) (core.Outcome, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.hs[hs]
	if !ok {
		return core.OutcomeNone, gate.ErrNotFound
	}
	return s.Outcome, nil
}

// Get returns a copy of handshake hs (tests read its Payload).
func (h *MemHandshakes) Get(hs uuid.UUID) (MemHandshake, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.hs[hs]; ok {
		return *s, true
	}
	return MemHandshake{}, false
}

// Complete records outcome o for hs (tests: a finished upgrade handshake).
func (h *MemHandshakes) Complete(hs uuid.UUID, o core.Outcome) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.hs[hs]
	if !ok {
		return gate.ErrNotFound
	}
	s.Claimed, s.Outcome = true, o
	return nil
}

// CosignTTL is the lifetime of a co-sign intent.
const CosignTTL = 5 * time.Minute

// MemCosign is the reference co-sign gate. An intent belongs to one user and one
// purpose, lives CosignTTL and is single use; its id is the nonce of the draft
// (issued_at = the intent's creation time). When the purpose has a registered
// drafter the draft is built at Begin and the WebAuthn challenge is
// sigs.Challenge over it; otherwise the draft is built from the body at Require
// and the challenge is the id. Require consumes the intent before it
// verifies the assertion through D.Passkeys.VerifyAssertion, so a failed attempt
// cannot be retried with the same intent.
type MemCosign struct {
	D *core.Deps

	mu       sync.Mutex
	intents  map[uuid.UUID]*memIntent
	drafters map[string]core.Drafter
}

type memIntent struct {
	user    core.UserID
	purpose string
	used    bool
	issued  time.Time
	kind    core.RecordKind
	draft   []byte // set when the purpose has a drafter
}

var (
	_ core.Cosign         = (*MemCosign)(nil)
	_ core.CosignDrafters = (*MemCosign)(nil)
)

// RegisterDrafter registers the drafter of purpose.
func (c *MemCosign) RegisterDrafter(purpose string, f core.Drafter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.drafters == nil {
		c.drafters = map[string]core.Drafter{}
	}
	c.drafters[purpose] = f
}

func (c *MemCosign) drafter(purpose string) core.Drafter {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drafters[purpose]
}

// draftFields may appear in a draft without being in the caller's body.
var draftFields = map[string]bool{"nonce": true, "issued_at": true, "created_at": true, "at": true}

// DraftMatches checks the body a caller passes to Cosign.Require against the
// stored draft (core.CosignDrafters): same kind, every field of body equal to
// the draft's, and nothing else in the draft but nonce, issued_at, created_at
// and at. A mismatch is gate.ErrNotFound.
func DraftMatches(draft []byte, kind core.RecordKind, body any) error {
	want, err := sigs.CanonicalRecord(kind.String(), core.RecordSchemaVersion, body)
	if err != nil {
		return gate.ErrNotFound
	}
	var got, exp map[string]json.RawMessage
	if json.Unmarshal(draft, &got) != nil || json.Unmarshal(want, &exp) != nil {
		return gate.ErrNotFound
	}
	for k, v := range exp {
		if !bytes.Equal(got[k], v) {
			return gate.ErrNotFound
		}
	}
	for k := range got {
		if _, ok := exp[k]; !ok && !draftFields[k] {
			return gate.ErrNotFound
		}
	}
	return nil
}

// BeginIntent creates an intent of v for purpose.
func (c *MemCosign) BeginIntent(ctx context.Context, v core.Viewer, purpose string, target gate.Ref) (core.Intent, error) {
	if v.ID == 0 || purpose == "" {
		return core.Intent{}, gate.ErrNotFound
	}
	id := uuid.Must(uuid.NewV7())
	issued := nowOf(c.D)
	in := &memIntent{user: v.ID, purpose: purpose, issued: issued}
	challenge := id[:]
	var rec []byte
	if f := c.drafter(purpose); f != nil {
		kind, body, err := f(ctx, v, target, issued)
		if err != nil {
			return core.Intent{}, err
		}
		canon, ch, err := DraftRecord(kind, body, id, issued)
		if err != nil {
			return core.Intent{}, err
		}
		in.kind, in.draft, rec, challenge = kind, canon, canon, ch[:]
	}
	var opts []byte
	if c.D.Passkeys != nil {
		raw, err := c.D.Passkeys.BeginAssertion(ctx, v, challenge, purpose)
		if err != nil {
			return core.Intent{}, err
		}
		opts = raw
	}
	if rec == nil {
		var err error
		if rec, err = json.Marshal(map[string]string{"purpose": purpose, "nonce": id.String()}); err != nil {
			return core.Intent{}, err
		}
	}
	c.mu.Lock()
	if c.intents == nil {
		c.intents = map[uuid.UUID]*memIntent{}
	}
	c.intents[id] = in
	c.mu.Unlock()
	return core.Intent{ID: c.D.Refs.Handshake(v.ID, id), Record: rec, Options: opts}, nil
}

// Require consumes v's intent for purpose, verifies the assertion and returns it
// with the exact draft bytes: the stored draft when the purpose has a drafter
// (body must match it, DraftMatches), else the draft of body.
func (c *MemCosign) Require(ctx context.Context, _ store.Q, v core.Viewer, purpose string, kind core.RecordKind, body any, intent gate.Ref, assertion []byte) (core.Assertion, []byte, error) {
	id, ok := c.D.Refs.OpenHandshake(v.ID, intent)
	if !ok {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	t := nowOf(c.D)
	c.mu.Lock()
	in, ok := c.intents[id]
	if !ok || in.used || in.user != v.ID || in.purpose != purpose || !t.Before(in.issued.Add(CosignTTL)) {
		c.mu.Unlock()
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	in.used = true
	issued, stored, storedKind := in.issued, in.draft, in.kind
	c.mu.Unlock()
	var canon []byte
	if stored != nil {
		if storedKind != kind || DraftMatches(stored, kind, body) != nil {
			return core.Assertion{}, nil, gate.ErrNotFound
		}
		canon = stored
	} else {
		var err error
		if canon, _, err = DraftRecord(kind, body, id, issued); err != nil {
			return core.Assertion{}, nil, err
		}
	}
	if c.D.Passkeys == nil {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	a, err := c.D.Passkeys.VerifyAssertion(ctx, v, purpose, assertion)
	if err != nil {
		return core.Assertion{}, nil, err
	}
	return a, canon, nil
}
