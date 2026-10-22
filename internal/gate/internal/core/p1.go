package core

import (
	"context"
	"crypto/sha256"
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

// ---- co-sign purposes and the signup ceremony (steward: contract request p1-w01-1) ----

// CosignBuilder builds the record body an intent of one purpose signs, for
// viewer v and the purpose's target (may be empty). Any failure to resolve the
// target is gate.ErrNotFound. The body follows the sigs value grammar and must
// not contain a "cosign" member (Signer.Draft adds it).
type CosignBuilder func(ctx context.Context, v Viewer, target gate.Ref) (body any, err error)

// CosignPurposes registers how intents of a purpose are drafted (impl:
// gate/cosign). The module that later calls Cosign.Require for the purpose
// registers its builder in Init; BeginIntent of an unregistered purpose is
// gate.ErrNotFound.
type CosignPurposes interface {
	RegisterPurpose(purpose string, kind RecordKind, build CosignBuilder)
}

// CosignRecord is one record the invitee signs during the signup ceremony.
type CosignRecord struct {
	Kind RecordKind
	Body any
}

// SignupCosign runs the invitee signup ceremony (fidelity #21; impl:
// gate/cosign): after Passkeys.FinishRegistration, one get assertion with the
// NEW credential over the drafts of every record the signup writes, verified
// inside the RegisterFinish transaction before it commits.
type SignupCosign interface {
	// BeginSignup binds the drafts of recs to guest session sess and returns
	// the WebAuthn get options (allowCredentials = cred only, UV required,
	// challenge = BundleChallenge over the drafts' challenges) and the drafts.
	BeginSignup(ctx context.Context, sess [32]byte, cred Credential, recs []CosignRecord) (Intent, error)
	// FinishSignup consumes the session's ceremony in tx, checks that recs are
	// the records begun, verifies the assertion against cred (exact origin, no
	// crossOrigin/topOrigin, UP, UV, rpIdHash, BE flag as registered, counter,
	// signature) and returns one Assertion per record (Draft, Bundle, User =
	// user). Any failure is gate.ErrNotFound: the caller's transaction aborts
	// and nothing is created.
	FinishSignup(ctx context.Context, tx store.Q, sess [32]byte, user UserID, cred Credential, recs []CosignRecord, assertion []byte) ([]Assertion, error)
}

// BundleChallenge is the challenge of one assertion over several drafts:
// sha256("pcc-v1:bundle" ‖ c_1 ‖ … ‖ c_n), each c_i a draft challenge.
func BundleChallenge(challenges [][]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("pcc-v1:bundle"))
	for _, c := range challenges {
		h.Write(c)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
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
	OpenUpgrade(ctx context.Context, v Viewer, peer UserID, quiet bool) (gate.HandshakeView, error)
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

// ThemeMedia keeps the images of a reader's own advanced theme (impl:
// gate/media; contract request p1-w09-3). They are uploads like any other
// (scanned, re-encoded, attached with Media.Attach), but they belong to the
// theme instead of a post, so only their owner ever gets a URL for them.
type ThemeMedia interface {
	// Release marks owner's media ids deleted in tx (media.process purges them).
	Release(ctx context.Context, tx store.Q, owner UserID, ids []uuid.UUID) error
	// Own renders owner's ready, live media among ids, in order, with owner's refs.
	Own(ctx context.Context, owner UserID, ids []uuid.UUID) ([]gate.MediaView, error)
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
	Media       uuid.UUID      // CSAM items: the held media (mod_queue.subject_media); zero = none (p1-w04-1)
}

// ModQueue enqueues moderation work (impl: gate/moderation, P1-W06).
type ModQueue interface {
	Enqueue(ctx context.Context, tx store.Q, item ModQueueItem) error
}

// ---- CSAM holds (steward: contract request p1-w04-1) ----

// CSAMHold is what moderators see of a held media item: the scanner's match
// metadata and the preservation state, never the content (§6.1, security #7).
type CSAMHold struct {
	Media       uuid.UUID
	SubjectUser UserID            // the uploader, or the author of the post that linked it (0 = none)
	SourcePost  PostID            // preview images: the post whose link produced it (uuid.Nil otherwise)
	SourceURL   string            // preview images: the canonical URL that served the image
	MatchInfo   map[string]string // scanner verdict metadata only
	HeldAt      time.Time
	RetainUntil time.Time  // HeldAt + 1 year (evidence preservation)
	ReportedAt  *time.Time // set by Report
	ReportRef   string     // the CyberTipline report id, set by Report
	AccountHeld bool       // the subject's uploads are refused (media_rejected)
}

// CSAMHolds is the media-side state of CSAM matches (impl: gate/media). The
// moderation queue (P1-W06) reads holds for its CSAM items and calls Report from
// the two-person csam_report action. Viewing the held content is a break-glass
// procedure outside the application (deploy/runbooks/csam.md): no method here
// returns it.
type CSAMHolds interface {
	// Hold returns the hold of media; gate.ErrNotFound unless media is held.
	Hold(ctx context.Context, media uuid.UUID) (CSAMHold, error)
	// Report records the CyberTipline report reference in tx (once): Problem
	// csam_already_reported if a report is recorded, Problem bad_request (field
	// report_ref) for an empty or over-long reference.
	Report(ctx context.Context, tx store.Q, media uuid.UUID, reportRef string) error
	// ReleaseAccount lifts the subject's upload hold of media after review (a
	// false positive) in tx. The evidence stays preserved until RetainUntil.
	ReleaseAccount(ctx context.Context, tx store.Q, media uuid.UUID) error
}

// StepUp requires a passkey step-up for a session (impl: gate/auth, P1-W07).
type StepUp interface {
	Require(ctx context.Context, user UserID, session [32]byte, reason string) error
}

// ---- quarantine penalties and removed-account marks (steward: contract request p1-w06-1) ----

// InvitePenalties applies the invite-tree quarantine penalty to an inviter's
// invite budget (§8; impl: gate/invites, which owns invite_budgets). Called by
// gate/moderation inside the second moderator's confirmation transaction.
type InvitePenalties interface {
	// Penalize records one quarantine offense of inviter in tx
	// (invite_budgets.offenses + 1, creating the bucket if needed): the first
	// offense pauses its invites until until (paused_until), any later one
	// revokes them (revoked = true). It returns the offense count after this one.
	Penalize(ctx context.Context, tx store.Q, inviter UserID, until time.Time) (offenses int, err error)
}

// RemovedMarks answers whether a viewer sees the "invited a removed account"
// mark on an inviter (§8, fidelity #19; impl: gate/moderation, which owns
// removed_account_marks). A mark lasts 90 days and is visible only to the
// users in its stored viewers bitmap (the removed account's component at
// removal time) — never to someone who joined later, and never to the
// inviter about themself.
type RemovedMarks interface {
	Marked(ctx context.Context, viewer, inviter UserID) bool
}
