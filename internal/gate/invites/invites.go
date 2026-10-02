// Package invites owns in-person invites: the token-bucket budget, probation,
// QR handshakes with SAS confirmation and the join flow (ARCHITECTURE §7, §8,
// §10.8). It implements core.Invites, core.Probation and (P1) core.Handshakes.
//
// Owner: P0-W04 → P1-W02. Scaffold stub: core interfaces delegate to coreref;
// the handler-facing API returns gate.ErrNotImplemented.
package invites

import (
	"context"
	"encoding/json"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Service is the invites module.
type Service struct {
	d   *core.Deps
	ref *coreref.MemInvites
	hs  coreref.MemHandshakes
}

var (
	_ core.Invites    = (*Service)(nil)
	_ core.Probation  = (*Service)(nil)
	_ core.Handshakes = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d, ref: &coreref.MemInvites{D: d}} }

// Init registers probation.evaluate and the EdgeAdded hook in W04.
func (s *Service) Init(context.Context) error { return nil }

// Registration is the join form (POST /api/join/register/begin).
type Registration struct {
	Handle      string   `json:"handle"`
	DisplayName string   `json:"display_name"`
	Locale      string   `json:"locale"`
	Languages   []string `json:"languages"`
	TOSVersion  int      `json:"tos_version"`
	AgeAttested bool     `json:"age_attested"`
}

// ---- handler-facing API ----

// Budget returns the viewer's own invite budget.
func (s *Service) Budget(ctx context.Context) (gate.BudgetView, error) {
	return gate.BudgetView{}, s.todo(ctx)
}

// Invitees lists the viewer's own invitees with probation state.
func (s *Service) Invitees(ctx context.Context) ([]gate.InviteeView, error) { return nil, s.todo(ctx) }

// Open opens an invite handshake ("qr" in P0).
func (s *Service) Open(ctx context.Context, mode string, quiet bool) (gate.HandshakeView, error) {
	return gate.HandshakeView{}, s.todo(ctx)
}

// Frame returns the current rotating QR frame.
func (s *Service) Frame(ctx context.Context, hs gate.Ref) (gate.FrameView, error) {
	return gate.FrameView{}, s.todo(ctx)
}

// Status returns the inviter-side handshake state.
func (s *Service) Status(ctx context.Context, hs gate.Ref) (gate.HandshakeStatus, error) {
	return gate.HandshakeStatus{}, s.todo(ctx)
}

// Confirm records "codes match".
func (s *Service) Confirm(ctx context.Context, hs gate.Ref) error { return s.todo(ctx) }

// Cancel cancels a handshake.
func (s *Service) Cancel(ctx context.Context, hs gate.Ref) error { return s.todo(ctx) }

// Claim binds a scanned frame to the caller's guest session (any failure ⇒ ErrNotFound).
func (s *Service) Claim(ctx context.Context, payload string) error { return gate.ErrNotImplemented }

// JoinStatus returns the invitee-side handshake state.
func (s *Service) JoinStatus(ctx context.Context) (gate.HandshakeStatus, error) {
	return gate.HandshakeStatus{}, gate.ErrNotImplemented
}

// RegisterBegin validates the registration and starts the passkey ceremony.
func (s *Service) RegisterBegin(ctx context.Context, r Registration) (json.RawMessage, error) {
	return nil, gate.ErrNotImplemented
}

// RegisterFinish creates the account, records and friendship; returns a user session token.
func (s *Service) RegisterFinish(ctx context.Context, body []byte) (string, gate.MeView, error) {
	return "", gate.MeView{}, gate.ErrNotImplemented
}

func (s *Service) todo(ctx context.Context) error {
	if _, err := core.RequireUser(ctx); err != nil {
		return err
	}
	return gate.ErrNotImplemented
}

// ---- core.Invites / core.Probation (reference delegation) ----

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
		out = append(out, core.UserID(id))
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
		out.Add(uint32(id))
	}
	return out, rows.Err()
}

// Reset puts users back on probation (P1 quarantine).
func (s *Service) Reset(ctx context.Context, tx store.Q, users []core.UserID, until time.Time) error {
	return s.ref.Reset(ctx, tx, users, until)
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
