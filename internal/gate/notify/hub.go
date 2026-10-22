package notify

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// Hub limits.
const (
	// StreamsPerUser bounds one account's open streams (tabs); the oldest closes first.
	StreamsPerUser = 8
	// MaxStreams bounds the open streams of the process.
	MaxStreams = 20_000
	// streamBuffer events may wait for a slow client; a full buffer closes the
	// stream (the client reconnects and gets hello{unread}).
	streamBuffer = 16
	// commitWait bounds how long a ping waits for its notifying transaction.
	commitWait = 15 * time.Second
	// commitPoll is how often pending pings check for their committed rows.
	commitPoll = 25 * time.Millisecond
)

type stream struct {
	user    core.UserID
	session [32]byte
	seq     uint64
	ch      chan StreamEvent
}

// pending is a ping waiting for the row (id, count ≥ count) to be committed.
type pending struct {
	user  core.UserID
	id    int64
	count int
	since time.Time
}

// hub is the in-process SSE fan-out: per-user stream sets. Every send happens
// under mu, and a stream's channel is closed only under mu, so a send never
// races a close.
type hub struct {
	s *Service

	mu      sync.Mutex
	byUser  map[core.UserID]map[*stream]struct{}
	total   int
	seq     uint64
	pending []pending
	polling bool
}

func newHub(s *Service) *hub {
	return &hub{s: s, byUser: map[core.UserID]map[*stream]struct{}{}}
}

// Stream opens an SSE stream for the viewer's session. The first event is
// hello{unread}; then notification{unread} after each new notification, live
// events, and the channel closes when the session ends. Call stop when done.
func (s *Service) Stream(ctx context.Context) (<-chan StreamEvent, func(), error) {
	v, err := core.RequireUser(ctx)
	if err != nil {
		return nil, func() {}, err
	}
	n, err := s.unread(ctx, v.ID)
	if err != nil {
		return nil, func() {}, err
	}
	st, err := s.hub.open(v.ID, v.Session, StreamEvent{Type: EventHello, Data: unreadData(n)})
	if err != nil {
		return nil, func() {}, err
	}
	return st.ch, func() { s.hub.drop(st) }, nil
}

func unreadData(n int) map[string]any { return map[string]any{"unread": n} }

func (h *hub) open(u core.UserID, session [32]byte, first StreamEvent) (*stream, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= MaxStreams {
		return nil, gate.RateLimited(time.Minute)
	}
	set := h.byUser[u]
	if set == nil {
		set = map[*stream]struct{}{}
		h.byUser[u] = set
	}
	for len(set) >= StreamsPerUser {
		var oldest *stream
		for st := range set {
			if oldest == nil || st.seq < oldest.seq {
				oldest = st
			}
		}
		h.closeLocked(oldest)
	}
	h.seq++
	st := &stream{user: u, session: session, seq: h.seq, ch: make(chan StreamEvent, streamBuffer)}
	st.ch <- first
	set[st] = struct{}{}
	h.byUser[u] = set
	h.total++
	return st, nil
}

// closeLocked removes st and closes its channel (once).
func (h *hub) closeLocked(st *stream) {
	set := h.byUser[st.user]
	if _, ok := set[st]; !ok {
		return
	}
	delete(set, st)
	if len(set) == 0 {
		delete(h.byUser, st.user)
	}
	h.total--
	close(st.ch)
}

func (h *hub) drop(st *stream) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked(st)
}

func (h *hub) closeSession(u core.UserID, session [32]byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for st := range h.byUser[u] {
		if st.session == session {
			h.closeLocked(st)
		}
	}
}

func (h *hub) closeUser(u core.UserID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for st := range h.byUser[u] {
		h.closeLocked(st)
	}
}

func (h *hub) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.byUser {
		for st := range set {
			h.closeLocked(st)
		}
	}
	h.pending = nil
}

func (h *hub) has(u core.UserID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byUser[u]) > 0
}

// sendLocked delivers ev to st without blocking; a full buffer closes st.
func (h *hub) sendLocked(st *stream, ev StreamEvent) {
	select {
	case st.ch <- ev:
	default:
		h.closeLocked(st)
	}
}

func (h *hub) sendUser(u core.UserID, ev StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for st := range h.byUser[u] {
		h.sendLocked(st, ev)
	}
}

func (h *hub) sendSession(u core.UserID, session [32]byte, ev StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for st := range h.byUser[u] {
		if st.session == session {
			h.sendLocked(st, ev)
		}
	}
}

// pushUnread sends notification{unread} to every stream of u (no-op without one).
func (h *hub) pushUnread(ctx context.Context, u core.UserID) {
	if !h.has(u) {
		return
	}
	n, err := h.s.unread(ctx, u)
	if err != nil {
		h.logErr(err)
		return
	}
	h.sendUser(u, StreamEvent{Type: EventNotification, Data: unreadData(n)})
}

// afterCommit queues a ping for u once row id is visible with at least count
// from another connection, i.e. once the notifying transaction committed. A
// recipient without an open stream needs no ping (hello carries the count).
func (h *hub) afterCommit(u core.UserID, id int64, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.byUser[u]) == 0 {
		return
	}
	h.pending = append(h.pending, pending{user: u, id: id, count: count, since: time.Now()})
	if !h.polling {
		h.polling = true
		go h.poll()
	}
}

// poll announces committed rows until nothing is pending. Rows still invisible
// after commitWait belong to rolled-back transactions and are forgotten.
func (h *hub) poll() {
	for {
		time.Sleep(commitPoll)
		h.mu.Lock()
		batch := h.pending
		h.pending = nil
		if len(batch) == 0 {
			h.polling = false
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		done, err := h.committed(ctx, batch)
		if err != nil {
			h.logErr(err)
		}
		users := map[core.UserID]bool{}
		var keep []pending
		for i, p := range batch {
			switch {
			case done[i]:
				users[p.user] = true
			case time.Since(p.since) < commitWait:
				keep = append(keep, p)
			}
		}
		for u := range users {
			h.pushUnread(ctx, u)
		}
		cancel()
		if len(keep) > 0 {
			h.mu.Lock()
			h.pending = append(keep, h.pending...)
			h.mu.Unlock()
		}
	}
}

// committed reports, per pending ping, whether its row is visible with its count.
func (h *hub) committed(ctx context.Context, batch []pending) ([]bool, error) {
	ids := make([]int64, len(batch))
	for i, p := range batch {
		ids[i] = p.id
	}
	done := make([]bool, len(batch))
	rows, err := h.s.d.DB.Q().Query(ctx, `SELECT id, count FROM notifications WHERE id = ANY($1)`, ids)
	if err != nil {
		return done, err
	}
	defer rows.Close()
	seen := map[int64]int{}
	for rows.Next() {
		var (
			id int64
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return done, err
		}
		seen[id] = n
	}
	for i, p := range batch {
		if n, ok := seen[p.id]; ok && n >= p.count {
			done[i] = true
		}
	}
	return done, rows.Err()
}

func (h *hub) logErr(err error) {
	if h.s.d.Log != nil && !errors.Is(err, context.Canceled) {
		h.s.d.Log.Warn("notify: ping failed", "err", err)
	}
}

// Flush pings every batched group that changed since its last ping and whose
// last ping is at least BatchPingEvery old (notify.flush), so one group pings at
// most once per hour however many events it collects.
func (s *Service) Flush(ctx context.Context) error {
	now := s.now()
	rows, err := s.d.DB.Q().Query(ctx, `UPDATE notifications SET delivered_at = $1::timestamptz
		WHERE read_at IS NULL AND group_key IS NOT NULL AND updated_at > delivered_at
			AND delivered_at <= $1::timestamptz - $2 * interval '1 second'
		RETURNING id, user_id`, now, int64(BatchPingEvery/time.Second))
	if err != nil {
		return err
	}
	users := map[core.UserID]bool{}
	type pinged struct {
		id int64
		u  core.UserID
	}
	var groups []pinged
	for rows.Next() {
		var id, u int64
		if err := rows.Scan(&id, &u); err != nil {
			rows.Close()
			return err
		}
		users[core.UserID(u)] = true
		groups = append(groups, pinged{id, core.UserID(u)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// The hourly ping of a batched group is also its push (with the new count).
	for _, g := range groups {
		if err := s.queuePush(ctx, nil, g.u, g.id); err != nil {
			return err
		}
	}
	for u := range users {
		s.hub.pushUnread(ctx, u)
	}
	if len(users) > 0 && s.d.Log != nil {
		s.d.Log.Debug("notify: batched groups pinged", "count", len(users))
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
