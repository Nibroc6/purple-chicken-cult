package invites

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// P0 handshake (§7, plan §4.7, D24): the inviter's screen shows a QR frame
// "pcc1.<hsid>.<window>.<mac>" that rotates every FrameWindow; the invitee's
// guest session claims it; both screens then show the same 6-digit SAS, which
// the inviter confirms before the invitee may register. Every claim failure is
// the uniform not-found.

const framePrefix = "pcc1"

var b64 = base64.RawURLEncoding

// window returns the frame window of t.
func window(t time.Time) int64 { return t.Unix() / int64(FrameWindow/time.Second) }

// frameMAC is HMAC-SHA256(secret, hsid ‖ window)[:10].
func frameMAC(secret []byte, hs uuid.UUID, w int64) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(hs[:])
	var wb [8]byte
	binary.BigEndian.PutUint64(wb[:], uint64(w)) //nolint:gosec // unix windows are positive
	m.Write(wb[:])
	return m.Sum(nil)[:10]
}

// framePayload is the QR payload of handshake hs in window w.
func framePayload(secret []byte, hs uuid.UUID, w int64) string {
	return fmt.Sprintf("%s.%s.%d.%s", framePrefix, b64.EncodeToString(hs[:]), w, b64.EncodeToString(frameMAC(secret, hs, w)))
}

// parseFrame splits a payload into its handshake ID, window and MAC.
func parseFrame(p string) (uuid.UUID, int64, []byte, bool) {
	parts := strings.Split(p, ".")
	if len(parts) != 4 || parts[0] != framePrefix || len(p) > 128 {
		return uuid.Nil, 0, nil, false
	}
	raw, err := b64.DecodeString(parts[1])
	if err != nil || len(raw) != 16 {
		return uuid.Nil, 0, nil, false
	}
	w, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || w <= 0 || strconv.FormatInt(w, 10) != parts[2] {
		return uuid.Nil, 0, nil, false
	}
	mac, err := b64.DecodeString(parts[3])
	if err != nil || len(mac) != 10 {
		return uuid.Nil, 0, nil, false
	}
	id, err := uuid.FromBytes(raw)
	if err != nil {
		return uuid.Nil, 0, nil, false
	}
	return id, w, mac, true
}

// sas is the short authentication string both screens show once a session
// claimed the handshake: HMAC(secret, claimed_session_hash) mod 10^6.
func sas(secret, session []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("pcc-sas-v1"))
	m.Write(session)
	n := binary.BigEndian.Uint64(m.Sum(nil)[:8]) % 1_000_000
	return fmt.Sprintf("%06d", n)
}

// hsRow is one invite handshake.
type hsRow struct {
	id        uuid.UUID
	inviter   core.UserID
	secret    []byte
	state     int16
	createdAt time.Time
	expires   time.Time
	claimed   []byte
	tries     int16
	pending   []byte
}

const selectHS = `SELECT id, initiator_id, secret, state, created_at, expires_at, claimed_session, handle_tries, pending
	FROM handshake_sessions `

func scanHS(row pgx.Row) (hsRow, error) {
	var (
		h   hsRow
		inv int64
	)
	err := row.Scan(&h.id, &inv, &h.secret, &h.state, &h.createdAt, &h.expires, &h.claimed, &h.tries, &h.pending)
	h.inviter = core.UserID(inv) //nolint:gosec // users.id < 2^32 (DB CHECK)
	return h, notFoundOr(err)
}

// live is the state as of now: an unfinished handshake past its deadline is
// expired, whatever the stored state says.
func (h hsRow) live(now time.Time) int16 {
	if h.state <= stConfirmed && !now.Before(h.expires) {
		return stExpired
	}
	return h.state
}

func (h hsRow) status(now time.Time) gate.HandshakeStatus {
	st := h.live(now)
	out := gate.HandshakeStatus{State: stateName(st)}
	if (st == stClaimed || st == stConfirmed) && h.claimed != nil {
		out.SAS = sas(h.secret, h.claimed)
	}
	return out
}

// own loads the viewer's own invite handshake named by ref.
func (s *Service) own(ctx context.Context, q store.Q, v core.Viewer, ref gate.Ref, lock bool) (hsRow, error) {
	id, ok := s.d.Refs.OpenHandshake(v.ID, ref)
	if !ok {
		return hsRow{}, gate.ErrNotFound
	}
	sql := selectHS + "WHERE id = $1 AND initiator_id = $2 AND kind = $3"
	if lock {
		sql = selectHS + "WHERE id = $1 AND initiator_id = $2 AND kind = $3 FOR UPDATE"
	}
	return scanHS(q.QueryRow(ctx, sql, id, int64(v.ID), kindInvite))
}

// Open opens an invite handshake. P0 accepts mode "qr" only; quiet is a P1
// (audio) option. Opening a new handshake cancels the viewer's unfinished ones.
func (s *Service) Open(ctx context.Context, mode string, quiet bool) (gate.HandshakeView, error) {
	_ = quiet
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	if mode != "qr" {
		return gate.HandshakeView{}, gate.NewProblem(gate.StatusUnprocessable, "mode_unsupported", "mode")
	}
	m, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	now := s.d.Clock.Now()
	if err := s.canInvite(ctx, s.d.DB.Q(), m, now); err != nil {
		return gate.HandshakeView{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return gate.HandshakeView{}, err
	}
	id, err := uuid.NewRandom() // random, so a scanned QR reveals nothing (§7)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE handshake_sessions SET state = $3 WHERE initiator_id = $1 AND kind = $2
			AND state IN (0, 1, 2)`, int64(v.ID), kindInvite, stCancelled); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO handshake_sessions (id, kind, initiator_id, secret, state, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, kindInvite, int64(v.ID), secret, stOpen, now, now.Add(OpenTTL))
		return err
	})
	if err != nil {
		return gate.HandshakeView{}, err
	}
	return gate.HandshakeView{HS: s.d.Refs.Handshake(v.ID, id), Mode: "qr",
		FrameTTLms: int(FrameWindow / time.Millisecond)}, nil
}

// canInvite checks the inviter side (probation, budget, pause, friend cap).
func (s *Service) canInvite(ctx context.Context, q store.Q, m core.UserMeta, now time.Time) error {
	if m.Status != core.StatusActive {
		return gate.ErrNotFound
	}
	if m.OnProbation(now) {
		return gate.NewProblem(gate.StatusConflict, "on_probation")
	}
	b, err := s.loadBudget(ctx, q, m, now, false)
	if err != nil {
		return err
	}
	if b.revoked || b.paused != nil {
		return gate.NewProblem(gate.StatusConflict, "invites_paused")
	}
	if b.available() < 1 {
		return gate.NewProblem(gate.StatusConflict, "no_invites")
	}
	edges, pending, err := s.d.Edges.Count(ctx, m.ID)
	if err != nil {
		return err
	}
	if edges+pending >= FriendCap {
		return gate.NewProblem(gate.StatusConflict, "friend_cap")
	}
	return nil
}

// Frame returns the current rotating QR frame of an open handshake.
func (s *Service) Frame(ctx context.Context, ref gate.Ref) (gate.FrameView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.FrameView{}, err
	}
	h, err := s.own(ctx, s.d.DB.Q(), v, ref, false)
	if err != nil {
		return gate.FrameView{}, err
	}
	now := s.d.Clock.Now()
	if h.live(now) != stOpen {
		return gate.FrameView{}, gate.ErrNotFound
	}
	w := window(now)
	until := time.Unix((w+1)*int64(FrameWindow/time.Second), 0).UTC()
	return gate.FrameView{Payload: framePayload(h.secret, h.id, w), ValidUntil: until}, nil
}

// Status returns the inviter-side handshake state (with the SAS once claimed).
func (s *Service) Status(ctx context.Context, ref gate.Ref) (gate.HandshakeStatus, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.HandshakeStatus{}, err
	}
	h, err := s.own(ctx, s.d.DB.Q(), v, ref, false)
	if err != nil {
		return gate.HandshakeStatus{}, err
	}
	return h.status(s.d.Clock.Now()), nil
}

// Confirm records "codes match": a claimed handshake becomes confirmed.
func (s *Service) Confirm(ctx context.Context, ref gate.Ref) error {
	return s.transition(ctx, ref, []int16{stClaimed}, stConfirmed)
}

// Cancel cancels an unfinished handshake ("codes don't match" also cancels).
func (s *Service) Cancel(ctx context.Context, ref gate.Ref) error {
	return s.transition(ctx, ref, []int16{stOpen, stClaimed, stConfirmed}, stCancelled)
}

func (s *Service) transition(ctx context.Context, ref gate.Ref, from []int16, to int16) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		h, err := s.own(ctx, tx, v, ref, true)
		if err != nil {
			return err
		}
		st, ok := h.live(now), false
		for _, f := range from {
			ok = ok || st == f
		}
		if !ok {
			return gate.ErrNotFound
		}
		var confirmed *time.Time
		if to == stConfirmed {
			confirmed = &now
		}
		_, err = tx.Exec(ctx, "UPDATE handshake_sessions SET state = $2, confirmed_at = COALESCE($3, confirmed_at) WHERE id = $1",
			h.id, to, confirmed)
		return err
	})
}

// guest returns the caller's guest session (the join flow runs only in one).
func guest(ctx context.Context) (core.Viewer, error) {
	v, ok := core.ViewerFrom(ctx)
	if !ok || v.Kind != core.SessGuest || v.Session == ([32]byte{}) {
		return core.Viewer{}, gate.ErrNotFound
	}
	return v, nil
}

// Claim binds a scanned frame to the caller's guest session. A frame is valid
// in its own window and the next one; any failure (stale or forged frame,
// taken or finished handshake, not a guest) is the uniform not-found.
func (s *Service) Claim(ctx context.Context, payload string) error {
	v, err := guest(ctx)
	if err != nil {
		return err
	}
	id, w, mac, ok := parseFrame(payload)
	if !ok {
		return gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	if cur := window(now); w != cur && w != cur-1 {
		return gate.ErrNotFound
	}
	var inviter core.UserID
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		h, err := scanHS(tx.QueryRow(ctx, selectHS+"WHERE id = $1 AND kind = $2 FOR UPDATE", id, kindInvite))
		if err != nil {
			return err
		}
		if !hmac.Equal(mac, frameMAC(h.secret, h.id, w)) || h.live(now) != stOpen {
			return gate.ErrNotFound
		}
		inviter = h.inviter
		_, err = tx.Exec(ctx, `UPDATE handshake_sessions SET state = $2, claimed_session = $3, claimed_at = $4,
			expires_at = $5 WHERE id = $1`, h.id, stClaimed, v.Session[:], now, now.Add(ClaimedTTL))
		return err
	})
	if err != nil {
		return err
	}
	if s.d.Notify != nil {
		s.d.Notify.Live(inviter, liveTopic, map[string]any{"state": "claimed"})
	}
	return nil
}

// claimedBy loads the handshake the guest session sess claimed most recently.
func (s *Service) claimedBy(ctx context.Context, q store.Q, sess [32]byte, lock bool) (hsRow, error) {
	sql := selectHS + "WHERE claimed_session = $1 AND kind = $2 ORDER BY claimed_at DESC LIMIT 1"
	if lock {
		sql = selectHS + "WHERE claimed_session = $1 AND kind = $2 ORDER BY claimed_at DESC LIMIT 1 FOR UPDATE"
	}
	return scanHS(q.QueryRow(ctx, sql, sess[:], kindInvite))
}

// JoinStatus returns the invitee-side state of the handshake this guest
// session claimed (with the SAS while it waits for the inviter).
func (s *Service) JoinStatus(ctx context.Context) (gate.HandshakeStatus, error) {
	v, err := guest(ctx)
	if err != nil {
		return gate.HandshakeStatus{}, err
	}
	h, err := s.claimedBy(ctx, s.d.DB.Q(), v.Session, false)
	if err != nil {
		return gate.HandshakeStatus{}, err
	}
	return h.status(s.d.Clock.Now()), nil
}
