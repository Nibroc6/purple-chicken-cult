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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Job kinds registered by the invites module (plan §4.3).
const JobProbationEvaluate = "probation.evaluate"

// Budget, probation and handshake parameters (plan §4.7 invites, §8, D23).
const (
	TokenEvery        = 24 * time.Hour // +1 invite per day
	DefaultCap        = 3
	GenesisCap        = 5
	FriendCap         = 1000 // own edges + outgoing pending requests (D23)
	ProbationFor      = 7 * 24 * time.Hour
	OutsideFriends    = 3 // edges to users outside the own invite subtree
	FrameWindow       = 10 * time.Second
	OpenTTL           = 10 * time.Minute // an unclaimed handshake
	ClaimedTTL        = 15 * time.Minute // a claimed handshake must finish registration
	HandleTries       = 10               // handle_taken answers per handshake (D21)
	ProbationJobEvery = 24 * time.Hour
	notifyRedeemed    = "invite_redeemed"
	metricRedeemed    = "invites.redeemed"
	liveTopic         = "handshake"
)

// handshake_sessions.kind and .state values.
const (
	kindInvite int16 = 0

	stOpen      int16 = 0
	stClaimed   int16 = 1
	stConfirmed int16 = 2
	stDone      int16 = 3
	stCancelled int16 = 4
	stExpired   int16 = 5
)

func stateName(st int16) string {
	switch st {
	case stClaimed:
		return "claimed"
	case stConfirmed:
		return "confirmed"
	case stDone:
		return "done"
	case stCancelled:
		return "cancelled"
	case stExpired:
		return "expired"
	}
	return "open"
}

// Service is the invites module.
type Service struct {
	d  *core.Deps
	hs coreref.MemHandshakes
}

var (
	_ core.Invites    = (*Service)(nil)
	_ core.Probation  = (*Service)(nil)
	_ core.Handshakes = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers probation.evaluate (daily) and the EdgeAdded hook: a new edge
// can be the one that ends either side's probation.
func (s *Service) Init(context.Context) error {
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.EdgeAdded) {
			ctx := context.Background()
			for _, u := range []core.UserID{e.A, e.B} {
				if _, err := s.evaluate(ctx, u); err != nil && s.d.Log != nil {
					s.d.Log.Error("invites: probation evaluation failed", logx.UID(uint32(u)), "err", err)
				}
			}
		})
	}
	if s.d.Jobs == nil {
		return nil
	}
	s.d.Jobs.Handle(JobProbationEvaluate, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.EvaluateAll(ctx)
		return err
	})
	s.d.Jobs.Every(JobProbationEvaluate, ProbationJobEvery)
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
// accrual at now. lock takes the row lock for a spend. A new bucket starts full
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
	sql := "SELECT tokens, cap, refreshed_at, paused_until, revoked FROM invite_budgets WHERE user_id = $1"
	if lock {
		sql = "SELECT tokens, cap, refreshed_at, paused_until, revoked FROM invite_budgets WHERE user_id = $1 FOR UPDATE"
	}
	var (
		b       budget
		cap16   int16
		refresh time.Time
	)
	if err := q.QueryRow(ctx, sql, int64(m.ID)).Scan(&b.tokens, &cap16, &refresh, &b.paused, &b.revoked); err != nil {
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
	if el := now.Sub(refresh); el > 0 {
		b.tokens += float64(el) / float64(TokenEvery)
	}
	if b.tokens >= float64(b.cap) {
		b.tokens = float64(b.cap)
		return b, nil
	}
	frac := b.tokens - math.Floor(b.tokens)
	next := now.Add(time.Duration((1 - frac) * float64(TokenEvery)))
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
	if !b.probation && !b.revoked && b.paused == nil {
		out.NextAt = b.nextAt
	}
	if b.probation {
		need, err := s.needOutside(ctx, v.ID)
		if err != nil {
			return gate.BudgetView{}, err
		}
		out.Probation = &gate.ProbationView{Until: *m.ProbationUntil, NeedOutsideFriends: need}
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

// Reset puts users back on probation until until (P1 quarantine). Their
// buckets empty; clearance grants one token again.
func (s *Service) Reset(ctx context.Context, tx store.Q, users []core.UserID, until time.Time) error {
	if len(users) == 0 {
		return nil
	}
	q := s.q(tx)
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = int64(u)
	}
	if _, err := q.Exec(ctx, `UPDATE users SET probation_until = $2, probation_cleared_at = NULL
		WHERE id = ANY($1) AND NOT is_genesis`, ids, until); err != nil {
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

// ---- core.Handshakes (P1-W02) ----

// OpenUpgrade opens a friend+ upgrade handshake.
func (s *Service) OpenUpgrade(ctx context.Context, v core.Viewer, peer core.UserID) (gate.HandshakeView, error) {
	return s.hs.OpenUpgrade(ctx, v, peer)
}

// OpenRecovery opens a guardian recovery handshake.
func (s *Service) OpenRecovery(ctx context.Context, guardian, account core.UserID) (gate.HandshakeView, error) {
	return s.hs.OpenRecovery(ctx, guardian, account)
}

// ClaimRecovery claims a recovery handshake from a guest session.
func (s *Service) ClaimRecovery(ctx context.Context, sess [32]byte, payload string) (core.UserID, error) {
	return s.hs.ClaimRecovery(ctx, sess, payload)
}

// Result returns a handshake's outcome.
func (s *Service) Result(ctx context.Context, hs uuid.UUID) (core.Outcome, error) {
	return s.hs.Result(ctx, hs)
}
