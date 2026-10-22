package core

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Cross-module interfaces (plan §4.5). Every interface has a correct in-memory
// reference implementation in gate/internal/coreref, and every scaffold stub
// delegates to it until the implementing work package lands.
//
// Scaffold naming notes (deviations from the plan text, to avoid method-name
// clashes with the handler-facing API on the same *Service type):
//   Engagement.Likers/Savers/ShowLess → LikerSet/SaverSet/ShowLessCounts
//   Invites.Invitees                  → InviteesOf
//   Cosign.Begin                      → BeginIntent

// ---- users (impl: gate/users) ----

// UserMeta is the cached, non-secret part of a user row.
type UserMeta struct {
	ID               UserID
	ExtID            uuid.UUID
	Handle           string
	DisplayName      string
	InvitedBy        UserID // 0 = none
	Genesis          bool
	Status           uint8
	Role             Role
	CreatedAt        time.Time
	ProbationUntil   *time.Time
	ProbationCleared bool
	NameInChains     bool
	Suggestible      bool
	NamedInLikes     bool
	SlipperyDefault  bool
	Preset           Preset
	Locale           string
	Languages        []string
	// INT-0 additions (the session view's settings summary): the rest of the
	// reader's settings (§10.7). UIPrefs is the stored JSON ("{}" when unset).
	TranslateTo   string
	Autotranslate bool
	UIPrefs       []byte
}

// OnProbation reports whether u is (still) on probation: both the time part and
// the outside-friends part must be satisfied before probation_cleared_at is set.
func (u UserMeta) OnProbation(now time.Time) bool {
	if u.Genesis || u.ProbationUntil == nil || u.ProbationCleared {
		return false
	}
	return true
}

// NewUser is the input of Users.Create.
type NewUser struct {
	Handle, DisplayName, Locale string
	Languages                   []string
	InvitedBy                   UserID
	Genesis                     bool
	ProbationUntil              *time.Time
	TOSVersion                  int
	AgeAttested                 bool
	WebAuthnID                  []byte
	Status                      uint8
	// ExtID is the account's ext_id when the caller reserved it before the
	// transaction (the P1 signup ceremony signs drafts that name the invitee);
	// uuid.Nil ⇒ generated. Steward: contract request p1-w01-1.
	ExtID uuid.UUID
}

// Users is the user directory.
type Users interface {
	Get(ctx context.Context, id UserID) (UserMeta, error) // gate.ErrNotFound if absent
	GetMany(ctx context.Context, ids []UserID) (map[UserID]UserMeta, error)
	InviterLabel(ctx context.Context, id UserID) (gate.InviterLabel, error)
	InviterOf(id UserID) (UserID, bool)                                // in-memory, for rank caps
	ByHandle(ctx context.Context, canon string) (UserID, bool)         // INTERNAL ONLY (mentions within horizon, CLI)
	Create(ctx context.Context, tx store.Q, u NewUser) (UserID, error) // Problem handle_taken on unique violation
	HandleAvailable(ctx context.Context, canon string) bool            // join flow only (D21)
}

// DisplayNames validates display names (impl: gate/users): NFC, ≤ 50 code points,
// graphic code points and plain spaces only, none of the integrity glyphs.
// Steward: contract request p0-w11-1.
type DisplayNames interface {
	CleanDisplayName(name string) (clean string, ok bool)
}

// HandleRules canonicalizes handles (impl: gate/users): NFKC, lowercase, an
// optional leading "@" dropped, then ^[a-z0-9_]{3,20}$ and not reserved. The join
// flow checks a handle before it starts the passkey ceremony.
// Steward: contract request p0-w04-1.
type HandleRules interface {
	CanonHandle(h string) (canon string, ok bool)
}

// Accounts exposes the account facts auth needs inside a transaction
// (impl: gate/users). Steward: contract request p0-w01-1.
type Accounts interface {
	// Account reads the WebAuthn user handle, ext_id and status of id through q
	// (so an account created earlier in the same transaction is visible).
	// forUpdate also locks the row (FOR NO KEY UPDATE) until q's transaction
	// ends, so a status change cannot interleave with the caller's work.
	// gate.ErrNotFound when there is no such account.
	Account(ctx context.Context, q store.Q, id UserID, forUpdate bool) (webauthnID []byte, ext uuid.UUID, status uint8, err error)
	// ActivateGenesis turns a pending genesis account active and records the
	// accepted terms version and the age attestation at at (D29). gate.ErrNotFound
	// unless the account is a pending genesis account. The caller publishes
	// UserChanged after commit.
	ActivateGenesis(ctx context.Context, tx store.Q, id UserID, tosVersion int, at time.Time) error
}

// Suspensions suspends accounts on a confirmed two-person moderator action
// (impl: gate/users). Steward: contract request p0-w12-1.
type Suspensions interface {
	// Suspend turns the active account id suspended (users.status 0 → 2) in tx;
	// gate.ErrNotFound unless it is active. The caller publishes UserChanged
	// after commit (the auth middleware already refuses non-active accounts'
	// sessions).
	Suspend(ctx context.Context, tx store.Q, id UserID) error
}

// ---- graph (impl: gate/graph) ----

// Edge is one adjacency entry.
type Edge struct {
	To    UserID
	Level EdgeLevel
	Since time.Time
}

// Exclusions are the READER's own blocks and hide-vias only (D7).
type Exclusions struct {
	Blocked   *roaring.Bitmap
	HiddenVia *roaring.Bitmap
}

// Search is the result of one BFS from a reader.
type Search interface {
	Viewer() UserID
	Epoch() uint64
	Dist(u UserID) (int, bool) // exact shortest distance avoiding the reader's exclusions; (0,true) for self
	FirstHop(u UserID) UserID  // chain's first hop (0 if dist ≤ 1), chosen by the D22 rule
	FirstHopPlus(u UserID) bool
	Path(u UserID) []UserID // a shortest path viewer→u (inclusive) consistent with FirstHop
	Layer(k int) []UserID   // exact set at distance k (sorted)
	LayerBitmap(k int) *roaring.Bitmap
	MaxLayer() int
}

// Graph is the in-memory friendship graph.
type Graph interface {
	Epoch() uint64
	Friends(u UserID) []Edge // sorted by (level desc, since asc, id asc)
	Level(a, b UserID) (EdgeLevel, bool)
	Degree(u UserID) int
	Search(ctx context.Context, v UserID, ex Exclusions) (Search, error)
	// Mutations: called AFTER commit by the table owner (plan §3.4).
	AddEdge(a, b UserID, lvl EdgeLevel, since time.Time)
	RemoveEdge(a, b UserID)                    // bumps epoch
	SetBlock(blocker, blocked UserID, on bool) // bumps epoch on add
	SetHideVia(user, via UserID, on bool)      // P1; bumps epoch on add
	Exclusions(v UserID) Exclusions
	Blocks(a, b UserID) bool // a has blocked b (one direction)
	Load(ctx context.Context) error
}

// Edges is the sole writer of edges/edge_events (impl: gate/friends).
type Edges interface {
	Create(ctx context.Context, tx store.Q, a, b UserID, lvl EdgeLevel, recordHash []byte) error
	Revoke(ctx context.Context, tx store.Q, a, b UserID, by UserID) error
	Upgrade(ctx context.Context, tx store.Q, a, b UserID, recordHash []byte) error // P1
	Count(ctx context.Context, u UserID) (edges, outgoingPending int, err error)   // cap accounting (D23)
}

// Mutes lists a reader's muted authors (impl: gate/friends).
type Mutes interface {
	MutedAuthors(ctx context.Context, v UserID) map[UserID]struct{}
}

// RequestStates exposes the friend-request state between v and u as v sees it
// (impl: gate/friends): "outgoing" (v asked u; a declined request still reads
// "outgoing"), "incoming" (u asked v, pending) or "".
// Steward: contract request p0-w02-1.
type RequestStates interface {
	RequestState(ctx context.Context, v, u UserID) string
}

// ---- posts index (impl: gate/postindex) ----

// PostMeta is the ranking/visibility metadata of a post (no body).
type PostMeta struct {
	ID             PostID
	Author         UserID
	CreatedAt      time.Time
	Audience       Audience
	AudienceForced bool
	RefPolicy      RefPolicy
	IsReply        bool   // has a ref parent (thread: head part has one)
	ContParent     PostID // uuid.Nil if none
	ContChild      PostID
	Deleted        bool
	Version        int
	EditedAt       *time.Time
	Lang           string
}

// LinkMeta is one parent link.
type LinkMeta struct {
	Child, Parent             PostID
	Kind                      LinkKind
	ChildAuthor, ParentAuthor UserID
	Detached, Shadow          bool
	CreatedAt                 time.Time
}

// PostIndex is the in-memory post index (§4.2).
type PostIndex interface {
	Meta(ctx context.Context, id PostID) (PostMeta, bool)
	MetaMany(ctx context.Context, ids []PostID) map[PostID]PostMeta
	Recent(author UserID) []PostID // window posts, newest first
	RecentLen(author UserID) int
	WindowScan(fn func(PostMeta) bool) // newest first over all window posts (time scan)
	WindowSize() int
	Children(ctx context.Context, parent PostID, fn func(LinkMeta) bool) // newest first, ALL links; RAW (archtest A10)
	Parents(ctx context.Context, child PostID) []LinkMeta                // RAW (archtest A10)
	Bodies(ctx context.Context, ids []PostID) map[PostID]string
	// Mutations (posts module only).
	Put(m PostMeta)
	Delete(id PostID)
	PutLink(l LinkMeta)
	UpdateLink(l LinkMeta)
}

// MentionIndex resolves a post's stored mentions (impl: gate/postindex). The map is
// ord (index of the @handle occurrence in the body) → target; it holds only the
// mentions resolved at write time within the author's horizon (mentions table).
// Steward: contract request p0-w02-1.
type MentionIndex interface {
	MentionTargets(ctx context.Context, p PostID) map[int]UserID
}

// PostAdmin deletes posts on behalf of the system (impl: gate/posts).
// Steward: contract request p0-w11-2.
type PostAdmin interface {
	// DeleteAllBy tombstones every live post of author in tx exactly like
	// posts.Delete (Tombstone record; body and salt NULL) and returns their IDs;
	// the caller runs PostIndex.Delete and publishes PostDeleted after commit.
	DeleteAllBy(ctx context.Context, tx store.Q, author UserID) ([]PostID, error)
}

// PostRemover removes one post on a moderator's decision (impl: gate/posts).
// Steward: contract request p0-w12-1.
type PostRemover interface {
	// RemovePost tombstones the live post id in tx exactly like posts.Delete
	// (Tombstone record signed with by as the actor; body, salt and mentions
	// hard-deleted) and returns its author; ok is false when the post is not
	// live. The caller runs PostIndex.Delete and publishes PostDeleted after
	// commit.
	RemovePost(ctx context.Context, tx store.Q, id PostID, by UserID) (author UserID, ok bool, err error)
}

// ---- visibility (impl: gate/visibility) ----

// Checked is a capability: a post the viewer was verified to see. Only
// visibility (and coreref) may call MintChecked (archtest A10).
type Checked struct {
	meta   PostMeta
	viewer UserID
	epoch  uint64
}

// MintChecked builds a Checked. Restricted by archtest A10.
func MintChecked(m PostMeta, v UserID, epoch uint64) Checked {
	return Checked{meta: m, viewer: v, epoch: epoch}
}

// Meta returns the checked post's metadata.
func (c Checked) Meta() PostMeta { return c.meta }

// Viewer returns the viewer the check was made for.
func (c Checked) Viewer() UserID { return c.viewer }

// Epoch returns the graph epoch of the check.
func (c Checked) Epoch() uint64 { return c.epoch }

// VisibleLink is a link the viewer may see; Other is the child (VisibleChildren)
// or the parent (VisibleParents).
type VisibleLink struct {
	Link  LinkMeta
	Other Checked
}

// ChildFilter filters VisibleChildren.
type ChildFilter struct {
	RefOnly bool // kind == ref
	NotSelf bool // child author ≠ parent author
	Limit   int  // newest-first, applied AFTER every other filter; 0 = no limit
}

// Visibility implements §2.2, §4 and §10.1.3.
type Visibility interface {
	SearchFor(ctx context.Context, v UserID) (Search, error)
	CanSeeUser(s Search, u UserID) bool
	CheckPost(ctx context.Context, s Search, p PostMeta) (Checked, bool)
	CheckPosts(ctx context.Context, s Search, ps []PostMeta) []Checked
	CanSeeLink(ctx context.Context, s Search, l LinkMeta, child, parent PostMeta) bool
	VisibleChildren(ctx context.Context, s Search, parent PostMeta, f ChildFilter) []VisibleLink
	VisibleParents(ctx context.Context, s Search, child PostMeta) []VisibleLink
	CanReference(ctx context.Context, s Search, parent PostMeta, kind LinkKind) bool
}

// Chains builds §4.5 chains (impl: gate/visibility).
type Chains interface {
	Chain(ctx context.Context, s Search, u UserID) (gate.ChainView, error)
}

// ---- engagement (impl: gate/engage) ----

// Engagement exposes per-post and per-reader engagement state.
type Engagement interface {
	LikerSet(ctx context.Context, p PostID) *roaring.Bitmap // read-only snapshot
	SaverSet(ctx context.Context, p PostID) *roaring.Bitmap // P1
	LikedByMe(ctx context.Context, v UserID, ps []PostID) map[PostID]bool
	SeenSet(ctx context.Context, v UserID, since time.Time) map[PostID]struct{}
	HiddenSet(ctx context.Context, v UserID) map[PostID]struct{}
	ShowLessCounts(ctx context.Context, v UserID) map[UserID]int
}

// Prefer computes network preference (§5.2/§5.5) for ranking.
type Prefer interface {
	// N is n for post p as seen from s, memoized per (snapshot, post).
	N(ctx context.Context, s Search, p Checked, memo *Memo) float64
	// KidsN is n_kid per visible child (VisibleChildren RefOnly, NotSelf, Limit 20,
	// non-shadow); gens=2 adds β·Σ grandchildren (P1, K² cap). A slippery parent
	// (PolNobody) returns nil.
	KidsN(ctx context.Context, s Search, p Checked, memo *Memo, gens int) []float64
}

// ---- views (impl: gate/views) ----

// ViewOpts selects optional parts of a PostView.
type ViewOpts struct {
	WithBody    bool
	WithContext bool
}

// Views builds API views. Posts accepts only Checked input (A10).
type Views interface {
	Posts(ctx context.Context, s Search, ps []Checked, o ViewOpts) ([]gate.PostView, error)
	Person(ctx context.Context, s Search, u UserID) (gate.PersonView, error) // ErrNotFound unless CanSeeUser
	Author(ctx context.Context, s Search, u UserID) (gate.AuthorView, error)
}

// ---- records (impl: gate/records) ----

// RecordKind is a signed record kind.
type RecordKind uint8

// Record kinds (signed_records.kind).
const (
	RecInvite        RecordKind = 1
	RecFriendship    RecordKind = 2
	RecRevocation    RecordKind = 3
	RecPost          RecordKind = 4
	RecTombstone     RecordKind = 5
	RecBlock         RecordKind = 6
	RecReport        RecordKind = 7
	RecPasskeyChange RecordKind = 8
	RecModAction     RecordKind = 9
	// Steward: contract request p1-w10-1 (co-signed deletion and recovery).
	RecAccountDelete    RecordKind = 10
	RecRecoveryApproval RecordKind = 11
)

// String returns the record body "kind" value.
func (k RecordKind) String() string {
	switch k {
	case RecInvite:
		return "invite"
	case RecFriendship:
		return "friendship"
	case RecRevocation:
		return "revocation"
	case RecPost:
		return "post"
	case RecTombstone:
		return "tombstone"
	case RecBlock:
		return "block"
	case RecReport:
		return "report"
	case RecPasskeyChange:
		return "passkey_change"
	case RecModAction:
		return "mod_action"
	case RecAccountDelete:
		return "account_delete"
	case RecRecoveryApproval:
		return "recovery_approval"
	}
	return ""
}

// RecordSchemaVersion is the schema_version of every record body in Phase 0/1.
const RecordSchemaVersion = 1

// Assertion is a WebAuthn assertion attached to a record (P1 co-signing).
type Assertion struct {
	CredentialID, AuthenticatorData, ClientDataJSON, Signature []byte
	User                                                       UserID
	// Draft is the exact co-sign draft this assertion signed (Signer.Draft
	// bytes: the record body plus its "cosign" {nonce, issued_at} member).
	// Bundle, when set, lists the challenges of every draft one assertion
	// signed together (the signup ceremony); clientDataJSON.challenge is then
	// BundleChallenge(Bundle). Cosign fills both; Signer stores them with the
	// record so the assertion verifies offline. Steward: contract request p1-w01-1.
	Draft  []byte
	Bundle [][]byte
}

// Signer writes signed records.
type Signer interface {
	// Sign canonicalizes body (sigs value grammar; "kind" and "schema_version" are
	// injected), signs it (P0: server key; P1: author key + server countersign) and
	// stores it with its parties. Returns the record hash.
	Sign(ctx context.Context, tx store.Q, kind RecordKind, author UserID, body any, parties []UserID, a ...Assertion) (hash []byte, err error)
	// Draft returns the canonical bytes and passkey challenge of a co-sign draft (P1).
	Draft(kind RecordKind, body any, nonce uuid.UUID, issuedAt time.Time) (canon []byte, challenge [32]byte, err error)
	SetPurgeAfter(ctx context.Context, tx store.Q, hash []byte, at time.Time) error // §2.3 revoked-edge retention
	Delete(ctx context.Context, tx store.Q, hash []byte) error                      // Block record on unblock
}

// RecordExporter produces the signed part of an export (impl: gate/records):
// records.jsonl (the exact stored envelopes of every record user is a party to,
// never Report records of others) and keys.json ({"server": [KeyView]}).
// Steward: contract request p0-w11-1.
type RecordExporter interface {
	ExportRecords(ctx context.Context, q store.Q, user UserID) (recordsJSONL, keysJSON []byte, err error)
}

// ---- passkeys / sessions (impl: gate/auth) ----

// Credential is a verified WebAuthn credential.
type Credential struct {
	ID, PublicKey               []byte
	Alg                         int
	AAGUID                      []byte
	SignCount                   uint32
	Transports                  []string
	BackupEligible, BackupState bool
	UV                          bool
	AttestationFmt              string
}

// PendingUser is a not-yet-created user registering a passkey.
type PendingUser struct {
	WebAuthnID          []byte
	Handle, DisplayName string
	Exclude             [][]byte
}

// Passkeys runs WebAuthn ceremonies and owns sessions/credentials.
type Passkeys interface {
	BeginRegistration(ctx context.Context, sess [32]byte, u PendingUser) (json.RawMessage, error)
	FinishRegistration(ctx context.Context, sess [32]byte, body []byte) (Credential, error) // not yet bound to a user row
	BindCredential(ctx context.Context, tx store.Q, user UserID, c Credential) error        // also writes the PasskeyChange record
	Upgrade(ctx context.Context, tx store.Q, sess [32]byte, user UserID, cred []byte) (newToken string, err error)
	// Assertions over a server challenge, restricted to the VIEWER's own credentials
	// (security #9). Used by step-up (P0) and co-signing (P1).
	BeginAssertion(ctx context.Context, v Viewer, challenge []byte, purpose string) (json.RawMessage, error)
	VerifyAssertion(ctx context.Context, v Viewer, purpose string, body []byte) (Assertion, error)
	IssueEnrollment(ctx context.Context, tx store.Q, user UserID, purpose uint8, usableFrom time.Time) (token string, err error)
	RevokeAll(ctx context.Context, tx store.Q, user UserID) error
}

// Enrollments cancels issued enrollment tokens (impl: gate/auth).
// Steward: contract request p0-w11-2.
type Enrollments interface {
	// CancelEnrollment voids the unused token whose sha256 is tokenHash.
	CancelEnrollment(ctx context.Context, tx store.Q, tokenHash []byte) error
}

// ---- invites (impl: gate/invites) ----

// Invites exposes the invite tree.
type Invites interface {
	InviteesOf(ctx context.Context, inviter UserID) ([]UserID, error)
	Subtree(ctx context.Context, root UserID) (*roaring.Bitmap, error)
}

// Probation lets moderation reset probation (P1 quarantine).
type Probation interface {
	Reset(ctx context.Context, tx store.Q, users []UserID, until time.Time) error
}

// ---- notifications (impl: gate/notify) ----

// Event is a notification to persist.
type Event struct {
	To          UserID
	Kind        string
	GroupKey    string
	SubjectUser UserID
	SubjectPost PostID
	Data        map[string]any // codes and numbers only
}

// Notifier persists and delivers notifications.
type Notifier interface {
	// Notify persists (batching on GroupKey) and pings SSE after commit. It DROPS
	// the event if e.To has blocked e.SubjectUser (D7).
	Notify(ctx context.Context, tx store.Q, e Event) error
	Live(to UserID, topic string, data any) // ephemeral SSE event
}

// ---- metrics (impl: gate/metrics) — aggregates only, no IDs ----

// Metrics records aggregate product metrics.
type Metrics interface {
	Inc(name, bucket string, n int64) // name/bucket from the closed list in gate/metrics
	ObserveMs(name string, ms int64)
}

// ---- limits (impl: gate/limits) ----

// ReadClass is a read budget class.
type ReadClass string

// WriteKind is a write limit kind.
type WriteKind string

// Limiter enforces read budgets and human-pace write limits (§17).
type Limiter interface {
	ChargeRead(ctx context.Context, v Viewer, c ReadClass) error         // gate.ErrRateLimited
	ChargeWrite(ctx context.Context, v Viewer, k WriteKind, n int) error // gate.ErrRateLimited
	ChargeAnon(ip netip.Prefix, bucket string) error                     // unauthenticated surface (memory only)
	// WriteAllowance reports what is left of a build-up write limit of kind k
	// (ok false when k has none).
	WriteAllowance(ctx context.Context, v Viewer, k WriteKind) (a Allowance, ok bool, err error)
}

// Allowance is what is left of a build-up limit: Available of Cap now, and
// when the next one builds up (nil while full).
type Allowance struct {
	Available, Cap int
	NextAt         *time.Time
}
