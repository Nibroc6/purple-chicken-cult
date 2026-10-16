package gate

import (
	"encoding/json"
	"time"
)

// Views are built by gate services from the viewer's horizon H(V) (ARCHITECTURE
// §2.2) and are the only data handlers can return. JSON field names are the API
// contract (api/openapi/_common.yaml mirrors them; web/src/vm maps them).

// InviterLabel is the display-only "invited by" tag (§3). Deliberately no ref.
type InviterLabel struct {
	Kind   string `json:"kind"`             // "user" | "genesis" | "deleted"
	Handle string `json:"handle,omitempty"` // display-only
}

// ChainHop names one person in a chain.
type ChainHop struct {
	Ref    Ref    `json:"ref"`
	Handle string `json:"handle"`
}

// ChainView is the shortest-path chain from the viewer to a person (§4.5).
type ChainView struct {
	Dist         int        `json:"dist"`                // 0 self, 1 friend, k ≥ 2
	FirstHop     *ChainHop  `json:"first_hop,omitempty"` // nil when dist ≤ 1
	Middle       []ChainHop `json:"middle,omitempty"`    // only when every named edge's endpoints opted in
	HiddenMiddle int        `json:"hidden_middle"`       // collapsed count ("2 others")
	FirstHopPlus bool       `json:"first_hop_plus"`      // P1: first edge is friend+
}

// PersonView is a person as seen by the viewer.
type PersonView struct {
	Ref         Ref          `json:"ref"`
	Handle      string       `json:"handle"`
	DisplayName string       `json:"display_name"`
	Inviter     InviterLabel `json:"inviter"`
	Relation    string       `json:"relation"`          // "self" | "friend" | "friend_plus" | "connected"
	Request     string       `json:"request,omitempty"` // "outgoing" | "incoming" (a declined request still reads "outgoing")
	Chain       ChainView    `json:"chain"`
	Blocked     bool         `json:"blocked,omitempty"` // only ever true on the BLOCKER's own view
	Muted       bool         `json:"muted,omitempty"`
	HiddenVia   bool         `json:"hidden_via,omitempty"` // P1
}

// AuthorView is a compact person for cards.
type AuthorView struct {
	Ref         Ref          `json:"ref"`
	Handle      string       `json:"handle"`
	DisplayName string       `json:"display_name"`
	Inviter     InviterLabel `json:"inviter"`
	Chain       ChainView    `json:"chain"`
}

// ContextRow is a visible parent link (§10.1.2).
type ContextRow struct {
	Ref          Ref    `json:"ref"`
	Kind         string `json:"kind"` // "ref" | "cont"
	AuthorHandle string `json:"author_handle"`
	Excerpt      string `json:"excerpt"` // ≤80 runes
}

// ThreadPos is a part's position over the VISIBLE parts of a thread.
type ThreadPos struct {
	Index int `json:"index"`
	Total int `json:"total"`
}

// PreviewView is a server-built link preview (§10.1.6).
type PreviewView struct {
	URL         string     `json:"url"`
	Host        string     `json:"host"`
	HostUnicode string     `json:"host_unicode"`
	Lookalike   bool       `json:"lookalike"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Image       *MediaView `json:"image,omitempty"` // P1
}

// MentionView resolves the ord-th @handle in a body; no ref ⇒ render as plain text.
type MentionView struct {
	Ord int `json:"ord"`
	Ref Ref `json:"ref,omitempty"`
}

// LikeSummary is the in-network like count (§5.3).
type LikeSummary struct {
	InNetwork int        `json:"in_network"`      // likers within 2 hops of the viewer
	Names     []ChainHop `json:"names,omitempty"` // viewer's friends who allow naming (≤3)
	Others    int        `json:"others"`
	ByMe      bool       `json:"by_me"`
}

// MediaView is an attached image (P1); its URL is fetched separately.
type MediaView struct {
	Ref Ref    `json:"ref"`
	W   int    `json:"w"`
	H   int    `json:"h"`
	Alt string `json:"alt,omitempty"`
}

// PostView is a post as seen by the viewer.
type PostView struct {
	Ref            Ref           `json:"ref"`
	Author         AuthorView    `json:"author"`
	Tier           int           `json:"tier"` // = Author.Chain.Dist
	Body           string        `json:"body"` // Markdown source; "" when not loaded (tree overflow)
	Lang           string        `json:"lang"`
	CreatedAt      time.Time     `json:"created_at"`
	EditedAt       *time.Time    `json:"edited_at,omitempty"`
	Audience       string        `json:"audience"`                  // "connected" | "d1" | "d2" | "d3" | "friend_plus"(P1)
	AudienceForced bool          `json:"audience_forced,omitempty"` // set ONLY on the author's own view
	RefPolicy      string        `json:"ref_policy"`                // "anyone" | "two_hops" | "friends" | "nobody"
	CanReference   bool          `json:"can_reference"`
	Mine           bool          `json:"mine"`
	Context        []ContextRow  `json:"context,omitempty"`
	Thread         *ThreadPos    `json:"thread,omitempty"`
	Previews       []PreviewView `json:"previews,omitempty"`
	Mentions       []MentionView `json:"mentions,omitempty"`
	Likes          LikeSummary   `json:"likes"`
	SavedByMe      bool          `json:"saved_by_me,omitempty"` // P1
	ReplyCount     int           `json:"reply_count"`           // visible links only
	Media          []MediaView   `json:"media,omitempty"`       // P1
}

// FeedItem is one entry of a feed page. Headers come only from these markers.
type FeedItem struct {
	Type      string     `json:"type"` // "tier_start" | "post" | "thread" | "collapsed" | "tier_end" | "caught_up"
	Tier      int        `json:"tier,omitempty"`
	Post      *PostView  `json:"post,omitempty"`
	Thread    []PostView `json:"thread,omitempty"` // earlier visible parts (collapsed context) …, last = ranked part
	Collapsed *Collapsed `json:"collapsed,omitempty"`
}

// Collapsed groups distant low-trust posts (§4.4).
type Collapsed struct {
	Count  int    `json:"count"`
	Token  string `json:"token"`
	Reason string `json:"reason"` // "low_trust_distant"
}

// FeedPage is one page of the tiered feed.
type FeedPage struct {
	Session string     `json:"session"` // random 128-bit, keyed by (viewer, session)
	Preset  string     `json:"preset"`  // "latest" | "balanced" | "top"
	Items   []FeedItem `json:"items"`
	Next    string     `json:"next,omitempty"` // empty ⇔ caught up
}

// WhyView explains a post's placement (§5.3).
type WhyView struct {
	Tier         int       `json:"tier"`
	Via          ChainView `json:"via"`
	PostedAt     time.Time `json:"posted_at"`
	Preset       string    `json:"preset"`
	ShiftMinutes int       `json:"shift_minutes"`
	Reasons      []string  `json:"reasons"` // network_likes, below_author_usual, reply_penalty, good_replies, cold_start, show_less, chronological
}

// TreeItem is one post in a family-tree row.
type TreeItem struct {
	Post          PostView   `json:"post"`
	TierDivider   bool       `json:"tier_divider,omitempty"`
	ShownAbove    bool       `json:"shown_above,omitempty"`
	AlsoRepliesTo []ChainHop `json:"also_replies_to,omitempty"`
	AlsoParents   []Ref      `json:"also_parents,omitempty"`
}

// TreeRow is one generation of the family tree (§10.1.4).
type TreeRow struct {
	Gen    int        `json:"gen"`   // −k … +k relative to the focal row (0)
	Label  string     `json:"label"` // "earlier" | "replying_to" | "this_post" | "replies" | "replies_to_replies" | "generations_down" | "generations_up"
	Center Ref        `json:"center"`
	Items  []TreeItem `json:"items"` // ranked; Items[0] is the centered post
	More   int        `json:"more"`  // visible items beyond those returned
	Cursor string     `json:"cursor,omitempty"`
	// Beyond (rows loaded by Row, gen ≠ 0): the row's first post has a further
	// visible row away from the focal row (parents for gen < 0, replies for
	// gen > 0), so the client knows whether to offer "earlier" / "later".
	Beyond bool `json:"beyond,omitempty"`
}

// TreeView is the opened family tree.
type TreeView struct {
	Snapshot   string    `json:"snapshot"`
	Focal      Ref       `json:"focal"`
	Rows       []TreeRow `json:"rows"`
	HasEarlier bool      `json:"has_earlier"`
	HasLater   bool      `json:"has_later"`
}

// MeView is the signed-in user.
type MeView struct {
	Ref         Ref          `json:"ref"`
	Handle      string       `json:"handle"`
	DisplayName string       `json:"display_name"`
	Role        string       `json:"role"` // "member" | "moderator" | "admin"
	Inviter     InviterLabel `json:"inviter"`
	Settings    SettingsView `json:"settings"`
}

// SettingsView is the user's settings.
type SettingsView struct {
	UILocale        string          `json:"ui_locale"`
	Languages       []string        `json:"languages"`
	TranslateTo     string          `json:"translate_to,omitempty"`
	Autotranslate   bool            `json:"autotranslate"`
	RankPreset      string          `json:"rank_preset"`
	NameInChains    bool            `json:"name_in_chains"`
	Suggestible     bool            `json:"suggestible"`
	NamedInLikes    bool            `json:"named_in_likes"`
	SlipperyDefault bool            `json:"slippery_default"`
	UIPrefs         json.RawMessage `json:"ui_prefs"`
}

// SessionView describes the caller's session.
type SessionView struct {
	Authenticated bool    `json:"authenticated"`
	Kind          string  `json:"kind"` // "none" | "guest" | "user" | "enrollment"
	CSRF          string  `json:"csrf,omitempty"`
	Me            *MeView `json:"me,omitempty"`
	StepUp        bool    `json:"step_up,omitempty"`
}

// NotificationView is one notification (codes and numbers only).
type NotificationView struct {
	ID     Ref            `json:"id"`
	Kind   string         `json:"kind"`
	Count  int            `json:"count"`
	Person *ChainHop      `json:"person,omitempty"`
	Post   *Ref           `json:"post,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
	At     time.Time      `json:"at"`
	Read   bool           `json:"read"`
}

// BudgetView is the caller's own invite budget (§8).
type BudgetView struct {
	Available   int            `json:"available"`
	Cap         int            `json:"cap"`
	NextAt      *time.Time     `json:"next_at,omitempty"`
	Probation   *ProbationView `json:"probation,omitempty"`
	PausedUntil *time.Time     `json:"paused_until,omitempty"`
	Revoked     bool           `json:"revoked,omitempty"`
}

// ProbationView shows what is left of the caller's probation.
type ProbationView struct {
	Until              time.Time `json:"until"`
	NeedOutsideFriends int       `json:"need_outside_friends"`
}

// HandshakeView opens an in-person handshake (invites P0; friend+ upgrade and recovery P1).
type HandshakeView struct {
	HS         Ref    `json:"hs"`
	Mode       string `json:"mode"` // "qr" | "qr_audio" | "dual_qr"
	FrameTTLms int    `json:"frame_ttl_ms"`
	FrameKey   string `json:"frame_key,omitempty"` // P1: base64url key for client-rendered 250 ms frames
}

// HandshakeStatus is the state of a handshake for either side.
type HandshakeStatus struct {
	State string `json:"state"`         // "open" | "claimed" | "confirmed" | "done" | "cancelled" | "expired"
	SAS   string `json:"sas,omitempty"` // 6 digits, shown on BOTH screens once claimed
}

// FrameView is one rotating QR frame.
type FrameView struct {
	Payload    string    `json:"payload"`
	ValidUntil time.Time `json:"valid_until"`
}

// InviteeView is one of the inviter's own invitees (§8: probation visible to the inviter).
type InviteeView struct {
	Person         PersonView `json:"person"`
	JoinedAt       time.Time  `json:"joined_at"`
	OnProbation    bool       `json:"on_probation"`
	ProbationUntil *time.Time `json:"probation_until,omitempty"`
}

// Page is a cursor-paginated list.
type Page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

// ---- service DTOs used across the API ----

// SettingsPatch is PATCH /api/me/settings; nil = unchanged.
type SettingsPatch struct {
	UILocale        *string          `json:"ui_locale,omitempty"`
	Languages       *[]string        `json:"languages,omitempty"`
	TranslateTo     *string          `json:"translate_to,omitempty"`
	Autotranslate   *bool            `json:"autotranslate,omitempty"`
	RankPreset      *string          `json:"rank_preset,omitempty"`
	NameInChains    *bool            `json:"name_in_chains,omitempty"`
	Suggestible     *bool            `json:"suggestible,omitempty"`
	NamedInLikes    *bool            `json:"named_in_likes,omitempty"`
	SlipperyDefault *bool            `json:"slippery_default,omitempty"`
	UIPrefs         *json.RawMessage `json:"ui_prefs,omitempty"`
	DisplayName     *string          `json:"display_name,omitempty"`
}

// KeyView is a published signing key.
type KeyView struct {
	KeyID       string     `json:"key_id"`
	Alg         string     `json:"alg"`
	PublicKey   string     `json:"public_key"`  // base64url
	Fingerprint string     `json:"fingerprint"` // hex sha256(pubkey)
	ValidFrom   time.Time  `json:"valid_from"`
	ValidUntil  *time.Time `json:"valid_until,omitempty"`
}

// VerifyReport is the result of verifying an export offline.
type VerifyReport struct {
	Records       int             `json:"records"`
	Valid         int             `json:"valid"`
	Failures      []VerifyFailure `json:"failures,omitempty"`
	KeysPinned    bool            `json:"keys_pinned"`
	BodiesChecked int             `json:"bodies_checked"`
}

// VerifyFailure is one failing export line.
type VerifyFailure struct {
	Line int    `json:"line"`
	Code string `json:"code"` // bad_sig, kind_mismatch, not_canonical, unknown_key, key_window, commit_mismatch, bad_assertion,
	// P1 (contract request p1-w01-1): bad_cosig, missing_assertion, broken_chain
}

// PushSubKeys holds a push subscription's keys.
type PushSubKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// PushSub is a browser push subscription (P1).
type PushSub struct {
	Endpoint    string      `json:"endpoint"`
	Keys        PushSubKeys `json:"keys"`
	HideContent bool        `json:"hide_content"`
}

// ModItem is one moderation queue entry (moderators only).
type ModItem struct {
	ID               Ref            `json:"id"`
	Source           string         `json:"source"` // report | signal | csam | stepup | invite_cluster
	Priority         int            `json:"priority"`
	Reason           string         `json:"reason"`
	Subject          *ChainHop      `json:"subject,omitempty"` // moderator-scoped ref
	SubjectInviter   *InviterLabel  `json:"subject_inviter,omitempty"`
	SubjectProbation bool           `json:"subject_probation"`
	Post             *PostView      `json:"post,omitempty"` // reported content only; CSAM items NEVER carry content
	Summary          map[string]int `json:"summary,omitempty"`
	At               time.Time      `json:"at"`
	ClaimedByMe      bool           `json:"claimed_by_me"`
	AwaitingSecond   bool           `json:"awaiting_second"`
}

// BlameItem is an inviter's blame-inbox entry (§10.4).
type BlameItem struct {
	ID            Ref       `json:"id"`
	Action        string    `json:"action"`
	Reason        string    `json:"reason"`
	InviteeHandle string    `json:"invitee_handle"`
	Post          *PostView `json:"post,omitempty"` // only if the inviter could already see it
	At            time.Time `json:"at"`
	Read          bool      `json:"read"`
}

// LogItem is one public moderation-log row.
type LogItem struct {
	Day     string      `json:"day"`
	Action  string      `json:"action"`
	Reason  string      `json:"reason"`
	Count   int         `json:"count"`
	Subject *LogSubject `json:"subject,omitempty"`
}

// LogSubject names a log subject: a pseudonym, or a ref+handle for viewers who
// already have that person in their horizon.
type LogSubject struct {
	Pseudonym string `json:"pseudonym,omitempty"`
	Ref       Ref    `json:"ref,omitempty"`
	Handle    string `json:"handle,omitempty"`
}
