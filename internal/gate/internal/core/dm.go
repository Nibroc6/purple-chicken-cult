package core

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Direct messages (impl: gate/dm). Steward: contract request dm-1.

// Message is one direct message as moderation needs it.
type Message struct {
	ID         uuid.UUID
	Conv       uuid.UUID
	Sender     UserID
	Body       string // "" once deleted
	SharedPost PostID // uuid.Nil if none
	At         time.Time
	Deleted    bool
}

// Messages lets moderation resolve and act on reported messages (impl: gate/dm).
type Messages interface {
	// ForReport opens message ref for reporter v: another member's message,
	// not deleted, in a conversation v is a member of and has not cleared past
	// it. ok is false otherwise (the report is then dropped silently).
	ForReport(ctx context.Context, v UserID, ref gate.Ref) (Message, bool)
	// MessageByID returns a message for a moderator (ok false once purged).
	MessageByID(ctx context.Context, id uuid.UUID) (Message, bool)
	// RemoveMessage erases the body of message id in tx on a moderator's
	// decision (it reads "deleted" for everyone) and returns its sender; ok is
	// false when it was already deleted or purged.
	RemoveMessage(ctx context.Context, tx store.Q, id uuid.UUID) (sender UserID, ok bool, err error)
}

// PushText is one push message to render in the recipient's locale at
// delivery: the catalog key push.<Key> with Args (codes, numbers and handles
// only), the Tag that replaces an older push of the same tag on the device,
// and the URL a click opens. Never any message or post text.
type PushText struct {
	Key  string
	Args map[string]any
	Tag  string
	URL  string
}

// MessagePush queues pushes outside the notifications list (impl: gate/notify).
type MessagePush interface {
	// PushText enqueues p for to in q (the sending transaction) when push is
	// on and to has a subscription; a subscription with hide_content gets the
	// generic text. A rolled-back message is never pushed.
	PushText(ctx context.Context, q store.Q, to UserID, p PushText) error
}
