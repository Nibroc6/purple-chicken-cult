package tree

// Snapshots is the number of snapshots kept in memory.
func (s *Service) Snapshots() int { return s.store.len() }
