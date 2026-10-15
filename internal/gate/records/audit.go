package records

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
)

type extPair struct{ a, b uuid.UUID }

func orderedExt(a, b uuid.UUID) extPair {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	return extPair{a, b}
}

// AuditEdges checks every edges row (both directions) against its signed
// Friendship record (§4.5, fidelity #31) and returns the number of rows that
// fail: the record is missing, is not a Friendship, does not hash to its key,
// does not verify under its signing key, names other users, is scheduled for
// purging (revoked), or is followed by a Revocation of the same pair.
func (s *Service) AuditEdges(ctx context.Context) (int, error) {
	q := s.d.DB.Q()
	revoked := map[extPair][]time.Time{}
	rrows, err := q.Query(ctx, "SELECT body FROM signed_records WHERE kind = $1", int16(core.RecRevocation))
	if err != nil {
		return 0, err
	}
	for rrows.Next() {
		var body []byte
		if err := rrows.Scan(&body); err != nil {
			rrows.Close()
			return 0, err
		}
		var rb struct {
			Edge struct {
				A uuid.UUID `json:"a"`
				B uuid.UUID `json:"b"`
			} `json:"edge"`
			At sigs.Time `json:"at"`
		}
		if json.Unmarshal(body, &rb) != nil {
			continue
		}
		p := orderedExt(rb.Edge.A, rb.Edge.B)
		revoked[p] = append(revoked[p], rb.At.Time())
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return 0, err
	}

	rows, err := q.Query(ctx, `SELECT ua.ext_id, ub.ext_id, e.record_hash, r.hash, r.kind, r.body, r.sig, r.key_id,
		r.purge_after IS NOT NULL, k.public_key
		FROM edges e
		JOIN users ua ON ua.id = e.user_id
		JOIN users ub ON ub.id = e.friend_id
		LEFT JOIN signed_records r ON r.hash = e.record_hash
		LEFT JOIN signing_keys k ON k.key_id = r.key_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	bad := 0
	for rows.Next() {
		var (
			extA, extB              uuid.UUID
			edgeHash, recHash, body []byte
			sig, pub                []byte
			kind                    *int16
			keyID                   *string
			purging                 *bool
		)
		if err := rows.Scan(&extA, &extB, &edgeHash, &recHash, &kind, &body, &sig, &keyID, &purging, &pub); err != nil {
			return 0, err
		}
		if !friendshipValid(extA, extB, edgeHash, recHash, kind, body, sig, keyID, purging, pub, revoked) {
			bad++
		}
	}
	return bad, rows.Err()
}

func friendshipValid(extA, extB uuid.UUID, edgeHash, recHash []byte, kind *int16, body, sig []byte, keyID *string,
	purging *bool, pub []byte, revoked map[extPair][]time.Time) bool {
	if recHash == nil || kind == nil || core.RecordKind(*kind) != core.RecFriendship || keyID == nil {
		return false
	}
	if purging != nil && *purging {
		return false
	}
	if h := sigs.RecordHash(body); !bytes.Equal(h[:], edgeHash) {
		return false
	}
	if len(pub) != ed25519.PublicKeySize || !sigs.Verify(pub, *keyID, body, sig) {
		return false
	}
	var fb struct {
		A         uuid.UUID `json:"a"`
		B         uuid.UUID `json:"b"`
		CreatedAt sigs.Time `json:"created_at"`
	}
	if json.Unmarshal(body, &fb) != nil {
		return false
	}
	p := orderedExt(extA, extB)
	if orderedExt(fb.A, fb.B) != p {
		return false
	}
	for _, at := range revoked[p] {
		if !at.Before(fb.CreatedAt.Time()) {
			return false
		}
	}
	return true
}
