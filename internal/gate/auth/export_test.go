package auth

import "context"

// PurgeExpired runs the sessions.purge job body (tests).
func (s *Service) PurgeExpired(ctx context.Context) error { return s.purgeExpired(ctx) }

// SetTestHook installs fn at the flow's hook points ("before-use", "after-use").
func (s *Service) SetTestHook(fn func(point string)) { s.testHook = fn }

// WaitRevoked waits for every pending RevokeAll publisher.
func (s *Service) WaitRevoked() { s.pending.Wait() }
