package auth

import "context"

// PurgeExpired runs the sessions.purge job body (tests).
func (s *Service) PurgeExpired(ctx context.Context) error { return s.purgeExpired(ctx) }
