package invites

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

// Handshake protocol v2 (ARCHITECTURE §7, plan P1-W02).
//
//  1. Open: the server creates handshake H with a random frame_key (given to
//     the initiator's device) and a secret (never leaves the server). The
//     initiator's device makes an ephemeral P-256 key pair and posts E_i
//     (/epub); it then renders a new QR frame every FrameEvery from frame_key:
//     "pcc2.<H>.<E_i>.<n>.<mac>", n = frames since the handshake opened (the
//     time nonce nonce_t), mac = HMAC(frame_key, "pcc-frame-v2" ‖ H ‖ E_i ‖ n).
//  2. Claim: the other device scans a frame, makes its own ephemeral key pair
//     (E_v) and a separate non-extractable ECDSA P-256 key, and claims H with
//     {payload, e_v, sig_pub, proof}, proof = its signature over
//     "pcc-join-claim-v1" ‖ payload ‖ E_v. The frame must verify and be fresh
//     by the SERVER's clock (at most FrameMaxAge old). The claim binds E_v, the
//     signing key and the session; every later join call is signed over
//     (H, challenge = sha256(payload)) — a challenge keyed by the server.
//  3. Both screens show the SAS (HMAC(secret, session ‖ E_v)); the initiator
//     confirms "codes match" (every kind, security #6). The server relays E_v
//     to the initiator (/peer); both devices compute S = ECDH and
//     kmac = HKDF-SHA256(S, salt H, "pcc-hs-v2 kmac").
//  4. Sound (mode qr_audio, audio.go): the claiming device reports kmac and
//     gets the server's play time; it plays MAC_kmac(transcript) on both
//     layers; the initiator's device decodes them and reports the bits with
//     sha256(kmac). The server checks the commit and the bits and measures the
//     time bound itself (report_at − play_at); client timings only tighten it.
//     Dual QR (accessibility) replaces the sound with a second, kmac-keyed QR
//     that the claiming device scans; it is never Verified.
//  5. The outcome (Verified ⇒ friend+, Accepted ⇒ friend, Retry) decides what
//     follows: an invite issues the signup token bound to E_v (register.go), an
//     upgrade drafts the friend+ Friendship both sides co-sign (cosign.go), a
//     recovery reports its result to the account module (handshakes.go).
//
// Every failure the other device can cause is the uniform not-found.

const (
	framePrefix = "pcc2"
	dualPrefix  = "pcc2d"
	epubLen     = 65 // uncompressed P-256 point
	macLen      = 10
)

var b64 = base64.RawURLEncoding

// handshake_sessions.kind, .state and .mode values.
const (
	kindInvite   int16 = 0
	kindUpgrade  int16 = 1
	kindRecovery int16 = 2

	stOpen      int16 = 0
	stClaimed   int16 = 1
	stConfirmed int16 = 2
	stDone      int16 = 3
	stCancelled int16 = 4
	stExpired   int16 = 5

	modeQR      int16 = 0 // Phase 0 only; never opened from Phase 1 on
	modeQRAudio int16 = 1
	modeDualQR  int16 = 2
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

func kindName(k int16) string {
	switch k {
	case kindUpgrade:
		return "upgrade"
	case kindRecovery:
		return "recovery"
	}
	return "invite"
}

func modeName(m int16) string {
	switch m {
	case modeDualQR:
		return "dual_qr"
	case modeQR:
		return "qr"
	}
	return "qr_audio"
}

func outcomeName(o core.Outcome) string {
	switch o {
	case core.OutcomeRetry:
		return "retry"
	case core.OutcomeAccepted:
		return "accepted"
	case core.OutcomeVerified:
		return "verified"
	}
	return ""
}

// frameIndex is the frame number (nonce_t) of t in a handshake opened at opened.
func frameIndex(opened, t time.Time) int64 {
	if t.Before(opened) {
		return 0
	}
	return int64(t.Sub(opened) / FrameEvery)
}

// fresh reports whether frame n is current at now by the server's clock: at
// most FrameMaxAge frames old and at most FrameMaxAhead frames early.
func fresh(opened, now time.Time, n int64) bool {
	cur := frameIndex(opened, now)
	return n >= cur-FrameMaxAge && n <= cur+FrameMaxAhead
}

func be64(n int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n)) //nolint:gosec // frame indexes are never negative
	return b[:]
}

// frameMAC is HMAC-SHA256(frame_key, "pcc-frame-v2" ‖ hs ‖ E_i ‖ n)[:10].
func frameMAC(key []byte, hs uuid.UUID, ei []byte, n int64) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("pcc-frame-v2"))
	m.Write(hs[:])
	m.Write(ei)
	m.Write(be64(n))
	return m.Sum(nil)[:macLen]
}

// framePayload is the QR payload of frame n.
func framePayload(key []byte, hs uuid.UUID, ei []byte, n int64) string {
	return fmt.Sprintf("%s.%s.%s.%d.%s", framePrefix, b64.EncodeToString(hs[:]), b64.EncodeToString(ei), n,
		b64.EncodeToString(frameMAC(key, hs, ei, n)))
}

// parsedFrame is a decoded frame payload.
type parsedFrame struct {
	hs  uuid.UUID
	ei  []byte
	n   int64
	mac []byte
}

func parseIndex(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == s
}

func parseHS(s string) (uuid.UUID, bool) {
	raw, err := b64.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return uuid.Nil, false
	}
	id, err := uuid.FromBytes(raw)
	return id, err == nil
}

// parseFrame decodes "pcc2.<hs>.<E_i>.<n>.<mac>".
func parseFrame(p string) (parsedFrame, bool) {
	parts := strings.Split(p, ".")
	if len(p) > 256 || len(parts) != 5 || parts[0] != framePrefix {
		return parsedFrame{}, false
	}
	var f parsedFrame
	var ok bool
	if f.hs, ok = parseHS(parts[1]); !ok {
		return parsedFrame{}, false
	}
	ei, err := b64.DecodeString(parts[2])
	if err != nil || len(ei) != epubLen {
		return parsedFrame{}, false
	}
	f.ei = ei
	if f.n, ok = parseIndex(parts[3]); !ok {
		return parsedFrame{}, false
	}
	if f.mac, err = b64.DecodeString(parts[4]); err != nil || len(f.mac) != macLen {
		return parsedFrame{}, false
	}
	return f, true
}

// validPoint reports whether raw is an uncompressed P-256 public key.
func validPoint(raw []byte) bool {
	if len(raw) != epubLen {
		return false
	}
	_, err := ecdh.P256().NewPublicKey(raw)
	return err == nil
}

// sas is the short authentication string both screens show once a device
// claimed the handshake: HMAC(secret, session ‖ E_v) mod 10^6. A bystander who
// claims first (another session, another key) produces a different code.
func sas(secret, session, ev []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("pcc-sas-v2"))
	m.Write(session)
	m.Write(ev)
	n := binary.BigEndian.Uint64(m.Sum(nil)[:8]) % 1_000_000
	return fmt.Sprintf("%06d", n)
}

// hsRow is one handshake.
type hsRow struct {
	id         uuid.UUID
	kind       int16
	initiator  core.UserID
	peer       core.UserID // upgrade: the friend; recovery: the account (0 for invites)
	secret     []byte
	state      int16
	createdAt  time.Time
	expires    time.Time
	claimed    []byte
	tries      int16
	pending    []byte
	mode       int16
	quiet      bool
	frameKey   []byte
	ei, ev     []byte
	kmac       []byte
	commit     []byte
	sigPub     []byte
	playAt     *time.Time
	reportAt   *time.Time
	report     []byte
	attempts   int16
	outcome    core.Outcome
	scanIndex  *int64
	challenge  []byte
	assertions []byte
}

const selectHS = `SELECT id, kind, initiator_id, peer_user_id, secret, state, created_at, expires_at, claimed_session,
	handle_tries, pending, mode, quiet, frame_key, inviter_epub, invitee_epub, kmac, kmac_commit, invitee_sig_pub,
	play_at, report_at, report, attempts, outcome, scan_index, join_challenge, assertions
	FROM handshake_sessions `

func scanHS(row pgx.Row) (hsRow, error) {
	var (
		h        hsRow
		ini      int64
		peer     *int64
		outcome  int16
		attempts int16
	)
	err := row.Scan(&h.id, &h.kind, &ini, &peer, &h.secret, &h.state, &h.createdAt, &h.expires, &h.claimed, &h.tries,
		&h.pending, &h.mode, &h.quiet, &h.frameKey, &h.ei, &h.ev, &h.kmac, &h.commit, &h.sigPub, &h.playAt, &h.reportAt,
		&h.report, &attempts, &outcome, &h.scanIndex, &h.challenge, &h.assertions)
	if err != nil {
		return hsRow{}, notFoundOr(err)
	}
	h.initiator = core.UserID(ini) //nolint:gosec // users.id < 2^32 (DB CHECK)
	if peer != nil {
		h.peer = core.UserID(*peer) //nolint:gosec // users.id < 2^32 (DB CHECK)
	}
	h.attempts, h.outcome = attempts, core.Outcome(outcome) //nolint:gosec // 0..3 (handshake_sessions.outcome)
	return h, nil
}

// live is the state as of now: an unfinished handshake past its deadline is
// expired, whatever the stored state says.
func (h hsRow) live(now time.Time) int16 {
	if h.state <= stConfirmed && !now.Before(h.expires) {
		return stExpired
	}
	return h.state
}

// passed reports whether the handshake's outcome lets its kind continue.
func (h hsRow) passed() bool {
	if h.kind == kindUpgrade {
		return h.outcome == core.OutcomeVerified
	}
	return h.outcome == core.OutcomeAccepted || h.outcome == core.OutcomeVerified
}

// StatusView is the state of a handshake for either device (plan §5.3): the
// common HandshakeStatus (state and, once claimed, the SAS) plus the Phase 1
// protocol state.
type StatusView struct {
	gate.HandshakeStatus
	Kind  string `json:"kind"`  // invite | upgrade | recovery
	Mode  string `json:"mode"`  // qr_audio | dual_qr
	Quiet bool   `json:"quiet"` // the jingle is off (ultrasonic only)
	// Attempt counts the sound (or dual-QR) attempts so far; AttemptsLeft is
	// what remains of MaxAttempts.
	Attempt      int `json:"attempt"`
	AttemptsLeft int `json:"attempts_left"`
	// Playing: an attempt started and its decode report has not arrived yet
	// (the initiator's device listens).
	Playing bool   `json:"playing,omitempty"`
	Outcome string `json:"outcome,omitempty"` // retry | accepted | verified (of the latest attempt)
	Hint    string `json:"hint,omitempty"`    // volume_up | try_jingle (with retry)
	// Sign is what the viewer still has to co-sign: "needed" (the records are
	// drafted and this side has not signed) or "waiting" (this side signed, the
	// other has not). PeerSigned: the other side's signature is in.
	Sign       string `json:"sign,omitempty"`
	PeerSigned bool   `json:"peer_signed,omitempty"`
	// HS is the viewer's own ref to the handshake (join side, signed-in peer of
	// an upgrade: for POST /invites/handshakes/{hs}/cosign).
	HS gate.Ref `json:"hs,omitempty"`
	// SignupToken (join side, invites): issued once the outcome passed, bound to
	// E_v; register/begin needs it.
	SignupToken string `json:"signup_token,omitempty"`
}

// status renders h for the initiator (join false) or the claiming device.
func (s *Service) status(h hsRow, v core.Viewer, join bool, now time.Time) StatusView {
	st := h.live(now)
	out := StatusView{HandshakeStatus: gate.HandshakeStatus{State: stateName(st)}, Kind: kindName(h.kind),
		Mode: modeName(h.mode), Quiet: h.quiet, Attempt: int(h.attempts), AttemptsLeft: MaxAttempts - int(h.attempts)}
	if (st == stClaimed || st == stConfirmed) && h.claimed != nil {
		out.SAS = sas(h.secret, h.claimed, h.ev)
	}
	if st != stConfirmed {
		return out
	}
	out.Playing = h.playAt != nil && h.reportAt == nil && h.outcome == core.OutcomeNone
	if h.attempts > 0 && !out.Playing {
		o := h.outcome
		if h.kind == kindUpgrade && o == core.OutcomeAccepted {
			o = core.OutcomeRetry // an upgrade exists to be Verified
		}
		out.Outcome = outcomeName(o)
		if o == core.OutcomeRetry {
			out.Hint = s.hintOf(h)
		}
	}
	if join && h.kind == kindInvite && h.passed() {
		out.SignupToken = signupToken(h.secret, h.ev)
	}
	if join && v.Kind == core.SessUser && v.ID != 0 {
		out.HS = s.d.Refs.Handshake(v.ID, h.id)
	}
	var sg signatures
	if len(h.assertions) > 0 {
		_ = json.Unmarshal(h.assertions, &sg)
	}
	drafted := h.passed() && hasDrafts(h.pending)
	switch {
	case !drafted:
	case h.kind == kindInvite && !join:
		out.Sign = signState(len(sg.Initiator) > 0, false)
	case h.kind == kindInvite && join:
		out.PeerSigned = len(sg.Initiator) > 0
	case h.kind == kindUpgrade:
		mine, theirs := sg.Initiator, sg.Peer
		if join {
			mine, theirs = sg.Peer, sg.Initiator
		}
		out.Sign = signState(len(mine) > 0, len(theirs) > 0)
		out.PeerSigned = len(theirs) > 0
	}
	return out
}

func signState(mine, theirs bool) string {
	switch {
	case !mine:
		return "needed"
	case !theirs:
		return "waiting"
	}
	return ""
}

// own loads the viewer's own handshake (as its initiator) named by ref.
func (s *Service) own(ctx context.Context, q store.Q, v core.Viewer, ref gate.Ref, lock bool) (hsRow, error) {
	id, ok := s.d.Refs.OpenHandshake(v.ID, ref)
	if !ok {
		return hsRow{}, gate.ErrNotFound
	}
	sql := selectHS + "WHERE id = $1 AND initiator_id = $2"
	if lock {
		sql = selectHS + "WHERE id = $1 AND initiator_id = $2 FOR UPDATE"
	}
	return scanHS(q.QueryRow(ctx, sql, id, int64(v.ID)))
}

// Open opens an invite handshake. Phase 1 modes are "qr_audio" (the default)
// and "dual_qr" (accessibility, Accepted at most); quiet turns the jingle off.
// Opening a new invite handshake cancels the viewer's unfinished ones.
func (s *Service) Open(ctx context.Context, mode string, quiet bool) (gate.HandshakeView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	var m int16
	switch mode {
	case "", "qr_audio":
		m = modeQRAudio
	case "dual_qr":
		m, quiet = modeDualQR, false
	default:
		return gate.HandshakeView{}, gate.NewProblem(gate.StatusUnprocessable, "mode_unsupported", "mode")
	}
	meta, err := s.d.Users.Get(ctx, v.ID)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	now := s.d.Clock.Now()
	if err := s.canInvite(ctx, s.d.DB.Q(), meta, now); err != nil {
		return gate.HandshakeView{}, err
	}
	return s.openKind(ctx, v.ID, 0, kindInvite, m, quiet, now)
}

// openKind creates a handshake of kind (cancelling the initiator's unfinished
// ones of the same kind and peer) and returns its view with the frame key.
func (s *Service) openKind(ctx context.Context, initiator, peer core.UserID, kind, mode int16, quiet bool, now time.Time) (gate.HandshakeView, error) {
	secret := make([]byte, 32)
	key := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return gate.HandshakeView{}, err
	}
	if _, err := rand.Read(key); err != nil {
		return gate.HandshakeView{}, err
	}
	id, err := uuid.NewRandom() // random, so a scanned QR reveals nothing (§7)
	if err != nil {
		return gate.HandshakeView{}, err
	}
	var peerID *int64
	if peer != 0 {
		p := int64(peer)
		peerID = &p
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE handshake_sessions SET state = $4, pending = NULL, assertions = NULL
			WHERE initiator_id = $1 AND kind = $2 AND peer_user_id IS NOT DISTINCT FROM $3 AND state IN (0, 1, 2)`,
			int64(initiator), kind, peerID, stCancelled); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO handshake_sessions (id, kind, initiator_id, peer_user_id, secret, state,
			created_at, expires_at, mode, quiet, frame_key) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			id, kind, int64(initiator), peerID, secret, stOpen, now, now.Add(OpenTTL), mode, quiet, key)
		return err
	})
	if err != nil {
		return gate.HandshakeView{}, err
	}
	return gate.HandshakeView{HS: s.d.Refs.Handshake(initiator, id), Mode: modeName(mode),
		FrameTTLms: int(FrameEvery / time.Millisecond), FrameKey: b64.EncodeToString(key)}, nil
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

// Epub records the initiator device's ephemeral public key E_i (once, while
// the handshake is open; base64url). Without it no frame verifies.
func (s *Service) Epub(ctx context.Context, ref gate.Ref, epub string) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	id, ok := s.d.Refs.OpenHandshake(v.ID, ref)
	if !ok {
		return gate.ErrNotFound
	}
	ei, err := b64.DecodeString(epub)
	if err != nil || !validPoint(ei) {
		return gate.NewProblem(gate.StatusBadRequest, "bad_request", "e_i")
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE handshake_sessions SET inviter_epub = $3
		WHERE id = $1 AND initiator_id = $2 AND state = $4 AND expires_at > $5 AND inviter_epub IS NULL`,
		id, int64(v.ID), ei, stOpen, s.d.Clock.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return gate.ErrNotFound
	}
	return nil
}

// Frame returns the current frame of an open handshake as the server renders
// it (the initiator's device syncs its frame counter to it, then renders the
// next frames itself from the frame key).
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
	if h.live(now) != stOpen || h.ei == nil || h.frameKey == nil {
		return gate.FrameView{}, gate.ErrNotFound
	}
	n := frameIndex(h.createdAt, now)
	return gate.FrameView{Payload: framePayload(h.frameKey, h.id, h.ei, n),
		ValidUntil: h.createdAt.Add(time.Duration(n+1) * FrameEvery)}, nil
}

// Status returns the initiator-side state of the viewer's handshake.
func (s *Service) Status(ctx context.Context, ref gate.Ref) (StatusView, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return StatusView{}, err
	}
	h, err := s.own(ctx, s.d.DB.Q(), v, ref, false)
	if err != nil {
		return StatusView{}, err
	}
	return s.status(h, v, false, s.d.Clock.Now()), nil
}

// Peer relays the claiming device's E_v (base64url) to the initiator once claimed.
func (s *Service) Peer(ctx context.Context, ref gate.Ref) (string, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return "", err
	}
	h, err := s.own(ctx, s.d.DB.Q(), v, ref, false)
	if err != nil {
		return "", err
	}
	if st := h.live(s.d.Clock.Now()); (st != stClaimed && st != stConfirmed) || h.ev == nil {
		return "", gate.ErrNotFound
	}
	return b64.EncodeToString(h.ev), nil
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
	var h hsRow
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		h, err = s.own(ctx, tx, v, ref, true)
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
		_, err = tx.Exec(ctx, `UPDATE handshake_sessions SET state = $2, confirmed_at = COALESCE($3, confirmed_at),
			pending = CASE WHEN $4 THEN NULL ELSE pending END, assertions = CASE WHEN $4 THEN NULL ELSE assertions END
			WHERE id = $1`, h.id, to, confirmed, to == stCancelled)
		return err
	})
	if err == nil {
		s.liveEvent(h, stateName(to))
	}
	return err
}

// liveEvent tells both devices' signed-in users that h changed (SSE
// "handshake"); the claiming device of an invite polls instead.
func (s *Service) liveEvent(h hsRow, state string) {
	if s.d.Notify == nil {
		return
	}
	s.d.Notify.Live(h.initiator, liveTopic, map[string]any{"hs": s.d.Refs.Handshake(h.initiator, h.id), "state": state})
	if h.kind == kindUpgrade && h.peer != 0 {
		s.d.Notify.Live(h.peer, liveTopic, map[string]any{"hs": s.d.Refs.Handshake(h.peer, h.id), "state": state})
	}
}
