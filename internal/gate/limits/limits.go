// Package limits enforces per-account read budgets and human-pace write limits
// (ARCHITECTURE §17.2(3), §17.4) and the anonymous per-prefix limiter of the
// unauthenticated surface. It implements core.Limiter and apix.Charger
// (ChargeRequest): every request is charged in apix.Route before the handler and
// before any ref is resolved (D19), so a valid ref and garbage cost exactly the
// same, and exhaustion is the one uniform 429 whose retry_after_s comes only from
// the caller's own bucket.
//
// Storage: rate_counters (one row per user, bucket and window; upserted on every
// charge) with an in-memory write-through cache that answers an already
// exhausted bucket without a database round trip. The minimum spacing between
// two writes of one kind and the anonymous counters live in memory only (no IPs
// at rest). limits.purge drops windows older than two days.
//
// v2 (P1-W07): the P1 classes have budgets of their own — media_full (60 a
// day, the full-resolution budget of §17.3), save, media and the anonymous
// dbsc bucket of the device-bound-session endpoints — and every class a route
// declares must have one (apix/pad's TestEveryRouteClassHasABudget). Padded
// routes hold their 429 like every other outcome (apix/pad), so a budget's
// exhaustion is not faster than a charge that passes; charging stays before
// the handler and before any ref is resolved.
//
// Owner: P0-W12 → P1-W07.
package limits

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/httpx"
)

// Read classes (plan §5.1).
const (
	ReadFeedPage  core.ReadClass = "feed_page"
	ReadTreeView  core.ReadClass = "tree_view"
	ReadGraph     core.ReadClass = "graph_read"
	ReadPost      core.ReadClass = "post_read"
	ReadMediaFull core.ReadClass = "media_full" // P1
)

// Write kinds (plan §5.1).
const (
	WritePost      core.WriteKind = "post"
	WriteLike      core.WriteKind = "like"
	WriteFriendReq core.WriteKind = "friend_request"
	WriteReport    core.WriteKind = "report"
	WriteReference core.WriteKind = "reference"
	WriteInvite    core.WriteKind = "invite_handshake"
	WriteSocial    core.WriteKind = "social"
	WriteAccount   core.WriteKind = "account"
	WriteSave      core.WriteKind = "save"  // P1
	WriteMedia     core.WriteKind = "media" // P1
	// WriteUpgrade opens a friend upgrade with an acquaintance: no new account
	// and no new connection, so it spends no code attempt (WriteInvite); its
	// own cap bounds the scanner offers it sends.
	WriteUpgrade core.WriteKind = "friend_upgrade"
)

// Window lengths.
const (
	Hour = time.Hour
	Day  = 24 * time.Hour
	// AnonWindow is the window of the anonymous per-prefix counters.
	AnonWindow = time.Minute
)

// Jobs.
const (
	// JobPurge deletes rate_counters windows older than Keep.
	JobPurge = "limits.purge"
	// PurgeEvery is the limits.purge period.
	PurgeEvery = time.Hour
	// Keep is how long a finished window's row is kept.
	Keep = 2 * Day
)

// UnauthBucket is the anonymous bucket that charges read and write routes
// called without a signed-in session (they answer the uniform not-found).
const UnauthBucket = "unauth"

// Cap is one counted limit: at most Max charges per Window (UTC hour or UTC day).
type Cap struct {
	Window time.Duration
	Max    int
}

// Refill is a build-up limit: a bucket of Cap that gains one charge back every
// Every (so Cap per Cap×Every at a steady pace), instead of a counter that
// resets at a window boundary. Its state is what the person sees on screen.
type Refill struct {
	Cap   int
	Every time.Duration
}

// Policy holds every limit. Names are the class names of plan §5.1; a name
// missing from Reads or Writes falls back to DefaultRead / DefaultWrite.
type Policy struct {
	Reads  map[string]int   // per UTC day
	Writes map[string][]Cap // hourly and/or daily
	// Refills are the build-up write limits; a kind here has no Writes caps.
	Refills map[string]Refill
	// ProbationFriendRequests replaces the daily friend_request cap while the
	// caller is on probation (§8).
	ProbationFriendRequests int
	// Spacing is the minimum time between two writes of one kind (§17.4).
	Spacing time.Duration
	// FreeSteps lists the write kinds whose steps (gate.ClassStep) are not
	// charged at all: their cap counts the actions people start, and the
	// server already bounds the steps of each one (an invite handshake takes
	// one key, a few sound reports, one confirm and one co-signature).
	FreeSteps map[string]bool
	// Anon caps each anonymous bucket per client prefix (/24 or /56) per AnonWindow.
	Anon map[string]int
	// DefaultRead, DefaultWrite and DefaultAnon apply to names outside the maps.
	DefaultRead  int
	DefaultWrite []Cap
	DefaultAnon  int
}

// DefaultPolicy returns the plan's starting points (plan §4.7 limits, §17.4).
func DefaultPolicy() Policy {
	return Policy{
		Reads: map[string]int{
			string(ReadFeedPage): 400, string(ReadTreeView): 400, string(ReadPost): 1500,
			string(ReadGraph): 200, string(ReadMediaFull): 60,
		},
		Writes: map[string][]Cap{
			string(WritePost):      {{Hour, 10}, {Day, 30}},
			string(WriteLike):      {{Hour, 100}},
			string(WriteFriendReq): {{Day, 20}},
			string(WriteReport):    {{Day, 20}},
			string(WriteSocial):    {{Day, 200}},
			string(WriteAccount):   {{Day, 50}},
			string(WriteSave):      {{Day, 200}},
			string(WriteMedia):     {{Day, 50}},
			string(WriteUpgrade):   {{Hour, 10}, {Day, 30}},
		},
		// 20 handshakes, one more every 72 min: a full day's worth at once,
		// shown on the invite page (trial feedback).
		Refills:                 map[string]Refill{string(WriteInvite): {Cap: 20, Every: Day / 20}},
		ProbationFriendRequests: 5,
		Spacing:                 2 * time.Second,
		FreeSteps:               map[string]bool{string(WriteInvite): true},
		// 30/min per prefix for login, enroll and CSP reports. The join flow polls
		// /join/status every 1.5 s while the invitee waits for the SAS, so join
		// gets room for that plus the claim and the two registration calls.
		Anon: map[string]int{
			"login": 30, "enroll": 30, "csp_report": 30, "recovery": 30, "dbsc": 30, "join": 120, UnauthBucket: 60,
		},
		DefaultRead:  200,
		DefaultWrite: []Cap{{Day, 200}},
		DefaultAnon:  30,
	}
}

type counterKey struct {
	user   core.UserID
	bucket string
	window int64 // window start, unix seconds
}

type spacingKey struct {
	user core.UserID
	kind string
}

type anonKey struct {
	prefix netip.Prefix
	bucket string
}

type anonCount struct {
	window int64
	n      int
}

// Service is the limits module.
type Service struct {
	d *core.Deps

	mu      sync.Mutex
	policy  Policy
	counts  map[counterKey]int
	last    map[spacingKey]time.Time
	anon    map[anonKey]anonCount
	charges int
}

var _ core.Limiter = (*Service)(nil)

// New returns the service with DefaultPolicy. Test servers (PCC_ENV=test) run on
// a frozen fake clock, on which every two writes would be 0 s apart, so they
// start without the minimum spacing; limits' own tests set it with SetPolicy.
func New(d *core.Deps) *Service {
	p := DefaultPolicy()
	if d != nil && d.Cfg != nil && d.Cfg.Env == config.EnvTest {
		p.Spacing = 0
	}
	s := &Service{d: d, policy: p}
	s.Reset()
	return s
}

// Init registers limits.purge.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error { return s.Purge(ctx) })
		s.d.Jobs.Every(JobPurge, PurgeEvery)
	}
	return nil
}

// SetPolicy replaces the policy (tests, operators) and forgets the cached counts.
func (s *Service) SetPolicy(p Policy) {
	s.mu.Lock()
	s.policy = p
	s.counts = map[counterKey]int{}
	s.mu.Unlock()
}

// Policy returns the current policy.
func (s *Service) Policy() Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy
}

// Reset forgets every in-memory counter: the cache, the spacing clocks and the
// anonymous buckets (test hooks; contract request p0-w12-2).
func (s *Service) Reset() {
	s.mu.Lock()
	s.counts = map[counterKey]int{}
	s.last = map[spacingKey]time.Time{}
	s.anon = map[anonKey]anonCount{}
	s.charges = 0
	s.mu.Unlock()
}

// ChargeRequest charges one request of class c to the caller (viewer from ctx).
// Read and write classes called without a signed-in session are charged to the
// anonymous UnauthBucket of the client prefix (RealIP from trusted proxies only),
// like the anonymous classes. Exhaustion ⇒ gate.ErrRateLimited.
func (s *Service) ChargeRequest(ctx context.Context, c gate.Class) error {
	switch c.Kind {
	case gate.ClassFree:
		return nil
	case gate.ClassAnon:
		return s.ChargeAnon(clientPrefix(ctx), c.Name)
	case gate.ClassRead, gate.ClassWrite, gate.ClassStep:
		v, ok := core.ViewerFrom(ctx)
		if !ok || v.Kind != core.SessUser || v.ID == 0 {
			return s.ChargeAnon(clientPrefix(ctx), UnauthBucket)
		}
		if c.Kind == gate.ClassStep {
			s.mu.Lock()
			free := s.policy.FreeSteps[c.Name]
			s.mu.Unlock()
			if free {
				return nil
			}
		}
		if c.Kind == gate.ClassRead {
			return s.ChargeRead(ctx, v, core.ReadClass(c.Name))
		}
		return s.chargeWrite(ctx, v, core.WriteKind(c.Name), 1, c.Kind == gate.ClassWrite)
	}
	return nil
}

func clientPrefix(ctx context.Context) netip.Prefix {
	a, ok := httpx.ClientIPFrom(ctx)
	if !ok {
		return netip.Prefix{}
	}
	return httpx.ClientPrefix(a)
}

// ReadBucket is the rate_counters bucket of a read class.
func ReadBucket(c core.ReadClass) string { return "r:" + string(c) }

// WriteBucket is the rate_counters bucket of a write kind for one window length.
func WriteBucket(k core.WriteKind, window time.Duration) string {
	if window == Hour {
		return "w:" + string(k) + ":h"
	}
	return "w:" + string(k)
}

type charge struct {
	bucket string
	start  time.Time
	window time.Duration
	max    int
}

// ChargeRead charges one read of class c (per UTC day).
func (s *Service) ChargeRead(ctx context.Context, v core.Viewer, c core.ReadClass) error {
	s.mu.Lock()
	max, ok := s.policy.Reads[string(c)]
	if !ok {
		max = s.policy.DefaultRead
	}
	s.mu.Unlock()
	now := s.d.Clock.Now()
	return s.charge(ctx, v.ID, 1, now, []charge{{bucket: ReadBucket(c), start: now.Truncate(Day), window: Day, max: max}})
}

// ChargeWrite charges n writes of kind k: the minimum spacing since the
// previous write of k, then every hourly and daily cap of k.
func (s *Service) ChargeWrite(ctx context.Context, v core.Viewer, k core.WriteKind, n int) error {
	return s.chargeWrite(ctx, v, k, n, true)
}

// chargeWrite is ChargeWrite; spaced false skips the minimum spacing and leaves
// its clock alone (gate.ClassStep).
func (s *Service) chargeWrite(ctx context.Context, v core.Viewer, k core.WriteKind, n int, spaced bool) error {
	if n < 1 {
		n = 1
	}
	now := s.d.Clock.Now()
	s.mu.Lock()
	caps, ok := s.policy.Writes[string(k)]
	if !ok {
		caps = s.policy.DefaultWrite
	}
	probation := s.policy.ProbationFriendRequests
	spacing := s.policy.Spacing
	if !spaced {
		spacing = 0
	}
	if spacing > 0 {
		if last, ok := s.last[spacingKey{v.ID, string(k)}]; ok && now.Sub(last) < spacing {
			s.mu.Unlock()
			return gate.RateLimited(last.Add(spacing).Sub(now))
		}
	}
	refill, isRefill := s.policy.Refills[string(k)]
	s.mu.Unlock()
	if isRefill {
		if err := s.take(ctx, v.ID, k, n, refill, now); err != nil {
			return err
		}
		if spacing > 0 {
			s.mu.Lock()
			s.last[spacingKey{v.ID, string(k)}] = now
			s.mu.Unlock()
		}
		return nil
	}
	cs := make([]charge, 0, len(caps))
	for _, c := range caps {
		max := c.Max
		if k == WriteFriendReq && c.Window == Day && probation > 0 && probation < max && s.onProbation(ctx, v.ID, now) {
			max = probation
		}
		cs = append(cs, charge{bucket: WriteBucket(k, c.Window), start: now.Truncate(c.Window), window: c.Window, max: max})
	}
	if err := s.charge(ctx, v.ID, n, now, cs); err != nil {
		return err
	}
	if spacing > 0 {
		s.mu.Lock()
		s.last[spacingKey{v.ID, string(k)}] = now
		s.mu.Unlock()
	}
	return nil
}

// RefillBucket is the rate_buckets bucket of a build-up write kind.
func RefillBucket(k core.WriteKind) string { return "b:" + string(k) }

// take charges n to the build-up bucket of k (GCRA on rate_buckets.tat): the
// charge moves tat on by n×Every from max(tat, now) and is refused, changing
// nothing, if tat would end up more than Cap×Every ahead of now.
func (s *Service) take(ctx context.Context, u core.UserID, k core.WriteKind, n int, r Refill, now time.Time) error {
	step := time.Duration(n) * r.Every
	limit := time.Duration(r.Cap) * r.Every
	tag, err := s.d.DB.Q().Exec(ctx, `INSERT INTO rate_buckets AS b (user_id, bucket, tat) VALUES ($1, $2, $3::timestamptz + $4::interval)
		ON CONFLICT (user_id, bucket) DO UPDATE SET tat = GREATEST(b.tat, $3) + $4::interval
		WHERE GREATEST(b.tat, $3) + $4::interval <= $3::timestamptz + $5::interval`,
		int64(u), RefillBucket(k), now, step, limit)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var tat time.Time
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT tat FROM rate_buckets WHERE user_id = $1 AND bucket = $2",
		int64(u), RefillBucket(k)).Scan(&tat); err != nil {
		return err
	}
	return gate.RateLimited(tat.Add(step).Sub(now.Add(limit)))
}

// WriteAllowance reports what is left of the build-up limit of k for v
// (core.Limiter): ok false when k has none.
func (s *Service) WriteAllowance(ctx context.Context, v core.Viewer, k core.WriteKind) (core.Allowance, bool, error) {
	s.mu.Lock()
	r, ok := s.policy.Refills[string(k)]
	s.mu.Unlock()
	if !ok {
		return core.Allowance{}, false, nil
	}
	now := s.d.Clock.Now()
	var tat time.Time
	err := s.d.DB.Q().QueryRow(ctx, "SELECT tat FROM rate_buckets WHERE user_id = $1 AND bucket = $2",
		int64(v.ID), RefillBucket(k)).Scan(&tat)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return core.Allowance{}, false, err
	}
	return allowance(r, tat, now), true, nil
}

// allowance is the bucket state at now for tat: every whole Every between now
// and tat is a charge still in use.
func allowance(r Refill, tat, now time.Time) core.Allowance {
	a := core.Allowance{Available: r.Cap, Cap: r.Cap}
	ahead := tat.Sub(now)
	if ahead <= 0 {
		return a
	}
	used := int((ahead + r.Every - 1) / r.Every) // whole charges still in use
	a.Available = max(r.Cap-used, 0)
	next := tat.Add(-time.Duration(used-1) * r.Every)
	a.NextAt = &next
	return a
}

func (s *Service) onProbation(ctx context.Context, u core.UserID, now time.Time) bool {
	m, err := s.d.Users.Get(ctx, u)
	return err == nil && m.OnProbation(now)
}

// charge adds n to every bucket of cs (one upsert) unless the cache already
// knows one of them is exhausted. The first exhausted bucket decides the
// retry time; nothing about the request's target takes part.
func (s *Service) charge(ctx context.Context, u core.UserID, n int, now time.Time, cs []charge) error {
	if len(cs) == 0 {
		return nil
	}
	s.mu.Lock()
	for _, c := range cs {
		if s.counts[counterKey{u, c.bucket, c.start.Unix()}] >= c.max {
			s.mu.Unlock()
			return gate.RateLimited(c.start.Add(c.window).Sub(now))
		}
	}
	s.mu.Unlock()
	buckets := make([]string, len(cs))
	starts := make([]time.Time, len(cs))
	for i, c := range cs {
		buckets[i], starts[i] = c.bucket, c.start
	}
	rows, err := s.d.DB.Q().Query(ctx, `INSERT INTO rate_counters (user_id, bucket, window_start, count)
		SELECT $1, t.b, t.w, $4 FROM unnest($2::text[], $3::timestamptz[]) AS t(b, w)
		ON CONFLICT (user_id, bucket, window_start) DO UPDATE SET count = rate_counters.count + EXCLUDED.count
		RETURNING bucket, window_start, count`, int64(u), buckets, starts, n)
	if err != nil {
		return err
	}
	got := map[string]int{}
	for rows.Next() {
		var (
			b     string
			w     time.Time
			count int
		)
		if err := rows.Scan(&b, &w, &count); err != nil {
			rows.Close()
			return err
		}
		got[b] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var limited error
	for _, c := range cs {
		count := got[c.bucket]
		s.counts[counterKey{u, c.bucket, c.start.Unix()}] = count
		if count > c.max && limited == nil {
			limited = gate.RateLimited(c.start.Add(c.window).Sub(now))
		}
	}
	s.charges++
	if s.charges%4096 == 0 {
		s.pruneLocked(now)
	}
	return limited
}

// ChargeAnon charges one request of the anonymous bucket to the client prefix
// ip (memory only, per AnonWindow).
func (s *Service) ChargeAnon(ip netip.Prefix, bucket string) error {
	now := s.d.Clock.Now()
	start := now.Truncate(AnonWindow)
	s.mu.Lock()
	defer s.mu.Unlock()
	max, ok := s.policy.Anon[bucket]
	if !ok {
		max = s.policy.DefaultAnon
	}
	k := anonKey{ip, bucket}
	c := s.anon[k]
	if c.window != start.Unix() {
		c = anonCount{window: start.Unix()}
		if len(s.anon) >= maxAnonKeys {
			s.pruneAnonLocked(start.Unix())
		}
	}
	if c.n >= max {
		s.anon[k] = c
		return gate.RateLimited(start.Add(AnonWindow).Sub(now))
	}
	c.n++
	s.anon[k] = c
	return nil
}

// maxAnonKeys bounds the anonymous map between window turns.
const maxAnonKeys = 1 << 16

func (s *Service) pruneAnonLocked(current int64) {
	for k, c := range s.anon {
		if c.window != current {
			delete(s.anon, k)
		}
	}
}

// pruneLocked drops cached windows that have ended and stale spacing clocks.
func (s *Service) pruneLocked(now time.Time) {
	for k := range s.counts {
		w := Day
		if len(k.bucket) > 2 && k.bucket[len(k.bucket)-2:] == ":h" {
			w = Hour
		}
		if time.Unix(k.window, 0).Add(w).Before(now) {
			delete(s.counts, k)
		}
	}
	for k, t := range s.last {
		if now.Sub(t) > time.Minute {
			delete(s.last, k)
		}
	}
	s.pruneAnonLocked(now.Truncate(AnonWindow).Unix())
}

// Purge deletes rate_counters windows that ended more than Keep ago and prunes
// the memory (limits.purge).
func (s *Service) Purge(ctx context.Context) error {
	now := s.d.Clock.Now()
	if _, err := s.d.DB.Q().Exec(ctx, "DELETE FROM rate_counters WHERE window_start < $1", now.Add(-Keep)); err != nil {
		return err
	}
	// A build-up bucket whose tat passed is full: the row says nothing more.
	if _, err := s.d.DB.Q().Exec(ctx, "DELETE FROM rate_buckets WHERE tat < $1", now); err != nil {
		return err
	}
	s.mu.Lock()
	s.pruneLocked(now)
	s.mu.Unlock()
	return nil
}
