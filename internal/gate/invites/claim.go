package invites

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"math/big"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// The claiming device (§7 step 2): the invitee's browser in a guest session,
// the friend's signed-in browser for an upgrade, the recovering browser in a
// guest session for a recovery. Its separate non-extractable ECDSA P-256 key
// signs the claim and every later join call; a call without a valid signature
// is the uniform not-found, so a stolen session cookie alone cannot continue
// a handshake.

// Join calls a signature names (the "call" of the signed message).
const (
	CallStatus         = "status"
	CallAudioStart     = "audio_start"
	CallDualFrame      = "dual_frame"
	CallRegisterBegin  = "register_begin"
	CallRegisterFinish = "register_finish"
	CallRegisterCosign = "register_cosign"
)

// ClaimIn is the claim of a scanned frame (POST /api/join/claim). Byte fields
// are base64url without padding.
type ClaimIn struct {
	Payload string `json:"payload"`
	EV      string `json:"e_v"`     // the claiming device's ephemeral ECDH P-256 key (uncompressed point)
	SigPub  string `json:"sig_pub"` // its ECDSA P-256 signing key (uncompressed point)
	Proof   string `json:"proof"`   // P1363 signature over "pcc-join-claim-v1" ‖ payload ‖ E_v
}

// claimMessage is what the claim proof signs.
func claimMessage(payload string, ev []byte) []byte {
	return append(append([]byte("pcc-join-claim-v1"), payload...), ev...)
}

// callMessage is what a later join call's signature signs.
func callMessage(h hsRow, call string) []byte {
	m := append([]byte("pcc-join-v1"), h.id[:]...)
	m = append(m, h.challenge...)
	return append(m, call...)
}

// verifySig checks a P1363 (r ‖ s) ECDSA P-256 SHA-256 signature by pub.
func verifySig(pub, msg, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	k, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	if err != nil {
		return false
	}
	d := sha256.Sum256(msg)
	return ecdsa.Verify(k, d[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

// claimer is the caller's session for the join side.
func claimer(ctx context.Context) (core.Viewer, error) {
	v, ok := core.ViewerFrom(ctx)
	if !ok || v.Session == ([32]byte{}) || (v.Kind != core.SessGuest && (v.Kind != core.SessUser || v.ID == 0)) {
		return core.Viewer{}, gate.ErrNotFound
	}
	return v, nil
}

// decodeClaim checks the claim's own parts (frame syntax, keys, proof).
func decodeClaim(in ClaimIn) (parsedFrame, []byte, []byte, bool) {
	f, ok := parseFrame(in.Payload)
	if !ok {
		return parsedFrame{}, nil, nil, false
	}
	ev, err1 := b64.DecodeString(in.EV)
	pub, err2 := b64.DecodeString(in.SigPub)
	proof, err3 := b64.DecodeString(in.Proof)
	if err1 != nil || err2 != nil || err3 != nil || !validPoint(ev) || !validPoint(pub) ||
		!verifySig(pub, claimMessage(in.Payload, ev), proof) {
		return parsedFrame{}, nil, nil, false
	}
	return f, ev, pub, true
}

// Claim binds a scanned frame to the caller's session (invites: a guest
// session; upgrades: the friend's own signed-in session). Any failure (stale
// or forged frame, missing or wrong key or proof, taken or finished
// handshake, wrong session) is the uniform not-found.
func (s *Service) Claim(ctx context.Context, in ClaimIn) error {
	v, err := claimer(ctx)
	if err != nil {
		return err
	}
	_, err = s.claim(ctx, v, in, false)
	return err
}

// claim runs a claim; recovery claims come only through ClaimRecovery.
func (s *Service) claim(ctx context.Context, v core.Viewer, in ClaimIn, recovery bool) (hsRow, error) {
	f, ev, pub, ok := decodeClaim(in)
	if !ok {
		return hsRow{}, gate.ErrNotFound
	}
	now := s.d.Clock.Now()
	var h hsRow
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		h, err = scanHS(tx.QueryRow(ctx, selectHS+"WHERE id = $1 FOR UPDATE", f.hs))
		if err != nil {
			return err
		}
		switch {
		case recovery:
			ok = h.kind == kindRecovery && v.Kind == core.SessGuest
		case h.kind == kindInvite:
			ok = v.Kind == core.SessGuest
		case h.kind == kindUpgrade:
			ok = v.Kind == core.SessUser && v.ID == h.peer
		default:
			ok = false
		}
		if !ok || h.mode == modeQR || h.live(now) != stOpen || h.ei == nil || !hmac.Equal(h.ei, f.ei) ||
			!hmac.Equal(f.mac, frameMAC(h.frameKey, h.id, h.ei, f.n)) || !fresh(h.createdAt, now, f.n) {
			return gate.ErrNotFound
		}
		challenge := sha256.Sum256([]byte(in.Payload))
		h.state, h.claimed, h.ev, h.sigPub, h.challenge = stClaimed, v.Session[:], ev, pub, challenge[:]
		_, err = tx.Exec(ctx, `UPDATE handshake_sessions SET state = $2, claimed_session = $3, claimed_at = $4,
			expires_at = $5, invitee_epub = $6, invitee_sig_pub = $7, scan_index = $8, join_challenge = $9
			WHERE id = $1`, h.id, stClaimed, v.Session[:], now, now.Add(ClaimedTTL), ev, pub, f.n, challenge[:])
		return err
	})
	if err != nil {
		return hsRow{}, err
	}
	s.liveEvent(h, "claimed")
	return h, nil
}

// claimedBy loads the handshake the session sess claimed most recently.
func (s *Service) claimedBy(ctx context.Context, q store.Q, sess [32]byte, lock bool) (hsRow, error) {
	sql := selectHS + "WHERE claimed_session = $1 ORDER BY claimed_at DESC LIMIT 1"
	if lock {
		sql = selectHS + "WHERE claimed_session = $1 ORDER BY claimed_at DESC LIMIT 1 FOR UPDATE"
	}
	return scanHS(q.QueryRow(ctx, sql, sess[:]))
}

// joinCall authenticates a later join call: the caller's session claimed the
// handshake, the session kind fits the handshake kind, and sig (base64url) is
// the claiming device's signature over (hs, challenge, call).
func (s *Service) joinCall(ctx context.Context, q store.Q, sig, call string, lock bool) (core.Viewer, hsRow, error) {
	v, err := claimer(ctx)
	if err != nil {
		return core.Viewer{}, hsRow{}, err
	}
	h, err := s.claimedBy(ctx, q, v.Session, lock)
	if err != nil {
		return core.Viewer{}, hsRow{}, err
	}
	raw, err := b64.DecodeString(sig)
	kindOK := (h.kind == kindUpgrade) == (v.Kind == core.SessUser) && (h.kind != kindUpgrade || v.ID == h.peer)
	if err != nil || !kindOK || h.sigPub == nil || !verifySig(h.sigPub, callMessage(h, call), raw) {
		return core.Viewer{}, hsRow{}, gate.ErrNotFound
	}
	return v, h, nil
}

// JoinStatus returns the claiming device's view of its handshake (signed call
// "status").
func (s *Service) JoinStatus(ctx context.Context, sig string) (StatusView, error) {
	v, h, err := s.joinCall(ctx, s.d.DB.Q(), sig, CallStatus, false)
	if err != nil {
		return StatusView{}, err
	}
	return s.status(h, v, true, s.d.Clock.Now()), nil
}
