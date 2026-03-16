package cache

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Cache implements a two-tier DNS cache: a fast in-memory LRU for the hot
// working set backed by a persistent SQLite store for the long tail.
type Cache struct {
	db         *sql.DB
	mem        *memLRU    // hot tier: sub-microsecond lookups
	mu         sync.Mutex // serialize writes
	maxEntries int
	closed     bool
	logger     *slog.Logger
}

// New creates a new two-tier DNS cache. hotSetSize controls the in-memory LRU
// capacity (0 uses the default of 5000). maxEntries caps the SQLite store.
func New(dbPath string, maxEntries int, logger *slog.Logger, hotSetSize ...int) (*Cache, error) {
	hsz := maxEntries // default: match SQLite capacity
	if hsz <= 0 || hsz > 5000 {
		hsz = 5000
	}
	if len(hotSetSize) > 0 && hotSetSize[0] > 0 {
		hsz = hotSetSize[0]
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Enable WAL mode for better concurrency and performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}

	// Use NORMAL synchronous mode for faster writes with acceptable durability
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		db.Close()
		return nil, err
	}

	// Create the cache table if it doesn't exist
	schema := `
	CREATE TABLE IF NOT EXISTS dns_cache (
		query_name  TEXT NOT NULL,
		query_type  INTEGER NOT NULL,
		response    BLOB NOT NULL,
		ttl_seconds INTEGER NOT NULL,
		cached_at   INTEGER NOT NULL,
		last_used   INTEGER NOT NULL,
		PRIMARY KEY (query_name, query_type)
	);
	CREATE INDEX IF NOT EXISTS idx_cache_lru ON dns_cache(last_used);
	`

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	c := &Cache{
		db:         db,
		mem:        newMemLRU(hsz),
		maxEntries: maxEntries,
		logger:     logger,
	}

	// Pre-warm the in-memory tier from SQLite (most recently used entries)
	c.prewarm(hsz)

	return c, nil
}

// Get retrieves a cached DNS response. It checks the in-memory hot tier
// first (sub-microsecond), then falls back to SQLite, promoting hits to
// the hot tier.
func (c *Cache) Get(name string, qtype uint16) ([]byte, bool) {
	if c.closed {
		return nil, false
	}

	// Fast path: check in-memory LRU
	if resp, ok := c.mem.get(name, qtype); ok {
		// Keep SQLite last_used in sync so eviction stays consistent.
		// This is a single UPDATE by primary key — fast enough to be synchronous.
		c.mu.Lock()
		nowMs := time.Now().UnixMilli()
		_, _ = c.db.Exec(`UPDATE dns_cache SET last_used = ? WHERE query_name = ? AND query_type = ?`, nowMs, name, qtype)
		c.mu.Unlock()
		return resp, true
	}

	// Slow path: check SQLite
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()

	var response []byte
	var ttlSeconds int64
	var cachedAt int64
	err := c.db.QueryRow(`
		SELECT response, ttl_seconds, cached_at
		FROM dns_cache
		WHERE query_name = ? AND query_type = ?
	`, name, qtype).Scan(&response, &ttlSeconds, &cachedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		c.logger.Error("cache get error", "name", name, "type", qtype, "err", err)
		return nil, false
	}

	// Check if expired
	if now > cachedAt+ttlSeconds {
		_, _ = c.db.Exec("DELETE FROM dns_cache WHERE query_name = ? AND query_type = ?", name, qtype)
		return nil, false
	}

	// Update last_used in SQLite
	nowMs := time.Now().UnixMilli()
	_, _ = c.db.Exec(`
		UPDATE dns_cache SET last_used = ? WHERE query_name = ? AND query_type = ?
	`, nowMs, name, qtype)

	// Promote to in-memory hot tier
	remainingTTL := int(ttlSeconds - (now - cachedAt))
	if remainingTTL > 0 {
		c.mem.put(name, qtype, response, remainingTTL)
	}

	return response, true
}

// Put stores a DNS response in both the in-memory hot tier and SQLite.
// Evicts least-recently-used entries from SQLite when capacity is exceeded.
func (c *Cache) Put(name string, qtype uint16, response []byte, ttl int) {
	if c.closed {
		return
	}

	// Write-through to in-memory tier (fast, lock-free from caller's perspective)
	c.mem.put(name, qtype, response, ttl)

	// Persist to SQLite
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	nowMs := time.Now().UnixMilli()

	_, err := c.db.Exec(`
		INSERT INTO dns_cache (query_name, query_type, response, ttl_seconds, cached_at, last_used)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(query_name, query_type) DO UPDATE SET
			response = excluded.response,
			ttl_seconds = excluded.ttl_seconds,
			cached_at = excluded.cached_at,
			last_used = excluded.last_used
	`, name, qtype, response, ttl, now, nowMs)

	if err != nil {
		c.logger.Error("cache put error", "name", name, "type", qtype, "err", err)
		return
	}

	c.evictIfNeeded()
}

// evictIfNeeded removes the least recently used entries if cache size exceeds maxEntries.
// Must be called with c.mu held.
func (c *Cache) evictIfNeeded() {
	var count int
	err := c.db.QueryRow("SELECT COUNT(*) FROM dns_cache").Scan(&count)
	if err != nil {
		c.logger.Error("failed to count cache entries", "err", err)
		return
	}

	if count <= c.maxEntries {
		return
	}

	// Calculate how many entries to delete
	toDelete := count - c.maxEntries

	// Delete the oldest entries by last_used timestamp
	result, err := c.db.Exec(`
		DELETE FROM dns_cache
		WHERE rowid IN (
			SELECT rowid FROM dns_cache
			ORDER BY last_used ASC
			LIMIT ?
		)
	`, toDelete)

	if err != nil {
		c.logger.Error("eviction error", "err", err)
		return
	}

	deleted, err := result.RowsAffected()
	if err == nil && deleted > 0 {
		c.logger.Debug("evicted lru entries", "count", deleted)
	}
}

// Prune deletes all expired entries from the cache.
// Returns the number of entries deleted.
func (c *Cache) Prune() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()

	result, err := c.db.Exec(`
		DELETE FROM dns_cache
		WHERE ? > cached_at + ttl_seconds
	`, now)

	if err != nil {
		c.logger.Error("prune error", "err", err)
		return 0
	}

	count, err := result.RowsAffected()
	if err != nil {
		c.logger.Error("failed to get rows affected", "err", err)
		return 0
	}

	return int(count)
}

// Stats returns the total number of entries and the number of expired entries in the cache.
func (c *Cache) Stats() (total int, expired int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Count total entries
	err := c.db.QueryRow("SELECT COUNT(*) FROM dns_cache").Scan(&total)
	if err != nil {
		c.logger.Error("failed to count total entries", "err", err)
		return 0, 0
	}

	// Count expired entries
	now := time.Now().Unix()
	err = c.db.QueryRow(`
		SELECT COUNT(*) FROM dns_cache
		WHERE ? > cached_at + ttl_seconds
	`, now).Scan(&expired)
	if err != nil {
		c.logger.Error("failed to count expired entries", "err", err)
		return total, 0
	}

	return total, expired
}

// StartPruner starts a background goroutine that periodically prunes expired entries.
func (c *Cache) StartPruner(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.logger.Info("cache pruner shutting down")
				return
			case <-ticker.C:
				deleted := c.Prune()
				if deleted > 0 {
					c.logger.Debug("cache prune cycle completed", "deleted", deleted)
				}
			}
		}
	}()
}

// prewarm loads the most recently used entries from SQLite into the in-memory
// tier at startup so the hot path is ready immediately.
func (c *Cache) prewarm(limit int) {
	now := time.Now().Unix()

	rows, err := c.db.Query(`
		SELECT query_name, query_type, response, ttl_seconds, cached_at
		FROM dns_cache
		WHERE ? <= cached_at + ttl_seconds
		ORDER BY last_used DESC
		LIMIT ?
	`, now, limit)
	if err != nil {
		c.logger.Warn("cache prewarm query failed", "err", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var name string
		var qtype int
		var response []byte
		var ttlSeconds, cachedAt int64
		if err := rows.Scan(&name, &qtype, &response, &ttlSeconds, &cachedAt); err != nil {
			continue
		}
		remaining := int(ttlSeconds - (now - cachedAt))
		if remaining > 0 {
			c.mem.put(name, uint16(qtype), response, remaining)
			count++
		}
	}

	if count > 0 {
		c.logger.Info("cache prewarmed from SQLite", "entries", count)
	}
}

// MemSize returns the number of entries in the in-memory hot tier.
func (c *Cache) MemSize() int {
	return c.mem.size()
}

// Close closes the database connection and clears the in-memory tier.
func (c *Cache) Close() error {
	c.closed = true
	c.mem.clear()
	return c.db.Close()
}
