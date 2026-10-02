// Package users is the user directory: it implements core.Users (cached,
// invalidated via core.UserChanged), the display-only inviter labels
// ("user" / "genesis" / "deleted") and Create, the ONLY `INSERT INTO users`.
//
// Owner: SCAFFOLD-0a wrote it complete; P0-W11 owns it afterwards (→ P1-W10).
package users

import (
	"context"
	"crypto/rand"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service implements core.Users over the users table.
type Service struct {
	d *core.Deps

	mu      sync.RWMutex
	cache   map[core.UserID]core.UserMeta
	inviter map[core.UserID]core.UserID
}

var _ core.Users = (*Service)(nil)

// New returns the service.
func New(d *core.Deps) *Service {
	return &Service{d: d, cache: map[core.UserID]core.UserMeta{}, inviter: map[core.UserID]core.UserID{}}
}

// Init loads the invite tree (for InviterOf) and subscribes to UserChanged.
func (s *Service) Init(ctx context.Context) error {
	core.Subscribe(s.d.Bus, func(e core.UserChanged) { s.Invalidate(e.ID) })
	core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.Invalidate(e.ID) })
	return s.Reload(ctx)
}

// Reload re-reads the invite tree and drops the cache.
func (s *Service) Reload(ctx context.Context) error {
	rows, err := s.d.DB.Q().Query(ctx, "SELECT id, invited_by FROM users WHERE invited_by IS NOT NULL")
	if err != nil {
		return err
	}
	inv := map[core.UserID]core.UserID{}
	for rows.Next() {
		var id, by int64
		if err := rows.Scan(&id, &by); err != nil {
			rows.Close()
			return err
		}
		inv[core.UserID(id)] = core.UserID(by)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.inviter = inv
	s.cache = map[core.UserID]core.UserMeta{}
	s.mu.Unlock()
	return nil
}

// Invalidate drops id from the cache.
func (s *Service) Invalidate(id core.UserID) {
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
}

const selectUser = `SELECT id, ext_id, handle, display_name, COALESCE(invited_by, 0), is_genesis, status, role, created_at,
	probation_until, probation_cleared_at IS NOT NULL, name_in_chains, suggestible, named_in_likes, slippery_default,
	rank_preset, ui_locale, languages FROM users `

func scanUser(row pgx.Row) (core.UserMeta, error) {
	var (
		m                    core.UserMeta
		id, invitedBy        int64
		status, role, preset int16
		probUntil            *time.Time
	)
	err := row.Scan(&id, &m.ExtID, &m.Handle, &m.DisplayName, &invitedBy, &m.Genesis, &status, &role, &m.CreatedAt,
		&probUntil, &m.ProbationCleared, &m.NameInChains, &m.Suggestible, &m.NamedInLikes, &m.SlipperyDefault,
		&preset, &m.Locale, &m.Languages)
	if err != nil {
		return core.UserMeta{}, err
	}
	m.ID, m.InvitedBy = core.UserID(id), core.UserID(invitedBy)
	m.Status, m.Role, m.Preset = uint8(status), core.Role(role), core.Preset(preset)
	m.ProbationUntil = probUntil
	return m, nil
}

// Get returns a user or gate.ErrNotFound.
func (s *Service) Get(ctx context.Context, id core.UserID) (core.UserMeta, error) {
	if id == 0 {
		return core.UserMeta{}, gate.ErrNotFound
	}
	s.mu.RLock()
	m, ok := s.cache[id]
	s.mu.RUnlock()
	if ok {
		return m, nil
	}
	m, err := scanUser(s.d.DB.Q().QueryRow(ctx, selectUser+"WHERE id = $1", int64(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.UserMeta{}, gate.ErrNotFound
	}
	if err != nil {
		return core.UserMeta{}, err
	}
	s.mu.Lock()
	s.cache[id] = m
	s.mu.Unlock()
	return m, nil
}

// GetMany returns every existing user among ids.
func (s *Service) GetMany(ctx context.Context, ids []core.UserID) (map[core.UserID]core.UserMeta, error) {
	out := make(map[core.UserID]core.UserMeta, len(ids))
	for _, id := range ids {
		m, err := s.Get(ctx, id)
		if errors.Is(err, gate.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, nil
}

// InviterLabel returns id's display-only inviter label (§3).
func (s *Service) InviterLabel(ctx context.Context, id core.UserID) (gate.InviterLabel, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return gate.InviterLabel{}, err
	}
	if m.Genesis || m.InvitedBy == 0 {
		return gate.InviterLabel{Kind: "genesis"}, nil
	}
	inv, err := s.Get(ctx, m.InvitedBy)
	if errors.Is(err, gate.ErrNotFound) || (err == nil && inv.Status == core.StatusDeleted) {
		return gate.InviterLabel{Kind: "deleted"}, nil
	}
	if err != nil {
		return gate.InviterLabel{}, err
	}
	return gate.InviterLabel{Kind: "user", Handle: inv.Handle}, nil
}

// InviterOf returns id's inviter from memory.
func (s *Service) InviterOf(id core.UserID) (core.UserID, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	by, ok := s.inviter[id]
	return by, ok
}

// ByHandle resolves a canonical handle. INTERNAL ONLY: mentions within the
// author's horizon, the CLI and test hooks. Never reachable from a raw API input.
func (s *Service) ByHandle(ctx context.Context, canon string) (core.UserID, bool) {
	var id int64
	err := s.d.DB.Q().QueryRow(ctx, "SELECT id FROM users WHERE handle_canon = $1", canon).Scan(&id)
	if err != nil {
		return 0, false
	}
	return core.UserID(id), true
}

// HandleAvailable reports whether canon is free (join flow only, D21).
func (s *Service) HandleAvailable(ctx context.Context, canon string) bool {
	var n int
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT count(*) FROM users WHERE handle_canon = $1", canon).Scan(&n); err != nil {
		return false
	}
	return n == 0
}

// Create inserts a user. Problems: handle_invalid, handle_taken.
func (s *Service) Create(ctx context.Context, tx store.Q, u core.NewUser) (core.UserID, error) {
	canon, ok := Canon(u.Handle)
	if !ok {
		return 0, gate.NewProblem(gate.StatusUnprocessable, "handle_invalid", "handle")
	}
	webID := u.WebAuthnID
	if len(webID) == 0 {
		webID = make([]byte, 32)
		if _, err := rand.Read(webID); err != nil {
			return 0, err
		}
	}
	locale := u.Locale
	if locale == "" {
		locale = "en"
	}
	langs := u.Languages
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	now := s.d.Clock.Now()
	var tosVersion *int16
	var tosAt, ageAt *time.Time
	if u.TOSVersion > 0 {
		v := int16(u.TOSVersion)
		tosVersion, tosAt = &v, &now
	}
	if u.AgeAttested {
		ageAt = &now
	}
	var invitedBy *int64
	if u.InvitedBy != 0 {
		v := int64(u.InvitedBy)
		invitedBy = &v
	}
	ext := s.d.IDs.New()
	if ext == uuid.Nil {
		ext = uuid.Must(uuid.NewV7())
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO users (ext_id, handle, handle_canon, display_name, invited_by, is_genesis, status,
		created_at, probation_until, ui_locale, languages, webauthn_user_id, tos_version, tos_accepted_at, age_attested_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
		ext, u.Handle, canon, u.DisplayName, invitedBy, u.Genesis, int16(u.Status), now, u.ProbationUntil, locale, langs,
		webID, tosVersion, tosAt, ageAt).Scan(&id)
	if store.IsUniqueViolation(err) {
		return 0, gate.NewProblem(gate.StatusConflict, "handle_taken", "handle")
	}
	if err != nil {
		return 0, err
	}
	if u.InvitedBy != 0 {
		s.mu.Lock()
		s.inviter[core.UserID(id)] = u.InvitedBy
		s.mu.Unlock()
	}
	return core.UserID(id), nil
}

var handleRE = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// Canon canonicalizes a handle: NFKC, lowercase, then ^[a-z0-9_]{3,20}$ and not reserved.
func Canon(h string) (string, bool) {
	c := strings.ToLower(norm.NFKC.String(strings.TrimPrefix(strings.TrimSpace(h), "@")))
	if !handleRE.MatchString(c) || Reserved[c] {
		return "", false
	}
	return c, true
}
