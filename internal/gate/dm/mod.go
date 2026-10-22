package dm

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// ---- core.Messages (moderation) ----

func (s *Service) message(ctx context.Context, q store.Q, id uuid.UUID) (core.Message, bool) {
	var (
		m    core.Message
		snd  int64
		post *uuid.UUID
	)
	err := q.QueryRow(ctx, `SELECT id, conversation_id, sender_id, coalesce(body, ''), shared_post, created_at,
			deleted_at IS NOT NULL
		FROM dm_messages WHERE id = $1 AND kind = 0`, id).Scan(&m.ID, &m.Conv, &snd, &m.Body, &post, &m.At, &m.Deleted)
	if err != nil {
		return core.Message{}, false
	}
	m.Sender, m.SharedPost = core.UserID(snd), idOf(post)
	return m, true
}

// ForReport opens message ref for reporter v (core.Messages).
func (s *Service) ForReport(ctx context.Context, v core.UserID, ref gate.Ref) (core.Message, bool) {
	id, ok := s.d.Refs.OpenMessage(v, ref)
	if !ok {
		return core.Message{}, false
	}
	q := s.d.DB.Q()
	m, ok := s.message(ctx, q, id)
	if !ok || m.Deleted || m.Sender == v {
		return core.Message{}, false
	}
	ms, err := s.loadMembers(ctx, q, m.Conv)
	if err != nil {
		return core.Message{}, false
	}
	me, ok := find(ms, v)
	if !ok || me.state != stateMember || (me.cleared != uuid.Nil && !lessID(me.cleared, m.ID)) {
		return core.Message{}, false
	}
	return m, true
}

// MessageByID returns a message for a moderator (core.Messages).
func (s *Service) MessageByID(ctx context.Context, id uuid.UUID) (core.Message, bool) {
	return s.message(ctx, s.d.DB.Q(), id)
}

// RemoveMessage erases a message on a moderator's decision (core.Messages).
func (s *Service) RemoveMessage(ctx context.Context, tx store.Q, id uuid.UUID) (core.UserID, bool, error) {
	var snd int64
	err := tx.QueryRow(ctx, `UPDATE dm_messages SET body = NULL, shared_post = NULL, deleted_at = $2
		WHERE id = $1 AND kind = 0 AND deleted_at IS NULL RETURNING sender_id`, id, s.now()).Scan(&snd)
	if isNoRows(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return core.UserID(snd), true, nil
}

// ---- retention ----

// Purge erases the messages every member has cleared past and deletes
// conversations nobody is a member of any more (dm.purge).
func (s *Service) Purge(ctx context.Context) error {
	if _, err := s.d.DB.Q().Exec(ctx, `DELETE FROM dm_messages m
		WHERE NOT EXISTS (SELECT 1 FROM dm_members mb WHERE mb.conversation_id = m.conversation_id
			AND (mb.cleared_up_to IS NULL OR mb.cleared_up_to < m.id))`); err != nil {
		return err
	}
	_, err := s.d.DB.Q().Exec(ctx, `DELETE FROM dm_conversations c
		WHERE NOT EXISTS (SELECT 1 FROM dm_members mb WHERE mb.conversation_id = c.id AND mb.state = 0)`)
	return err
}

// accountDeleted erases the account's messages and memberships: its 1:1
// conversations stay for the other person (sending is off), its groups go on
// without it (a group left without members is deleted).
func (s *Service) accountDeleted(u core.UserID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM dm_messages WHERE sender_id = $1 AND kind = 0`, int64(u)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT conversation_id FROM dm_members WHERE user_id = $1 AND admin`, int64(u))
		if err != nil {
			return err
		}
		admin, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM dm_members WHERE user_id = $1`, int64(u)); err != nil {
			return err
		}
		for _, c := range admin {
			if _, err := tx.Exec(ctx, `UPDATE dm_members SET admin = true WHERE conversation_id = $1 AND state = 0
				AND user_id = (SELECT user_id FROM dm_members WHERE conversation_id = $1 AND state = 0
					ORDER BY joined_at, user_id LIMIT 1)
				AND NOT EXISTS (SELECT 1 FROM dm_members WHERE conversation_id = $1 AND admin)`, c); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM dm_conversations c WHERE c.kind = 1
			AND NOT EXISTS (SELECT 1 FROM dm_members mb WHERE mb.conversation_id = c.id AND mb.state = 0)`)
		return err
	})
	if err != nil && s.d.Log != nil {
		s.d.Log.Warn("dm: rows of a deleted account not removed", "err", err)
	}
}
