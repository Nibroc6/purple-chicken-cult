package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Phase 1 contracts (SCAFFOLD-1).

// Intent is a co-sign intent shown to the user (P1-W01).
type Intent struct {
	ID      gate.Ref
	Record  json.RawMessage // the record being signed, shown to the user
	Options json.RawMessage // WebAuthn get options (challenge = sigs.Challenge over the draft)
}

// Cosign is the single owner of "graph-changing writes need a passkey assertion"
// (impl: gate/cosign).
type Cosign interface {
	BeginIntent(ctx context.Context, v Viewer, purpose string, target gate.Ref) (Intent, error)
	// Require verifies assertion for intent (viewer's own credential, exact origin,
	// UV) and consumes the intent in the caller's transaction. It returns the
	// assertion and the exact draft bytes that were signed.
	Require(ctx context.Context, tx store.Q, v Viewer, purpose string, kind RecordKind, body any, intent gate.Ref, assertion []byte) (Assertion, []byte, error)
}

// Drafter builds the record a co-sign intent of one purpose asks the viewer to
// sign (P1-W01, steward addition). target is the ref the client sent (empty
// when none) and is resolved by the drafter, which owns its kind; issuedAt is
// the intent's creation time, which a drafter uses for the record's own time
// field (created_at, at) so the write can reproduce the draft. The body must
// not contain "nonce" or "issued_at": the intent injects them. Any target that
// does not resolve is the uniform gate.ErrNotFound.
type Drafter func(ctx context.Context, v Viewer, target gate.Ref, issuedAt time.Time) (RecordKind, any, error)

// CosignDrafters is implemented by every Cosign: the module that owns a purpose
// registers its drafter in Init (d.Cosign.(core.CosignDrafters)). BeginIntent
// for a purpose without a drafter is gate.ErrNotFound.
//
// Require then checks body against the stored draft: every field of body must
// equal the draft's, and the draft may add only "nonce", "issued_at" and the
// fields the caller cannot know before the intent existed ("created_at", "at").
// Callers sign the returned draft bytes (pass json.RawMessage(draft) to
// Signer.Sign with the assertion), so the stored record is exactly what the
// passkey signed.
type CosignDrafters interface {
	RegisterDrafter(purpose string, f Drafter)
}

// Outcome is a handshake score outcome (§7).
type Outcome uint8

// Outcomes (handshake_sessions.outcome).
const (
	OutcomeNone     Outcome = 0
	OutcomeRetry    Outcome = 1
	OutcomeAccepted Outcome = 2
	OutcomeVerified Outcome = 3
)

// Handshakes opens non-invite in-person handshakes (impl: gate/invites, P1-W02).
type Handshakes interface {
	OpenUpgrade(ctx context.Context, v Viewer, peer UserID) (gate.HandshakeView, error)
	OpenRecovery(ctx context.Context, guardian, account UserID) (gate.HandshakeView, error)
	ClaimRecovery(ctx context.Context, sess [32]byte, payload string) (account UserID, err error)
	Result(ctx context.Context, hs uuid.UUID) (Outcome, error)
}

// Media attaches and renders media (impl: gate/media, P1-W04).
type Media interface {
	Attach(ctx context.Context, tx store.Q, owner UserID, refs []gate.Ref) ([]uuid.UUID, error)
	Views(ctx context.Context, s Search, ids []uuid.UUID) ([]gate.MediaView, error)
	Ingest(ctx context.Context, img []byte, sourcePost PostID, sourceURL string) (uuid.UUID, error)
}

// ModQueue sources (mod_queue.source).
const (
	ModSrcReport        uint8 = 0
	ModSrcSignalCluster uint8 = 1
	ModSrcCSAM          uint8 = 2
	ModSrcStepUpRepeat  uint8 = 3
	ModSrcInviteCluster uint8 = 4
)

// ModQueueItem is a new moderation queue entry.
type ModQueueItem struct {
	Source      uint8
	Priority    uint8 // 0 critical (csam) .. 3 low
	SubjectUser UserID
	SubjectPost PostID
	ReportID    uuid.UUID
	Summary     map[string]int // codes + counts
}

// ModQueue enqueues moderation work (impl: gate/moderation, P1-W06).
type ModQueue interface {
	Enqueue(ctx context.Context, tx store.Q, item ModQueueItem) error
}

// StepUp requires a passkey step-up for a session (impl: gate/auth, P1-W07).
type StepUp interface {
	Require(ctx context.Context, user UserID, session [32]byte, reason string) error
}
