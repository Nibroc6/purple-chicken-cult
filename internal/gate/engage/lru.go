package engage

import "container/list"

// lru is a small fixed-capacity LRU map. Not safe for concurrent use: the Service
// guards it with its mutex.
type lru[K comparable, V any] struct {
	cap   int
	ll    *list.List
	items map[K]*list.Element
}

type entry[K comparable, V any] struct {
	k K
	v V
}

func newLRU[K comparable, V any](capacity int) *lru[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	return &lru[K, V]{cap: capacity, ll: list.New(), items: map[K]*list.Element{}}
}

func (c *lru[K, V]) get(k K) (V, bool) {
	if e, ok := c.items[k]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*entry[K, V]).v, true
	}
	var zero V
	return zero, false
}

// peek returns the value without touching recency.
func (c *lru[K, V]) peek(k K) (V, bool) {
	if e, ok := c.items[k]; ok {
		return e.Value.(*entry[K, V]).v, true
	}
	var zero V
	return zero, false
}

func (c *lru[K, V]) put(k K, v V) {
	if e, ok := c.items[k]; ok {
		e.Value.(*entry[K, V]).v = v
		c.ll.MoveToFront(e)
		return
	}
	c.items[k] = c.ll.PushFront(&entry[K, V]{k: k, v: v})
	for c.ll.Len() > c.cap {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*entry[K, V]).k)
	}
}

func (c *lru[K, V]) remove(k K) {
	if e, ok := c.items[k]; ok {
		c.ll.Remove(e)
		delete(c.items, k)
	}
}

// each calls fn for every entry (recency unchanged).
func (c *lru[K, V]) each(fn func(K, V)) {
	for e := c.ll.Front(); e != nil; e = e.Next() {
		en := e.Value.(*entry[K, V])
		fn(en.k, en.v)
	}
}

func (c *lru[K, V]) clear() {
	c.ll.Init()
	clear(c.items)
}
