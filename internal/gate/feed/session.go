package feed

import (
	"container/list"
	"crypto/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// sessionID is 128 random bits. It is not a capability: every lookup is keyed
// by (viewer, id) and every id travels MAC'd with the viewer (D5).
type sessionID [16]byte

// session is one feed session: the reader's per-session state and the tier
// snapshots built so far.
type session struct {
	id      sessionID
	viewer  core.UserID
	preset  core.Preset
	reached atomic.Int32 // deepest tier served (feed.tier_reached)

	mu       sync.Mutex
	seen     map[core.PostID]struct{} // 14-day seen set at session start
	showLess map[core.UserID]int
	memo     *core.Memo
	tiers    map[int]*snapshot
}

func newSession(v core.UserID, p core.Preset) (*session, error) {
	s := &session{viewer: v, preset: p, memo: core.NewMemo(), tiers: map[int]*snapshot{}}
	if _, err := rand.Read(s.id[:]); err != nil {
		return nil, err
	}
	return s, nil
}

type sessionKey struct {
	viewer core.UserID
	id     sessionID
}

type storeEntry struct {
	sess *session
	used time.Time
}

// sessionStore keeps sessions in memory: least recently used first out when
// full, at most perUser per viewer (oldest out), and gone ttl after their
// last use. ended observes every session leaving.
type sessionStore struct {
	mu      sync.Mutex
	max     int
	perUser int
	ttl     time.Duration
	ended   func(*session)
	lru     *list.List // front = most recently used; values *storeEntry
	byKey   map[sessionKey]*list.Element
	byUser  map[core.UserID][]sessionID // creation order
}

func newSessionStore(maxSessions, perUser int, ttl time.Duration, ended func(*session)) *sessionStore {
	return &sessionStore{max: maxSessions, perUser: perUser, ttl: ttl, ended: ended, lru: list.New(),
		byKey: map[sessionKey]*list.Element{}, byUser: map[core.UserID][]sessionID{}}
}

// put stores a new session. It returns the preset of the viewer's most recent
// other live session, if any (feed.preset_switch).
func (st *sessionStore) put(s *session, now time.Time) (core.Preset, bool) {
	st.mu.Lock()
	var gone []*session
	defer func() {
		st.mu.Unlock()
		st.report(gone)
	}()
	gone = st.expireLocked(now)
	var (
		prev    core.Preset
		hasPrev bool
	)
	if ids := st.byUser[s.viewer]; len(ids) > 0 {
		if el, ok := st.byKey[sessionKey{s.viewer, ids[len(ids)-1]}]; ok {
			prev, hasPrev = el.Value.(*storeEntry).sess.preset, true
		}
	}
	for len(st.byUser[s.viewer]) >= st.perUser {
		gone = append(gone, st.removeLocked(sessionKey{s.viewer, st.byUser[s.viewer][0]}))
	}
	for st.lru.Len() >= st.max {
		e := st.lru.Back().Value.(*storeEntry)
		gone = append(gone, st.removeLocked(sessionKey{e.sess.viewer, e.sess.id}))
	}
	k := sessionKey{s.viewer, s.id}
	st.byKey[k] = st.lru.PushFront(&storeEntry{sess: s, used: now})
	st.byUser[s.viewer] = append(st.byUser[s.viewer], s.id)
	return prev, hasPrev
}

// get returns v's session id (refreshing its last use), or false when it is
// not kept (expired, evicted, or never existed).
func (st *sessionStore) get(v core.UserID, id sessionID, now time.Time) (*session, bool) {
	st.mu.Lock()
	var gone []*session
	defer func() {
		st.mu.Unlock()
		st.report(gone)
	}()
	gone = st.expireLocked(now)
	el, ok := st.byKey[sessionKey{v, id}]
	if !ok {
		return nil, false
	}
	e := el.Value.(*storeEntry)
	e.used = now
	st.lru.MoveToFront(el)
	return e.sess, true
}

// dropViewer forgets every session of v (account deleted).
func (st *sessionStore) dropViewer(v core.UserID) {
	st.mu.Lock()
	var gone []*session
	for _, id := range append([]sessionID(nil), st.byUser[v]...) {
		gone = append(gone, st.removeLocked(sessionKey{v, id}))
	}
	st.mu.Unlock()
	st.report(gone)
}

// reset forgets everything.
func (st *sessionStore) reset() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lru.Init()
	st.byKey = map[sessionKey]*list.Element{}
	st.byUser = map[core.UserID][]sessionID{}
}

// len is the number of sessions kept.
func (st *sessionStore) len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lru.Len()
}

// expireLocked removes sessions unused for ttl (oldest use at the back).
func (st *sessionStore) expireLocked(now time.Time) []*session {
	var gone []*session
	for el := st.lru.Back(); el != nil; el = st.lru.Back() {
		e := el.Value.(*storeEntry)
		if now.Sub(e.used) < st.ttl {
			break
		}
		gone = append(gone, st.removeLocked(sessionKey{e.sess.viewer, e.sess.id}))
	}
	return gone
}

func (st *sessionStore) removeLocked(k sessionKey) *session {
	el, ok := st.byKey[k]
	if !ok {
		return nil
	}
	delete(st.byKey, k)
	st.lru.Remove(el)
	ids := st.byUser[k.viewer]
	for i, id := range ids {
		if id == k.id {
			ids = append(ids[:i:i], ids[i+1:]...)
			break
		}
	}
	if len(ids) == 0 {
		delete(st.byUser, k.viewer)
	} else {
		st.byUser[k.viewer] = ids
	}
	return el.Value.(*storeEntry).sess
}

func (st *sessionStore) report(gone []*session) {
	if st.ended == nil {
		return
	}
	for _, s := range gone {
		if s != nil {
			st.ended(s)
		}
	}
}
