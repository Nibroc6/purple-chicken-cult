package coreref

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// MemUsers is a map-based user directory.
type MemUsers struct {
	mu       sync.RWMutex
	byID     map[core.UserID]core.UserMeta
	byHandle map[string]core.UserID
	next     core.UserID
}

var _ core.Users = (*MemUsers)(nil)

// NewMemUsers returns an empty directory.
func NewMemUsers() *MemUsers {
	return &MemUsers{byID: map[core.UserID]core.UserMeta{}, byHandle: map[string]core.UserID{}, next: 1}
}

// Put inserts or replaces a user (reference helper).
func (u *MemUsers) Put(m core.UserMeta) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if old, ok := u.byID[m.ID]; ok {
		delete(u.byHandle, strings.ToLower(old.Handle))
	}
	u.byID[m.ID] = m
	u.byHandle[strings.ToLower(m.Handle)] = m.ID
	if m.ID >= u.next {
		u.next = m.ID + 1
	}
}

// Get returns a user or gate.ErrNotFound.
func (u *MemUsers) Get(_ context.Context, id core.UserID) (core.UserMeta, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	m, ok := u.byID[id]
	if !ok {
		return core.UserMeta{}, gate.ErrNotFound
	}
	return m, nil
}

// GetMany returns every known user among ids.
func (u *MemUsers) GetMany(_ context.Context, ids []core.UserID) (map[core.UserID]core.UserMeta, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make(map[core.UserID]core.UserMeta, len(ids))
	for _, id := range ids {
		if m, ok := u.byID[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

// InviterLabel returns the display-only inviter label of id.
func (u *MemUsers) InviterLabel(_ context.Context, id core.UserID) (gate.InviterLabel, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	m, ok := u.byID[id]
	if !ok {
		return gate.InviterLabel{}, gate.ErrNotFound
	}
	return labelFor(m, u.byID[m.InvitedBy]), nil
}

func labelFor(m, inviter core.UserMeta) gate.InviterLabel {
	if m.Genesis || m.InvitedBy == 0 {
		return gate.InviterLabel{Kind: "genesis"}
	}
	if inviter.ID == 0 || inviter.Status == core.StatusDeleted {
		return gate.InviterLabel{Kind: "deleted"}
	}
	return gate.InviterLabel{Kind: "user", Handle: inviter.Handle}
}

// InviterOf returns id's inviter.
func (u *MemUsers) InviterOf(id core.UserID) (core.UserID, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	m, ok := u.byID[id]
	return m.InvitedBy, ok && m.InvitedBy != 0
}

// ByHandle resolves a canonical handle (internal only).
func (u *MemUsers) ByHandle(_ context.Context, canon string) (core.UserID, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	id, ok := u.byHandle[canon]
	return id, ok
}

// Create adds a user; a taken handle is Problem handle_taken.
func (u *MemUsers) Create(_ context.Context, _ store.Q, n core.NewUser) (core.UserID, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	canon := strings.ToLower(n.Handle)
	if _, taken := u.byHandle[canon]; taken {
		return 0, gate.NewProblem(gate.StatusConflict, "handle_taken", "handle")
	}
	id := u.next
	u.next++
	langs := n.Languages
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	locale := n.Locale
	if locale == "" {
		locale = "en"
	}
	ext := n.ExtID
	if ext == uuid.Nil {
		ext = uuid.Must(uuid.NewV7())
	}
	u.byID[id] = core.UserMeta{
		ID: id, ExtID: ext, Handle: n.Handle, DisplayName: n.DisplayName,
		InvitedBy: n.InvitedBy, Genesis: n.Genesis, Status: n.Status, CreatedAt: time.Now().UTC(),
		ProbationUntil: n.ProbationUntil, Suggestible: true, NamedInLikes: true,
		Preset: core.PresetBalanced, Locale: locale, Languages: langs,
	}
	u.byHandle[canon] = id
	return id, nil
}

// HandleAvailable reports whether canon is free.
func (u *MemUsers) HandleAvailable(_ context.Context, canon string) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	_, taken := u.byHandle[canon]
	return !taken
}
