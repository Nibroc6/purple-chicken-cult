package invites

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// The handshake sound and the score (ARCHITECTURE §7 "The handshake sound",
// "Handshake score").
//
// transcript = "pcc-hs-v2" ‖ H ‖ E_i ‖ E_v ‖ nonce_t ‖ attempt; the claiming
// device plays ultrasonic = HMAC(kmac, transcript)[:4] and, unless quiet,
// jingle = HMAC(kmac, transcript ‖ "jingle")[:3] at once. Only bits and
// timings reach the server; no audio is recorded, uploaded or stored.

// Channels recorded in a record's "handshake" field.
const (
	chQR     = "qr"
	chUltra  = "ultrasonic"
	chJingle = "jingle"
	chSync   = "sync"
	chDual   = "dual_qr"
)

// Retry hints.
const (
	HintVolumeUp  = "volume_up"
	HintTryJingle = "try_jingle"
)

// transcript is the MAC input of one attempt.
func transcript(h hsRow, attempt int16) []byte {
	var n int64
	if h.scanIndex != nil {
		n = *h.scanIndex
	}
	t := append([]byte("pcc-hs-v2"), h.id[:]...)
	t = append(t, h.ei...)
	t = append(t, h.ev...)
	t = append(t, be64(n)...)
	return append(t, byte(attempt)) //nolint:gosec // attempts ≤ MaxAttempts
}

// soundBits are the bits the claiming device plays in attempt.
func soundBits(kmac []byte, h hsRow, attempt int16) (ultra, jingle []byte) {
	tr := transcript(h, attempt)
	m := hmac.New(sha256.New, kmac)
	m.Write(tr)
	ultra = m.Sum(nil)[:4]
	m = hmac.New(sha256.New, kmac)
	m.Write(tr)
	m.Write([]byte("jingle"))
	return ultra, m.Sum(nil)[:3]
}

// dualMAC is the MAC of dual-QR frame n: HMAC(kmac, "pcc-dual-v2" ‖ H ‖ n)[:10].
func dualMAC(kmac []byte, h hsRow, n int64) []byte {
	m := hmac.New(sha256.New, kmac)
	m.Write([]byte("pcc-dual-v2"))
	m.Write(h.id[:])
	m.Write(be64(n))
	return m.Sum(nil)[:macLen]
}

// Evidence is what the server established about one attempt (the score input).
type Evidence struct {
	Dual     bool // dual-QR mode
	Quiet    bool // jingle off
	CommitOK bool // sha256(kmac) from the claiming device = the initiator's commit (dual: the kmac-keyed frame verified)
	InBound  bool // report_at − play_at within AudioBound, measured by the server (dual: the frame was fresh)
	UltraOK  bool // ultrasonic bits match, within the layer bound
	JingleOK bool // jingle bits match, within the layer bound (never when quiet)
	Sync     bool // both layers arrived together
}

// Score turns evidence into an outcome (the score table of §7), the retry
// hint and the channels that passed. A QR frame was always scanned (the claim
// needed a fresh one).
func Score(e Evidence) (core.Outcome, string, []string) {
	channels := []string{chQR}
	if e.Dual {
		if e.CommitOK && e.InBound {
			return core.OutcomeAccepted, "", append(channels, chDual)
		}
		return core.OutcomeRetry, "", channels
	}
	if !e.CommitOK || !e.InBound {
		return core.OutcomeRetry, "", channels
	}
	jingle := e.JingleOK && !e.Quiet
	if e.UltraOK {
		channels = append(channels, chUltra)
	}
	if jingle {
		channels = append(channels, chJingle)
	}
	if e.UltraOK && jingle && e.Sync {
		channels = append(channels, chSync)
	}
	switch {
	case e.UltraOK:
		return core.OutcomeVerified, "", channels
	case jingle:
		return core.OutcomeAccepted, "", channels
	case e.Quiet:
		return core.OutcomeRetry, HintTryJingle, channels
	}
	return core.OutcomeRetry, HintVolumeUp, channels
}

// attemptReport is the stored result of the latest attempt (handshake_sessions.report).
type attemptReport struct {
	Channels  []string `json:"channels"`
	ElapsedMs int64    `json:"elapsed_ms"` // server-measured, bucketed
	Hint      string   `json:"hint,omitempty"`
}

// bucket rounds a duration up to TimingBucket (records keep bucketed timings only).
func bucket(d time.Duration) int64 {
	if d < 0 {
		d = 0
	}
	b := (d + TimingBucket - 1) / TimingBucket
	return int64(b * TimingBucket / time.Millisecond)
}

func (s *Service) hintOf(h hsRow) string {
	var r attemptReport
	if len(h.report) == 0 || json.Unmarshal(h.report, &r) != nil {
		return ""
	}
	if r.Hint == "" && h.kind == kindUpgrade && h.outcome == core.OutcomeAccepted {
		return HintVolumeUp
	}
	return r.Hint
}

// AudioStartOut answers POST /api/join/audio/start.
type AudioStartOut struct {
	PlayAt  time.Time `json:"play_at"` // the server's time of the play start (the time bound runs from here)
	Attempt int       `json:"attempt"` // 1-based; part of the transcript
}

// AudioStart begins one sound attempt (signed call "audio_start"): the
// claiming device reports kmac (HKDF of its ECDH secret, base64url) and is
// about to play. The server records its own play time. At most MaxAttempts
// attempts per handshake; the next one is the uniform not-found.
func (s *Service) AudioStart(ctx context.Context, sig, kmac string) (AudioStartOut, error) {
	k, err := b64.DecodeString(kmac)
	if err != nil || len(k) != 32 {
		return AudioStartOut{}, gate.ErrNotFound
	}
	var (
		h   hsRow
		out AudioStartOut
	)
	now := s.d.Clock.Now()
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		_, h, err = s.joinCall(ctx, tx, sig, CallAudioStart, true)
		if err != nil {
			return err
		}
		if h.live(now) != stConfirmed || h.mode != modeQRAudio || h.passed() || h.attempts >= MaxAttempts ||
			(h.kmac != nil && !hmac.Equal(h.kmac, k)) {
			return gate.ErrNotFound
		}
		h.attempts++
		out = AudioStartOut{PlayAt: now, Attempt: int(h.attempts)}
		_, err = tx.Exec(ctx, `UPDATE handshake_sessions SET kmac = $2, play_at = $3, report_at = NULL, report = NULL,
			outcome = 0, attempts = $4 WHERE id = $1`, h.id, k, now, h.attempts)
		return err
	})
	if err != nil {
		return AudioStartOut{}, err
	}
	s.liveEvent(h, "playing")
	return out, nil
}

// ReportIn is the initiator device's decode report (POST
// /api/invites/handshakes/{hs}/report). Bits and the commit are base64url;
// client timings are milliseconds and can only tighten the bound.
type ReportIn struct {
	UltraBits    string   `json:"ultra_bits,omitempty"`
	JingleBits   string   `json:"jingle_bits,omitempty"`
	KmacCommit   string   `json:"kmac_commit"`
	TUltraMs     *int     `json:"t_ultra_ms,omitempty"`
	TJingleMs    *int     `json:"t_jingle_ms,omitempty"`
	OffsetMs     *int     `json:"offset_ms,omitempty"`
	MotifLevelDb *float64 `json:"motif_level_db,omitempty"`
}

// ReportOut is the outcome of an attempt.
type ReportOut struct {
	Outcome string `json:"outcome"` // verified | accepted | retry
	Hint    string `json:"hint,omitempty"`
}

func layerOK(got string, want []byte, t *int) bool {
	b, err := b64.DecodeString(got)
	if err != nil || len(b) != len(want) || !hmac.Equal(b, want) {
		return false
	}
	return t == nil || (*t >= 0 && *t <= int(LayerBound/time.Millisecond))
}

// Report scores the pending sound attempt from the initiator's decode report.
// The time bound is the server's own report_at − play_at; a report after it is
// Retry whatever the client timings claim.
func (s *Service) Report(ctx context.Context, ref gate.Ref, in ReportIn) (ReportOut, error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return ReportOut{}, err
	}
	var (
		h   hsRow
		out ReportOut
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		h, err = s.own(ctx, tx, v, ref, true)
		if err != nil {
			return err
		}
		now := s.d.Clock.Now()
		if h.live(now) != stConfirmed || h.mode != modeQRAudio || h.playAt == nil || h.reportAt != nil ||
			h.outcome != core.OutcomeNone || h.kmac == nil {
			return gate.ErrNotFound
		}
		commit, err := b64.DecodeString(in.KmacCommit)
		sum := sha256.Sum256(h.kmac)
		elapsed := now.Sub(*h.playAt)
		ultra, jingle := soundBits(h.kmac, h, h.attempts)
		ev := Evidence{Quiet: h.quiet, CommitOK: err == nil && hmac.Equal(commit, sum[:]), InBound: elapsed <= AudioBound,
			UltraOK: layerOK(in.UltraBits, ultra, in.TUltraMs), JingleOK: !h.quiet && layerOK(in.JingleBits, jingle, in.TJingleMs)}
		ev.Sync = in.OffsetMs != nil && *in.OffsetMs >= -SyncMaxMs && *in.OffsetMs <= SyncMaxMs
		var channels []string
		h.outcome, out.Hint, channels = Score(ev)
		if h.kind == kindUpgrade && h.outcome == core.OutcomeAccepted && out.Hint == "" {
			out.Hint = HintVolumeUp
		}
		h.report, err = json.Marshal(attemptReport{Channels: channels, ElapsedMs: bucket(elapsed), Hint: out.Hint})
		if err != nil {
			return err
		}
		return s.recordOutcome(ctx, tx, &h, now)
	})
	if err != nil {
		return ReportOut{}, err
	}
	o := h.outcome
	if h.kind == kindUpgrade && o == core.OutcomeAccepted {
		o = core.OutcomeRetry
	}
	out.Outcome = outcomeName(o)
	s.liveEvent(h, "reported")
	return out, nil
}

// recordOutcome stores h's latest attempt and, when it passed, starts what
// follows: an upgrade or a connect drafts the Friendship both sides sign; a
// recovery ends (the account module reads Result).
func (s *Service) recordOutcome(ctx context.Context, tx pgx.Tx, h *hsRow, now time.Time) error {
	state, pending := h.state, h.pending
	if h.passed() {
		switch h.kind {
		case kindUpgrade, kindConnect:
			p, err := s.upgradeDraft(ctx, *h, now)
			if err != nil {
				return err
			}
			pending = p
		case kindRecovery:
			state = stDone
		}
	}
	h.state, h.pending = state, pending
	var completed *time.Time
	if state == stDone {
		completed = &now
	}
	_, err := tx.Exec(ctx, `UPDATE handshake_sessions SET report_at = $2, report = $3, outcome = $4, state = $5,
		pending = $6, completed_at = COALESCE($7, completed_at) WHERE id = $1`,
		h.id, now, h.report, int16(h.outcome), state, pending, completed)
	return err
}

// DualIn is the claiming device's scan of the initiator's dual-QR frame (POST
// /api/join/dual/frame), with its kmac (base64url).
type DualIn struct {
	Payload string `json:"payload"`
	Kmac    string `json:"kmac"`
}

// parseDual decodes "pcc2d.<hs>.<n>.<mac>".
func parseDual(p string) (parsedFrame, bool) {
	parts := strings.Split(p, ".")
	if len(p) > 128 || len(parts) != 4 || parts[0] != dualPrefix {
		return parsedFrame{}, false
	}
	var f parsedFrame
	var ok bool
	if f.hs, ok = parseHS(parts[1]); !ok {
		return parsedFrame{}, false
	}
	if f.n, ok = parseIndex(parts[2]); !ok {
		return parsedFrame{}, false
	}
	mac, err := b64.DecodeString(parts[3])
	if err != nil || len(mac) != macLen {
		return parsedFrame{}, false
	}
	f.mac = mac
	return f, true
}

// DualFrame scores one dual-QR attempt (signed call "dual_frame",
// accessibility): the initiator's device shows a second QR keyed by kmac; the
// claiming device scans it within the frame freshness window. The outcome is
// Accepted at most, never Verified, until a later audio handshake.
func (s *Service) DualFrame(ctx context.Context, sig string, in DualIn) (ReportOut, error) {
	k, err := b64.DecodeString(in.Kmac)
	if err != nil || len(k) != 32 {
		return ReportOut{}, gate.ErrNotFound
	}
	var h hsRow
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		_, h, err = s.joinCall(ctx, tx, sig, CallDualFrame, true)
		if err != nil {
			return err
		}
		now := s.d.Clock.Now()
		if h.live(now) != stConfirmed || h.mode != modeDualQR || h.passed() || h.attempts >= MaxAttempts {
			return gate.ErrNotFound
		}
		h.attempts++
		f, ok := parseDual(in.Payload)
		ev := Evidence{Dual: true, CommitOK: ok && f.hs == h.id && hmac.Equal(f.mac, dualMAC(k, h, f.n)),
			InBound: ok && fresh(h.createdAt, now, f.n)}
		var channels []string
		h.outcome, _, channels = Score(ev)
		if h.report, err = json.Marshal(attemptReport{Channels: channels}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE handshake_sessions SET kmac = $2, play_at = $3, attempts = $4 WHERE id = $1",
			h.id, k, now, h.attempts); err != nil {
			return err
		}
		return s.recordOutcome(ctx, tx, &h, now)
	})
	if err != nil {
		return ReportOut{}, err
	}
	s.liveEvent(h, "reported")
	return ReportOut{Outcome: outcomeName(h.outcome)}, nil
}
