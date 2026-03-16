package cache

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Cache implements an SQLite-based LRU DNS cache.
type Cache struct {
	db         *sql.DB
	mu         sync.Mutex // serialize writes
	maxEntries int
	logger     *slog.Logger
}

// New creates a new SQLite-backed DNS cache at the specified path.
func New(dbPath string, maxEntries int, logger *slog.Logger) (*Cache, error) {
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

	return &Cache{
		db:         db,
		maxEntries: maxEntries,
		logger:     logger,
	}, nil
}

// Get retrieves a cached DNS response if it exists and hasn't expired.
// Returns the response bytes and true if found and valid, false otherwise.
func (c *Cache) Get(name string, qtype uint16) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()

	// Query the cache
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

	// Check if expired (cached_at is in seconds)
	if now > cachedAt+ttlSeconds {
		// Entry is expired, delete it
		_, _ = c.db.Exec("DELETE FROM dns_cache WHERE query_name = ? AND query_type = ?", name, qtype)
		return nil, false
	}

	// Update last_used timestamp (milliseconds for precise LRU ordering)
	nowMs := time.Now().UnixMilli()
	_, _ = c.db.Exec(`
		UPDATE dns_cache SET last_used = ? WHERE query_name = ? AND query_type = ?
	`, nowMs, name, qtype)

	return response, true
}

// Put stores or updates a DNS response in the cache with the given TTL.
// If the cache exceeds maxEntries, it evicts the least recently used entries.
func (c *Cache) Put(name string, qtype uint16, response []byte, ttl int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	nowMs := time.Now().UnixMilli()

	// UPSERT: insert or replace
	// cached_at is in seconds (for TTL expiry check), last_used is in milliseconds (for LRU ordering)
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

	// Check if we exceeded maxEntries and evict LRU entries if needed
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

// Close closes the database connection.
func (c *Cache) Close() error {
	return c.db.Close()
}
