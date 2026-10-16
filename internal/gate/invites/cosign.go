package invites

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Co-signing by the signed-in side(s) of a handshake (core.Cosign, plan
// P1-W01 hand-off p1-w01-2). The other device of an invite co-signs in the
// signup ceremony (register.go).
//
//   - invite_inviter: the inviter signs the drafted Invite record once the
//     invitee's register/finish fixed it; the verified assertion waits on the
//     handshake and is attached when the RegisterFinish transaction writes
//     the record.
//   - friend_upgrade: after a Verified upgrade handshake both friends sign the
//     friend+ Friendship record; the second signature writes it and promotes
//     the edge (Edges.Upgrade).
//
// The client opens the intent with POST /api/cosign {purpose, target: hs} and
// sends {intent, assertion} to POST /api/invites/handshakes/{hs}/cosign.

// Co-sign purposes registered by this module.
const (
	PurposeInviteInviter = "invite_inviter"
	PurposeFriendUpgrade = "friend_upgrade"
)

// signatures are the assertions verified so far (handshake_sessions.assertions).
type signatures struct {
	Initiator []core.Assertion `json:"initiator,omitempty"`
	Peer      []core.Assertion `json:"peer,omitempty"`
}

// registerPurposes registers the draft builders with gate/cosign.
func (s *Service) registerPurposes() {
	if s.d.CosignPurposes == nil {
		return
	}
	s.d.CosignPurposes.RegisterPurpose(PurposeInviteInviter, core.RecInvite,
		func(ctx context.Context, v core.Viewer, target gate.Ref) (any, error) {
			_, p, err := s.drafted(ctx, s.d.DB.Q(), v, target, false)
			if err != nil || p.Invite == nil {
				return nil, gate.ErrNotFound
			}
			return *p.Invite, nil
		})
	s.d.CosignPurposes.RegisterPurpose(PurposeFriendUpgrade, core.RecFriendship,
		func(ctx context.Context, v core.Viewer, target gate.Ref) (any, error) {
			h, p, err := s.drafted(ctx, s.d.DB.Q(), v, target, false)
			if err != nil || h.kind != kindUpgrade {
				return nil, gate.ErrNotFound
			}
			return *p.Friendship, nil
		})
}

// party loads the handshake ref names for v when v is a party that co-signs
// it: the initiator of an invite or an upgrade, or the friend of an upgrade
// (the peer's session claimed it).
func (s *Service) party(ctx context.Context, q store.Q, v core.Viewer, ref gate.Ref, lock bool) (hsRow, bool, error) {
	if v.Kind != core.SessUser || v.ID == 0 {
		return hsRow{}, false, gate.ErrNotFound
	}
	id, ok := s.d.Refs.OpenHandshake(v.ID, ref)
	if !ok {
		return hsRow{}, false, gate.ErrNotFound
	}
	sql := selectHS + "WHERE id = $1 AND kind IN (0, 1)"
	if lock {
		sql = selectHS + "WHERE id = $1 AND kind IN (0, 1) FOR UPDATE"
	}
	h, err := scanHS(q.QueryRow(ctx, sql, id))
	if err != nil {
		return hsRow{}, false, err
	}
	switch {
	case h.initiator == v.ID:
		return h, false, nil
	case h.kind == kindUpgrade && h.peer == v.ID && string(h.claimed) == string(v.Session[:]):
		return h, true, nil
	}
	return hsRow{}, false, gate.ErrNotFound
}

// drafted loads a handshake of v whose records are drafted and still to sign.
func (s *Service) drafted(ctx context.Context, q store.Q, v core.Viewer, ref gate.Ref, lock bool) (hsRow, pending, error) {
	h, _, err := s.party(ctx, q, v, ref, lock)
	if err != nil {
		return hsRow{}, pending{}, err
	}
	p, ok := readPending(h.pending)
	if h.live(s.d.Clock.Now()) != stConfirmed || !h.passed() || !ok || p.Friendship == nil {
		return hsRow{}, pending{}, gate.ErrNotFound
	}
	return h, p, nil
}

// CosignIn is one side's co-signature (POST /api/invites/handshakes/{hs}/cosign).
type CosignIn struct {
	Intent    gate.Ref        `json:"intent"`
	Assertion json.RawMessage `json:"assertion"`
}

// Cosign verifies the viewer's co-signature of the handshake's drafted record
// (the Invite for an inviter, the friend+ Friendship for either friend of an
// upgrade) in a transaction that consumes the intent. The second signature of
// an upgrade writes the record and promotes the edge.
func (s *Service) Cosign(ctx context.Context, ref gate.Ref, in CosignIn) error {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return err
	}
	var (
		h    hsRow
		done bool
		now  time.Time
	)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var (
			p   pending
			err error
		)
		h, p, err = s.drafted(ctx, tx, v, ref, true)
		if err != nil {
			return err
		}
		peer := h.initiator != v.ID
		var sg signatures
		if len(h.assertions) > 0 {
			if err := json.Unmarshal(h.assertions, &sg); err != nil {
				return gate.ErrNotFound
			}
		}
		var a core.Assertion
		switch h.kind {
		case kindInvite:
			if len(sg.Initiator) > 0 || p.Invite == nil {
				return gate.ErrNotFound
			}
			if a, _, err = s.d.Cosign.Require(ctx, tx, v, PurposeInviteInviter, core.RecInvite, *p.Invite, in.Intent,
				in.Assertion); err != nil {
				return err
			}
			sg.Initiator = []core.Assertion{a}
		default:
			mine := &sg.Initiator
			if peer {
				mine = &sg.Peer
			}
			if len(*mine) > 0 {
				return gate.ErrNotFound
			}
			if a, _, err = s.d.Cosign.Require(ctx, tx, v, PurposeFriendUpgrade, core.RecFriendship, *p.Friendship,
				in.Intent, in.Assertion); err != nil {
				return err
			}
			*mine = []core.Assertion{a}
		}
		raw, err := json.Marshal(sg)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE handshake_sessions SET assertions = $2 WHERE id = $1", h.id, raw); err != nil {
			return err
		}
		if h.kind == kindUpgrade && len(sg.Initiator) > 0 && len(sg.Peer) > 0 {
			now = s.d.Clock.Now()
			done = true
			return s.completeUpgrade(ctx, tx, h, p, sg, now)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if done {
		s.upgraded(h.initiator, h.peer, now)
		s.liveEvent(h, "done")
		return nil
	}
	s.liveEvent(h, "signed")
	return nil
}

// upgradeDraft drafts the friend+ Friendship of a Verified upgrade handshake.
func (s *Service) upgradeDraft(ctx context.Context, h hsRow, now time.Time) ([]byte, error) {
	ma, err := s.d.Users.Get(ctx, h.initiator)
	if err != nil {
		return nil, err
	}
	mb, err := s.d.Users.Get(ctx, h.peer)
	if err != nil {
		return nil, err
	}
	a, b := ordered(ma.ExtID, mb.ExtID)
	info := infoOf(h)
	return json.Marshal(pending{Friendship: &friendshipBody{A: a, B: b, Level: "friend+", CreatedAt: sigs.T(now),
		Handshake: &info}})
}

// completeUpgrade writes the co-signed friend+ Friendship and promotes the edge.
func (s *Service) completeUpgrade(ctx context.Context, tx pgx.Tx, h hsRow, p pending, sg signatures, now time.Time) error {
	if lvl, ok := s.d.Graph.Level(h.initiator, h.peer); !ok || lvl != core.LevelFriend {
		return gate.ErrNotFound // unfriended (or already friend+) meanwhile
	}
	hash, err := s.d.Signer.Sign(ctx, tx, core.RecFriendship, h.initiator, *p.Friendship,
		[]core.UserID{h.initiator, h.peer}, sg.Initiator[0], sg.Peer[0])
	if err != nil {
		return err
	}
	if err := s.d.Edges.Upgrade(ctx, tx, h.initiator, h.peer, hash); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE handshake_sessions SET state = $2, completed_at = $3, pending = NULL, assertions = NULL
		WHERE id = $1`, h.id, stDone, now)
	return err
}

// upgraded updates graph memory after a committed upgrade (keeping the edge's
// age) and publishes EdgeUpgraded.
func (s *Service) upgraded(a, b core.UserID, now time.Time) {
	since := now
	for _, e := range s.d.Graph.Friends(a) {
		if e.To == b {
			since = e.Since
		}
	}
	s.d.Graph.AddEdge(a, b, core.LevelFriendPlus, since)
	s.d.Bus.Publish(core.EdgeUpgraded{A: a, B: b, At: now})
}
