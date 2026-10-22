// Package invites owns in-person invites: the token-bucket budget, probation,
// QR handshakes with SAS confirmation and the join flow (ARCHITECTURE §7, §8,
// §10.8). It implements core.Invites, core.Probation and (P1) core.Handshakes,
// and is the sole writer of invites, invite_budgets, handshake_sessions and the
// probation_* columns of users (plan §3.4).
//
// Owner: P0-W04 → P1-W02.
package invites

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Job kinds registered by the invites module (plan §4.3).
const (
	JobProbationEvaluate = "probation.evaluate"
	JobDiversity         = "invites.diversity"
)

// Budget, probation and handshake parameters (plan §4.7 invites, §7, §8, D23).
const (
	TokenEvery     = 24 * time.Hour // +1 invite per day (× the diversity multiplier)
	DefaultCap     = 3
	GenesisCap     = 5
	FriendCap      = 1000                // own edges + outgoing pending requests (D23)
	ProbationFor   = 30 * 24 * time.Hour // Phase 1 rule: 30 days and 3 friend+ edges outside the own subtree
	ProbationForP0 = 7 * 24 * time.Hour  // Phase 0 rule (accounts mid-probation at the cut-over)
	OutsideFriends = 3                   // edges to users outside the own invite subtree
	OpenTTL        = 10 * time.Minute    // an unclaimed handshake
	ClaimedTTL     = 15 * time.Minute    // a claimed handshake must finish (sound, registration, co-signing)
	HandleTries    = 10                  // handle_taken answers per handshake (D21)

	FrameEvery    = 250 * time.Millisecond // client-rendered QR frames
	FrameMaxAge   = 4                      // frames: the camera saw the screen within ~1 s (server clock)
	FrameMaxAhead = 1                      // frames a device may run early
	MaxAttempts   = 5                      // sound (or dual-QR) attempts per handshake
	AudioBound    = 5 * time.Second        // server-measured report_at − play_at
	LayerBound    = 3 * time.Second        // a layer decoded later than this (client timing) fails
	SyncMaxMs     = 100                    // |offset| for the "both layers in sync" bonus
	TimingBucket  = 250 * time.Millisecond // records keep timings in these buckets

	DiversityTarget = 0.5  // share of edges outside the inviter's subtree that earns the full rate
	DiversityFloor  = 0.25 // slowest rate (one invite in four days)

	ProbationJobEvery = 24 * time.Hour
	DiversityJobEvery = 24 * time.Hour
	notifyRedeemed    = "invite_redeemed"
	metricRedeemed    = "invites.redeemed"
	liveTopic         = "handshake"
)

// users.probation_rule values.
const (
	RuleP0 int16 = 0
	RuleP1 int16 = 1
)

// Service is the invites module.
type Service struct {
	d *core.Deps
}

var (
	_ core.Invites    = (*Service)(nil)
	_ core.Probation  = (*Service)(nil)
	_ core.Handshakes = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers the co-sign purposes, probation.evaluate and
// invites.diversity (daily) and the edge hooks: a new or upgraded edge can be
// the one that ends either side's probation.
func (s *Service) Init(context.Context) error {
	s.registerPurposes()
	if s.d.Bus != nil {
		eval := func(us ...core.UserID) {
			ctx := context.Background()
			for _, u := range us {
				if _, err := s.evaluate(ctx, u); err != nil && s.d.Log != nil {
					s.d.Log.Error("invites: probation evaluation failed", logx.UID(uint32(u)), "err", err)
				}
			}
		}
		core.Subscribe(s.d.Bus, func(e core.EdgeAdded) { eval(e.A, e.B) })
		core.Subscribe(s.d.Bus, func(e core.EdgeUpgraded) { eval(e.A, e.B) })
	}
	if s.d.Jobs == nil {
		return nil
	}
	s.d.Jobs.Handle(JobProbationEvaluate, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.EvaluateAll(ctx)
		return err
	})
	s.d.Jobs.Every(JobProbationEvaluate, ProbationJobEvery)
	s.d.Jobs.Handle(JobDiversity, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.EvaluateDiversity(ctx)
		return err
	})
	s.d.Jobs.Every(JobDiversity, DiversityJobEvery)
	return nil
}

// q returns tx, or the pool when tx is nil.
func (s *Service) q(tx store.Q) store.Q {
	if tx != nil {
		return tx
	}
	return s.d.DB.Q()
}

func notFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return gate.ErrNotFound
	}
	return err
}

// ---- budget (§8) ----

// budget is one invite_budgets row with the accrual applied at now.
type budget struct {
	tokens    float64 // accrued, capped
	diversity float64 // accrual multiplier (§8 diversity decay)
	cap       int
	paused    *time.Time
	revoked   bool
	probation bool
	nextAt    *time.Time
}

// available is the number of whole invites the user may spend now.
func (b budget) available() int {
	if b.probation || b.revoked || b.paused != nil {
		return 0
	}
	return int(math.Floor(b.tokens + 1e-9))
}

// loadBudget reads (creating it on first use) u's bucket and applies the
// accrual at now (one token per TokenEvery, times the diversity multiplier). lock takes the row lock for a spend. A new bucket starts full
// for genesis accounts, empty during probation (clearance sets one token) and
// with one token otherwise.
func (s *Service) loadBudget(ctx context.Context, q store.Q, m core.UserMeta, now time.Time, lock bool) (budget, error) {
	cap, start := DefaultCap, 1.0
	switch {
	case m.Genesis:
		cap, start = GenesisCap, GenesisCap
	case m.OnProbation(now):
		start = 0
	}
	if _, err := q.Exec(ctx, `INSERT INTO invite_budgets (user_id, tokens, cap, refreshed_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO NOTHING`, int64(m.ID), start, int16(cap), now); err != nil {
		return budget{}, err
	}
	sql := "SELECT tokens, cap, refreshed_at, paused_until, revoked, diversity FROM invite_budgets WHERE user_id = $1"
	if lock {
		sql = "SELECT tokens, cap, refreshed_at, paused_until, revoked, diversity FROM invite_budgets WHERE user_id = $1 FOR UPDATE"
	}
	var (
		b       budget
		cap16   int16
		refresh time.Time
	)
	if err := q.QueryRow(ctx, sql, int64(m.ID)).Scan(&b.tokens, &cap16, &refresh, &b.paused, &b.revoked,
		&b.diversity); err != nil {
		return budget{}, err
	}
	b.cap = int(cap16)
	if b.paused != nil && !b.paused.After(now) {
		b.paused = nil
	}
	b.probation = m.OnProbation(now)
	if b.probation || b.revoked || b.paused != nil {
		return b, nil // nothing accrues while invites are locked
	}
	b.diversity = min(max(b.diversity, DiversityFloor), 1)
	if el := now.Sub(refresh); el > 0 {
		b.tokens += float64(el) / float64(TokenEvery) * b.diversity
	}
	if b.tokens >= float64(b.cap) {
		b.tokens = float64(b.cap)
		return b, nil
	}
	frac := b.tokens - math.Floor(b.tokens)
	next := now.Add(time.Duration((1 - frac) * float64(TokenEvery) / b.diversity))
	if t := next.Truncate(time.Second); t.Before(next) {
		next = t.Add(time.Second) // a countdown never reaches zero early
	}
	b.nextAt = &next
	return b, nil
}

// spend takes one token from u's locked bucket (materializing the accrual).
func (s *Service) spend(ctx context.Context, q store.Q, u core.UserID, b budget, now time.Time) error {
	_, err := q.Exec(ctx, "UPDATE invite_budgets SET tokens = $2, refreshed_at = $3 WHERE user_id = $1",
		int64(u), b.tokens-1, now)
	return err
}

// Budget returns the viewer's own invite budget.
func (s *Service) Budget(ctx context.Context) (gate.BudgetView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.BudgetView{}, err
	}
	m, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.BudgetView{}, err
	}
	now := s.d.Clock.Now()
	b, err := s.loadBudget(ctx, s.d.DB.Q(), m, now, false)
	if err != nil {
		return gate.BudgetView{}, err
	}
	out := gate.BudgetView{Available: b.available(), Cap: b.cap, PausedUntil: b.paused, Revoked: b.revoked}
	if s.d.Limits != nil {
		a, ok, err := s.d.Limits.WriteAllowance(ctx, v, core.WriteKind("invite_handshake"))
		if err != nil {
			return gate.BudgetView{}, err
		}
		if ok {
			out.Attempts = &gate.AllowanceView{Available: a.Available, Cap: a.Cap, NextAt: a.NextAt}
		}
	}
	if !b.probation && !b.revoked && b.paused == nil {
		out.NextAt = b.nextAt
	}
	if b.probation {
		rule, err := s.rule(ctx, v.ID)
		if err != nil {
			return gate.BudgetView{}, err
		}
		need, err := s.needOutside(ctx, v.ID, rule)
		if err != nil {
			return gate.BudgetView{}, err
		}
		out.Probation = &gate.ProbationView{Until: *m.ProbationUntil, NeedOutsideFriends: need, FriendPlus: rule == RuleP1}
	}
	return out, nil
}

// Invitees lists the viewer's own invitees, newest first, with their probation
// state (§8: probation is visible to the inviter). Deleted accounts and
// invitees outside the viewer's horizon are left out.
func (s *Service) Invitees(ctx context.Context) ([]gate.InviteeView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.d.DB.Q().Query(ctx, `SELECT u.id, COALESCE(i.created_at, u.created_at) FROM users u
		LEFT JOIN invites i ON i.invitee_id = u.id
		WHERE u.invited_by = $1 AND u.status <> $2 ORDER BY 2 DESC, u.id DESC`, int64(v.ID), int16(core.StatusDeleted))
	if err != nil {
		return nil, err
	}
	type row struct {
		id     core.UserID
		joined time.Time
	}
	var list []row
	for rows.Next() {
		var (
			id int64
			at time.Time
		)
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, row{core.UserID(id), at}) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []gate.InviteeView{}
	if len(list) == 0 {
		return out, nil
	}
	sr, err := s.d.Vis.SearchFor(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	now := s.d.Clock.Now()
	for _, r := range list {
		m, err := s.d.Users.Get(ctx, r.id)
		if err != nil {
			return nil, err
		}
		p, err := s.d.Views.Person(ctx, sr, r.id)
		if errors.Is(err, gate.ErrNotFound) {
			continue // no longer in the inviter's horizon: the graph hides them (§2.2)
		}
		if err != nil {
			return nil, err
		}
		iv := gate.InviteeView{Person: p, JoinedAt: r.joined, OnProbation: m.OnProbation(now)}
		if iv.OnProbation {
			iv.ProbationUntil = m.ProbationUntil
		}
		out = append(out, iv)
	}
	return out, nil
}

// ---- core.Invites / core.Probation ----

// InviteesOf returns the direct invitees of inviter.
func (s *Service) InviteesOf(ctx context.Context, inviter core.UserID) ([]core.UserID, error) {
	rows, err := s.d.DB.Q().Query(ctx, "SELECT id FROM users WHERE invited_by = $1 ORDER BY id", int64(inviter))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.UserID
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, core.UserID(id)) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	return out, rows.Err()
}

// Subtree returns root and every transitive invitee.
func (s *Service) Subtree(ctx context.Context, root core.UserID) (*roaring.Bitmap, error) {
	rows, err := s.d.DB.Q().Query(ctx, `WITH RECURSIVE t(id) AS (SELECT $1::bigint
		UNION SELECT u.id FROM users u JOIN t ON u.invited_by = t.id) SELECT id FROM t`, int64(root))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := roaring.New()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out.Add(uint32(id)) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	return out, rows.Err()
}

// Penalize records one invite-tree quarantine offense of inviter in tx
// (core.InvitePenalties; steward body, contract request p1-w06-1): the first
// offense pauses its invites until until, any later one revokes them.
func (s *Service) Penalize(ctx context.Context, tx store.Q, inviter core.UserID, until time.Time) (int, error) {
	var n int16
	err := s.q(tx).QueryRow(ctx, `INSERT INTO invite_budgets (user_id, tokens, refreshed_at, paused_until, offenses)
		VALUES ($1, 0, $2, $3, 1)
		ON CONFLICT (user_id) DO UPDATE SET offenses = invite_budgets.offenses + 1,
			paused_until = CASE WHEN invite_budgets.offenses = 0 THEN EXCLUDED.paused_until ELSE invite_budgets.paused_until END,
			revoked = invite_budgets.revoked OR invite_budgets.offenses >= 1
		RETURNING offenses`, int64(inviter), s.d.Clock.Now(), until).Scan(&n)
	if err != nil {
		return 0, err
	}
	s.d.Bus.Publish(core.UserChanged{ID: inviter})
	return int(n), nil
}

// Reset puts users back on probation until until (P1 quarantine), under the
// Phase 1 rule. Their buckets empty; clearance grants one token again.
func (s *Service) Reset(ctx context.Context, tx store.Q, users []core.UserID, until time.Time) error {
	if len(users) == 0 {
		return nil
	}
	q := s.q(tx)
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = int64(u)
	}
	if _, err := q.Exec(ctx, `UPDATE users SET probation_until = $2, probation_cleared_at = NULL, probation_rule = $3
		WHERE id = ANY($1) AND NOT is_genesis`, ids, until, RuleP1); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, "UPDATE invite_budgets SET tokens = 0, refreshed_at = $2 WHERE user_id = ANY($1)",
		ids, s.d.Clock.Now()); err != nil {
		return err
	}
	for _, u := range users {
		s.d.Bus.Publish(core.UserChanged{ID: u})
	}
	return nil
}
