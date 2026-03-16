package cache

import (
	"container/list"
	"sync"
	"time"
)

// cacheKey is the key used in the in-memory LRU.
type cacheKey struct {
	Name  string
	Qtype uint16
}

// memEntry is an entry in the in-memory LRU cache.
type memEntry struct {
	key      cacheKey
	response []byte
	ttl      int       // seconds
	cachedAt time.Time // when the entry was cached
}

// isExpired returns true if the entry has expired.
func (e *memEntry) isExpired() bool {
	return time.Since(e.cachedAt) > time.Duration(e.ttl)*time.Second
}

// memLRU is an in-memory LRU cache for the hot DNS working set.
// It sits in front of the SQLite cache for sub-microsecond lookups.
type memLRU struct {
	mu       sync.RWMutex
	items    map[cacheKey]*list.Element
	eviction *list.List // front = most recently used
	maxSize  int
}

// newMemLRU creates a new in-memory LRU cache with the given capacity.
func newMemLRU(maxSize int) *memLRU {
	if maxSize < 1 {
		maxSize = 5000
	}
	return &memLRU{
		items:    make(map[cacheKey]*list.Element, maxSize),
		eviction: list.New(),
		maxSize:  maxSize,
	}
}

// get retrieves an entry from the in-memory cache.
// Returns the response bytes and true if found and not expired.
// Promotes the entry to the front of the LRU list on hit.
func (m *memLRU) get(name string, qtype uint16) ([]byte, bool) {
	key := cacheKey{Name: name, Qtype: qtype}

	m.mu.Lock()
	defer m.mu.Unlock()

	elem, ok := m.items[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*memEntry)
	if entry.isExpired() {
		// Remove expired entry
		m.eviction.Remove(elem)
		delete(m.items, key)
		return nil, false
	}

	// Promote to front (most recently used)
	m.eviction.MoveToFront(elem)
	return entry.response, true
}

// put inserts or updates an entry in the in-memory cache.
// Evicts the least recently used entry if the cache is full.
func (m *memLRU) put(name string, qtype uint16, response []byte, ttl int) {
	key := cacheKey{Name: name, Qtype: qtype}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update existing entry
	if elem, ok := m.items[key]; ok {
		entry := elem.Value.(*memEntry)
		entry.response = response
		entry.ttl = ttl
		entry.cachedAt = time.Now()
		m.eviction.MoveToFront(elem)
		return
	}

	// Evict if at capacity
	if m.eviction.Len() >= m.maxSize {
		m.evictOldest()
	}

	// Insert new entry
	entry := &memEntry{
		key:      key,
		response: response,
		ttl:      ttl,
		cachedAt: time.Now(),
	}
	elem := m.eviction.PushFront(entry)
	m.items[key] = elem
}

// evictOldest removes the least recently used entry.
// Must be called with m.mu held.
func (m *memLRU) evictOldest() {
	back := m.eviction.Back()
	if back == nil {
		return
	}
	entry := back.Value.(*memEntry)
	m.eviction.Remove(back)
	delete(m.items, entry.key)
}

// size returns the current number of entries.
func (m *memLRU) size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.eviction.Len()
}

// clear removes all entries from the in-memory cache.
func (m *memLRU) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = make(map[cacheKey]*list.Element, m.maxSize)
	m.eviction.Init()
}
