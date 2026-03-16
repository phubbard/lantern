package blocker

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// Helper to create a test logger
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// Test New
func TestNew(t *testing.T) {
	blocker := New(testLogger())
	if blocker == nil {
		t.Fatal("New() returned nil")
	}
	if blocker.domains == nil {
		t.Fatal("New() created blocker with nil domains map")
	}
	if blocker.Count() != 0 {
		t.Errorf("New() created blocker with %d domains, want 0", blocker.Count())
	}
}

// Test New with nil logger
func TestNewNilLogger(t *testing.T) {
	blocker := New(nil)
	if blocker == nil {
		t.Fatal("New(nil) returned nil")
	}
	if blocker.logger == nil {
		t.Fatal("New(nil) did not create default logger")
	}
}

// Test LoadFile with various formats
func TestLoadFile(t *testing.T) {
	tmpdir := t.TempDir()

	tests := []struct {
		name      string
		content   string
		wantCount int
		wantErr   bool
	}{
		{
			name: "0.0.0.0 format",
			content: `0.0.0.0 ads.example.com
0.0.0.0 tracker.example.com`,
			wantCount: 2,
		},
		{
			name: "127.0.0.1 format",
			content: `127.0.0.1 ads.example.com
127.0.0.1 tracker.example.com`,
			wantCount: 2,
		},
		{
			name:      "bare domain format",
			content:   "ads.example.com\ntracker.example.com",
			wantCount: 2,
		},
		{
			name:      "mixed formats",
			content:   "0.0.0.0 ads.example.com\n127.0.0.1 tracker.example.com\nbare-domain.com",
			wantCount: 3,
		},
		{
			name: "with comments",
			content: `# This is a comment
0.0.0.0 ads.example.com
# Another comment
tracker.example.com`,
			wantCount: 2,
		},
		{
			name: "with blank lines",
			content: `0.0.0.0 ads.example.com

0.0.0.0 tracker.example.com`,
			wantCount: 2,
		},
		{
			name:      "empty file",
			content:   "",
			wantCount: 0,
		},
		{
			name:      "only comments",
			content:   "# comment1\n# comment2",
			wantCount: 0,
		},
		{
			name:      "only blank lines",
			content:   "\n\n\n",
			wantCount: 0,
		},
		{
			name: "trailing dots",
			content: `0.0.0.0 ads.example.com.
tracker.example.com.`,
			wantCount: 2,
		},
		{
			name: "whitespace handling",
			content: `  0.0.0.0   ads.example.com
	tracker.example.com	`,
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			filePath := tmpdir + "/" + strings.ReplaceAll(tt.name, " ", "_") + ".txt"
			if err := os.WriteFile(filePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			blocker := New(testLogger())
			count, err := blocker.LoadFile(filePath)

			if (err != nil) != tt.wantErr {
				t.Errorf("LoadFile(%s) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if !tt.wantErr && count != tt.wantCount {
				t.Errorf("LoadFile(%s) count = %d, want %d", tt.name, count, tt.wantCount)
			}
		})
	}
}

// Test LoadFile non-existent file
func TestLoadFileNotFound(t *testing.T) {
	blocker := New(testLogger())
	_, err := blocker.LoadFile("/nonexistent/path/blocklist.txt")
	if err == nil {
		t.Error("LoadFile with non-existent file should return error")
	}
}

// Test IsBlocked basic functionality
func TestIsBlocked(t *testing.T) {
	tmpdir := t.TempDir()
	filePath := tmpdir + "/test.txt"
	content := `0.0.0.0 ads.example.com
0.0.0.0 tracker.example.com`
	os.WriteFile(filePath, []byte(content), 0644)

	blocker := New(testLogger())
	blocker.LoadFile(filePath)

	tests := []struct {
		name     string
		domain   string
		expected bool
	}{
		{
			name:     "exact match",
			domain:   "ads.example.com",
			expected: true,
		},
		{
			name:     "unblocked domain",
			domain:   "safe.example.com",
			expected: false,
		},
		{
			name:     "case insensitive - uppercase",
			domain:   "ADS.EXAMPLE.COM",
			expected: true,
		},
		{
			name:     "case insensitive - mixed",
			domain:   "Ads.Example.Com",
			expected: true,
		},
		{
			name:     "trailing dot",
			domain:   "ads.example.com.",
			expected: true,
		},
		{
			name:     "uppercase with trailing dot",
			domain:   "ADS.EXAMPLE.COM.",
			expected: true,
		},
		{
			name:     "subdomain not in list",
			domain:   "sub.ads.example.com",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := blocker.IsBlocked(tt.domain)
			if result != tt.expected {
				t.Errorf("IsBlocked(%s) = %v, want %v", tt.domain, result, tt.expected)
			}
		})
	}
}

// Test Add
func TestAdd(t *testing.T) {
	blocker := New(testLogger())

	tests := []struct {
		name     string
		domain   string
		expected bool
	}{
		{
			name:     "simple add",
			domain:   "new.example.com",
			expected: true,
		},
		{
			name:     "uppercase",
			domain:   "NEW.EXAMPLE.COM",
			expected: true,
		},
		{
			name:     "with trailing dot",
			domain:   "another.example.com.",
			expected: true,
		},
		{
			name:     "empty domain",
			domain:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocker.Add(tt.domain)
			// Normalize to check
			normalized := strings.ToLower(strings.TrimSuffix(tt.domain, "."))
			result := blocker.IsBlocked(normalized)
			if result != tt.expected {
				t.Errorf("After Add(%s), IsBlocked(%s) = %v, want %v", tt.domain, normalized, result, tt.expected)
			}
		})
	}
}

// Test Remove
func TestRemove(t *testing.T) {
	blocker := New(testLogger())
	blocker.Add("test.example.com")

	if !blocker.IsBlocked("test.example.com") {
		t.Fatal("Add failed: domain not blocked")
	}

	blocker.Remove("test.example.com")

	if blocker.IsBlocked("test.example.com") {
		t.Error("After Remove, domain should not be blocked")
	}

	// Test case-insensitive remove
	blocker.Add("another.example.com")
	blocker.Remove("ANOTHER.EXAMPLE.COM")
	if blocker.IsBlocked("another.example.com") {
		t.Error("Case-insensitive remove failed")
	}
}

// Test Count
func TestCount(t *testing.T) {
	blocker := New(testLogger())

	if blocker.Count() != 0 {
		t.Errorf("Initial count = %d, want 0", blocker.Count())
	}

	blocker.Add("ads.example.com")
	if blocker.Count() != 1 {
		t.Errorf("After Add(1), count = %d, want 1", blocker.Count())
	}

	blocker.Add("tracker.example.com")
	if blocker.Count() != 2 {
		t.Errorf("After Add(2), count = %d, want 2", blocker.Count())
	}

	blocker.Remove("ads.example.com")
	if blocker.Count() != 1 {
		t.Errorf("After Remove(1), count = %d, want 1", blocker.Count())
	}

	// Adding duplicate should not increase count
	blocker.Add("tracker.example.com")
	if blocker.Count() != 1 {
		t.Errorf("After Add(duplicate), count = %d, want 1", blocker.Count())
	}
}

// Test Search
func TestSearch(t *testing.T) {
	blocker := New(testLogger())
	blocker.Add("ads.example.com")
	blocker.Add("ads.google.com")
	blocker.Add("tracker.example.com")
	blocker.Add("analytics.google.com")
	blocker.Add("safe.example.com")

	tests := []struct {
		name     string
		query    string
		minCount int
		maxCount int
	}{
		{
			name:     "search for ads",
			query:    "ads",
			minCount: 2,
			maxCount: 2,
		},
		{
			name:     "search for example.com",
			query:    "example.com",
			minCount: 3,
			maxCount: 3,
		},
		{
			name:     "search for google",
			query:    "google",
			minCount: 2,
			maxCount: 2,
		},
		{
			name:     "case insensitive search",
			query:    "ADS",
			minCount: 2,
			maxCount: 2,
		},
		{
			name:     "no matches",
			query:    "nomatch",
			minCount: 0,
			maxCount: 0,
		},
		{
			name:     "empty query",
			query:    "",
			minCount: 0,
			maxCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := blocker.Search(tt.query)
			if len(results) < tt.minCount || len(results) > tt.maxCount {
				t.Errorf("Search(%s) returned %d results, want %d-%d", tt.query, len(results), tt.minCount, tt.maxCount)
			}
		})
	}
}

// Test Search with more than 100 results
func TestSearchLimit(t *testing.T) {
	blocker := New(testLogger())

	// Add more than 100 domains
	for i := 0; i < 150; i++ {
		blocker.Add("ads" + string(rune(i)) + ".example.com")
	}

	results := blocker.Search("ads")
	if len(results) != 100 {
		t.Errorf("Search() returned %d results, want max 100", len(results))
	}
}

// Test Reload
func TestReload(t *testing.T) {
	tmpdir := t.TempDir()
	filePath := tmpdir + "/reload_test.txt"

	// Create initial file
	content := "0.0.0.0 ads.example.com"
	os.WriteFile(filePath, []byte(content), 0644)

	blocker := New(testLogger())
	blocker.LoadFile(filePath)

	if !blocker.IsBlocked("ads.example.com") {
		t.Fatal("Initial load failed")
	}

	// Modify file
	newContent := `0.0.0.0 newad.example.com
0.0.0.0 anotheroad.example.com`
	os.WriteFile(filePath, []byte(newContent), 0644)

	// Reload
	err := blocker.Reload()
	if err != nil {
		t.Errorf("Reload() error = %v", err)
	}

	// Check that old entry is gone and new ones are loaded
	if blocker.IsBlocked("ads.example.com") {
		t.Error("Old domain still blocked after reload")
	}

	if !blocker.IsBlocked("newad.example.com") {
		t.Error("New domain not loaded after reload")
	}

	if !blocker.IsBlocked("anotheroad.example.com") {
		t.Error("Another new domain not loaded after reload")
	}

	if blocker.Count() != 2 {
		t.Errorf("Count after reload = %d, want 2", blocker.Count())
	}
}

// Test concurrent access
func TestConcurrentAccess(t *testing.T) {
	blocker := New(testLogger())

	// Pre-load some domains
	for i := 0; i < 10; i++ {
		blocker.Add("domain" + string(rune(i)) + ".com")
	}

	// Concurrent reads and writes
	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	// 50 goroutines doing reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			domain := "domain" + string(rune(idx%10)) + ".com"
			if !blocker.IsBlocked(domain) {
				errChan <- nil // Domain should be blocked
			}
		}(i)
	}

	// 20 goroutines adding new domains
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			blocker.Add("newdomain" + string(rune(idx)) + ".com")
		}(i)
	}

	// 10 goroutines removing domains
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			blocker.Remove("domain" + string(rune(idx)) + ".com")
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check final state
	finalCount := blocker.Count()
	if finalCount <= 0 {
		t.Error("Final count should be > 0 after concurrent operations")
	}
}

// Test Lists info tracking
func TestListsInfo(t *testing.T) {
	tmpdir := t.TempDir()
	filePath1 := tmpdir + "/list1.txt"
	filePath2 := tmpdir + "/list2.txt"

	os.WriteFile(filePath1, []byte("0.0.0.0 ads1.com\n0.0.0.0 ads2.com"), 0644)
	os.WriteFile(filePath2, []byte("0.0.0.0 track1.com"), 0644)

	blocker := New(testLogger())
	blocker.LoadFile(filePath1)
	blocker.LoadFile(filePath2)

	lists := blocker.Lists()
	if len(lists) != 2 {
		t.Errorf("Lists() returned %d lists, want 2", len(lists))
	}

	// Check counts
	if lists[0].Count != 2 {
		t.Errorf("First list count = %d, want 2", lists[0].Count)
	}
	if lists[1].Count != 1 {
		t.Errorf("Second list count = %d, want 1", lists[1].Count)
	}

	// Check enabled status
	for i, list := range lists {
		if !list.Enabled {
			t.Errorf("List %d not marked as enabled", i)
		}
	}
}

// Test LoadFile idempotency
func TestLoadFileIdempotent(t *testing.T) {
	tmpdir := t.TempDir()
	filePath := tmpdir + "/idempotent.txt"
	os.WriteFile(filePath, []byte("0.0.0.0 ads.com"), 0644)

	blocker := New(testLogger())

	count1, _ := blocker.LoadFile(filePath)
	count2, _ := blocker.LoadFile(filePath)

	if count1 != 1 || count2 != 1 {
		t.Errorf("LoadFile idempotency: first=%d, second=%d, want 1,1", count1, count2)
	}

	// Domains should still be blocked and count should not increase
	if blocker.Count() != 1 {
		t.Errorf("After idempotent loads, count = %d, want 1", blocker.Count())
	}
}

// Test AddFiles method
func TestLoadFiles(t *testing.T) {
	tmpdir := t.TempDir()
	file1 := tmpdir + "/enabled.txt"
	file2 := tmpdir + "/disabled.txt"

	os.WriteFile(file1, []byte("0.0.0.0 enabled.com"), 0644)
	os.WriteFile(file2, []byte("0.0.0.0 disabled.com"), 0644)

	blocker := New(testLogger())

	configs := []struct {
		Path    string
		Enabled bool
	}{
		{Path: file1, Enabled: true},
		{Path: file2, Enabled: false},
	}

	err := blocker.LoadFiles(configs)
	if err != nil {
		t.Errorf("LoadFiles() error = %v", err)
	}

	// Only enabled file should be loaded
	if blocker.IsBlocked("enabled.com") {
		// This is correct
	} else {
		t.Error("Enabled file was not loaded")
	}

	if blocker.IsBlocked("disabled.com") {
		t.Error("Disabled file was loaded (should not be)")
	}
}
