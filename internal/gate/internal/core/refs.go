package core

import (
	"encoding/binary"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/refs"
)

// Refs wraps platform/refs with typed helpers. It never mints for viewer 0
// (Seal would panic; callers check RequireUser first).
type Refs struct{ C *refs.Codec }

func u32(id UserID) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(id))
	return b[:]
}

func openU32(b []byte, ok bool) (UserID, bool) {
	if !ok || len(b) != 4 {
		return 0, false
	}
	id := UserID(binary.BigEndian.Uint32(b))
	return id, id != 0
}

func openUUID(b []byte, ok bool) (uuid.UUID, bool) {
	if !ok || len(b) != 16 {
		return uuid.Nil, false
	}
	u, err := uuid.FromBytes(b)
	return u, err == nil && u != uuid.Nil
}

// ModViewer is the moderator-scoped viewer value for moderator refs.
func ModViewer(mod UserID) uint64 { return refs.ModeratorViewer | uint64(mod) }

// User returns viewer v's ref for user target.
func (r Refs) User(v, target UserID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindUser, u32(target)))
}

// OpenUser opens a user ref minted for v.
func (r Refs) OpenUser(v UserID, ref gate.Ref) (UserID, bool) {
	return openU32(r.C.Open(uint64(v), refs.KindUser, string(ref)))
}

// ModUser returns moderator mod's moderator-scoped ref for user target.
func (r Refs) ModUser(mod, target UserID) gate.Ref {
	return gate.Ref(r.C.Seal(ModViewer(mod), refs.KindUser, u32(target)))
}

// OpenModUser opens a moderator-scoped user ref.
func (r Refs) OpenModUser(mod UserID, ref gate.Ref) (UserID, bool) {
	return openU32(r.C.Open(ModViewer(mod), refs.KindUser, string(ref)))
}

// Post returns viewer v's ref for post p.
func (r Refs) Post(v UserID, p PostID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindPost, p[:]))
}

// OpenPost opens a post ref minted for v.
func (r Refs) OpenPost(v UserID, ref gate.Ref) (PostID, bool) {
	return openUUID(r.C.Open(uint64(v), refs.KindPost, string(ref)))
}

// Media returns viewer v's ref for media m.
func (r Refs) Media(v UserID, m uuid.UUID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindMedia, m[:]))
}

// OpenMedia opens a media ref minted for v.
func (r Refs) OpenMedia(v UserID, ref gate.Ref) (uuid.UUID, bool) {
	return openUUID(r.C.Open(uint64(v), refs.KindMedia, string(ref)))
}

// Notif returns viewer v's ref for notification id.
func (r Refs) Notif(v UserID, id int64) gate.Ref {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(id))
	return gate.Ref(r.C.Seal(uint64(v), refs.KindNotif, b[:]))
}

// OpenNotif opens a notification ref minted for v.
func (r Refs) OpenNotif(v UserID, ref gate.Ref) (int64, bool) {
	b, ok := r.C.Open(uint64(v), refs.KindNotif, string(ref))
	if !ok || len(b) != 8 {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(b)), true
}

// Report returns viewer v's ref for report id.
func (r Refs) Report(v UserID, id uuid.UUID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindReport, id[:]))
}

// OpenReport opens a report ref minted for v.
func (r Refs) OpenReport(v UserID, ref gate.Ref) (uuid.UUID, bool) {
	return openUUID(r.C.Open(uint64(v), refs.KindReport, string(ref)))
}

// Cred returns viewer v's ref for one of their credential IDs.
func (r Refs) Cred(v UserID, credID []byte) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindCred, credID))
}

// OpenCred opens a credential ref minted for v.
func (r Refs) OpenCred(v UserID, ref gate.Ref) ([]byte, bool) {
	b, ok := r.C.Open(uint64(v), refs.KindCred, string(ref))
	return b, ok && len(b) > 0
}

// Handshake returns viewer v's ref for handshake hs.
func (r Refs) Handshake(v UserID, hs uuid.UUID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindHandshake, hs[:]))
}

// OpenHandshake opens a handshake ref minted for v.
func (r Refs) OpenHandshake(v UserID, ref gate.Ref) (uuid.UUID, bool) {
	return openUUID(r.C.Open(uint64(v), refs.KindHandshake, string(ref)))
}

// ModItem returns moderator mod's ref for a queue item / recovery request.
func (r Refs) ModItem(mod UserID, id uuid.UUID) gate.Ref {
	return gate.Ref(r.C.Seal(ModViewer(mod), refs.KindModItem, id[:]))
}

// OpenModItem opens a moderator-scoped item ref.
func (r Refs) OpenModItem(mod UserID, ref gate.Ref) (uuid.UUID, bool) {
	return openUUID(r.C.Open(ModViewer(mod), refs.KindModItem, string(ref)))
}

// Conv returns viewer v's ref for direct-message conversation c.
func (r Refs) Conv(v UserID, c uuid.UUID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindConv, c[:]))
}

// OpenConv opens a conversation ref minted for v.
func (r Refs) OpenConv(v UserID, ref gate.Ref) (uuid.UUID, bool) {
	return openUUID(r.C.Open(uint64(v), refs.KindConv, string(ref)))
}

// Message returns viewer v's ref for direct message m.
func (r Refs) Message(v UserID, m uuid.UUID) gate.Ref {
	return gate.Ref(r.C.Seal(uint64(v), refs.KindMessage, m[:]))
}

// OpenMessage opens a message ref minted for v.
func (r Refs) OpenMessage(v UserID, ref gate.Ref) (uuid.UUID, bool) {
	return openUUID(r.C.Open(uint64(v), refs.KindMessage, string(ref)))
}
