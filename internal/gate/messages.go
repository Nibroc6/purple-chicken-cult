package gate

import "time"

// Direct-message views (gate/dm). A conversation lists only its members, and a
// member is shown to the others only once they joined; an invitation shows
// the invitee only who invited them, the title and the member count. A shared
// post is rendered for each viewer from their own horizon: someone who could
// not already see it gets Unavailable and nothing else about it.

// ConvMember is a member of a conversation as another member sees them.
type ConvMember struct {
	Ref         Ref    `json:"ref"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin,omitempty"`
	// Relation is the viewer's own edge with the member: "friend" (an
	// acquaintance), "friend_plus" (a friend), or "" when not connected.
	Relation string `json:"relation,omitempty"`
}

// MessagePreview is the last message of a conversation in the list.
type MessagePreview struct {
	Kind    string    `json:"kind"` // "text" | "post" | "event" | "deleted"
	Excerpt string    `json:"excerpt,omitempty"`
	Event   string    `json:"event,omitempty"`
	FromMe  bool      `json:"from_me"`
	From    string    `json:"from,omitempty"` // the sender's handle (others' messages)
	At      time.Time `json:"at"`
}

// ConversationView is a conversation as one member (or invitee) sees it.
type ConversationView struct {
	Ref         Ref             `json:"ref"`
	Kind        string          `json:"kind"`  // "direct" | "group"
	State       string          `json:"state"` // "member" | "invited"
	Title       string          `json:"title,omitempty"`
	Members     []ConvMember    `json:"members"` // the others (invited: only who invited you)
	MemberCount int             `json:"member_count"`
	InvitedBy   *ConvMember     `json:"invited_by,omitempty"`
	Admin       bool            `json:"admin,omitempty"`
	CanSend     bool            `json:"can_send"`
	Muted       bool            `json:"muted,omitempty"`
	Unread      int             `json:"unread"`
	Last        *MessagePreview `json:"last,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// SharedPostView is a post shared into a conversation, for one viewer.
type SharedPostView struct {
	Post        *PostView `json:"post,omitempty"`
	Unavailable bool      `json:"unavailable,omitempty"`
}

// MessageView is one message as a member sees it.
type MessageView struct {
	Ref     Ref             `json:"ref"`
	Kind    string          `json:"kind"` // "text" | "event" | "deleted"
	Mine    bool            `json:"mine"`
	From    *ConvMember     `json:"from,omitempty"` // nil for the viewer's own and for a deleted account
	Body    string          `json:"body,omitempty"`
	Post    *SharedPostView `json:"post,omitempty"`
	Event   string          `json:"event,omitempty"`   // created | joined | left | removed | renamed
	Subject *ConvMember     `json:"subject,omitempty"` // who joined, left or was removed
	At      time.Time       `json:"at"`
}

// MessagesPage is a page of messages, newest first; Next pages to older ones.
type MessagesPage struct {
	Items []MessageView `json:"items"`
	Next  string        `json:"next,omitempty"`
}

// ModMessageView is a reported direct message as a moderator sees it: the
// text the reporter saw (kept with the report) and whether it is gone now.
// Nothing else of the conversation.
type ModMessageView struct {
	Body       string    `json:"body"`
	SharedPost bool      `json:"shared_post,omitempty"`
	At         time.Time `json:"at"`
	Removed    bool      `json:"removed,omitempty"` // deleted by its sender or removed by a moderator since
}
