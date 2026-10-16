package invites

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

// Registration is the join form (POST /api/join/register/begin).
type Registration struct {
	Handle      string   `json:"handle"`
	DisplayName string   `json:"display_name"`
	Locale      string   `json:"locale"`
	Languages   []string `json:"languages"`
	TOSVersion  int      `json:"tos_version"`
	AgeAttested bool     `json:"age_attested"`
}

// pending is what RegisterBegin keeps on the handshake for RegisterFinish.
type pending struct {
	Handle      string   `json:"handle"`
	DisplayName string   `json:"display_name"`
	Locale      string   `json:"locale"`
	Languages   []string `json:"languages"`
	WebAuthnID  []byte   `json:"webauthn_user_id"`
	TOSVersion  int      `json:"tos_version"`
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

// RegisterBegin validates the join form and starts the passkey ceremony in the
// guest session. It needs the session's claimed handshake to be confirmed (the
// inviter tapped "codes match"); otherwise, and once HandleTries handle_taken
// answers were given on this handshake, the uniform not-found (D21).
func (s *Service) RegisterBegin(ctx context.Context, r Registration) (json.RawMessage, error) {
	v, err := guest(ctx)
	if err != nil {
		return nil, err
	}
	now := s.d.Clock.Now()
	h, err := s.claimedBy(ctx, s.d.DB.Q(), v.Session, false)
	if err != nil {
		return nil, err
	}
	if h.live(now) != stConfirmed || h.tries >= HandleTries {
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
	tag, err := s.d.DB.Q().Exec(ctx, "UPDATE handshake_sessions SET pending = $2 WHERE id = $1 AND state = $3",
		h.id, raw, stConfirmed)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, gate.ErrNotFound
	}
	return s.d.Passkeys.BeginRegistration(ctx, v.Session, core.PendingUser{WebAuthnID: p.WebAuthnID, Handle: p.Handle,
		DisplayName: p.DisplayName})
}

// RegisterFinish verifies the passkey and redeems the invite: it creates the
// account, its Invite and Friendship records and the inviter edge, and turns
// the guest session into a user session (returned as a new token).
func (s *Service) RegisterFinish(ctx context.Context, body []byte) (string, gate.MeView, error) {
	v, err := guest(ctx)
	if err != nil {
		return "", gate.MeView{}, err
	}
	h, err := s.claimedBy(ctx, s.d.DB.Q(), v.Session, false)
	if err != nil {
		return "", gate.MeView{}, err
	}
	if h.live(s.d.Clock.Now()) != stConfirmed || h.pending == nil {
		return "", gate.MeView{}, gate.ErrNotFound
	}
	cred, err := s.d.Passkeys.FinishRegistration(ctx, v.Session, body)
	if err != nil {
		return "", gate.MeView{}, err
	}
	tok, uid, err := s.redeem(ctx, v.Session, cred)
	if err != nil {
		return "", gate.MeView{}, err
	}
	me, err := s.me(ctx, uid)
	if err != nil {
		return "", gate.MeView{}, err
	}
	return tok, me, nil
}

// handshakeInfo is the "handshake" field of the Invite and Friendship records
// and of the invites row (§7: which channels passed; P0 has only the QR).
type handshakeInfo struct {
	Mode           string   `json:"mode"`
	ChannelsPassed []string `json:"channels_passed"`
}

var p0Handshake = handshakeInfo{Mode: "qr", ChannelsPassed: []string{"qr"}}

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

// redeem is the RegisterFinish transaction (plan §4.7, serializable): re-check
// the inviter ⇒ create the user ⇒ bind the credential ⇒ Invite and Friendship
// records ⇒ invites row ⇒ edge ⇒ spend one token ⇒ handshake done ⇒ upgrade
// the guest session. Graph memory, the bus, the metric and the live event
// follow the commit. Exactly one redemption of a handshake can succeed.
func (s *Service) redeem(ctx context.Context, sess [32]byte, cred core.Credential) (string, core.UserID, error) {
	var (
		tok     string
		uid     core.UserID
		inviter core.UserID
		now     time.Time
	)
	// The inviter checks that read through the pool run first: inside the
	// transaction only tx is used, so concurrent redemptions waiting on the
	// handshake lock can never starve the winner of a connection.
	pre, err := s.claimedBy(ctx, s.d.DB.Q(), sess, false)
	if err != nil {
		return "", 0, err
	}
	inviter = pre.inviter
	m, err := s.d.Users.Get(ctx, inviter)
	if err != nil {
		return "", 0, err
	}
	if edges, outgoing, err := s.d.Edges.Count(ctx, inviter); err != nil {
		return "", 0, err
	} else if edges+outgoing >= FriendCap {
		return "", 0, gate.ErrNotFound
	}
	var hsID uuid.UUID
	err = s.d.DB.TxSerializable(ctx, func(tx pgx.Tx) error {
		now = s.d.Clock.Now()
		h, err := s.claimedBy(ctx, tx, sess, true)
		if err != nil {
			return err
		}
		if h.live(now) != stConfirmed || h.pending == nil || h.inviter != inviter {
			return gate.ErrNotFound
		}
		hsID = h.id
		var p pending
		if err := json.Unmarshal(h.pending, &p); err != nil {
			return gate.ErrNotFound
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
			AgeAttested: true, WebAuthnID: p.WebAuthnID, Status: core.StatusActive})
		if err != nil {
			return err
		}
		_, ext, _, err := s.d.Accounts.Account(ctx, tx, uid, false)
		if err != nil {
			return err
		}
		if err := s.d.Passkeys.BindCredential(ctx, tx, uid, cred); err != nil {
			return err
		}
		parties := []core.UserID{inviter, uid}
		invHash, err := s.d.Signer.Sign(ctx, tx, core.RecInvite, inviter, inviteBody{Inviter: m.ExtID, Invitee: ext,
			IssuedAt: sigs.T(h.createdAt), Handshake: p0Handshake, InviteeCredential: sigs.Bytes(cred.ID)}, parties)
		if err != nil {
			return err
		}
		a, bb := m.ExtID, ext
		if bytes.Compare(a[:], bb[:]) > 0 {
			a, bb = bb, a
		}
		hsInfo := p0Handshake
		frHash, err := s.d.Signer.Sign(ctx, tx, core.RecFriendship, inviter, friendshipBody{A: a, B: bb, Level: "friend",
			CreatedAt: sigs.T(now), Handshake: &hsInfo}, parties)
		if err != nil {
			return err
		}
		hsJSON, err := json.Marshal(p0Handshake)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO invites (id, inviter_id, invitee_id, created_at, handshake, invite_record,
			friendship_record) VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.d.IDs.New(), int64(inviter), int64(uid), now, hsJSON,
			invHash, frHash); err != nil {
			return err
		}
		if err := s.d.Edges.Create(ctx, tx, inviter, uid, core.LevelFriend, frHash); err != nil {
			return err
		}
		if err := s.spend(ctx, tx, inviter, b, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE handshake_sessions SET state = $2, completed_at = $3, pending = NULL
			WHERE id = $1`, h.id, stDone, now); err != nil {
			return err
		}
		if tok, err = s.d.Passkeys.Upgrade(ctx, tx, sess, uid, cred.ID); err != nil {
			return err
		}
		return s.d.Notify.Notify(ctx, tx, core.Event{To: inviter, Kind: notifyRedeemed, SubjectUser: uid})
	})
	if err != nil {
		return "", 0, err
	}
	s.d.Graph.AddEdge(inviter, uid, core.LevelFriend, now)
	s.d.Bus.Publish(core.EdgeAdded{A: inviter, B: uid, Level: core.LevelFriend, At: now})
	s.d.Metrics.Inc(metricRedeemed, "", 1)
	s.d.Notify.Live(inviter, liveTopic, map[string]any{"hs": s.d.Refs.Handshake(inviter, hsID), "state": "done"})
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
