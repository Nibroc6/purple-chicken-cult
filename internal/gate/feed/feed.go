// Package feed builds the tiered feed (ARCHITECTURE §4.2, §4.3, §4.4, §5.6,
// §10.1.2, §10.1.7): per-tier candidate planning (author scan vs time scan),
// whole-tier ranking with rank.RankTier, viewer-keyed snapshots with HMAC'd
// cursors re-checked on every page, tier markers and "caught up".
//
// Owner: P0-W07 → P1-W08.
//
// A feed session starts with an empty cursor. Each tier k is planned, filtered,
// grouped (a continuation thread is one candidate: its newest unseen visible
// part), ranked as a whole (never a prefix, §5.6) and snapshotted the first
// time the session reaches it. Snapshots live in memory only, keyed by
// (viewer, 128-bit random session id), 30 minutes after their last use, at most
// five per viewer (D5). Every page re-runs the viewer's search and re-checks
// each post it is about to serve, so an unfriend, block, deletion or audience
// change takes effect on the next request (§4.6); ordering is never recomputed,
// items only disappear.
//
// Uniform failure (plan §0.8): a session, cursor or collapse token that does not
// verify for the viewer (forged, garbage, another viewer's) is gate.ErrNotFound;
// the viewer's own session that expired is Problem feed_session_expired (409),
// and the client starts over.
package feed

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/rank"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
)

// Page and snapshot limits (§4.3, D5).
const (
	// PageSize is the number of feed items (posts, threads, collapsed groups) per page.
	PageSize = 20
	// SessionTTL is how long an unused session (and its snapshots) is kept.
	SessionTTL = 30 * time.Minute
	// SessionsPerUser bounds the live sessions of one viewer; the oldest goes first.
	SessionsPerUser = 5
	// MaxSessions bounds the sessions kept in memory overall (least recently used goes first).
	MaxSessions = 50_000
	// SeenWindow is the seen state that removes candidates (§4.3: 14 days).
	SeenWindow = 14 * 24 * time.Hour
	// ExpandMax bounds the items one collapsed group expands to.
	ExpandMax = 100
	// threadWalk bounds a walk along a continuation thread (a split has ≤ 25 parts).
	threadWalk = 32
	// kidsGens is the depth of the upward credit (§5.1): children and, P1,
	// their own children at weight β.
	kidsGens = 2
)

// Bot isolation (§4.4): from tier CollapseFrom on, posts of accounts younger
// than LowTrustAge fold into one "collapsed" item per tier.
const (
	CollapseFrom   = 3
	LowTrustAge    = 60 * 24 * time.Hour
	ReasonLowTrust = "low_trust_distant"
)

// Feed item types (gate.FeedItem.Type).
const (
	ItemTierStart = "tier_start"
	ItemTierEnd   = "tier_end"
	ItemCaughtUp  = "caught_up"
	ItemPost      = "post"
	ItemThread    = "thread"
	ItemCollapsed = "collapsed"
)

// Metrics (aggregates only; gate/metrics closed list).
const (
	MetricTierReached  = "feed.tier_reached"
	MetricLatency      = "feed.latency_ms"
	MetricPresetSwitch = "feed.preset_switch"
	MetricPlanner      = "feed.planner"
)

// Problem codes.
const (
	CodeSessionExpired    = "feed_session_expired"
	CodePresetUnavailable = "preset_unavailable"
)

// Service is the feed module.
type Service struct {
	d     *core.Deps
	store *sessionStore

	// onPlan, when set (tests), observes every planner choice.
	onPlan func(tier int, scan Scan)
}

// New returns the service.
func New(d *core.Deps) *Service {
	s := &Service{d: d}
	s.store = newSessionStore(MaxSessions, SessionsPerUser, SessionTTL, s.ended)
	return s
}

// Init drops every snapshot when an account is deleted (its sessions are gone).
func (s *Service) Init(context.Context) error {
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.store.dropViewer(e.ID) })
	}
	return nil
}

// Reset forgets every session (test hooks: the database was wiped).
func (s *Service) Reset() { s.store.reset() }

// Page returns a feed page. An empty cursor starts a new session with preset
// ("" = the viewer's saved preset); a cursor continues its session (preset is
// then ignored: a session keeps the order it started with).
func (s *Service) Page(ctx context.Context, preset string, cursor string) (gate.FeedPage, error) {
	began := time.Now()
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.FeedPage{}, err
	}
	var (
		sess *session
		pos  position
	)
	if cursor == "" {
		p, err := s.preset(ctx, v.ID, preset)
		if err != nil {
			return gate.FeedPage{}, err
		}
		if sess, err = s.start(ctx, v.ID, p); err != nil {
			return gate.FeedPage{}, err
		}
		pos = position{tier: 1}
	} else {
		var id sessionID
		if id, pos, err = s.openCursor(v.ID, cursor); err != nil {
			return gate.FeedPage{}, err
		}
		if sess, err = s.lookup(v.ID, id); err != nil {
			return gate.FeedPage{}, err
		}
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return gate.FeedPage{}, err
	}
	page, err := s.serve(ctx, sr, sess, pos)
	if err != nil {
		return gate.FeedPage{}, err
	}
	if s.d.Metrics != nil {
		s.d.Metrics.ObserveMs(MetricLatency, time.Since(began).Milliseconds())
	}
	return page, nil
}

// Expand returns the items of a collapsed group (§4.4), re-checked like a
// page, in ranked order, at most ExpandMax.
func (s *Service) Expand(ctx context.Context, token string) ([]gate.FeedItem, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	id, tier, err := s.openToken(v.ID, token)
	if err != nil {
		return nil, err
	}
	sess, err := s.lookup(v.ID, id)
	if err != nil {
		return nil, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	return s.expand(ctx, sr, sess, tier)
}

// NewAbove counts the posts the session's snapshots do not hold: published
// after the tier they belong to was snapshotted, in a tier the session already
// built (§4.3 "N new posts above"), and returns the closest such tier (0 when
// there is none). Snapshots never change; the client offers to start over.
func (s *Service) NewAbove(ctx context.Context, session string) (int, int, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return 0, 0, err
	}
	id, err := s.openSession(v.ID, session)
	if err != nil {
		return 0, 0, err
	}
	sess, err := s.lookup(v.ID, id)
	if err != nil {
		return 0, 0, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return 0, 0, err
	}
	sess.mu.Lock()
	built := make(map[int]time.Time, len(sess.tiers))
	oldest := time.Time{}
	for k, t := range sess.tiers {
		built[k] = t.built
		if oldest.IsZero() || t.built.Before(oldest) {
			oldest = t.built
		}
	}
	sess.mu.Unlock()
	if len(built) == 0 {
		return 0, 0, nil
	}
	f := s.filters(ctx, v.ID)
	count, closest := 0, 0
	s.d.Posts.WindowScan(func(m core.PostMeta) bool {
		if !m.CreatedAt.After(oldest) {
			return false // newest first: everything after this is older
		}
		d, ok := sr.Dist(m.Author)
		if !ok || d < 1 || !f.keep(m) {
			return true
		}
		if at, ok := built[d]; !ok || !m.CreatedAt.After(at) {
			return true
		}
		if _, ok := s.d.Vis.CheckPost(ctx, sr, m); !ok {
			return true
		}
		count++
		if closest == 0 || d < closest {
			closest = d
		}
		return true
	})
	return count, closest, nil
}

// Why explains where a visible post sits for the viewer (§5.3): its tier and
// chain, when it was posted, the viewer's preset and how far ranking moved it
// (minutes, positive = up) with the reason codes.
func (s *Service) Why(ctx context.Context, p gate.Ref) (gate.WhyView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.WhyView{}, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return gate.WhyView{}, err
	}
	id, ok := s.d.Refs.OpenPost(v.ID, p)
	if !ok {
		return gate.WhyView{}, gate.ErrNotFound
	}
	m, ok := s.d.Posts.Meta(ctx, id)
	if !ok {
		return gate.WhyView{}, gate.ErrNotFound
	}
	c, ok := s.d.Vis.CheckPost(ctx, sr, m)
	if !ok {
		return gate.WhyView{}, gate.ErrNotFound
	}
	me, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.WhyView{}, err
	}
	preset := me.Preset
	via, err := s.d.Chains.Chain(ctx, sr, m.Author)
	if err != nil {
		return gate.WhyView{}, err
	}
	head, _ := s.threadHead(ctx, sr, c)
	sc := rank.Score(rank.ParamsFor(preset), s.now(), s.candidate(ctx, sr, c, head, core.NewMemo(),
		s.d.Engage.ShowLessCounts(ctx, v.ID), s.hops(v.ID)))
	tier, _ := sr.Dist(m.Author)
	return gate.WhyView{
		Tier: tier, Via: via, PostedAt: m.CreatedAt, Preset: preset.String(),
		ShiftMinutes: int(math.Round(sc.TEff.Sub(m.CreatedAt).Minutes())), Reasons: sc.Reasons,
	}, nil
}

// preset resolves the requested preset: "" is the viewer's saved preset;
// unknown names are Problem preset_unavailable.
func (s *Service) preset(ctx context.Context, v core.UserID, name string) (core.Preset, error) {
	if name == "" {
		me, err := s.d.Users.Get(ctx, v)
		if err != nil {
			return 0, err
		}
		return me.Preset, nil
	}
	p, ok := core.ParsePreset(name)
	if !ok {
		return 0, gate.NewProblem(gate.StatusUnprocessable, "preset_unavailable", "preset")
	}
	return p, nil
}

// start opens a new session for v.
func (s *Service) start(ctx context.Context, v core.UserID, p core.Preset) (*session, error) {
	now := s.now()
	sess, err := newSession(v, p)
	if err != nil {
		return nil, err
	}
	sess.seen = s.d.Engage.SeenSet(ctx, v, now.Add(-SeenWindow))
	sess.showLess = s.d.Engage.ShowLessCounts(ctx, v)
	if prev, ok := s.store.put(sess, now); ok && prev != p && s.d.Metrics != nil {
		s.d.Metrics.Inc(MetricPresetSwitch, p.String(), 1)
	}
	return sess, nil
}

// lookup returns v's live session id; a verified id that is no longer kept is
// the viewer's own expired session.
func (s *Service) lookup(v core.UserID, id sessionID) (*session, error) {
	sess, ok := s.store.get(v, id, s.now())
	if !ok {
		return nil, gate.NewProblem(gate.StatusConflict, "feed_session_expired")
	}
	return sess, nil
}

// ended records the deepest tier a session served (feed.tier_reached) when the
// session leaves memory.
func (s *Service) ended(sess *session) {
	if k := int(sess.reached.Load()); k >= 1 && s.d.Metrics != nil {
		s.d.Metrics.Inc(MetricTierReached, degreeBucket(k), 1)
	}
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// logRank logs the t_eff components of a ranked tier at debug (§5.5).
func (s *Service) logRank(ctx context.Context, v core.UserID, k int, p rank.Params, scored []rank.Scored) {
	if s.d.Log == nil || !s.d.Log.Enabled(ctx, slog.LevelDebug) {
		return
	}
	for _, sc := range scored {
		args := append([]any{logx.UID(uint32(v)), "tier", k}, sc.LogArgs(p)...)
		s.d.Log.Debug("feed rank", args...)
	}
}
