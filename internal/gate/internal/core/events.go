package core

import "time"

// Bus event types (contract; all fields raw IDs). Adding a type or a field is an
// additive steward change. Events are published synchronously AFTER commit.

// EdgeAdded: a friendship was created.
type EdgeAdded struct {
	A, B  UserID
	Level EdgeLevel
	At    time.Time
}

// EdgeRemoved: a friendship was revoked.
type EdgeRemoved struct {
	A, B UserID
	By   UserID
	At   time.Time
}

// EdgeUpgraded: friend → friend+ (P1).
type EdgeUpgraded struct {
	A, B UserID
	At   time.Time
}

// BlockChanged: a block was added or removed.
type BlockChanged struct {
	Blocker, Blocked UserID
	On               bool
}

// HideViaChanged: a hide-via was added or removed (P1).
type HideViaChanged struct {
	User, Via UserID
	On        bool
}

// PostCreated: a post (with its links) was created.
type PostCreated struct {
	Post  PostMeta
	Links []LinkMeta
}

// PostEdited: a post got a new version.
type PostEdited struct {
	ID      PostID
	Version int
}

// PostDeleted: a post was tombstoned.
type PostDeleted struct {
	ID     PostID
	Author UserID
}

// LinkChanged: a link was detached or changed by a ref-policy change.
type LinkChanged struct{ Link LinkMeta }

// UserChanged: settings, display name or status changed.
type UserChanged struct{ ID UserID }

// ProbationCleared: a user left probation.
type ProbationCleared struct {
	ID UserID
	At time.Time
}

// Liked: a like was added or removed.
type Liked struct {
	User UserID
	Post PostID
	On   bool
}

// SessionEnded: logout, expiry, revocation or deletion. SSE closes; P1 push
// unsubscribes that session.
type SessionEnded struct {
	User    UserID
	Session [32]byte
}

// AccountDeleted: every owner deletes its own rows for the user.
type AccountDeleted struct{ ID UserID }

// SessionStepUp: a session must step up (P1).
type SessionStepUp struct {
	User    UserID
	Session [32]byte
}
