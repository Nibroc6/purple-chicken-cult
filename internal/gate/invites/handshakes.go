package invites

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// core.Handshakes: in-person handshakes that are not invites (kind 1 friend+
// upgrade, kind 2 recovery claim), on the same protocol as invites (SAS
// confirmation included). The initiator drives them through
// /api/invites/handshakes/{hs}/*, the other device through /api/join/*.

// OpenUpgrade opens a friend → friend+ upgrade handshake by v with a plain
// friend (callers: friends, POST /api/friends/{ref}/upgrade). Opening again
// cancels v's unfinished upgrade with the same friend. The friend is offered
// the scanner: a friend_upgrade notification (a push when no app is open) and
// a live friend_upgrade event (a toast in an open app). Quiet turns the jingle
// off as for invites: the ultrasonic layer still plays, so it can verify.
func (s *Service) OpenUpgrade(ctx context.Context, v core.Viewer, peer core.UserID, quiet bool) (gate.HandshakeView, error) {
	if v.Kind != core.SessUser || v.ID == 0 || peer == 0 || peer == v.ID {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if lvl, ok := s.d.Graph.Level(v.ID, peer); !ok || lvl != core.LevelFriend {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if !s.active(ctx, v.ID) || !s.active(ctx, peer) {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	hv, err := s.openKind(ctx, v.ID, peer, kindUpgrade, modeQRAudio, quiet, s.d.Clock.Now())
	if err != nil {
		return gate.HandshakeView{}, err
	}
	if s.d.Notify != nil {
		if err := s.d.Notify.Notify(ctx, nil, core.Event{To: peer, Kind: notifyFriendUpgrade, SubjectUser: v.ID}); err != nil {
			return gate.HandshakeView{}, err
		}
		if m, err := s.d.Users.Get(ctx, v.ID); err == nil {
			s.d.Notify.Live(peer, topicFriendUpgrade, map[string]any{"from": s.d.Refs.User(peer, v.ID), "handle": m.Handle})
		}
	}
	return hv, nil
}

// OpenRecovery opens a recovery handshake by guardian (a friend+ of account)
// for the account's recovering device (caller: account, P1-W10).
func (s *Service) OpenRecovery(ctx context.Context, guardian, account core.UserID) (gate.HandshakeView, error) {
	if guardian == 0 || account == 0 || guardian == account {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if lvl, ok := s.d.Graph.Level(guardian, account); !ok || lvl != core.LevelFriendPlus {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	if !s.active(ctx, guardian) {
		return gate.HandshakeView{}, gate.ErrNotFound
	}
	return s.openKind(ctx, guardian, account, kindRecovery, modeQRAudio, false, s.d.Clock.Now())
}

// ClaimRecovery claims a recovery handshake from the recovering device's guest
// session sess. payload is the JSON claim ({payload, e_v, sig_pub, proof},
// ClaimIn); it returns the account being recovered. The device then continues
// like an invitee (SAS, sound) through /api/join/*; the account module reads
// the outcome with Result.
func (s *Service) ClaimRecovery(ctx context.Context, sess [32]byte, payload string) (core.UserID, error) {
	var in ClaimIn
	if sess == ([32]byte{}) || json.Unmarshal([]byte(payload), &in) != nil {
		return 0, gate.ErrNotFound
	}
	h, err := s.claim(ctx, core.Viewer{Kind: core.SessGuest, Session: sess}, in, true)
	if err != nil {
		return 0, err
	}
	return h.peer, nil
}

// Result returns the outcome of handshake hs (any kind).
func (s *Service) Result(ctx context.Context, hs uuid.UUID) (core.Outcome, error) {
	h, err := scanHS(s.d.DB.Q().QueryRow(ctx, selectHS+"WHERE id = $1", hs))
	if err != nil {
		return core.OutcomeNone, err
	}
	if h.kind == kindUpgrade && h.outcome == core.OutcomeAccepted {
		return core.OutcomeRetry, nil
	}
	return h.outcome, nil
}

// active reports whether u is an active account.
func (s *Service) active(ctx context.Context, u core.UserID) bool {
	m, err := s.d.Users.Get(ctx, u)
	return err == nil && m.Status == core.StatusActive
}
