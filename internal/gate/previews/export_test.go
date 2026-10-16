package previews

// SetFetcher replaces the fetcher (tests).
func (s *Service) SetFetcher(f Fetcher) { s.fetch = f }
