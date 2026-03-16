package blocker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Subscription represents a remote blocklist that is fetched on a schedule.
type Subscription struct {
	URL            string
	UpdateInterval time.Duration
	CacheDir       string // directory to store downloaded lists
	ETag           string // HTTP ETag for conditional fetching
	LastModified   string // HTTP Last-Modified for conditional fetching
	LastUpdated    time.Time
	LastHash       string // SHA-256 of last downloaded content
}

// SubscriptionManager handles periodic fetching and updating of remote blocklists.
type SubscriptionManager struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription // keyed by URL
	blocker       *Blocker
	httpClient    *http.Client
	cacheDir      string
	logger        *slog.Logger
	cancel        context.CancelFunc
}

// NewSubscriptionManager creates a new subscription manager.
func NewSubscriptionManager(b *Blocker, cacheDir string, logger *slog.Logger) *SubscriptionManager {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &SubscriptionManager{
		subscriptions: make(map[string]*Subscription),
		blocker:       b,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cacheDir: cacheDir,
		logger:   logger,
	}
}

// Add registers a new subscription. It performs an initial fetch immediately.
func (sm *SubscriptionManager) Add(url string, updateInterval time.Duration) error {
	sm.mu.Lock()
	sub := &Subscription{
		URL:            url,
		UpdateInterval: updateInterval,
		CacheDir:       sm.cacheDir,
	}
	sm.subscriptions[url] = sub
	sm.mu.Unlock()

	// Initial fetch
	if err := sm.fetchAndLoad(sub); err != nil {
		sm.logger.Warn("initial fetch failed for subscription", "url", url, "error", err)
		return err
	}

	return nil
}

// Start begins background update loops for all subscriptions.
func (sm *SubscriptionManager) Start(ctx context.Context) {
	ctx, sm.cancel = context.WithCancel(ctx)

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, sub := range sm.subscriptions {
		go sm.updateLoop(ctx, sub)
	}
}

// Stop cancels all background update loops.
func (sm *SubscriptionManager) Stop() {
	if sm.cancel != nil {
		sm.cancel()
	}
}

// UpdateNow triggers an immediate update of all subscriptions.
func (sm *SubscriptionManager) UpdateNow() {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, sub := range sm.subscriptions {
		if err := sm.fetchAndLoad(sub); err != nil {
			sm.logger.Warn("manual update failed", "url", sub.URL, "error", err)
		}
	}
}

// Status returns the current state of all subscriptions.
func (sm *SubscriptionManager) Status() []SubscriptionStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []SubscriptionStatus
	for _, sub := range sm.subscriptions {
		result = append(result, SubscriptionStatus{
			URL:         sub.URL,
			LastUpdated: sub.LastUpdated,
			Interval:    sub.UpdateInterval,
		})
	}
	return result
}

// SubscriptionStatus is a read-only view of a subscription's state.
type SubscriptionStatus struct {
	URL         string        `json:"url"`
	LastUpdated time.Time     `json:"last_updated"`
	Interval    time.Duration `json:"interval_seconds"`
}

// updateLoop periodically fetches updates for a single subscription.
func (sm *SubscriptionManager) updateLoop(ctx context.Context, sub *Subscription) {
	interval := sub.UpdateInterval
	if interval < 1*time.Minute {
		interval = 1 * time.Hour // minimum interval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sm.fetchAndLoad(sub); err != nil {
				sm.logger.Warn("scheduled update failed", "url", sub.URL, "error", err)
			}
		}
	}
}

// fetchAndLoad downloads a remote blocklist, saves it to the cache dir,
// and loads it into the blocker. Uses conditional HTTP requests to avoid
// re-downloading unchanged lists.
func (sm *SubscriptionManager) fetchAndLoad(sub *Subscription) error {
	req, err := http.NewRequest("GET", sub.URL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Conditional fetch headers
	if sub.ETag != "" {
		req.Header.Set("If-None-Match", sub.ETag)
	}
	if sub.LastModified != "" {
		req.Header.Set("If-Modified-Since", sub.LastModified)
	}

	resp, err := sm.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Not modified — nothing to do
	if resp.StatusCode == http.StatusNotModified {
		sm.logger.Debug("blocklist not modified", "url", sub.URL)
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, sub.URL)
	}

	// Read the body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check if content has actually changed via hash
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	if hash == sub.LastHash {
		sm.logger.Debug("blocklist content unchanged", "url", sub.URL)
		return nil
	}

	// Save to cache directory
	if err := os.MkdirAll(sm.cacheDir, 0700); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}

	// Use a hash-based filename to avoid path traversal issues
	filename := fmt.Sprintf("blocklist-%x.txt", sha256.Sum256([]byte(sub.URL)))
	cachePath := filepath.Join(sm.cacheDir, filename)

	if err := os.WriteFile(cachePath, body, 0600); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	// Load into the blocker
	count, err := sm.blocker.LoadFile(cachePath)
	if err != nil {
		return fmt.Errorf("failed to load blocklist: %w", err)
	}

	// Update subscription metadata
	sm.mu.Lock()
	sub.ETag = resp.Header.Get("ETag")
	sub.LastModified = resp.Header.Get("Last-Modified")
	sub.LastUpdated = time.Now()
	sub.LastHash = hash
	sm.mu.Unlock()

	sm.logger.Info("updated blocklist subscription",
		"url", sub.URL,
		"entries", count,
		"cache_path", cachePath,
	)

	return nil
}
