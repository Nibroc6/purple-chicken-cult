package friends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Co-signed graph changes (ARCHITECTURE §3 Phase 1, §17.4; plan P1-W03,
// hand-off p1-w01-2). A friend request, its acceptance, an unfriend and a
// block are signed by the person making them with one of their passkeys:
// the client opens an intent with POST /api/cosign {purpose, target: person}
// (the builders below draft the exact record), signs it in the co-sign dialog
// and sends {intent, assertion} with the change. The change's transaction
// consumes the intent (core.Cosign.Require), checks that the drafted record
// is the one this change writes (the right people, level and author) and
// stores the assertion with the record, so exports verify offline.
//
//   - friend_request: the requester signs the Friendship draft; the draft and
//     the assertion wait on the request (friend_requests.draft,
//     draft_assertion). A request answering the other person's pending one
//     signs THAT draft and accepts it.
//   - friend_accept: the accepter signs the same Friendship record; it is
//     written with both assertions (the requester's first).
//   - unfriend: the Revocation record (by = the caller).
//   - block: the Block record; blocking a friend co-signs the Revocation the
//     block writes instead (purpose unfriend): a session holds one passkey
//     ceremony at a time, so one change carries one co-signature, and the
//     edge removal is what exports verify.
//
// A session authenticated with a passkey must co-sign: a graph-changing write
// without a fresh assertion (no intent, an expired, used or foreign intent, a
// draft for another person or another change) is the uniform not-found and
// changes nothing. Sessions without a credential exist only through the test
// hooks; their writes stay server-signed (Phase 0 records) unless they
// co-sign anyway. Once the co-sign cut-over is set (records.EnforceCosign,
// INT-1), exports flag unattested records after it.

// Co-sign purposes registered by this module (gate/cosign names them too).
const (
	PurposeFriendRequest = "friend_request"
	PurposeFriendAccept  = "friend_accept"
	PurposeUnfriend      = "unfriend"
	PurposeBlock         = "block"
)

// Cosig is a co-signature sent with a change: the intent from POST
// /api/cosign and the passkey assertion over its draft.
type Cosig struct {
	Intent    gate.Ref        `json:"intent"`
	Assertion json.RawMessage `json:"assertion"`
}

func (c *Cosig) present() bool { return c != nil && (c.Intent != "" || len(c.Assertion) > 0) }

// first returns the first co-signature given, or nil.
func first(cs []Cosig) *Cosig {
	if len(cs) == 0 {
		return nil
	}
	return &cs[0]
}

// mustCosign reports whether v's changes must be co-signed: every session
// created by a passkey ceremony.
func mustCosign(v core.Viewer) bool { return len(v.Credential) > 0 }

// signed is a verified co-signature: the assertion and the canonical record
// (the draft without its cosign member) it signed.
type signed struct {
	a      core.Assertion
	record []byte
}

// verify consumes c for purpose in tx. No co-signature is fine only for a
// session that need not co-sign (nil result).
func (s *Service) verify(ctx context.Context, tx store.Q, v core.Viewer, purpose string, kind core.RecordKind, c *Cosig) (*signed, error) {
	if !c.present() {
		if mustCosign(v) {
			return nil, gate.ErrNotFound
		}
		return nil, nil
	}
	if s.d.Cosign == nil {
		return nil, gate.ErrNotFound
	}
	a, draft, err := s.d.Cosign.Require(ctx, tx, v, purpose, kind, nil, c.Intent, c.Assertion)
	if err != nil {
		return nil, err
	}
	rec, _, _, err := coreref.RecordFromDraft(draft)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	return &signed{a: a, record: rec}, nil
}

// registerPurposes registers the draft builders with gate/cosign.
func (s *Service) registerPurposes() {
	if s.d.CosignPurposes == nil {
		return
	}
	s.d.CosignPurposes.RegisterPurpose(PurposeFriendRequest, core.RecFriendship, s.requestDraft)
	s.d.CosignPurposes.RegisterPurpose(PurposeFriendAccept, core.RecFriendship, s.acceptDraft)
	s.d.CosignPurposes.RegisterPurpose(PurposeUnfriend, core.RecRevocation, s.unfriendDraft)
	s.d.CosignPurposes.RegisterPurpose(PurposeBlock, core.RecBlock, s.blockDraft)
}

// requestDraft drafts the Friendship a request to target signs: the target's
// own pending request's record when there is one (the request accepts it),
// else a new one.
func (s *Service) requestDraft(ctx context.Context, v core.Viewer, target gate.Ref) (any, error) {
	u, _, err := s.visible(ctx, v.ID, target)
	if err != nil || u == v.ID {
		return nil, gate.ErrNotFound
	}
	if _, ok := s.d.Graph.Level(v.ID, u); ok {
		return nil, gate.ErrNotFound
	}
	if rec, _, ok := s.pendingDraft(ctx, s.d.DB.Q(), u, v.ID, false); ok {
		return recordBody(rec)
	}
	return s.friendship(ctx, v.ID, u)
}

// recordBody is the Friendship body of a canonical record (it re-canonicalizes
// to the same record).
func recordBody(rec []byte) (friendshipBody, error) {
	var fb friendshipBody
	if err := json.Unmarshal(rec, &fb); err != nil {
		return friendshipBody{}, gate.ErrNotFound
	}
	return fb, nil
}

// acceptDraft drafts the Friendship accepting from's pending request: the
// requester's record when they signed one.
func (s *Service) acceptDraft(ctx context.Context, v core.Viewer, from gate.Ref) (any, error) {
	u, err := s.resolve(v.ID, from)
	if err != nil || !s.active(ctx, u) {
		return nil, gate.ErrNotFound
	}
	var st int16
	if err := s.d.DB.Q().QueryRow(ctx, "SELECT state FROM friend_requests WHERE from_id = $1 AND to_id = $2",
		int64(u), int64(v.ID)).Scan(&st); err != nil || st != reqPending {
		return nil, gate.ErrNotFound
	}
	if rec, _, ok := s.pendingDraft(ctx, s.d.DB.Q(), u, v.ID, false); ok {
		return recordBody(rec)
	}
	return s.friendship(ctx, v.ID, u)
}

// unfriendDraft drafts the Revocation of the caller's friendship with who.
func (s *Service) unfriendDraft(ctx context.Context, v core.Viewer, who gate.Ref) (any, error) {
	u, err := s.resolve(v.ID, who)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	if _, ok := s.d.Graph.Level(v.ID, u); !ok {
		return nil, gate.ErrNotFound
	}
	return s.revocation(ctx, v.ID, u, v.ID)
}

// blockDraft drafts the Block record of the caller blocking who.
func (s *Service) blockDraft(ctx context.Context, v core.Viewer, who gate.Ref) (any, error) {
	u, _, err := s.visible(ctx, v.ID, who)
	if err != nil || u == v.ID || s.d.Graph.Blocks(v.ID, u) {
		return nil, gate.ErrNotFound
	}
	ev, eu, err := s.exts(ctx, v.ID, u)
	if err != nil {
		return nil, gate.ErrNotFound
	}
	return blockBody{Blocker: ev, Blocked: eu, At: sigs.T(s.d.Clock.Now())}, nil
}

// friendship is a new plain Friendship body of a and b.
func (s *Service) friendship(ctx context.Context, a, b core.UserID) (friendshipBody, error) {
	ea, eb, err := s.exts(ctx, a, b)
	if err != nil {
		return friendshipBody{}, gate.ErrNotFound
	}
	x, y := ordered(ea, eb)
	return friendshipBody{A: x, B: y, Level: levelName(core.LevelFriend), CreatedAt: sigs.T(s.d.Clock.Now())}, nil
}

// revocation is the Revocation body of a–b by by.
func (s *Service) revocation(ctx context.Context, a, b, by core.UserID) (revocationBody, error) {
	ea, eb, err := s.exts(ctx, a, b)
	if err != nil {
		return revocationBody{}, err
	}
	eby := ea
	if by == b {
		eby = eb
	}
	oa, ob := ordered(ea, eb)
	return revocationBody{Edge: edgeRef{A: oa, B: ob}, By: eby, At: sigs.T(s.d.Clock.Now())}, nil
}

// pendingDraft returns the canonical Friendship record and the requester's
// assertion of from's pending request to to, when the requester co-signed it.
func (s *Service) pendingDraft(ctx context.Context, q store.Q, from, to core.UserID, lock bool) ([]byte, *core.Assertion, bool) {
	sql := "SELECT draft, draft_assertion FROM friend_requests WHERE from_id = $1 AND to_id = $2 AND state = 0"
	if lock {
		sql += " FOR UPDATE"
	}
	var draft, raw []byte
	if err := q.QueryRow(ctx, sql, int64(from), int64(to)).Scan(&draft, &raw); err != nil || draft == nil || raw == nil {
		return nil, nil, false
	}
	rec, _, _, err := coreref.RecordFromDraft(draft)
	if err != nil {
		return nil, nil, false
	}
	var a core.Assertion
	if json.Unmarshal(raw, &a) != nil || !bytes.Equal(a.Draft, draft) {
		return nil, nil, false
	}
	return rec, &a, true
}

// ---- checking what was signed ----

// isFriendship reports whether rec is a plain Friendship record of a and b.
func (s *Service) isFriendship(ctx context.Context, rec []byte, a, b core.UserID) bool {
	var fb friendshipBody
	if json.Unmarshal(rec, &fb) != nil || fb.Level != levelName(core.LevelFriend) {
		return false
	}
	return s.samePair(ctx, fb.A, fb.B, a, b)
}

// isRevocation reports whether rec revokes a–b by by.
func (s *Service) isRevocation(ctx context.Context, rec []byte, a, b, by core.UserID) bool {
	var rb revocationBody
	if json.Unmarshal(rec, &rb) != nil || !s.samePair(ctx, rb.Edge.A, rb.Edge.B, a, b) {
		return false
	}
	eby, _, err := s.exts(ctx, by, by)
	return err == nil && rb.By == eby
}

// isBlock reports whether rec is blocker's Block of blocked.
func (s *Service) isBlock(ctx context.Context, rec []byte, blocker, blocked core.UserID) bool {
	var bb blockBody
	if json.Unmarshal(rec, &bb) != nil {
		return false
	}
	e1, e2, err := s.exts(ctx, blocker, blocked)
	return err == nil && bb.Blocker == e1 && bb.Blocked == e2
}

func (s *Service) samePair(ctx context.Context, x, y uuid.UUID, a, b core.UserID) bool {
	ea, eb, err := s.exts(ctx, a, b)
	if err != nil {
		return false
	}
	oa, ob := ordered(ea, eb)
	return x == oa && y == ob
}

// signRecord writes a record: from the co-signatures' draft when there are
// any (each assertion binds to the same record), else server-signed from body.
func (s *Service) signRecord(ctx context.Context, tx store.Q, kind core.RecordKind, author core.UserID, body any, parties []core.UserID, sg ...*signed) ([]byte, error) {
	var as []core.Assertion
	for _, x := range sg {
		if x != nil {
			as = append(as, x.a)
		}
	}
	if len(as) > 0 {
		body = nil
	}
	return s.d.Signer.Sign(ctx, tx, kind, author, body, parties, as...)
}

// errNotSigned is what a co-signature of the wrong record answers.
var errNotSigned = gate.ErrNotFound

// lockedState returns the state of from's request to to, locked (pgx.ErrNoRows ⇒ not found).
func lockedState(ctx context.Context, tx pgx.Tx, from, to core.UserID) (int16, error) {
	var st int16
	err := tx.QueryRow(ctx, "SELECT state FROM friend_requests WHERE from_id = $1 AND to_id = $2 FOR UPDATE",
		int64(from), int64(to)).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, gate.ErrNotFound
	}
	return st, err
}
