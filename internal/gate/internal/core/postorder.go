package core

import "bytes"

// PostsBefore reports whether a comes before b in a post listing: newest first
// is created_at desc, then id asc (what every reader sees); oldest first is
// the exact reverse.
func PostsBefore(a, b PostMeta, oldestFirst bool) bool {
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return (c > 0) != oldestFirst
	}
	if c := bytes.Compare(a.ID[:], b.ID[:]); c != 0 {
		return (c < 0) != oldestFirst
	}
	return false
}
