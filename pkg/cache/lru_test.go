package cache

import (
	"testing"
	"time"
)

func TestMemLRU_PutGet(t *testing.T) {
	lru := newMemLRU(10)

	// Put and get
	lru.put("example.com.", 1, []byte("response1"), 300)
	resp, ok := lru.get("example.com.", 1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(resp) != "response1" {
		t.Errorf("expected 'response1', got %q", string(resp))
	}
}

func TestMemLRU_Miss(t *testing.T) {
	lru := newMemLRU(10)

	_, ok := lru.get("nonexistent.com.", 1)
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestMemLRU_DifferentQtype(t *testing.T) {
	lru := newMemLRU(10)

	lru.put("example.com.", 1, []byte("A-record"), 300)
	lru.put("example.com.", 28, []byte("AAAA-record"), 300)

	resp, ok := lru.get("example.com.", 1)
	if !ok || string(resp) != "A-record" {
		t.Error("expected A record")
	}

	resp, ok = lru.get("example.com.", 28)
	if !ok || string(resp) != "AAAA-record" {
		t.Error("expected AAAA record")
	}
}

func TestMemLRU_Expiry(t *testing.T) {
	lru := newMemLRU(10)

	// Put with 1-second TTL
	lru.put("example.com.", 1, []byte("response"), 1)

	// Should be found immediately
	_, ok := lru.get("example.com.", 1)
	if !ok {
		t.Fatal("expected cache hit before expiry")
	}

	// Wait for expiry
	time.Sleep(1100 * time.Millisecond)

	_, ok = lru.get("example.com.", 1)
	if ok {
		t.Fatal("expected cache miss after expiry")
	}
}

func TestMemLRU_Eviction(t *testing.T) {
	lru := newMemLRU(3)

	lru.put("a.com.", 1, []byte("a"), 300)
	lru.put("b.com.", 1, []byte("b"), 300)
	lru.put("c.com.", 1, []byte("c"), 300)

	// Cache is full, adding a 4th should evict the LRU (a.com)
	lru.put("d.com.", 1, []byte("d"), 300)

	_, ok := lru.get("a.com.", 1)
	if ok {
		t.Error("expected a.com to be evicted")
	}

	_, ok = lru.get("d.com.", 1)
	if !ok {
		t.Error("expected d.com to be present")
	}

	if lru.size() != 3 {
		t.Errorf("expected size 3, got %d", lru.size())
	}
}

func TestMemLRU_UpdateExisting(t *testing.T) {
	lru := newMemLRU(10)

	lru.put("example.com.", 1, []byte("old"), 300)
	lru.put("example.com.", 1, []byte("new"), 600)

	resp, ok := lru.get("example.com.", 1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(resp) != "new" {
		t.Errorf("expected 'new', got %q", string(resp))
	}

	if lru.size() != 1 {
		t.Errorf("expected size 1, got %d", lru.size())
	}
}

func TestMemLRU_Clear(t *testing.T) {
	lru := newMemLRU(10)

	lru.put("a.com.", 1, []byte("a"), 300)
	lru.put("b.com.", 1, []byte("b"), 300)

	lru.clear()

	if lru.size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", lru.size())
	}

	_, ok := lru.get("a.com.", 1)
	if ok {
		t.Error("expected miss after clear")
	}
}

func TestMemLRU_LRUOrder(t *testing.T) {
	lru := newMemLRU(3)

	lru.put("a.com.", 1, []byte("a"), 300)
	lru.put("b.com.", 1, []byte("b"), 300)
	lru.put("c.com.", 1, []byte("c"), 300)

	// Access a.com to promote it
	lru.get("a.com.", 1)

	// Adding d.com should evict b.com (the new LRU) instead of a.com
	lru.put("d.com.", 1, []byte("d"), 300)

	_, ok := lru.get("a.com.", 1)
	if !ok {
		t.Error("expected a.com to survive (was promoted)")
	}

	_, ok = lru.get("b.com.", 1)
	if ok {
		t.Error("expected b.com to be evicted")
	}
}

func TestMemLRU_DefaultSize(t *testing.T) {
	lru := newMemLRU(0) // should default to 5000
	if lru.maxSize != 5000 {
		t.Errorf("expected default maxSize 5000, got %d", lru.maxSize)
	}
}
