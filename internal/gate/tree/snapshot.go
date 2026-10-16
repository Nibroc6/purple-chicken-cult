package tree

import (
	"container/list"
	"crypto/rand"
	"sync"
	"time"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// snapID is 128 random bits. It is not a capability: every lookup is keyed by
// (viewer, id) and every id travels MAC'd with the viewer (D5).
type snapID [16]byte

// rowKey names a row: the posts listed are center's children (gen ≥ 0) or
// parents (gen < 0). The focal row of a post without visible parents is
// {0, focal}: the focal post alone.
type rowKey struct {
	gen    int
	center core.PostID
}

// rowSnap is a ranked row: every visible candidate at build time, in order.
type rowSnap struct {
	entries []core.PostID
}

// viewRow is a row of the reader's current view: its center and the posts
// served in it so far (pages included), in order.
type viewRow struct {
	center core.PostID
	served []core.PostID
}

// snapshot is one opened tree: the reader's ranking context, the rows ranked so
// far and the rows currently on screen (for "shown above" and to check that a
// re-centered row follows the view).
type snapshot struct {
	id     snapID
	viewer core.UserID
	preset core.Preset
	focal  core.PostID

	mu       sync.Mutex
	memo     *core.Memo
	showLess map[core.UserID]int
	hops     map[core.UserID]time.Time
	rows     map[rowKey]*rowSnap
	built    []rowKey // build order (eviction)
	view     map[int]*viewRow
}

func newSnapshot(v core.UserID, p core.Preset, focal core.PostID) (*snapshot, error) {
	s := &snapshot{viewer: v, preset: p, focal: focal, memo: core.NewMemo(),
		rows: map[rowKey]*rowSnap{}, view: map[int]*viewRow{}}
	if _, err := rand.Read(s.id[:]); err != nil {
		return nil, err
	}
	return s, nil
}

// alone reports whether k is the focal row of a post without visible parents.
func (sn *snapshot) alone(k rowKey) bool { return k.gen == 0 && k.center == sn.focal }

// keep stores a ranked row, dropping the oldest rows outside the view when the
// snapshot holds too many.
func (sn *snapshot) keep(k rowKey, rs *rowSnap) {
	if len(sn.rows) >= maxRows {
		kept := sn.built[:0]
		for _, b := range sn.built {
			if vr, ok := sn.view[b.gen]; ok && vr.center == b.center {
				kept = append(kept, b)
				continue
			}
			delete(sn.rows, b)
		}
		sn.built = kept
	}
	sn.rows[k] = rs
	sn.built = append(sn.built, k)
}

// adjacentServed reports whether k's center was served in the row next to k
// on the focal side: row gen−1 for gen ≥ 1, row gen+1 for gen ≤ −1, and for
// the focal row the parents of the focal post (row −1).
func (sn *snapshot) adjacentServed(k rowKey) bool {
	from := k.gen - 1
	switch {
	case k.gen == 0:
		from = -1
	case k.gen < 0:
		from = k.gen + 1
	}
	vr, ok := sn.view[from]
	if !ok {
		return false
	}
	for _, id := range vr.served {
		if id == k.center {
			return true
		}
	}
	return false
}

// shownAbove reports whether id was served in a row displayed above row gen.
func (sn *snapshot) shownAbove(gen int, id core.PostID) bool {
	for g, vr := range sn.view {
		if g >= gen {
			continue
		}
		for _, s := range vr.served {
			if s == id {
				return true
			}
		}
	}
	return false
}

// setRow makes k the current row at k.gen. Re-centering row gen ≥ 1 reloads the
// rows below it and loading row gen ≤ −1 replaces the rows above it, so those
// leave the view; the focal row keeps the rows below (the focal post stays its
// first post).
func (sn *snapshot) setRow(k rowKey, served []core.PostID) {
	for g := range sn.view {
		if (k.gen >= 1 && g > k.gen) || (k.gen <= -1 && g < k.gen) {
			delete(sn.view, g)
		}
	}
	sn.view[k.gen] = &viewRow{center: k.center, served: served}
}

// addServed records a further page of the current row k.
func (sn *snapshot) addServed(k rowKey, served []core.PostID) {
	if vr, ok := sn.view[k.gen]; ok && vr.center == k.center {
		vr.served = append(vr.served, served...)
	}
}

type snapKey struct {
	viewer core.UserID
	id     snapID
}

type storeEntry struct {
	snap *snapshot
	used time.Time
}

// snapStore keeps snapshots in memory: least recently used first out when
// full, at most perUser per viewer (oldest out), gone ttl after their last use.
type snapStore struct {
	mu      sync.Mutex
	max     int
	perUser int
	ttl     time.Duration
	lru     *list.List // front = most recently used; values *storeEntry
	byKey   map[snapKey]*list.Element
	byUser  map[core.UserID][]snapID // creation order
}

func newSnapStore(maxSnaps, perUser int, ttl time.Duration) *snapStore {
	return &snapStore{max: maxSnaps, perUser: perUser, ttl: ttl, lru: list.New(),
		byKey: map[snapKey]*list.Element{}, byUser: map[core.UserID][]snapID{}}
}

// put stores a new snapshot.
func (st *snapStore) put(s *snapshot, now time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.expireLocked(now)
	for len(st.byUser[s.viewer]) >= st.perUser {
		st.removeLocked(snapKey{s.viewer, st.byUser[s.viewer][0]})
	}
	for st.lru.Len() >= st.max {
		e := st.lru.Back().Value.(*storeEntry)
		st.removeLocked(snapKey{e.snap.viewer, e.snap.id})
	}
	st.byKey[snapKey{s.viewer, s.id}] = st.lru.PushFront(&storeEntry{snap: s, used: now})
	st.byUser[s.viewer] = append(st.byUser[s.viewer], s.id)
}

// get returns v's snapshot id (refreshing its last use), or false when it is
// not kept (expired, evicted, or never existed).
func (st *snapStore) get(v core.UserID, id snapID, now time.Time) (*snapshot, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.expireLocked(now)
	el, ok := st.byKey[snapKey{v, id}]
	if !ok {
		return nil, false
	}
	e := el.Value.(*storeEntry)
	e.used = now
	st.lru.MoveToFront(el)
	return e.snap, true
}

// dropViewer forgets every snapshot of v (account deleted).
func (st *snapStore) dropViewer(v core.UserID) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, id := range append([]snapID(nil), st.byUser[v]...) {
		st.removeLocked(snapKey{v, id})
	}
}

// reset forgets everything.
func (st *snapStore) reset() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lru.Init()
	st.byKey = map[snapKey]*list.Element{}
	st.byUser = map[core.UserID][]snapID{}
}

// len is the number of snapshots kept.
func (st *snapStore) len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lru.Len()
}

// expireLocked removes snapshots unused for ttl (oldest use at the back).
func (st *snapStore) expireLocked(now time.Time) {
	for el := st.lru.Back(); el != nil; el = st.lru.Back() {
		e := el.Value.(*storeEntry)
		if now.Sub(e.used) < st.ttl {
			return
		}
		st.removeLocked(snapKey{e.snap.viewer, e.snap.id})
	}
}

func (st *snapStore) removeLocked(k snapKey) {
	el, ok := st.byKey[k]
	if !ok {
		return
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
}
