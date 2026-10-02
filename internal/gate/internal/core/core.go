// Package core is what gate modules share (plan §4.5): raw identifiers, the
// authenticated Viewer, cross-module interfaces, the in-process Bus and the late-
// bound dependency container. Go's internal rule makes it importable only from
// packages under internal/gate/, so handlers cannot even name a user ID or a row.
//
// Contract file (SCAFFOLD): additive changes only, through the contract steward.
package core

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// UserID is a user's internal ID (< 2^32, DB CHECK; roaring-friendly). 0 = none.
type UserID uint32

// PostID is a post's UUIDv7.
type PostID = uuid.UUID

// Role is a user's role.
type Role uint8

// Roles.
const (
	RoleMember    Role = 0
	RoleModerator Role = 1
	RoleAdmin     Role = 2
)

// String returns the API name of the role.
func (r Role) String() string {
	switch r {
	case RoleModerator:
		return "moderator"
	case RoleAdmin:
		return "admin"
	}
	return "member"
}

// SessionKind is the kind of session.
type SessionKind uint8

// Session kinds (sessions.kind).
const (
	SessGuest      SessionKind = 0
	SessUser       SessionKind = 1
	SessEnrollment SessionKind = 2
	SessNone       SessionKind = 255 // no session (never stored)
)

// String returns the API name of the session kind.
func (k SessionKind) String() string {
	switch k {
	case SessGuest:
		return "guest"
	case SessUser:
		return "user"
	case SessEnrollment:
		return "enrollment"
	}
	return "none"
}

// User statuses (users.status).
const (
	StatusActive            uint8 = 0
	StatusPendingEnrollment uint8 = 1
	StatusSuspended         uint8 = 2
	StatusDeleted           uint8 = 3
)

// EdgeLevel is a friendship level.
type EdgeLevel uint8

// Edge levels.
const (
	LevelFriend     EdgeLevel = 1
	LevelFriendPlus EdgeLevel = 2
)

// Audience is a post's audience limit.
type Audience uint8

// Audiences (posts.audience; NULL = AudConnected).
const (
	AudConnected  Audience = 0
	AudD1         Audience = 1
	AudD2         Audience = 2
	AudD3         Audience = 3
	AudFriendPlus Audience = 10 // P1
)

// String returns the API name.
func (a Audience) String() string {
	switch a {
	case AudD1:
		return "d1"
	case AudD2:
		return "d2"
	case AudD3:
		return "d3"
	case AudFriendPlus:
		return "friend_plus"
	}
	return "connected"
}

// RefPolicy controls who may reference a post.
type RefPolicy uint8

// Reference policies (posts.ref_policy).
const (
	PolAnyone  RefPolicy = 0
	PolTwoHops RefPolicy = 1
	PolFriends RefPolicy = 2
	PolNobody  RefPolicy = 3 // slippery
)

// String returns the API name.
func (p RefPolicy) String() string {
	switch p {
	case PolTwoHops:
		return "two_hops"
	case PolFriends:
		return "friends"
	case PolNobody:
		return "nobody"
	}
	return "anyone"
}

// LinkKind is a parent link kind.
type LinkKind uint8

// Link kinds (post_parents.kind).
const (
	LinkRef  LinkKind = 0
	LinkCont LinkKind = 1
)

// String returns the API name.
func (k LinkKind) String() string {
	if k == LinkCont {
		return "cont"
	}
	return "ref"
}

// Preset is a ranking preset.
type Preset uint8

// Presets (users.rank_preset).
const (
	PresetLatest   Preset = 0
	PresetBalanced Preset = 1
	PresetTop      Preset = 2 // P1
)

// String returns the API name.
func (p Preset) String() string {
	switch p {
	case PresetLatest:
		return "latest"
	case PresetTop:
		return "top"
	}
	return "balanced"
}

// ParsePreset parses an API preset name.
func ParsePreset(s string) (Preset, bool) {
	switch s {
	case "latest":
		return PresetLatest, true
	case "balanced":
		return PresetBalanced, true
	case "top":
		return PresetTop, true
	}
	return 0, false
}

// ModReason is a closed enum of moderation reasons (DB CHECK, archtest A7).
type ModReason string

// Moderation reasons.
const (
	ReasonSpam              ModReason = "spam"
	ReasonBot               ModReason = "bot"
	ReasonHarassment        ModReason = "harassment"
	ReasonCSAM              ModReason = "csam"
	ReasonIllegal           ModReason = "illegal"
	ReasonImpersonation     ModReason = "impersonation"
	ReasonMisleadingPreview ModReason = "misleading_preview"
	ReasonBanEvasion        ModReason = "ban_evasion"
	ReasonOther             ModReason = "other"
)

// ModReasons lists every ModReason.
var ModReasons = []ModReason{ReasonSpam, ReasonBot, ReasonHarassment, ReasonCSAM, ReasonIllegal,
	ReasonImpersonation, ReasonMisleadingPreview, ReasonBanEvasion, ReasonOther}

// ReportReason is the closed enum users may report with (ModReason minus ban_evasion).
type ReportReason string

// ReportReasons lists every ReportReason.
var ReportReasons = []ReportReason{"spam", "bot", "harassment", "csam", "illegal", "impersonation", "misleading_preview", "other"}

// ---- the viewer ----

// Viewer is the authenticated caller. It travels only in context.Context (set by
// the auth middleware); handlers never hold it.
type Viewer struct {
	ID         UserID
	Session    [32]byte // sha256 of the session token (sessions.id_hash)
	Kind       SessionKind
	Role       Role
	AuthAt     time.Time // last UV passkey verification
	StepUp     bool      // P1: automation signals require a step-up
	Credential []byte    // credential that authenticated this session
}

type viewerKey struct{}

// WithViewer stores v in ctx.
func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, v)
}

// ViewerFrom returns the viewer stored in ctx.
func ViewerFrom(ctx context.Context) (Viewer, bool) {
	v, ok := ctx.Value(viewerKey{}).(Viewer)
	return v, ok
}

// FreshFor is the step-up freshness window (D27).
const FreshFor = 5 * time.Minute

// RequireUser returns the signed-in viewer: gate.ErrNotFound unless Kind==SessUser,
// gate.ErrStepUp if a step-up is pending.
func RequireUser(ctx context.Context) (Viewer, error) {
	v, ok := ViewerFrom(ctx)
	if !ok || v.Kind != SessUser || v.ID == 0 {
		return Viewer{}, gate.ErrNotFound
	}
	if v.StepUp {
		return Viewer{}, gate.ErrStepUp
	}
	return v, nil
}

// RequireFresh is RequireUser plus a UV verification within FreshFor of now.
func RequireFresh(ctx context.Context, now time.Time) (Viewer, error) {
	v, err := RequireUser(ctx)
	if err != nil {
		return v, err
	}
	if v.AuthAt.IsZero() || now.Sub(v.AuthAt) > FreshFor {
		return Viewer{}, gate.ErrStepUp
	}
	return v, nil
}

// RequireModerator returns the viewer if they are a moderator or admin; otherwise
// gate.ErrNotFound (moderator surfaces do not exist for members).
func RequireModerator(ctx context.Context) (Viewer, error) {
	v, err := RequireUser(ctx)
	if err != nil {
		return v, err
	}
	if v.Role != RoleModerator && v.Role != RoleAdmin {
		return Viewer{}, gate.ErrNotFound
	}
	return v, nil
}
