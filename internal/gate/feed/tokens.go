package feed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Opaque strings handed to the client (security #2). Each is base64url of a
// payload followed by a 16-byte HMAC under the cursor key over a purpose
// label, the viewer and the payload, so it verifies only for the viewer it was
// minted for:
//
//	session  = id(16)                         ‖ mac
//	cursor   = id(16) ‖ tier(u16) ‖ offset(u32) ‖ mac
//	token    = id(16) ‖ tier(u16)              ‖ mac   (a collapsed group)
const (
	purposeSession = "session"
	purposeCursor  = "cursor"
	purposeToken   = "collapse"
	macLen         = 16
)

// position is where a page starts: an offset into tier's snapshot.
type position struct {
	tier   int
	offset int
}

func (s *Service) mac(v core.UserID, purpose string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.d.Keys.Cursor)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	m.Write([]byte("pcc/feed/v1\x00"))
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

func (s *Service) sessionString(sess *session) string {
	return s.seal(sess.viewer, purposeSession, append([]byte(nil), sess.id[:]...))
}

func (s *Service) openSession(v core.UserID, str string) (sessionID, error) {
	var id sessionID
	p, ok := s.open(v, purposeSession, str, len(id))
	if !ok {
		return id, gate.ErrNotFound
	}
	copy(id[:], p)
	return id, nil
}

func (s *Service) cursorString(sess *session, pos position) string {
	p := make([]byte, 0, 22)
	p = append(p, sess.id[:]...)
	p = binary.BigEndian.AppendUint16(p, uint16(pos.tier))
	p = binary.BigEndian.AppendUint32(p, uint32(pos.offset))
	return s.seal(sess.viewer, purposeCursor, p)
}

func (s *Service) openCursor(v core.UserID, str string) (sessionID, position, error) {
	var id sessionID
	p, ok := s.open(v, purposeCursor, str, 22)
	if !ok {
		return id, position{}, gate.ErrNotFound
	}
	copy(id[:], p)
	pos := position{tier: int(binary.BigEndian.Uint16(p[16:18])), offset: int(binary.BigEndian.Uint32(p[18:22]))}
	if pos.tier < 1 {
		return id, position{}, gate.ErrNotFound
	}
	return id, pos, nil
}

func (s *Service) tokenString(sess *session, tier int) string {
	p := make([]byte, 0, 18)
	p = append(p, sess.id[:]...)
	p = binary.BigEndian.AppendUint16(p, uint16(tier))
	return s.seal(sess.viewer, purposeToken, p)
}

func (s *Service) openToken(v core.UserID, str string) (sessionID, int, error) {
	var id sessionID
	p, ok := s.open(v, purposeToken, str, 18)
	if !ok {
		return id, 0, gate.ErrNotFound
	}
	copy(id[:], p)
	return id, int(binary.BigEndian.Uint16(p[16:18])), nil
}
