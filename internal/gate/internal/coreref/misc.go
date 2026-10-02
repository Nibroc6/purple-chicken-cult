package coreref

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// ---- notifications ----

// LiveEvent is an ephemeral event recorded by RecordingNotifier.
type LiveEvent struct {
	To    core.UserID
	Topic string
	Data  any
}

// RecordingNotifier keeps notifications in memory. It applies the batching rule
// (same unread GroupKey ⇒ count+1) and the D7 drop rule (recipient blocked the
// subject user) using D.Graph when set.
type RecordingNotifier struct {
	D *core.Deps

	mu     sync.Mutex
	Events []core.Event
	Counts map[string]int // "<to>|<group>" → count
	LiveEv []LiveEvent
}

var _ core.Notifier = (*RecordingNotifier)(nil)

// Notify records e unless the recipient blocked the subject user.
func (n *RecordingNotifier) Notify(_ context.Context, _ store.Q, e core.Event) error {
	if n.D != nil && n.D.Graph != nil && e.SubjectUser != 0 && n.D.Graph.Blocks(e.To, e.SubjectUser) {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Counts == nil {
		n.Counts = map[string]int{}
	}
	if e.GroupKey != "" {
		k := uidKey(e.To) + "|" + e.GroupKey
		if n.Counts[k] > 0 {
			n.Counts[k]++
			return nil
		}
		n.Counts[k] = 1
	}
	n.Events = append(n.Events, e)
	return nil
}

// Live records an ephemeral event.
func (n *RecordingNotifier) Live(to core.UserID, topic string, data any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.LiveEv = append(n.LiveEv, LiveEvent{To: to, Topic: topic, Data: data})
}

// For returns the recorded notifications of to.
func (n *RecordingNotifier) For(to core.UserID) []core.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []core.Event
	for _, e := range n.Events {
		if e.To == to {
			out = append(out, e)
		}
	}
	return out
}

func uidKey(u core.UserID) string {
	const digits = "0123456789"
	if u == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for x := uint32(u); x > 0; x /= 10 {
		i--
		b[i] = digits[x%10]
	}
	return string(b[i:])
}

// ---- limits ----

// PassLimiter never limits anything (reference: unlimited budgets).
type PassLimiter struct{}

var _ core.Limiter = PassLimiter{}

// ChargeRead always succeeds.
func (PassLimiter) ChargeRead(context.Context, core.Viewer, core.ReadClass) error { return nil }

// ChargeWrite always succeeds.
func (PassLimiter) ChargeWrite(context.Context, core.Viewer, core.WriteKind, int) error { return nil }

// ChargeAnon always succeeds.
func (PassLimiter) ChargeAnon(netip.Prefix, string) error { return nil }

// ---- metrics ----

// MemMetrics counts metrics in memory (no IDs can be recorded: names, buckets, numbers).
type MemMetrics struct {
	mu     sync.Mutex
	Counts map[string]int64 // "<name>|<bucket>"
	Obs    map[string][]int64
}

var _ core.Metrics = (*MemMetrics)(nil)

// Inc adds n to (name, bucket).
func (m *MemMetrics) Inc(name, bucket string, n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Counts == nil {
		m.Counts = map[string]int64{}
	}
	m.Counts[name+"|"+bucket] += n
}

// ObserveMs records a latency observation.
func (m *MemMetrics) ObserveMs(name string, ms int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Obs == nil {
		m.Obs = map[string][]int64{}
	}
	m.Obs[name] = append(m.Obs[name], ms)
}

// Get returns the count of (name, bucket).
func (m *MemMetrics) Get(name, bucket string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Counts[name+"|"+bucket]
}

// ---- edges & mutes ----

type pair struct{ a, b core.UserID }

func orderedPair(a, b core.UserID) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a, b}
}

// MemEdges is the reference edge writer. It records edges (and their record
// hashes) in memory; the caller mutates the Graph after commit (plan §3.4).
// Pending outgoing requests are tracked with SetPending for cap accounting.
type MemEdges struct {
	mu      sync.Mutex
	edges   map[pair]core.EdgeLevel
	hashes  map[pair][]byte
	pending map[core.UserID]int
	Revoked []pair
}

var _ core.Edges = (*MemEdges)(nil)

// NewMemEdges returns an empty edge store.
func NewMemEdges() *MemEdges {
	return &MemEdges{edges: map[pair]core.EdgeLevel{}, hashes: map[pair][]byte{}, pending: map[core.UserID]int{}}
}

// Create records a new edge; a duplicate is Problem already_friends.
func (e *MemEdges) Create(_ context.Context, _ store.Q, a, b core.UserID, lvl core.EdgeLevel, recordHash []byte) error {
	if a == b || a == 0 || b == 0 {
		return gate.ErrNotFound
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := orderedPair(a, b)
	if _, ok := e.edges[p]; ok {
		return gate.NewProblem(gate.StatusConflict, "already_friends")
	}
	e.edges[p] = lvl
	e.hashes[p] = recordHash
	return nil
}

// Revoke removes the edge a–b.
func (e *MemEdges) Revoke(_ context.Context, _ store.Q, a, b, _ core.UserID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := orderedPair(a, b)
	if _, ok := e.edges[p]; !ok {
		return gate.ErrNotFound
	}
	delete(e.edges, p)
	delete(e.hashes, p)
	e.Revoked = append(e.Revoked, p)
	return nil
}

// Upgrade makes a–b friend+ (P1).
func (e *MemEdges) Upgrade(_ context.Context, _ store.Q, a, b core.UserID, recordHash []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := orderedPair(a, b)
	if _, ok := e.edges[p]; !ok {
		return gate.ErrNotFound
	}
	e.edges[p] = core.LevelFriendPlus
	e.hashes[p] = recordHash
	return nil
}

// Count returns u's edges and outgoing pending requests (D23).
func (e *MemEdges) Count(_ context.Context, u core.UserID) (int, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for p := range e.edges {
		if p.a == u || p.b == u {
			n++
		}
	}
	return n, e.pending[u], nil
}

// SetPending sets u's outgoing pending request count (reference helper).
func (e *MemEdges) SetPending(u core.UserID, n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pending[u] = n
}

// MemMutes is the reference mute list.
type MemMutes struct {
	mu sync.RWMutex
	m  map[core.UserID]map[core.UserID]struct{}
}

var _ core.Mutes = (*MemMutes)(nil)

// SetMute mutes or unmutes author for v.
func (m *MemMutes) SetMute(v, author core.UserID, on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[core.UserID]map[core.UserID]struct{}{}
	}
	setRel(m.m, v, author, on)
}

// MutedAuthors returns v's muted authors.
func (m *MemMutes) MutedAuthors(_ context.Context, v core.UserID) map[core.UserID]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[core.UserID]struct{}, len(m.m[v]))
	for a := range m.m[v] {
		out[a] = struct{}{}
	}
	return out
}

// ---- invites ----

// MemInvites derives the invite tree from D.Users (InvitedBy).
type MemInvites struct {
	D     *core.Deps
	Users *MemUsers // the directory to scan; D.Users is used for single lookups

	mu     sync.Mutex
	Resets map[core.UserID]time.Time
}

var _ core.Invites = (*MemInvites)(nil)
var _ core.Probation = (*MemInvites)(nil)

// InviteesOf returns the direct invitees of inviter.
func (m *MemInvites) InviteesOf(_ context.Context, inviter core.UserID) ([]core.UserID, error) {
	if m.Users == nil {
		return nil, nil
	}
	m.Users.mu.RLock()
	defer m.Users.mu.RUnlock()
	var out []core.UserID
	for id, u := range m.Users.byID {
		if u.InvitedBy == inviter {
			out = append(out, id)
		}
	}
	sortIDs(out)
	return out, nil
}

// Subtree returns root and every transitive invitee.
func (m *MemInvites) Subtree(ctx context.Context, root core.UserID) (*roaring.Bitmap, error) {
	out := roaring.New()
	queue := []core.UserID{root}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if out.Contains(uint32(u)) {
			continue
		}
		out.Add(uint32(u))
		kids, err := m.InviteesOf(ctx, u)
		if err != nil {
			return nil, err
		}
		queue = append(queue, kids...)
	}
	return out, nil
}

// Reset puts users back on probation until until (P1 quarantine).
func (m *MemInvites) Reset(_ context.Context, _ store.Q, users []core.UserID, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Resets == nil {
		m.Resets = map[core.UserID]time.Time{}
	}
	for _, u := range users {
		m.Resets[u] = until
		if m.Users != nil {
			m.Users.mu.Lock()
			if meta, ok := m.Users.byID[u]; ok {
				t := until
				meta.ProbationUntil, meta.ProbationCleared = &t, false
				m.Users.byID[u] = meta
			}
			m.Users.mu.Unlock()
		}
	}
	return nil
}

func sortIDs(ids []core.UserID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// ---- Phase 1 references ----

// MemModQueue records moderation queue items.
type MemModQueue struct {
	mu    sync.Mutex
	Items []core.ModQueueItem
}

var _ core.ModQueue = (*MemModQueue)(nil)

// Enqueue records item.
func (q *MemModQueue) Enqueue(_ context.Context, _ store.Q, item core.ModQueueItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Items = append(q.Items, item)
	return nil
}

// MemStepUp records step-up requirements per session.
type MemStepUp struct {
	D *core.Deps

	mu       sync.Mutex
	Required map[[32]byte]string
}

var _ core.StepUp = (*MemStepUp)(nil)

// Require marks session as needing a step-up and publishes SessionStepUp.
func (s *MemStepUp) Require(_ context.Context, user core.UserID, session [32]byte, reason string) error {
	s.mu.Lock()
	if s.Required == nil {
		s.Required = map[[32]byte]string{}
	}
	s.Required[session] = reason
	s.mu.Unlock()
	if s.D != nil && s.D.Bus != nil {
		s.D.Bus.Publish(core.SessionStepUp{User: user, Session: session})
	}
	return nil
}

// MemMedia is the fail-closed media reference: without a configured pipeline,
// attaching or ingesting media is gate.Disabled("media").
type MemMedia struct{}

var _ core.Media = MemMedia{}

// Attach fails closed.
func (MemMedia) Attach(context.Context, store.Q, core.UserID, []gate.Ref) ([]uuid.UUID, error) {
	return nil, gate.Disabled("media")
}

// Views returns no media.
func (MemMedia) Views(context.Context, core.Search, []uuid.UUID) ([]gate.MediaView, error) {
	return nil, nil
}

// Ingest fails closed.
func (MemMedia) Ingest(context.Context, []byte, core.PostID, string) (uuid.UUID, error) {
	return uuid.Nil, gate.Disabled("media")
}

// MemHandshakes has no P1 handshake protocol: every call is gate.ErrNotImplemented
// until P1-W02 lands.
type MemHandshakes struct{}

var _ core.Handshakes = MemHandshakes{}

// OpenUpgrade is not implemented in the reference.
func (MemHandshakes) OpenUpgrade(context.Context, core.Viewer, core.UserID) (gate.HandshakeView, error) {
	return gate.HandshakeView{}, gate.ErrNotImplemented
}

// OpenRecovery is not implemented in the reference.
func (MemHandshakes) OpenRecovery(context.Context, core.UserID, core.UserID) (gate.HandshakeView, error) {
	return gate.HandshakeView{}, gate.ErrNotImplemented
}

// ClaimRecovery always fails with the uniform not-found.
func (MemHandshakes) ClaimRecovery(context.Context, [32]byte, string) (core.UserID, error) {
	return 0, gate.ErrNotFound
}

// Result returns OutcomeNone.
func (MemHandshakes) Result(context.Context, uuid.UUID) (core.Outcome, error) {
	return core.OutcomeNone, nil
}

// MemCosign is the reference co-sign gate: it builds drafts with DraftRecord and
// verifies assertions through D.Passkeys.VerifyAssertion.
type MemCosign struct {
	D *core.Deps

	mu      sync.Mutex
	intents map[uuid.UUID]memIntent
}

type memIntent struct {
	user    core.UserID
	purpose string
	used    bool
	exp     time.Time
}

var _ core.Cosign = (*MemCosign)(nil)

// BeginIntent creates an intent for purpose (5 minutes).
func (c *MemCosign) BeginIntent(ctx context.Context, v core.Viewer, purpose string, _ gate.Ref) (core.Intent, error) {
	if v.ID == 0 {
		return core.Intent{}, gate.ErrNotFound
	}
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	if c.D != nil && c.D.Clock != nil {
		now = c.D.Clock.Now()
	}
	c.mu.Lock()
	if c.intents == nil {
		c.intents = map[uuid.UUID]memIntent{}
	}
	c.intents[id] = memIntent{user: v.ID, purpose: purpose, exp: now.Add(5 * time.Minute)}
	c.mu.Unlock()
	var opts []byte
	if c.D != nil && c.D.Passkeys != nil {
		raw, err := c.D.Passkeys.BeginAssertion(ctx, v, id[:], purpose)
		if err != nil {
			return core.Intent{}, err
		}
		opts = raw
	}
	return core.Intent{ID: c.D.Refs.Handshake(v.ID, id), Options: opts}, nil
}

// Require verifies the assertion and consumes the intent.
func (c *MemCosign) Require(ctx context.Context, _ store.Q, v core.Viewer, purpose string, kind core.RecordKind, body any, intent gate.Ref, assertion []byte) (core.Assertion, []byte, error) {
	id, ok := c.D.Refs.OpenHandshake(v.ID, intent)
	if !ok {
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	c.mu.Lock()
	in, ok := c.intents[id]
	if !ok || in.used || in.user != v.ID || in.purpose != purpose {
		c.mu.Unlock()
		return core.Assertion{}, nil, gate.ErrNotFound
	}
	in.used = true
	c.intents[id] = in
	c.mu.Unlock()
	canon, _, err := DraftRecord(kind, body, id, time.Now().UTC())
	if err != nil {
		return core.Assertion{}, nil, err
	}
	a, err := c.D.Passkeys.VerifyAssertion(ctx, v, purpose, assertion)
	if err != nil {
		return core.Assertion{}, nil, err
	}
	return a, canon, nil
}
