// Package clock abstracts time so tests can control it.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time.
type Clock interface{ Now() time.Time }

// System is the real clock (UTC).
type System struct{}

// Now returns time.Now in UTC.
func (System) Now() time.Time { return time.Now().UTC() }

// Fake is a manually driven clock for tests and PCC_ENV=test servers.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake returns a Fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

// Now returns the fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

// Set sets the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.t = t.UTC()
	f.mu.Unlock()
}
