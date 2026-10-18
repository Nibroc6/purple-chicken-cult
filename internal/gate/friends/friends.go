// Package friends owns friendships, requests, blocks, mutes, hide-vias, people
// pages and suggestions (ARCHITECTURE §3, §4.6, §10.4, §10.5). It implements
// core.Edges, core.Mutes and core.RequestStates and is the sole writer of
// edges, edge_events, friend_requests, blocks, mutes and hide_via. Phase 1:
// graph-changing writes are co-signed with a passkey (cosign.go), hide-via
// cuts a hop without unfriending and friend+ upgrades open an in-person
// handshake (hidevia.go).
//
// Owner: P0-W03 → P1-W03.
//
// Graph memory is updated by whoever commits the change (plan §3.4): this
// package for its own handler-facing writes, invites and account for the edges
// they create or revoke through core.Edges.
package friends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Job kinds registered by the friends module (plan §4.3).
const (
	JobEdgeEventsPurge = "graph.edge_events_purge"
	JobRequestsScrub   = "friends.requests_scrub"
)

// Limits and windows (plan §4.7 friends, D23, §2.3).
const (
	FriendCap         = 1000                // edges + outgoing pending requests (D23)
	RequestsPerDay    = 20                  // new requests per rolling 24 h
	RequestsProbation = 5                   // the same on probation (§8)
	Retention         = 30 * 24 * time.Hour // revoked edges and declined requests (§2.3)
	SuggestEdgeAge    = 7 * 24 * time.Hour  // mutual friends count through edges at least this old
	SuggestMinMutual  = 2
	SuggestMax        = 20
	JobEvery          = 6 * time.Hour
	notifyRequest     = "friend_request"
	notifyAccepted    = "request_accepted"
	edgeEventCreate   = 0
	edgeEventUpgrade  = 1
	edgeEventRevoke   = 2
)

// friend_requests.state values.
const (
	reqPending   int16 = 0
	reqDeclined  int16 = 1 // looks pending to the sender
	reqAccepted  int16 = 2
	reqWithdrawn int16 = 3
)

// Service is the friends module.
type Service struct{ d *core.Deps }

var (
	_ core.Edges         = (*Service)(nil)
	_ core.Mutes         = (*Service)(nil)
	_ core.RequestStates = (*Service)(nil)
)

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers the co-sign purposes, the retention jobs and the
// AccountDeleted clean-up.
func (s *Service) Init(context.Context) error {
	s.registerPurposes()
	if s.d.Bus != nil {
		core.Subscribe(s.d.Bus, func(e core.AccountDeleted) { s.forget(e.ID) })
	}
	if s.d.Jobs == nil {
		return nil
	}
	s.d.Jobs.Handle(JobEdgeEventsPurge, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.PurgeEdgeEvents(ctx)
		return err
	})
	s.d.Jobs.Handle(JobRequestsScrub, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.ScrubRequests(ctx)
		return err
	})
	s.d.Jobs.Every(JobEdgeEventsPurge, JobEvery)
	s.d.Jobs.Every(JobRequestsScrub, JobEvery)
	return nil
}

// PurgeEdgeEvents drops revoke events older than Retention (§2.3).
func (s *Service) PurgeEdgeEvents(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM edge_events WHERE kind = $1 AND at < $2",
		int16(edgeEventRevoke), s.d.Clock.Now().Add(-Retention))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ScrubRequests nulls the timestamps of requests declined more than Retention
// ago, and the drafts the requester signed (they carry times too).
func (s *Service) ScrubRequests(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, `UPDATE friend_requests SET created_at = NULL, decided_at = NULL, draft = NULL,
		draft_assertion = NULL WHERE state = $1 AND decided_at < $2`, reqDeclined, s.d.Clock.Now().Add(-Retention))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// forget deletes the rows this module owns for a deleted account (edges were
// already revoked by account; graph memory forgets the account itself).
func (s *Service) forget(u core.UserID) {
	if s.d.DB == nil {
		return
	}
	ctx := context.Background()
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "DELETE FROM blocks WHERE blocker = $1 OR blocked = $1 RETURNING record_hash", int64(u))
		if err != nil {
			return err
		}
		hashes, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
		if err != nil {
			return err
		}
		for _, h := range hashes {
			if h != nil {
				if err := s.d.Signer.Delete(ctx, tx, h); err != nil {
					return err
				}
			}
		}
		for _, q := range []string{
			"DELETE FROM friend_requests WHERE from_id = $1 OR to_id = $1",
			"DELETE FROM mutes WHERE user_id = $1 OR author_id = $1",
			"DELETE FROM hide_via WHERE user_id = $1 OR via_id = $1",
		} {
			if _, err := tx.Exec(ctx, q, int64(u)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && s.d.Log != nil {
		s.d.Log.Error("friends: account clean-up failed", logx.UID(uint32(u)), "err", err)
	}
}

// ---- core.Edges ----

// friendshipBody is the signed Friendship record body (plan §4.7 records), a < b.
type friendshipBody struct {
	A         uuid.UUID `json:"a"`
	B         uuid.UUID `json:"b"`
	Level     string    `json:"level"`
	CreatedAt sigs.Time `json:"created_at"`
}

// edgeRef is the {a, b} pair of a Revocation, a < b.
type edgeRef struct {
	A uuid.UUID `json:"a"`
	B uuid.UUID `json:"b"`
}

// revocationBody is the signed Revocation record body.
type revocationBody struct {
	Edge edgeRef   `json:"edge"`
	By   uuid.UUID `json:"by"`
	At   sigs.Time `json:"at"`
}

// blockBody is the signed Block record body (party: blocker only).
type blockBody struct {
	Blocker uuid.UUID `json:"blocker"`
	Blocked uuid.UUID `json:"blocked"`
	At      sigs.Time `json:"at"`
}

func levelName(l core.EdgeLevel) string {
	if l == core.LevelFriendPlus {
		return "friend_plus"
	}
	return "friend"
}

// ordered returns two ext IDs in record order (a < b).
func ordered(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if bytes.Compare(a[:], b[:]) > 0 {
		return b, a
	}
	return a, b
}

// exts returns the ext IDs of a and b.
func (s *Service) exts(ctx context.Context, a, b core.UserID) (uuid.UUID, uuid.UUID, error) {
	ms, err := s.d.Users.GetMany(ctx, []core.UserID{a, b})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	ma, oka := ms[a]
	mb, okb := ms[b]
	if !oka || !okb {
		return uuid.Nil, uuid.Nil, gate.ErrNotFound
	}
	return ma.ExtID, mb.ExtID, nil
}

// q returns tx, or the pool when tx is nil (the convention of every core
// interface that takes a store.Q).
func (s *Service) q(tx store.Q) store.Q {
	if tx != nil {
		return tx
	}
	return s.d.DB.Q()
}

// Create records the edge a–b (both directions) and its create event. The
// caller has signed the Friendship record; a duplicate is Problem already_friends.
func (s *Service) Create(ctx context.Context, tx store.Q, a, b core.UserID, lvl core.EdgeLevel, recordHash []byte) error {
	if a == b || a == 0 || b == 0 {
		return gate.ErrNotFound
	}
	tx = s.q(tx)
	now := s.d.Clock.Now()
	tag, err := tx.Exec(ctx, `INSERT INTO edges (user_id, friend_id, level, since, record_hash)
		VALUES ($1, $2, $3, $4, $5), ($2, $1, $3, $4, $5) ON CONFLICT DO NOTHING`,
		int64(a), int64(b), int16(lvl), now, recordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 2 {
		return gate.NewProblem(gate.StatusConflict, "already_friends")
	}
	return s.edgeEvent(ctx, tx, a, b, edgeEventCreate, now, recordHash)
}

func (s *Service) edgeEvent(ctx context.Context, tx store.Q, a, b core.UserID, kind int16, at time.Time, hash []byte) error {
	_, err := tx.Exec(ctx, "INSERT INTO edge_events (id, a, b, kind, at, record_hash) VALUES ($1, $2, $3, $4, $5, $6)",
		s.d.IDs.New(), int64(a), int64(b), kind, at, hash)
	return err
}

// Revoke deletes the edge a–b, writes its Revocation record and schedules both
// records for purging after Retention (§2.3). The Invite record is untouched.
func (s *Service) Revoke(ctx context.Context, tx store.Q, a, b, by core.UserID) error {
	return s.revoke(ctx, tx, a, b, by, nil)
}

// revoke is Revoke with the revoker's co-signature of the Revocation, if any.
func (s *Service) revoke(ctx context.Context, tx store.Q, a, b, by core.UserID, sg *signed) error {
	tx = s.q(tx)
	rows, err := tx.Query(ctx, `DELETE FROM edges WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)
		RETURNING record_hash`, int64(a), int64(b))
	if err != nil {
		return err
	}
	hashes, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return err
	}
	if len(hashes) == 0 {
		return gate.ErrNotFound
	}
	body, err := s.revocation(ctx, a, b, by)
	if err != nil {
		return err
	}
	now := s.d.Clock.Now()
	rev, err := s.signRecord(ctx, tx, core.RecRevocation, by, body, []core.UserID{a, b}, sg)
	if err != nil {
		return err
	}
	purge := now.Add(Retention)
	seen := map[string]bool{}
	for _, h := range append(hashes, rev) {
		if h == nil || seen[string(h)] {
			continue
		}
		seen[string(h)] = true
		// An edge whose Friendship record is already gone (dev and fixture data
		// carry placeholder hashes) has nothing left to schedule; the new
		// Revocation record always exists.
		if err := s.d.Signer.SetPurgeAfter(ctx, tx, h, purge); err != nil &&
			(!errors.Is(err, gate.ErrNotFound) || bytes.Equal(h, rev)) {
			return err
		}
	}
	return s.edgeEvent(ctx, tx, a, b, edgeEventRevoke, now, rev)
}

// Upgrade makes a–b friend+ (P1).
func (s *Service) Upgrade(ctx context.Context, tx store.Q, a, b core.UserID, recordHash []byte) error {
	tx = s.q(tx)
	tag, err := tx.Exec(ctx, `UPDATE edges SET level = $3, record_hash = $4
		WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`,
		int64(a), int64(b), int16(core.LevelFriendPlus), recordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return gate.ErrNotFound
	}
	return s.edgeEvent(ctx, tx, a, b, edgeEventUpgrade, s.d.Clock.Now(), recordHash)
}

// Count returns u's edges and outgoing pending requests (D23). A declined
// request still counts: it looks pending to its sender.
func (s *Service) Count(ctx context.Context, u core.UserID) (int, int, error) {
	return s.count(ctx, s.d.DB.Q(), u)
}

func (s *Service) count(ctx context.Context, q store.Q, u core.UserID) (int, int, error) {
	var edges, pending int
	err := q.QueryRow(ctx, `SELECT (SELECT count(*) FROM edges WHERE user_id = $1),
		(SELECT count(*) FROM friend_requests WHERE from_id = $1 AND state IN (0, 1))`, int64(u)).Scan(&edges, &pending)
	return edges, pending, err
}

// ---- core.Mutes / core.RequestStates ----

// MutedAuthors returns v's muted authors (empty on any read error: muting only
// filters the reader's own feed, so failing open never exposes anything).
func (s *Service) MutedAuthors(ctx context.Context, v core.UserID) map[core.UserID]struct{} {
	out := map[core.UserID]struct{}{}
	rows, err := s.d.DB.Q().Query(ctx, "SELECT author_id FROM mutes WHERE user_id = $1", int64(v))
	if err != nil {
		return out
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return out
	}
	for _, id := range ids {
		out[core.UserID(id)] = struct{}{}
	}
	return out
}

// RequestState returns "outgoing" when v asked u (pending or declined),
// "incoming" when u's request to v is pending, otherwise "".
func (s *Service) RequestState(ctx context.Context, v, u core.UserID) string {
	var out, in bool
	err := s.d.DB.Q().QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM friend_requests WHERE from_id = $1 AND to_id = $2 AND state IN (0, 1)),
		EXISTS (SELECT 1 FROM friend_requests WHERE from_id = $2 AND to_id = $1 AND state = 0)`,
		int64(v), int64(u)).Scan(&out, &in)
	switch {
	case err != nil:
		return ""
	case out:
		return "outgoing"
	case in:
		return "incoming"
	}
	return ""
}

// ---- shared helpers ----

// errSelf is the Problem for acting on one's own account.
func errSelf() error { return gate.NewProblem(gate.StatusUnprocessable, "self_not_allowed") }

// resolve opens a user ref minted for v (gate.ErrNotFound on any failure).
func (s *Service) resolve(v core.UserID, ref gate.Ref) (core.UserID, error) {
	u, ok := s.d.Refs.OpenUser(v, ref)
	if !ok {
		return 0, gate.ErrNotFound
	}
	return u, nil
}

// visible resolves ref to a user the viewer can currently see and who is active.
func (s *Service) visible(ctx context.Context, v core.UserID, ref gate.Ref) (core.UserID, core.Search, error) {
	u, err := s.resolve(v, ref)
	if err != nil {
		return 0, nil, err
	}
	sr, err := s.d.Vis.SearchFor(ctx, v)
	if err != nil {
		return 0, nil, err
	}
	if !s.d.Vis.CanSeeUser(sr, u) || !s.active(ctx, u) {
		return 0, nil, gate.ErrNotFound
	}
	return u, sr, nil
}

func (s *Service) active(ctx context.Context, u core.UserID) bool {
	m, err := s.d.Users.Get(ctx, u)
	return err == nil && m.Status == core.StatusActive
}

func notFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return gate.ErrNotFound
	}
	return err
}
