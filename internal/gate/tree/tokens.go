package tree

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Opaque strings handed to the client (security #2). Each is base64url of a
// payload followed by a 16-byte HMAC under the cursor key over a purpose
// label, the viewer and the payload, so it verifies only for the viewer it was
// minted for (and never as a feed token: the domain label differs):
//
//	snapshot = id(16)                                       ‖ mac
//	cursor   = id(16) ‖ gen(i16) ‖ center(16) ‖ offset(u32)  ‖ mac
const (
	purposeSnapshot = "snapshot"
	purposeCursor   = "cursor"
	macLen          = 16
	cursorLen       = 16 + 2 + 16 + 4
)

// cursor is where the next page of a row starts: a raw offset into the row's
// ranked entries.
type cursor struct {
	id     snapID
	key    rowKey
	offset int
}

func (s *Service) mac(v core.UserID, purpose string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	m.Write([]byte("pcc/tree/v1\x00"))
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write(b[:])
	m.Write(payload)
	return m.Sum(nil)[:macLen]
}

func (s *Service) seal(v core.UserID, purpose string, payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(append(payload, s.mac(v, purpose, payload)...))
}

// open verifies a sealed string of a payload of exactly n bytes.
func (s *Service) open(v core.UserID, purpose string, str string, n int) ([]byte, bool) {
	if len(str) != base64.RawURLEncoding.EncodedLen(n+macLen) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(str)
	if err != nil || len(raw) != n+macLen {
		return nil, false
	}
	payload := raw[:n]
	if !hmac.Equal(raw[n:], s.mac(v, purpose, payload)) {
		return nil, false
	}
	return payload, true
}

func (s *Service) snapshotString(sn *snapshot) string {
	return s.seal(sn.viewer, purposeSnapshot, append([]byte(nil), sn.id[:]...))
}

func (s *Service) openSnapshot(v core.UserID, str string) (snapID, error) {
	var id snapID
	p, ok := s.open(v, purposeSnapshot, str, len(id))
	if !ok {
		return id, gate.ErrNotFound
	}
	copy(id[:], p)
	return id, nil
}

func (s *Service) cursorString(sn *snapshot, c cursor) string {
	p := make([]byte, 0, cursorLen)
	p = append(p, c.id[:]...)
	p = binary.BigEndian.AppendUint16(p, uint16(int16(c.key.gen)))
	p = append(p, c.key.center[:]...)
	p = binary.BigEndian.AppendUint32(p, uint32(c.offset))
	return s.seal(sn.viewer, purposeCursor, p)
}

func (s *Service) openCursor(v core.UserID, str string) (cursor, error) {
	p, ok := s.open(v, purposeCursor, str, cursorLen)
	if !ok {
		return cursor{}, gate.ErrNotFound
	}
	var c cursor
	copy(c.id[:], p[:16])
	c.key.gen = int(int16(binary.BigEndian.Uint16(p[16:18])))
	c.key.center = uuid.UUID(p[18:34])
	c.offset = int(binary.BigEndian.Uint32(p[34:38]))
	return c, nil
}
