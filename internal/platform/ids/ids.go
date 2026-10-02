// Package ids generates UUIDs. Only gate code uses it; handlers never see UUIDs.
package ids

import (
	"crypto/sha256"
	"encoding/binary"
	"sync/atomic"

	"github.com/google/uuid"
)

// Gen generates new IDs.
type Gen interface{ New() uuid.UUID }

// V7 generates time-ordered UUIDv7 values (production).
type V7 struct{}

// New returns a new UUIDv7.
func (V7) New() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// Seq is a deterministic counter-based generator for unit tests only.
type Seq struct{ n atomic.Uint64 }

// New returns the next sequential UUID (version 8 layout).
func (s *Seq) New() uuid.UUID {
	n := s.n.Add(1)
	var u uuid.UUID
	binary.BigEndian.PutUint64(u[8:], n)
	u[6] = (u[6] & 0x0f) | 0x80 // version 8
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return u
}

// Named returns a UUIDv8 derived only from label: sha256("pcc-fixture:" ‖ label).
// coretest.World uses it so a fixture post's ID never depends on how many other
// posts exist.
func Named(label string) uuid.UUID {
	h := sha256.Sum256([]byte("pcc-fixture:" + label))
	var u uuid.UUID
	copy(u[:], h[:16])
	u[6] = (u[6] & 0x0f) | 0x80
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}
