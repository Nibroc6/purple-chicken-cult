package core

import (
	"log/slog"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
	"github.com/Nibroc6/purple-chicken-cult/internal/store/jobq"
)

// DerivedKeys are HKDF outputs of the master key (plan §4.2 config).
type DerivedKeys struct {
	CSRF      []byte
	Cursor    []byte
	Pseudonym []byte
	MediaURL  []byte
	// LogSalt and MetricsSalt derive the daily salts (never stored).
	LogSalt     func(day string) []byte
	MetricsSalt func(day string) []byte
}

// Deps is the late-bound dependency container: gate/wire fills the base fields,
// constructs every service with New(*Deps), then assigns the interfaces below
// before calling each service's Init.
type Deps struct {
	DB    *store.DB
	Jobs  *jobq.Queue
	Clock clock.Clock
	IDs   ids.Gen
	Refs  Refs
	Cfg   *config.Config
	Log   *slog.Logger
	Bus   *Bus
	Keys  DerivedKeys

	Users     Users
	Graph     Graph
	Edges     Edges
	Mutes     Mutes
	Posts     PostIndex
	Vis       Visibility
	Chains    Chains
	Engage    Engagement
	Prefer    Prefer
	Views     Views
	Signer    Signer
	Passkeys  Passkeys
	Invites   Invites
	Probation Probation
	Notify    Notifier
	Limits    Limiter
	Metrics   Metrics

	// Phase 1 (SCAFFOLD-1).
	Cosign     Cosign
	Handshakes Handshakes
	Media      Media
	ModQueue   ModQueue
	StepUp     StepUp

	// Steward additions (contract requests p0-w11-1, p0-w11-2).
	DisplayNames   DisplayNames
	RecordExporter RecordExporter
	PostAdmin      PostAdmin
	Enrollments    Enrollments
	// Steward addition (contract request p1-w10-3); impl: gate/account.
	// Optional: nil skips the passkey veto.
	Recovery RecoveryGuard

	// Steward additions (contract request p0-w02-1). Optional: nil until the
	// implementing WP lands; gate/views treats nil as "none".
	Mentions MentionIndex
	Requests RequestStates

	// Steward addition (contract request p0-w01-1); impl: gate/users.
	Accounts Accounts

	// Steward addition (contract request p0-w04-1); impl: gate/users.
	Handles HandleRules

	// Steward additions (contract request p0-w12-1); impl: gate/posts, gate/users.
	PostRemover PostRemover
	Suspensions Suspensions

	// Steward additions (contract request p1-w01-1); impl: gate/cosign.
	CosignPurposes CosignPurposes
	Signup         SignupCosign

	// Steward addition (contract request p1-w04-1); impl: gate/media.
	CSAM CSAMHolds

	// Steward additions (contract request p1-w06-1); impl: gate/invites,
	// gate/moderation. Marks is optional: gate/views treats nil as "none".
	InvitePenalties InvitePenalties
	Marks           RemovedMarks
}
