package metrics

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

const (
	// Histogram range: 1 to 60_000_000 microseconds (60 seconds)
	histoMin      = int64(1)
	histoMax      = int64(60_000_000)
	histoSigFigs  = 3
)

// LatencySource constants for different query sources
const (
	SourceLocal         = "local"
	SourceCached        = "cached"
	SourceUpstreamDoH   = "upstream_doh"
	SourceUpstreamFall  = "upstream_fallback"
	SourceBlocked       = "blocked"
)

// Collector tracks latency and counters for lantern server operations.
type Collector struct {
	mu sync.Mutex

	// Latency histograms per source
	histograms map[string]*hdrhistogram.Histogram

	// Atomic counters
	QueriesTotal      uint64
	QueriesLocal      uint64
	QueriesCached     uint64
	QueriesUpstream   uint64
	QueriesBlocked    uint64
	DHCPDiscovers     uint64
	DHCPOffers        uint64
	DHCPRequests      uint64
	DHCPAcks          uint64
	DHCPNaks          uint64
	DHCPReleases      uint64
	CacheHits         uint64
	CacheMisses       uint64
}

// LatencyStats holds latency percentiles for a query source.
type LatencyStats struct {
	P5   float64 // milliseconds
	P50  float64 // milliseconds
	P90  float64 // milliseconds
	P95  float64 // milliseconds
	P99  float64 // milliseconds
	Count int64
}

// MetricsSnapshot captures a point-in-time view of all metrics.
type MetricsSnapshot struct {
	QueriesTotal    uint64
	QueriesLocal    uint64
	QueriesCached   uint64
	QueriesUpstream uint64
	QueriesBlocked  uint64
	DHCPDiscovers   uint64
	DHCPOffers      uint64
	DHCPRequests    uint64
	DHCPAcks        uint64
	DHCPNaks        uint64
	DHCPReleases    uint64
	CacheHits       uint64
	CacheMisses     uint64

	// Latency stats per source
	Latency map[string]LatencyStats
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	sources := []string{
		SourceLocal,
		SourceCached,
		SourceUpstreamDoH,
		SourceUpstreamFall,
		SourceBlocked,
	}

	histograms := make(map[string]*hdrhistogram.Histogram, len(sources))
	for _, source := range sources {
		h := hdrhistogram.New(histoMin, histoMax, histoSigFigs)
		histograms[source] = h
	}

	return &Collector{
		histograms: histograms,
	}
}

// RecordLatency records a latency measurement for a given source.
// d should be in microseconds for consistency with histogram range.
func (c *Collector) RecordLatency(source string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	h, exists := c.histograms[source]
	if !exists {
		return
	}

	// Convert duration to microseconds
	microseconds := d.Microseconds()
	if microseconds < histoMin {
		microseconds = histoMin
	}
	if microseconds > histoMax {
		microseconds = histoMax
	}

	h.RecordValue(microseconds)
}

// IncrCounter increments a named counter.
func (c *Collector) IncrCounter(name string) {
	switch name {
	case "queries_total":
		atomic.AddUint64(&c.QueriesTotal, 1)
	case "queries_local":
		atomic.AddUint64(&c.QueriesLocal, 1)
	case "queries_cached":
		atomic.AddUint64(&c.QueriesCached, 1)
	case "queries_upstream":
		atomic.AddUint64(&c.QueriesUpstream, 1)
	case "queries_blocked":
		atomic.AddUint64(&c.QueriesBlocked, 1)
	case "dhcp_discovers":
		atomic.AddUint64(&c.DHCPDiscovers, 1)
	case "dhcp_offers":
		atomic.AddUint64(&c.DHCPOffers, 1)
	case "dhcp_requests":
		atomic.AddUint64(&c.DHCPRequests, 1)
	case "dhcp_acks":
		atomic.AddUint64(&c.DHCPAcks, 1)
	case "dhcp_naks":
		atomic.AddUint64(&c.DHCPNaks, 1)
	case "dhcp_releases":
		atomic.AddUint64(&c.DHCPReleases, 1)
	case "cache_hits":
		atomic.AddUint64(&c.CacheHits, 1)
	case "cache_misses":
		atomic.AddUint64(&c.CacheMisses, 1)
	}
}

// IncQueriesTotal increments total query counter.
func (c *Collector) IncQueriesTotal() {
	atomic.AddUint64(&c.QueriesTotal, 1)
}

// IncQueriesLocal increments local query counter.
func (c *Collector) IncQueriesLocal() {
	atomic.AddUint64(&c.QueriesLocal, 1)
}

// IncQueriesCached increments cached query counter.
func (c *Collector) IncQueriesCached() {
	atomic.AddUint64(&c.QueriesCached, 1)
}

// IncQueriesUpstream increments upstream query counter.
func (c *Collector) IncQueriesUpstream() {
	atomic.AddUint64(&c.QueriesUpstream, 1)
}

// IncQueriesBlocked increments blocked query counter.
func (c *Collector) IncQueriesBlocked() {
	atomic.AddUint64(&c.QueriesBlocked, 1)
}

// IncCacheHits increments cache hits counter.
func (c *Collector) IncCacheHits() {
	atomic.AddUint64(&c.CacheHits, 1)
}

// IncCacheMisses increments cache misses counter.
func (c *Collector) IncCacheMisses() {
	atomic.AddUint64(&c.CacheMisses, 1)
}

// Snapshot returns a point-in-time snapshot of all metrics.
func (c *Collector) Snapshot() *MetricsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := &MetricsSnapshot{
		QueriesTotal:    atomic.LoadUint64(&c.QueriesTotal),
		QueriesLocal:    atomic.LoadUint64(&c.QueriesLocal),
		QueriesCached:   atomic.LoadUint64(&c.QueriesCached),
		QueriesUpstream: atomic.LoadUint64(&c.QueriesUpstream),
		QueriesBlocked:  atomic.LoadUint64(&c.QueriesBlocked),
		DHCPDiscovers:   atomic.LoadUint64(&c.DHCPDiscovers),
		DHCPOffers:      atomic.LoadUint64(&c.DHCPOffers),
		DHCPRequests:    atomic.LoadUint64(&c.DHCPRequests),
		DHCPAcks:        atomic.LoadUint64(&c.DHCPAcks),
		DHCPNaks:        atomic.LoadUint64(&c.DHCPNaks),
		DHCPReleases:    atomic.LoadUint64(&c.DHCPReleases),
		CacheHits:       atomic.LoadUint64(&c.CacheHits),
		CacheMisses:     atomic.LoadUint64(&c.CacheMisses),
		Latency:         make(map[string]LatencyStats),
	}

	// Extract percentiles from each histogram
	for source, h := range c.histograms {
		stats := LatencyStats{
			P5:   float64(h.ValueAtPercentile(5)) / 1000.0,   // convert microseconds to milliseconds
			P50:  float64(h.ValueAtPercentile(50)) / 1000.0,
			P90:  float64(h.ValueAtPercentile(90)) / 1000.0,
			P95:  float64(h.ValueAtPercentile(95)) / 1000.0,
			P99:  float64(h.ValueAtPercentile(99)) / 1000.0,
			Count: h.TotalCount(),
		}
		snap.Latency[source] = stats
	}

	return snap
}

// Reset clears all histograms (useful for rolling windows).
func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for source := range c.histograms {
		h := hdrhistogram.New(histoMin, histoMax, histoSigFigs)
		c.histograms[source] = h
	}
}
