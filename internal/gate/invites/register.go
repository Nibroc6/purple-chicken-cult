package invites

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Registration (the invitee side after a passed handshake, plan §4.7):
//
//  1. register/begin {form, signup_token}: the token (issued once the outcome
//     passed, bound to E_v) and the form are checked; the passkey creation
//     ceremony starts.
//  2. register/finish {credential}: the new credential is verified (not yet
//     bound to anyone), the account's ext_id is reserved and the Invite and
//     Friendship records are drafted (Verified ⇒ friend+, Accepted ⇒ friend,
//     fidelity #20); the signup ceremony (fidelity #21, core.SignupCosign)
//     returns get options over those drafts. The inviter is asked to co-sign
//     the Invite (purpose invite_inviter) from now on.
//  3. register/cosign {assertion}: the RegisterFinish transaction. It needs the
//     inviter's assertion (else Problem awaiting_inviter: the device waits and
//     sends the same assertion again) and verifies the invitee's get assertion
//     with the new credential inside the transaction; any failure aborts it
//     and nothing is created.
//
// Every call is signed by the claiming device's key (claim.go).

// Registration is the join form (POST /api/join/register/begin).
type Registration struct {
	Handle      string   `json:"handle"`
	DisplayName string   `json:"display_name"`
	Locale      string   `json:"locale"`
	Languages   []string `json:"languages"`
	TOSVersion  int      `json:"tos_version"`
	AgeAttested bool     `json:"age_attested"`
	SignupToken string   `json:"signup_token"`
}

// pending is what the registration keeps on the handshake between its calls
// (an upgrade keeps only its Friendship draft).
type pending struct {
	Handle      string   `json:"handle,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Locale      string   `json:"locale,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	WebAuthnID  []byte   `json:"webauthn_user_id,omitempty"`
	TOSVersion  int      `json:"tos_version,omitempty"`
	// Drafted at register/finish (invites) or at the outcome (upgrades).
	ExtID      *uuid.UUID       `json:"ext_id,omitempty"`
	Credential *core.Credential `json:"credential,omitempty"`
	Invite     *inviteBody      `json:"invite,omitempty"`
	Friendship *friendshipBody  `json:"friendship,omitempty"`
}

func readPending(raw []byte) (pending, bool) {
	var p pending
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return pending{}, false
	}
	return p, true
}

// hasDrafts reports whether the records to co-sign are drafted.
func hasDrafts(raw []byte) bool {
	p, ok := readPending(raw)
	return ok && p.Friendship != nil
}

// signupToken is the single-use signup token of a passed invite handshake,
// bound to E_v: HMAC(secret, "pcc-signup-v1" ‖ E_v)[:16] (base64url).
func signupToken(secret, ev []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("pcc-signup-v1"))
	m.Write(ev)
	return b64.EncodeToString(m.Sum(nil)[:16])
}

const maxLanguages = 10

var langRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)

// cleanLangs keeps the well-formed, distinct language tags (at most maxLanguages).
func cleanLangs(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range in {
		if langRE.MatchString(l) && !seen[l] && len(out) < maxLanguages {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// registering authenticates a registration call: a guest session's signed
// call on its invite handshake, confirmed and passed.
func (s *Service) registering(ctx context.Context, sig, call string) (core.Viewer, hsRow, error) {
	v, h, err := s.joinCall(ctx, s.d.DB.Q(), sig, call, false)
	if err != nil {
		return core.Viewer{}, hsRow{}, err
	}
	if v.Kind != core.SessGuest || h.kind != kindInvite || h.live(s.d.Clock.Now()) != stConfirmed || !h.passed() {
		return core.Viewer{}, hsRow{}, gate.ErrNotFound
	}
	return v, h, nil
}

// RegisterBegin validates the join form and the signup token and starts the
// passkey ceremony in the guest session. Before the outcome passed, with a
// wrong token, and once HandleTries handle_taken answers were given on this
// handshake: the uniform not-found (D21). A new begin discards drafts and
// signatures of an earlier attempt.
func (s *Service) RegisterBegin(ctx context.Context, sig string, r Registration) (json.RawMessage, error) {
	v, h, err := s.registering(ctx, sig, CallRegisterBegin)
	if err != nil {
		return nil, err
	}
	if h.tries >= HandleTries ||
		!hmac.Equal([]byte(r.SignupToken), []byte(signupToken(h.secret, h.ev))) {
		return nil, gate.ErrNotFound
	}
	if r.TOSVersion != s.d.Cfg.TOSVersion {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "tos_required", "tos_version")
	}
	if !r.AgeAttested {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "age_required", "age_attested")
	}
	canon, ok := s.d.Handles.CanonHandle(r.Handle)
	if !ok {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "handle_invalid", "handle")
	}
	name, ok := s.d.DisplayNames.CleanDisplayName(r.DisplayName)
	if !ok {
		return nil, gate.NewProblem(gate.StatusUnprocessable, "display_name_invalid", "display_name")
	}
	if !s.d.Users.HandleAvailable(ctx, canon) {
		tag, err := s.d.DB.Q().Exec(ctx, `UPDATE handshake_sessions SET handle_tries = handle_tries + 1
			WHERE id = $1 AND handle_tries < $2`, h.id, int16(HandleTries))
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, gate.ErrNotFound
		}
		return nil, gate.NewProblem(gate.StatusConflict, "handle_taken", "handle")
	}
	p := pending{Handle: canon, DisplayName: name, Locale: "en", Languages: cleanLangs(r.Languages),
		WebAuthnID: make([]byte, 32), TOSVersion: r.TOSVersion}
	if langRE.MatchString(r.Locale) {
		p.Locale = r.Locale
	}
	if len(p.Languages) == 0 {
		p.Languages = []string{p.Locale}
	}
	if _, err := rand.Read(p.WebAuthnID); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE handshake_sessions SET pending = $2, assertions = NULL
		WHERE id = $1 AND state = $3`, h.id, raw, stConfirmed)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, gate.ErrNotFound
	}
	return s.d.Passkeys.BeginRegistration(ctx, v.Session, core.PendingUser{WebAuthnID: p.WebAuthnID, Handle: p.Handle,
		DisplayName: p.DisplayName})
}

// RegisterFinishOut answers register/finish: the signup ceremony's get
// options and the drafts it signs (a JSON array: Invite, Friendship).
type RegisterFinishOut struct {
	Options json.RawMessage `json:"options"`
	Records json.RawMessage `json:"records"`
}

// handshakeInfo is the "handshake" field of the Invite and Friendship records
// and of the invites row (§7: which channels passed and the server-measured
// timing, bucketed; nothing else).
type handshakeInfo struct {
	Mode           string   `json:"mode"`
	ChannelsPassed []string `json:"channels_passed"`
	Timings        *timings `json:"timings,omitempty"`
}

type timings struct {
	ReportMs int64 `json:"report_ms"`
}

// infoOf is the handshake field of h's passed attempt.
func infoOf(h hsRow) handshakeInfo {
	info := handshakeInfo{Mode: modeName(h.mode), ChannelsPassed: []string{chQR}}
	var r attemptReport
	if len(h.report) > 0 && json.Unmarshal(h.report, &r) == nil && len(r.Channels) > 0 {
		info.ChannelsPassed = r.Channels
		if h.mode == modeQRAudio {
			info.Timings = &timings{ReportMs: r.ElapsedMs}
		}
	}
	return info
}

// levelOf is the friendship level a passed handshake creates (fidelity #20).
func levelOf(o core.Outcome) (core.EdgeLevel, string) {
	if o == core.OutcomeVerified {
		return core.LevelFriendPlus, "friend+"
	}
	return core.LevelFriend, "friend"
}

// inviteBody is the signed Invite record body.
type inviteBody struct {
	Inviter           uuid.UUID     `json:"inviter"`
	Invitee           uuid.UUID     `json:"invitee"`
	IssuedAt          sigs.Time     `json:"issued_at"`
	Handshake         handshakeInfo `json:"handshake"`
	InviteeCredential sigs.Bytes    `json:"invitee_credential"`
}

// friendshipBody is the signed Friendship record body (a < b), as written by
// gate/friends plus the handshake that created it.
type friendshipBody struct {
	A         uuid.UUID      `json:"a"`
	B         uuid.UUID      `json:"b"`
	Level     string         `json:"level"`
	CreatedAt sigs.Time      `json:"created_at"`
	Handshake *handshakeInfo `json:"handshake,omitempty"`
}

func ordered(x, y uuid.UUID) (uuid.UUID, uuid.UUID) {
	if bytes.Compare(x[:], y[:]) > 0 {
		return y, x
	}
	return x, y
}

// records are the records the invitee's signup ceremony signs, in order.
func (p pending) records() []core.CosignRecord {
	return []core.CosignRecord{{Kind: core.RecInvite, Body: *p.Invite}, {Kind: core.RecFriendship, Body: *p.Friendship}}
}

// RegisterFinish verifies the new passkey, drafts the records and starts the
// signup ceremony (see above).
func (s *Service) RegisterFinish(ctx context.Context, sig string, body []byte) (RegisterFinishOut, error) {
	v, h, err := s.registering(ctx, sig, CallRegisterFinish)
	if err != nil {
		return RegisterFinishOut{}, err
	}
	p, ok := readPending(h.pending)
	if !ok || p.Handle == "" || p.Credential != nil {
		return RegisterFinishOut{}, gate.ErrNotFound
	}
	cred, err := s.d.Passkeys.FinishRegistration(ctx, v.Session, body)
	if err != nil {
		return RegisterFinishOut{}, err
	}
	inviter, err := s.d.Users.Get(ctx, h.initiator)
	if err != nil {
		return RegisterFinishOut{}, err
	}
	ext, err := uuid.NewV7()
	if err != nil {
		return RegisterFinishOut{}, err
	}
	now := s.d.Clock.Now()
	info := infoOf(h)
	_, level := levelOf(h.outcome)
	a, b := ordered(inviter.ExtID, ext)
	p.ExtID, p.Credential = &ext, &cred
	p.Invite = &inviteBody{Inviter: inviter.ExtID, Invitee: ext, IssuedAt: sigs.T(h.createdAt), Handshake: info,
		InviteeCredential: sigs.Bytes(cred.ID)}
	p.Friendship = &friendshipBody{A: a, B: b, Level: level, CreatedAt: sigs.T(now), Handshake: &info}
	in, err := s.d.Signup.BeginSignup(ctx, v.Session, cred, p.records())
	if err != nil {
		return RegisterFinishOut{}, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return RegisterFinishOut{}, err
	}
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE handshake_sessions SET pending = $2, assertions = NULL
		WHERE id = $1 AND state = $3`, h.id, raw, stConfirmed)
	if err != nil {
		return RegisterFinishOut{}, err
	}
	if tag.RowsAffected() != 1 {
		return RegisterFinishOut{}, gate.ErrNotFound
	}
	s.liveEvent(h, "signing")
	return RegisterFinishOut{Options: in.Options, Records: in.Record}, nil
}

// RegisterCosign completes the join with the invitee's get assertion over the
// drafts and returns the new user session's token and the account's view.
func (s *Service) RegisterCosign(ctx context.Context, sig string, assertion []byte) (string, gate.MeView, error) {
	v, _, err := s.registering(ctx, sig, CallRegisterCosign)
	if err != nil {
		return "", gate.MeView{}, err
	}
	tok, uid, err := s.redeem(ctx, v.Session, assertion)
	if err != nil {
		return "", gate.MeView{}, err
	}
	me, err := s.me(ctx, uid)
	if err != nil {
		return "", gate.MeView{}, err
	}
	return tok, me, nil
}

// redeem is the RegisterFinish transaction (plan §4.7, serializable): re-check
// the inviter ⇒ create the user (reserved ext_id) ⇒ bind the credential ⇒
// verify the signup ceremony ⇒ Invite and Friendship records with the
// inviter's and the invitee's assertions ⇒ invites row ⇒ edge at the
// outcome's level ⇒ spend one token ⇒ handshake done ⇒ upgrade the guest
// session. Graph memory, the bus, the metric and the live event follow the
// commit. Exactly one redemption of a handshake can succeed; any failure,
// a failed assertion included, rolls everything back.
func (s *Service) redeem(ctx context.Context, sess [32]byte, assertion []byte) (string, core.UserID, error) {
	var (
		tok   string
		uid   core.UserID
		now   time.Time
		level core.EdgeLevel
		h     hsRow
	)
	// The inviter checks that read through the pool run first: inside the
	// transaction only tx is used, so concurrent redemptions waiting on the
	// handshake lock can never starve the winner of a connection.
	pre, err := s.claimedBy(ctx, s.d.DB.Q(), sess, false)
	if err != nil {
		return "", 0, err
	}
	inviter := pre.initiator
	m, err := s.d.Users.Get(ctx, inviter)
	if err != nil {
		return "", 0, err
	}
	if edges, outgoing, err := s.d.Edges.Count(ctx, inviter); err != nil {
		return "", 0, err
	} else if edges+outgoing >= FriendCap {
		return "", 0, gate.ErrNotFound
	}
	err = s.d.DB.TxSerializable(ctx, func(tx pgx.Tx) error {
		now = s.d.Clock.Now()
		var err error
		h, err = s.claimedBy(ctx, tx, sess, true)
		if err != nil {
			return err
		}
		p, ok := readPending(h.pending)
		if h.kind != kindInvite || h.live(now) != stConfirmed || !h.passed() || h.initiator != inviter || !ok ||
			p.Credential == nil || p.ExtID == nil || p.Invite == nil || p.Friendship == nil {
			return gate.ErrNotFound
		}
		var sg signatures
		if len(h.assertions) > 0 {
			if err := json.Unmarshal(h.assertions, &sg); err != nil {
				return gate.ErrNotFound
			}
		}
		if len(sg.Initiator) == 0 {
			return gate.NewProblem(gate.StatusConflict, "awaiting_inviter")
		}
		if m.Status != core.StatusActive || m.OnProbation(now) {
			return gate.ErrNotFound
		}
		b, err := s.loadBudget(ctx, tx, m, now, true)
		if err != nil {
			return err
		}
		if b.available() < 1 {
			return gate.ErrNotFound
		}
		uid, err = s.d.Users.Create(ctx, tx, core.NewUser{Handle: p.Handle, DisplayName: p.DisplayName, Locale: p.Locale,
			Languages: p.Languages, InvitedBy: inviter, ProbationUntil: probationUntil(now), TOSVersion: p.TOSVersion,
			AgeAttested: true, WebAuthnID: p.WebAuthnID, Status: core.StatusActive, ExtID: *p.ExtID})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE users SET probation_rule = $2 WHERE id = $1", int64(uid), RuleP1); err != nil {
			return err
		}
		if err := s.d.Passkeys.BindCredential(ctx, tx, uid, *p.Credential); err != nil {
			return err
		}
		mine, err := s.d.Signup.FinishSignup(ctx, tx, sess, uid, *p.Credential, p.records(), assertion)
		if err != nil {
			return err
		}
		if len(mine) != 2 {
			return gate.ErrNotFound
		}
		parties := []core.UserID{inviter, uid}
		invHash, err := s.d.Signer.Sign(ctx, tx, core.RecInvite, inviter, *p.Invite, parties, sg.Initiator[0], mine[0])
		if err != nil {
			return err
		}
		frHash, err := s.d.Signer.Sign(ctx, tx, core.RecFriendship, inviter, *p.Friendship, parties, mine[1])
		if err != nil {
			return err
		}
		hsJSON, err := json.Marshal(p.Invite.Handshake)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO invites (id, inviter_id, invitee_id, created_at, handshake, invite_record,
			friendship_record) VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.d.IDs.New(), int64(inviter), int64(uid), now, hsJSON,
			invHash, frHash); err != nil {
			return err
		}
		level, _ = levelOf(h.outcome)
		if err := s.d.Edges.Create(ctx, tx, inviter, uid, level, frHash); err != nil {
			return err
		}
		if err := s.spend(ctx, tx, inviter, b, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE handshake_sessions SET state = $2, completed_at = $3, pending = NULL,
			assertions = NULL WHERE id = $1`, h.id, stDone, now); err != nil {
			return err
		}
		if tok, err = s.d.Passkeys.Upgrade(ctx, tx, sess, uid, p.Credential.ID); err != nil {
			return err
		}
		return s.d.Notify.Notify(ctx, tx, core.Event{To: inviter, Kind: notifyRedeemed, SubjectUser: uid})
	})
	if err != nil {
		return "", 0, err
	}
	s.d.Graph.AddEdge(inviter, uid, level, now)
	s.d.Bus.Publish(core.EdgeAdded{A: inviter, B: uid, Level: level, At: now})
	s.d.Metrics.Inc(metricRedeemed, "", 1)
	s.liveEvent(h, "done")
	return tok, uid, nil
}

// me is the new account's own view.
func (s *Service) me(ctx context.Context, id core.UserID) (gate.MeView, error) {
	m, err := s.d.Users.Get(ctx, id)
	if err != nil {
		return gate.MeView{}, err
	}
	label, err := s.d.Users.InviterLabel(ctx, id)
	if err != nil {
		return gate.MeView{}, err
	}
	return gate.MeView{
		Ref: s.d.Refs.User(id, id), Handle: m.Handle, DisplayName: m.DisplayName, Role: m.Role.String(), Inviter: label,
		Settings: gate.SettingsView{UILocale: m.Locale, Languages: m.Languages, RankPreset: m.Preset.String(),
			NameInChains: m.NameInChains, Suggestible: m.Suggestible, NamedInLikes: m.NamedInLikes,
			SlipperyDefault: m.SlipperyDefault, UIPrefs: []byte("{}")},
	}, nil
}
