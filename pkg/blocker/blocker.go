package blocker

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

type Blocker struct {
	mu      sync.RWMutex
	domains map[string]bool  // blocked domains
	lists   []BlocklistInfo
	logger  *slog.Logger
}

type BlocklistInfo struct {
	Path    string
	Enabled bool
	Count   int // number of entries loaded from this list
}

// New creates a new Blocker instance.
func New(logger *slog.Logger) *Blocker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Blocker{
		domains: make(map[string]bool),
		lists:   []BlocklistInfo{},
		logger:  logger,
	}
}

// LoadFile parses a hosts-file-format blocklist.
// Supports:
// - 0.0.0.0 ads.example.com
// - 127.0.0.1 tracker.example.com
// - domain.com (domain-only format)
// - Comments (# prefix) and blank lines are ignored
// Returns the count of entries loaded.
// LoadFile is idempotent - can reload the same file.
func (b *Blocker) LoadFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open blocklist file %s: %w", path, err)
	}
	defer file.Close()

	b.mu.Lock()
	defer b.mu.Unlock()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse the line
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		var domain string
		// If first field looks like an IP address, take the second field as domain
		if isIPLike(fields[0]) && len(fields) > 1 {
			domain = fields[1]
		} else {
			// Otherwise, first field is the domain
			domain = fields[0]
		}

		// Normalize domain: lowercase, strip trailing dot
		domain = strings.ToLower(domain)
		domain = strings.TrimSuffix(domain, ".")

		if domain != "" {
			b.domains[domain] = true
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading blocklist file %s: %w", path, err)
	}

	// Update or add blocklist info
	found := false
	for i, list := range b.lists {
		if list.Path == path {
			b.lists[i].Count = count
			b.lists[i].Enabled = true
			found = true
			break
		}
	}
	if !found {
		b.lists = append(b.lists, BlocklistInfo{
			Path:    path,
			Enabled: true,
			Count:   count,
		})
	}

	b.logger.Info("loaded blocklist", "path", path, "count", count)
	return count, nil
}

// LoadFiles loads multiple blocklists from a config slice.
// Skips any with Enabled=false.
func (b *Blocker) LoadFiles(configs []struct{ Path string; Enabled bool }) error {
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		_, err := b.LoadFile(cfg.Path)
		if err != nil {
			return err
		}
	}
	return nil
}

// IsBlocked checks if a domain is blocked.
// Normalizes input: lowercase, strips trailing dot.
// Thread-safe read lock.
func (b *Blocker) IsBlocked(name string) bool {
	// Normalize: lowercase, strip trailing dot
	name = strings.ToLower(name)
	name = strings.TrimSuffix(name, ".")

	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.domains[name]
}

// Add manually adds a domain to the blocklist.
func (b *Blocker) Add(domain string) {
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")

	if domain == "" {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.domains[domain] = true
	b.logger.Debug("added domain to blocklist", "domain", domain)
}

// Remove manually removes a domain from the blocklist.
func (b *Blocker) Remove(domain string) {
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")

	if domain == "" {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.domains, domain)
	b.logger.Debug("removed domain from blocklist", "domain", domain)
}

// Count returns the total number of blocked domains.
func (b *Blocker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return len(b.domains)
}

// Lists returns info about all loaded blocklists.
func (b *Blocker) Lists() []BlocklistInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Return a copy to avoid external modifications
	result := make([]BlocklistInfo, len(b.lists))
	copy(result, b.lists)
	return result
}

// Search finds blocked domains containing the query string.
// Returns up to 100 matches, case-insensitive.
func (b *Blocker) Search(query string) []string {
	query = strings.ToLower(query)
	if query == "" {
		return []string{}
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	var results []string
	for domain := range b.domains {
		if strings.Contains(domain, query) {
			results = append(results, domain)
			if len(results) >= 100 {
				break
			}
		}
	}
	return results
}

// Reload reloads all enabled blocklists from disk.
func (b *Blocker) Reload() error {
	b.mu.RLock()
	lists := make([]BlocklistInfo, len(b.lists))
	copy(lists, b.lists)
	b.mu.RUnlock()

	// Clear domains
	b.mu.Lock()
	b.domains = make(map[string]bool)
	b.mu.Unlock()

	// Reload each enabled list
	for _, list := range lists {
		if !list.Enabled {
			continue
		}
		_, err := b.LoadFile(list.Path)
		if err != nil {
			return err
		}
	}

	b.logger.Info("reloaded all blocklists")
	return nil
}

// isIPLike checks if a string looks like an IP address (simple heuristic).
func isIPLike(s string) bool {
	// Check if it starts with a digit and contains dots
	return len(s) > 0 && (s[0] >= '0' && s[0] <= '9')
}
