package core

import (
	"reflect"
	"sync"
)

// Bus is a typed, synchronous in-process fan-out. Publish after commit.
type Bus struct {
	mu   sync.RWMutex
	subs map[reflect.Type][]func(any)
}

// Publish delivers ev to every subscriber of its exact type, in subscription order.
func (b *Bus) Publish(ev any) {
	b.mu.RLock()
	fns := b.subs[reflect.TypeOf(ev)]
	b.mu.RUnlock()
	for _, fn := range fns {
		fn(ev)
	}
}

// Subscribe registers fn for events of type T.
func Subscribe[T any](b *Bus, fn func(T)) {
	t := reflect.TypeFor[T]()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[reflect.Type][]func(any){}
	}
	b.subs[t] = append(b.subs[t], func(v any) { fn(v.(T)) })
}

// MemoKey keys a per-snapshot memo entry.
type MemoKey struct {
	Kind uint8
	Post PostID
}

// Memo kinds used by the reference implementations; WPs may add their own.
const (
	MemoN     uint8 = 1
	MemoKidsN uint8 = 2
)

// Memo is a per-snapshot cache (buildability B1).
type Memo struct {
	mu sync.Mutex
	m  map[MemoKey]any
}

// NewMemo returns an empty memo.
func NewMemo() *Memo { return &Memo{m: map[MemoKey]any{}} }

// Get returns a cached value.
func (m *Memo) Get(k MemoKey) (any, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[k]
	return v, ok
}

// Put caches a value.
func (m *Memo) Put(k MemoKey, v any) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[k] = v
}
