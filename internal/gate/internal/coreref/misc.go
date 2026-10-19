package coreref

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

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

	mu        sync.Mutex
	Resets    map[core.UserID]time.Time
	Offenses  map[core.UserID]int       // quarantine offenses per inviter (Penalize)
	PausedTil map[core.UserID]time.Time // first offense: invites paused until
}

var _ core.Invites = (*MemInvites)(nil)
var _ core.Probation = (*MemInvites)(nil)
var _ core.InvitePenalties = (*MemInvites)(nil)

// Penalize records one quarantine offense of inviter: the first pauses its
// invites until until, a later one revokes them (Offenses ≥ 2).
func (m *MemInvites) Penalize(_ context.Context, _ store.Q, inviter core.UserID, until time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Offenses == nil {
		m.Offenses, m.PausedTil = map[core.UserID]int{}, map[core.UserID]time.Time{}
	}
	m.Offenses[inviter]++
	if m.Offenses[inviter] == 1 {
		m.PausedTil[inviter] = until
	}
	return m.Offenses[inviter], nil
}

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
