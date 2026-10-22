// Package refs implements opaque per-viewer references (ARCHITECTURE §13):
//
//	ref = base64url_nopad( version(1B) ‖ AES-SIV(k_version, pt=target,
//	                       ad="pcc-ref-v1" ‖ version ‖ viewer(8B BE) ‖ kind) )
//
// Refs are deterministic (cacheable), differ per viewer (uncorrelatable), and a ref
// presented by another viewer, for another kind, or under an unknown key version
// fails to open. Viewer 0 is never used: guests get no refs.
package refs

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/tink-crypto/tink-go/v2/daead/subtle"
)

// Kind namespaces refs so a user ref can never be opened as a post ref.
type Kind byte

// Ref kinds.
const (
	KindUser      Kind = 1
	KindPost      Kind = 2
	KindMedia     Kind = 3
	KindNotif     Kind = 4
	KindReport    Kind = 5
	KindCred      Kind = 6
	KindHandshake Kind = 7
	KindModItem   Kind = 8
	KindConv      Kind = 9  // a direct-message conversation (gate/dm)
	KindMessage   Kind = 10 // one direct message
)

// KeySize is the AES-SIV key size (two AES-256 keys).
const KeySize = 64

// MaxTarget bounds the plaintext length accepted by Seal/Open.
const MaxTarget = 128

const (
	sivTag = 16
	adTag  = "pcc-ref-v1"
)

// ModeratorViewer is OR-ed into a moderator's user ID to form a moderator-scoped
// viewer value, so moderator refs never collide with the moderator's own refs.
const ModeratorViewer uint64 = 1 << 63

// Codec seals and opens refs. It is safe for concurrent use.
type Codec struct {
	keys    map[byte]*subtle.AESSIV
	current byte
}

// New builds a Codec. keys maps a version byte to a 64-byte key; current is the
// version used by Seal. Older versions keep opening until they are removed.
func New(keys map[byte][]byte, current byte) (*Codec, error) {
	if _, ok := keys[current]; !ok {
		return nil, errors.New("refs: current key version missing")
	}
	c := &Codec{keys: make(map[byte]*subtle.AESSIV, len(keys)), current: current}
	for v, k := range keys {
		if len(k) != KeySize {
			return nil, fmt.Errorf("refs: key version %d must be %d bytes", v, KeySize)
		}
		s, err := subtle.NewAESSIV(k)
		if err != nil {
			return nil, fmt.Errorf("refs: %w", err)
		}
		c.keys[v] = s
	}
	return c, nil
}

func ad(version byte, viewer uint64, k Kind) []byte {
	b := make([]byte, 0, len(adTag)+1+8+1)
	b = append(b, adTag...)
	b = append(b, version)
	b = binary.BigEndian.AppendUint64(b, viewer)
	b = append(b, byte(k))
	return b
}

// Seal returns the ref of target for viewer. It panics on viewer == 0 (refs are
// never minted for guests) and on an oversized target (a programming error).
func (c *Codec) Seal(viewer uint64, k Kind, target []byte) string {
	if viewer == 0 {
		panic("refs: Seal with viewer 0")
	}
	if len(target) == 0 || len(target) > MaxTarget {
		panic("refs: bad target length")
	}
	ct, err := c.keys[c.current].EncryptDeterministically(target, ad(c.current, viewer, k))
	if err != nil {
		panic("refs: encrypt: " + err.Error())
	}
	out := make([]byte, 0, 1+len(ct))
	out = append(out, c.current)
	out = append(out, ct...)
	return base64.RawURLEncoding.EncodeToString(out)
}

// Open returns the target sealed in ref for viewer and kind. Any failure
// (garbage, other viewer, other kind, unknown version) returns false.
func (c *Codec) Open(viewer uint64, k Kind, ref string) ([]byte, bool) {
	if viewer == 0 {
		return nil, false
	}
	// Length bounds before any decoding work: version + SIV tag + 1..MaxTarget.
	maxLen := base64.RawURLEncoding.EncodedLen(1 + sivTag + MaxTarget)
	minLen := base64.RawURLEncoding.EncodedLen(1 + sivTag + 1)
	if len(ref) < minLen || len(ref) > maxLen {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(ref)
	if err != nil || len(raw) < 1+sivTag+1 {
		return nil, false
	}
	s, ok := c.keys[raw[0]]
	if !ok {
		return nil, false
	}
	pt, err := s.DecryptDeterministically(raw[1:], ad(raw[0], viewer, k))
	if err != nil {
		return nil, false
	}
	return pt, true
}
