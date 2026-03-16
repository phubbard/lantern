package metrics

import (
	"sync"
	"testing"
	"time"
)

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("expected non-nil collector")
	}

	// Verify histograms are initialized for all sources
	sources := []string{SourceLocal, SourceCached, SourceUpstreamDoH, SourceUpstreamFall, SourceBlocked}
	for _, s := range sources {
		if _, ok := c.histograms[s]; !ok {
			t.Errorf("missing histogram for source %q", s)
		}
	}
}

func TestCollector_IncrCounter(t *testing.T) {
	c := NewCollector()

	c.IncrCounter("queries_total")
	c.IncrCounter("queries_total")
	c.IncrCounter("queries_local")
	c.IncrCounter("queries_cached")
	c.IncrCounter("queries_upstream")
	c.IncrCounter("queries_blocked")
	c.IncrCounter("dhcp_discovers")
	c.IncrCounter("dhcp_offers")
	c.IncrCounter("dhcp_requests")
	c.IncrCounter("dhcp_acks")
	c.IncrCounter("dhcp_naks")
	c.IncrCounter("dhcp_releases")
	c.IncrCounter("cache_hits")
	c.IncrCounter("cache_misses")
	c.IncrCounter("unknown_counter") // should be a no-op

	snap := c.Snapshot()
	if snap.QueriesTotal != 2 {
		t.Errorf("expected QueriesTotal=2, got %d", snap.QueriesTotal)
	}
	if snap.QueriesLocal != 1 {
		t.Errorf("expected QueriesLocal=1, got %d", snap.QueriesLocal)
	}
	if snap.QueriesCached != 1 {
		t.Errorf("expected QueriesCached=1, got %d", snap.QueriesCached)
	}
	if snap.QueriesUpstream != 1 {
		t.Errorf("expected QueriesUpstream=1, got %d", snap.QueriesUpstream)
	}
	if snap.QueriesBlocked != 1 {
		t.Errorf("expected QueriesBlocked=1, got %d", snap.QueriesBlocked)
	}
	if snap.DHCPDiscovers != 1 {
		t.Errorf("expected DHCPDiscovers=1, got %d", snap.DHCPDiscovers)
	}
	if snap.DHCPOffers != 1 {
		t.Errorf("expected DHCPOffers=1, got %d", snap.DHCPOffers)
	}
	if snap.DHCPRequests != 1 {
		t.Errorf("expected DHCPRequests=1, got %d", snap.DHCPRequests)
	}
	if snap.DHCPAcks != 1 {
		t.Errorf("expected DHCPAcks=1, got %d", snap.DHCPAcks)
	}
	if snap.DHCPNaks != 1 {
		t.Errorf("expected DHCPNaks=1, got %d", snap.DHCPNaks)
	}
	if snap.DHCPReleases != 1 {
		t.Errorf("expected DHCPReleases=1, got %d", snap.DHCPReleases)
	}
	if snap.CacheHits != 1 {
		t.Errorf("expected CacheHits=1, got %d", snap.CacheHits)
	}
	if snap.CacheMisses != 1 {
		t.Errorf("expected CacheMisses=1, got %d", snap.CacheMisses)
	}
}

func TestCollector_IncHelpers(t *testing.T) {
	c := NewCollector()

	c.IncQueriesTotal()
	c.IncQueriesLocal()
	c.IncQueriesCached()
	c.IncQueriesUpstream()
	c.IncQueriesBlocked()
	c.IncCacheHits()
	c.IncCacheMisses()

	snap := c.Snapshot()
	if snap.QueriesTotal != 1 {
		t.Error("IncQueriesTotal failed")
	}
	if snap.QueriesLocal != 1 {
		t.Error("IncQueriesLocal failed")
	}
	if snap.QueriesCached != 1 {
		t.Error("IncQueriesCached failed")
	}
	if snap.QueriesUpstream != 1 {
		t.Error("IncQueriesUpstream failed")
	}
	if snap.QueriesBlocked != 1 {
		t.Error("IncQueriesBlocked failed")
	}
	if snap.CacheHits != 1 {
		t.Error("IncCacheHits failed")
	}
	if snap.CacheMisses != 1 {
		t.Error("IncCacheMisses failed")
	}
}

func TestCollector_RecordLatency(t *testing.T) {
	c := NewCollector()

	// Record some latency values
	c.RecordLatency(SourceLocal, 100*time.Microsecond)
	c.RecordLatency(SourceLocal, 200*time.Microsecond)
	c.RecordLatency(SourceLocal, 500*time.Microsecond)

	snap := c.Snapshot()
	stats, ok := snap.Latency[SourceLocal]
	if !ok {
		t.Fatal("missing latency stats for local source")
	}
	if stats.Count != 3 {
		t.Errorf("expected count 3, got %d", stats.Count)
	}
	if stats.P50 <= 0 {
		t.Error("expected positive P50")
	}
}

func TestCollector_RecordLatency_UnknownSource(t *testing.T) {
	c := NewCollector()
	// Should not panic
	c.RecordLatency("nonexistent", 100*time.Microsecond)
}

func TestCollector_RecordLatency_Clamping(t *testing.T) {
	c := NewCollector()

	// Very small latency should be clamped to minimum
	c.RecordLatency(SourceLocal, 0)
	// Very large latency should be clamped to maximum
	c.RecordLatency(SourceLocal, 120*time.Second)

	snap := c.Snapshot()
	stats := snap.Latency[SourceLocal]
	if stats.Count != 2 {
		t.Errorf("expected count 2, got %d", stats.Count)
	}
}

func TestCollector_Snapshot_Latency(t *testing.T) {
	c := NewCollector()

	snap := c.Snapshot()
	if snap.Latency == nil {
		t.Fatal("expected non-nil Latency map")
	}

	// All sources should have zero-count stats
	for _, source := range []string{SourceLocal, SourceCached, SourceUpstreamDoH, SourceUpstreamFall, SourceBlocked} {
		stats, ok := snap.Latency[source]
		if !ok {
			t.Errorf("missing stats for source %q", source)
		}
		if stats.Count != 0 {
			t.Errorf("expected 0 count for %q, got %d", source, stats.Count)
		}
	}
}

func TestCollector_Reset(t *testing.T) {
	c := NewCollector()

	c.RecordLatency(SourceLocal, 100*time.Microsecond)
	c.Reset()

	snap := c.Snapshot()
	stats := snap.Latency[SourceLocal]
	if stats.Count != 0 {
		t.Errorf("expected 0 after reset, got %d", stats.Count)
	}
}

func TestCollector_ConcurrentAccess(t *testing.T) {
	c := NewCollector()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.IncQueriesTotal()
				c.RecordLatency(SourceLocal, time.Duration(j)*time.Microsecond)
			}
		}()
	}

	// Concurrent snapshots
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Snapshot()
		}()
	}

	wg.Wait()

	snap := c.Snapshot()
	if snap.QueriesTotal != 1000 {
		t.Errorf("expected QueriesTotal=1000, got %d", snap.QueriesTotal)
	}
}
