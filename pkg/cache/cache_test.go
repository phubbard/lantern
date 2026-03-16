package cache

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestLogger creates a test logger that discards output
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestCache_New(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 100, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Verify db file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("cache database file was not created")
	}
}

func TestCache_PutAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	name := "example.com"
	qtype := uint16(1) // TypeA
	response := []byte{0x00, 0x01, 0x02, 0x03}
	ttl := 300

	// Put entry
	cache.Put(name, qtype, response, ttl)

	// Get entry
	data, ok := cache.Get(name, qtype)
	if !ok {
		t.Fatal("failed to retrieve cached entry")
	}

	if len(data) != len(response) {
		t.Errorf("response data length mismatch: expected %d, got %d", len(response), len(data))
	}

	for i, b := range response {
		if data[i] != b {
			t.Errorf("response data mismatch at byte %d: expected %d, got %d", i, b, data[i])
		}
	}
}

func TestCache_GetMiss(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	data, ok := cache.Get("nonexistent.com", uint16(1))
	if ok {
		t.Error("expected cache miss, got hit")
	}
	if data != nil {
		t.Errorf("expected nil data on miss, got %v", data)
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	name := "example.com"
	qtype := uint16(1)
	response := []byte{0x00, 0x01}
	ttl := 1 // 1 second TTL

	cache.Put(name, qtype, response, ttl)

	// Should be available immediately
	data, ok := cache.Get(name, qtype)
	if !ok {
		t.Fatal("entry should be available immediately after Put")
	}
	if data == nil {
		t.Error("expected non-nil data")
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Should be expired now
	data, ok = cache.Get(name, qtype)
	if ok {
		t.Error("expected cache miss after TTL expiry, got hit")
	}
}

func TestCache_LRUEviction(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	maxEntries := 3

	cache, err := New(dbPath, maxEntries, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Put 4 entries (one more than maxEntries)
	for i := 0; i < 4; i++ {
		name := "example" + string(rune(byte(i))) + ".com"
		cache.Put(name, uint16(1), []byte{byte(i)}, 3600)
		time.Sleep(10 * time.Millisecond) // Small delay to ensure different last_used times
	}

	// The oldest entry (i=0) should have been evicted
	data, ok := cache.Get("example0.com", uint16(1))
	if ok {
		t.Error("expected oldest entry to be evicted, but it was found")
	}
	if data != nil {
		t.Error("expected nil data for evicted entry")
	}

	// The newest entries (i=1,2,3) should still be there
	for i := 1; i < 4; i++ {
		name := "example" + string(rune(byte(i))) + ".com"
		_, ok := cache.Get(name, uint16(1))
		if !ok {
			t.Errorf("entry %d should still be cached", i)
		}
	}
}

func TestCache_Prune(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Put some entries with short TTL
	cache.Put("expired1.com", uint16(1), []byte{0x01}, 1)
	cache.Put("expired2.com", uint16(1), []byte{0x02}, 1)
	cache.Put("valid.com", uint16(1), []byte{0x03}, 3600)

	// Wait for expired entries to expire
	time.Sleep(2 * time.Second)

	// Run prune
	deleted := cache.Prune()
	if deleted < 2 {
		t.Errorf("expected at least 2 expired entries to be deleted, got %d", deleted)
	}

	// Expired entries should be gone
	_, ok1 := cache.Get("expired1.com", uint16(1))
	_, ok2 := cache.Get("expired2.com", uint16(1))
	if ok1 || ok2 {
		t.Error("expired entries should be deleted after prune")
	}

	// Valid entry should still exist
	_, ok := cache.Get("valid.com", uint16(1))
	if !ok {
		t.Error("valid entry was incorrectly deleted during prune")
	}
}

func TestCache_Stats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Put some entries
	cache.Put("example1.com", uint16(1), []byte{0x01}, 3600)
	cache.Put("example2.com", uint16(1), []byte{0x02}, 1)
	cache.Put("example3.com", uint16(1), []byte{0x03}, 3600)

	// Wait for one to expire
	time.Sleep(2 * time.Second)

	total, expired := cache.Stats()

	if total != 3 {
		t.Errorf("expected 3 total entries, got %d", total)
	}

	if expired < 1 {
		t.Errorf("expected at least 1 expired entry, got %d", expired)
	}
}

func TestCache_UpdateExisting(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	name := "example.com"
	qtype := uint16(1)

	// Put initial value
	cache.Put(name, qtype, []byte{0x01}, 3600)

	data1, ok := cache.Get(name, qtype)
	if !ok {
		t.Fatal("failed to get initial value")
	}
	if len(data1) != 1 || data1[0] != 0x01 {
		t.Error("initial value mismatch")
	}

	// Put new value for same key
	cache.Put(name, qtype, []byte{0x02, 0x03}, 3600)

	data2, ok := cache.Get(name, qtype)
	if !ok {
		t.Fatal("failed to get updated value")
	}
	if len(data2) != 2 || data2[0] != 0x02 || data2[1] != 0x03 {
		t.Error("updated value mismatch")
	}

	// Verify there's still only one entry in the cache
	total, _ := cache.Stats()
	if total != 1 {
		t.Errorf("expected 1 total entry after update, got %d", total)
	}
}

func TestCache_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Add some data
	cache.Put("example.com", uint16(1), []byte{0x01}, 3600)

	// Close should succeed
	if err := cache.Close(); err != nil {
		t.Errorf("close failed: %v", err)
	}

	// Operations after close should fail gracefully
	cache.Put("test.com", uint16(1), []byte{0x02}, 3600)

	_, ok := cache.Get("test.com", uint16(1))
	if ok {
		t.Error("get after close should fail")
	}
}

func TestCache_DifferentQTypes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	name := "example.com"

	// Store different query types for same name
	cache.Put(name, uint16(1), []byte{0x01}, 3600)   // TypeA
	cache.Put(name, uint16(28), []byte{0x02}, 3600)  // TypeAAAA
	cache.Put(name, uint16(16), []byte{0x03}, 3600)  // TypeTXT

	// Retrieve each type
	data1, ok1 := cache.Get(name, uint16(1))
	data28, ok28 := cache.Get(name, uint16(28))
	data16, ok16 := cache.Get(name, uint16(16))

	if !ok1 || len(data1) == 0 || data1[0] != 0x01 {
		t.Error("TypeA data mismatch")
	}
	if !ok28 || len(data28) == 0 || data28[0] != 0x02 {
		t.Error("TypeAAAA data mismatch")
	}
	if !ok16 || len(data16) == 0 || data16[0] != 0x03 {
		t.Error("TypeTXT data mismatch")
	}

	total, _ := cache.Stats()
	if total != 3 {
		t.Errorf("expected 3 total entries, got %d", total)
	}
}

func TestCache_LRUUsageUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	maxEntries := 3

	cache, err := New(dbPath, maxEntries, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Put 3 entries
	cache.Put("example1.com", uint16(1), []byte{0x01}, 3600)
	time.Sleep(10 * time.Millisecond)
	cache.Put("example2.com", uint16(1), []byte{0x02}, 3600)
	time.Sleep(10 * time.Millisecond)
	cache.Put("example3.com", uint16(1), []byte{0x03}, 3600)
	time.Sleep(10 * time.Millisecond)

	// Access the first entry to update its last_used time
	cache.Get("example1.com", uint16(1))
	time.Sleep(10 * time.Millisecond)

	// Add a 4th entry (should evict example2, not example1 since we just accessed it)
	cache.Put("example4.com", uint16(1), []byte{0x04}, 3600)

	// example2 should be evicted
	_, ok2 := cache.Get("example2.com", uint16(1))
	if ok2 {
		t.Error("example2 should have been evicted")
	}

	// example1 should still be there (we accessed it recently)
	_, ok1 := cache.Get("example1.com", uint16(1))
	if !ok1 {
		t.Error("example1 should still be cached (recently accessed)")
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cache, err := New(dbPath, 1000, newTestLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	done := make(chan bool, 20)

	// Concurrent Put operations
	for i := 0; i < 10; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				name := "example" + string(rune(byte(idx*10+j))) + ".com"
				cache.Put(name, uint16(1), []byte{byte(idx)}, 3600)
			}
			done <- true
		}(i)
	}

	// Concurrent Get operations
	for i := 0; i < 10; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				name := "example" + string(rune(byte(idx*10+j))) + ".com"
				cache.Get(name, uint16(1))
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify cache is still functional
	total, _ := cache.Stats()
	if total == 0 {
		t.Error("cache should have entries after concurrent operations")
	}
}
