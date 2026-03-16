package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/miekg/dns"
	"github.com/phubbard/lantern/pkg/cache"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/metrics"
)

// Resolver handles DNS resolution with caching, DoH, and fallback support.
type Resolver struct {
	cfg        *config.Config
	cache      *cache.Cache
	metrics    *metrics.Collector
	httpClient *http.Client
	dnsClient  *dns.Client
	logger     *slog.Logger
}

// New creates a new upstream resolver with the given configuration and cache.
func New(cfg *config.Config, c *cache.Cache, m *metrics.Collector, logger *slog.Logger) *Resolver {
	// Create HTTP client with HTTP/2 support, pooling, and reasonable timeouts
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			// HTTP/2 is supported by default in Go's http package when using HTTPS
		},
	}

	// Create DNS client for plain DNS queries
	dnsClient := &dns.Client{
		Net:     "udp",
		Timeout: 3 * time.Second,
	}

	return &Resolver{
		cfg:        cfg,
		cache:      c,
		metrics:    m,
		httpClient: httpClient,
		dnsClient:  dnsClient,
		logger:     logger,
	}
}

// Resolve attempts to resolve a DNS query using cache, DoH, and fallback servers.
// Returns the DNS response, the source of the response, and any error.
func (r *Resolver) Resolve(ctx context.Context, msg *dns.Msg) (*dns.Msg, string, error) {
	if msg == nil || len(msg.Question) == 0 {
		return nil, "", fmt.Errorf("invalid DNS message")
	}

	question := msg.Question[0]
	name := question.Name
	qtype := question.Qtype

	// Step 1: Check cache
	if cachedResponse, found := r.lookupCache(name, qtype); found {
		r.logger.Debug("cache hit", "name", name, "type", dns.TypeToString[qtype])
		r.metrics.IncCacheHits()
		return cachedResponse, "cache", nil
	}

	// Step 2: Try DoH
	if r.cfg.Upstream.DOHURL != "" {
		response, err := r.resolveDoH(ctx, msg)
		if err == nil {
			r.logger.Debug("resolved via DoH", "name", name, "type", dns.TypeToString[qtype])
			r.cacheResponse(response)
			r.metrics.IncQueriesUpstream()
			return response, "upstream_doh", nil
		}
		r.logger.Debug("DoH resolution failed", "name", name, "type", dns.TypeToString[qtype], "err", err)
		r.metrics.IncCacheMisses()
	}

	// Step 3: Try fallback servers
	for _, server := range r.cfg.Upstream.FallbackServers {
		response, err := r.resolvePlainDNS(ctx, msg, server)
		if err == nil {
			r.logger.Debug("resolved via fallback", "name", name, "type", dns.TypeToString[qtype], "server", server)
			r.cacheResponse(response)
			r.metrics.IncQueriesUpstream()
			return response, "upstream_fallback", nil
		}
		r.logger.Debug("fallback resolution failed", "server", server, "name", name, "type", dns.TypeToString[qtype], "err", err)
		r.metrics.IncCacheMisses()
	}

	// Step 4: Return SERVFAIL if all resolution methods fail
	failResponse := new(dns.Msg)
	failResponse.SetReply(msg)
	failResponse.Rcode = dns.RcodeServerFailure
	r.logger.Warn("all resolution methods failed", "name", name, "type", dns.TypeToString[qtype])
	return failResponse, "", fmt.Errorf("all upstream servers failed")
}

// resolveDoH sends a DNS query via HTTPS (DoH - DNS over HTTPS).
func (r *Resolver) resolveDoH(ctx context.Context, msg *dns.Msg) (*dns.Msg, error) {
	// Pack the DNS message into wire format
	msgBytes, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("failed to pack DNS message: %w", err)
	}

	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, "POST", r.cfg.Upstream.DOHURL, bytes.NewReader(msgBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create DoH request: %w", err)
	}

	// Set appropriate headers for DoH
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	// Send the request
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DoH request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH server returned status %d", resp.StatusCode)
	}

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read DoH response: %w", err)
	}

	// Unpack DNS message
	responseMsg := new(dns.Msg)
	err = responseMsg.Unpack(respBody)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack DoH response: %w", err)
	}

	return responseMsg, nil
}

// resolvePlainDNS sends a DNS query via plain DNS (UDP/TCP).
// If the response is truncated, it retries with TCP.
func (r *Resolver) resolvePlainDNS(ctx context.Context, msg *dns.Msg, server string) (*dns.Msg, error) {
	// Ensure server has a port
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}

	// First attempt with UDP
	response, _, err := r.dnsClient.ExchangeContext(ctx, msg, server)
	if err != nil {
		return nil, fmt.Errorf("UDP query failed: %w", err)
	}

	// If truncated, retry with TCP
	if response != nil && response.Truncated {
		r.logger.Debug("response truncated, retrying with TCP", "server", server)
		tcpClient := &dns.Client{
			Net:     "tcp",
			Timeout: 3 * time.Second,
		}
		response, _, err = tcpClient.ExchangeContext(ctx, msg, server)
		if err != nil {
			return nil, fmt.Errorf("TCP query failed: %w", err)
		}
	}

	return response, nil
}

// cacheResponse extracts the TTL from the response and caches the packed bytes.
func (r *Resolver) cacheResponse(msg *dns.Msg) {
	if msg == nil || len(msg.Question) == 0 {
		return
	}

	question := msg.Question[0]
	name := question.Name
	qtype := question.Qtype

	// Extract minimum TTL from answer section
	ttl := extractMinTTL(msg)

	// Pack the message for caching
	msgBytes, err := msg.Pack()
	if err != nil {
		r.logger.Error("failed to pack message for cache", "name", name, "type", dns.TypeToString[qtype], "err", err)
		return
	}

	// Store in cache
	r.cache.Put(name, qtype, msgBytes, int(ttl))
}

// lookupCache checks the cache for a DNS response and unpacks it.
func (r *Resolver) lookupCache(name string, qtype uint16) (*dns.Msg, bool) {
	msgBytes, found := r.cache.Get(name, qtype)
	if !found {
		return nil, false
	}

	// Unpack the cached message
	msg := new(dns.Msg)
	err := msg.Unpack(msgBytes)
	if err != nil {
		r.logger.Error("failed to unpack cached message", "name", name, "type", dns.TypeToString[qtype], "err", err)
		return nil, false
	}

	return msg, true
}

// extractMinTTL finds the minimum TTL across all answer records.
// Returns a default TTL of 300 seconds if no answers are present.
func extractMinTTL(msg *dns.Msg) uint32 {
	if msg == nil || len(msg.Answer) == 0 {
		return 300 // default TTL
	}

	minTTL := ^uint32(0) // max uint32
	for _, rr := range msg.Answer {
		if rr.Header().Ttl < minTTL {
			minTTL = rr.Header().Ttl
		}
	}

	// Ensure we have a valid TTL (at least 1 second)
	if minTTL < 1 {
		minTTL = 1
	}

	// Cap at 24 hours; the two-tier cache handles eviction naturally
	if minTTL > 86400 {
		minTTL = 86400
	}

	return minTTL
}
