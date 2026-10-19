package moderation

import (
	"bytes"
	"context"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// "Invited a removed account" marks (§8, fidelity #19). When two moderators
// confirm a suspension, the removed account's inviter is marked for 90 days.
// The mark's audience is fixed at that moment: the users with a path to the
// removed account (its connected component, nobody's blocks applied), stored
// as a roaring bitmap. Someone who joins the component later never sees it;
// the inviter never sees their own mark; moderators see the count in the queue.

// mark writes the removed-account mark of removed's inviter in tx (nothing for
// a genesis account or one whose inviter is gone).
func (s *Service) mark(ctx context.Context, tx store.Q, removed core.UserID, now time.Time) error {
	m, err := s.d.Users.Get(ctx, removed)
	if err != nil || m.InvitedBy == 0 {
		return nil //nolint:nilerr // no inviter, no mark
	}
	sr, err := s.d.Graph.Search(ctx, removed, core.Exclusions{})
	if err != nil {
		return err
	}
	viewers := roaring.New()
	for k := 1; k <= sr.MaxLayer(); k++ {
		if b := sr.LayerBitmap(k); b != nil {
			viewers.Or(b)
		}
	}
	viewers.Remove(uint32(removed))
	viewers.Remove(uint32(m.InvitedBy))
	viewers.RunOptimize()
	var buf bytes.Buffer
	if _, err := viewers.WriteTo(&buf); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO removed_account_marks (inviter_id, removed_id, viewers, at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (inviter_id, removed_id) DO UPDATE SET viewers = EXCLUDED.viewers, at = EXCLUDED.at,
			expires_at = EXCLUDED.expires_at`, int64(m.InvitedBy), int64(removed), buf.Bytes(), now, now.Add(MarkFor))
	return err
}

// Marked reports whether viewer sees the "invited a removed account" mark on
// inviter (core.RemovedMarks): a live mark whose stored audience contains
// viewer. Never for the inviter themself.
func (s *Service) Marked(ctx context.Context, viewer, inviter core.UserID) bool {
	if viewer == 0 || inviter == 0 || viewer == inviter || s.d.DB == nil {
		return false
	}
	rows, err := s.d.DB.Q().Query(ctx, "SELECT viewers FROM removed_account_marks WHERE inviter_id = $1 AND expires_at > $2",
		int64(inviter), s.d.Clock.Now())
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			return false
		}
		b := roaring.New()
		if _, err := b.ReadFrom(bytes.NewReader(raw)); err == nil && b.Contains(uint32(viewer)) {
			return true
		}
	}
	return false
}

// liveMarks counts inviter's live removed-account marks (moderators only).
func (s *Service) liveMarks(ctx context.Context, inviter core.UserID) (int, error) {
	var n int
	err := s.d.DB.Q().QueryRow(ctx, "SELECT count(*) FROM removed_account_marks WHERE inviter_id = $1 AND expires_at > $2",
		int64(inviter), s.d.Clock.Now()).Scan(&n)
	return n, err
}
