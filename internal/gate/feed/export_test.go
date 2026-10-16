package feed

// SetPlanHook observes every planner choice (the planner's test hook).
func (s *Service) SetPlanHook(fn func(tier int, scan Scan)) { s.onPlan = fn }

// Sessions is the number of sessions kept in memory.
func (s *Service) Sessions() int { return s.store.len() }
