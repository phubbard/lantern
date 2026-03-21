package upstream

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/phubbard/lantern/pkg/cache"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/metrics"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestCache(t *testing.T) *cache.Cache {
	t.Helper()
	dbPath := t.TempDir() + "/test-cache.db"
	c, err := cache.New(dbPath, 1000, testLogger())
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func newTestResolver(t *testing.T, dohURL string, fallback []string) *Resolver {
	t.Helper()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{
			DOHURL:          dohURL,
			FallbackServers: fallback,
		},
	}
	c := newTestCache(t)
	m := metrics.NewCollector()
	return New(cfg, c, m, testLogger())
}

func TestNew(t *testing.T) {
	r := newTestResolver(t, "", nil)
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if r.httpClient == nil {
		t.Fatal("expected non-nil http client")
	}
	if r.dnsClient == nil {
		t.Fatal("expected non-nil dns client")
	}
}

func TestResolve_NilMessage(t *testing.T) {
	r := newTestResolver(t, "", nil)
	_, _, err := r.Resolve(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil message")
	}
}

func TestResolve_EmptyQuestion(t *testing.T) {
	r := newTestResolver(t, "", nil)
	msg := new(dns.Msg)
	_, _, err := r.Resolve(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for empty question")
	}
}

func TestResolve_CacheHit(t *testing.T) {
	r := newTestResolver(t, "", nil)

	// Build a DNS response and manually cache it
	question := dns.Question{Name: "cached.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}
	resp := new(dns.Msg)
	resp.SetQuestion(question.Name, question.Qtype)
	resp.Answer = append(resp.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   net.ParseIP("1.2.3.4"),
	})

	msgBytes, err := resp.Pack()
	if err != nil {
		t.Fatalf("failed to pack response: %v", err)
	}
	r.cache.Put(question.Name, question.Qtype, msgBytes, 300)

	// Now resolve - should hit cache
	query := new(dns.Msg)
	query.SetQuestion(question.Name, question.Qtype)

	result, source, err := r.Resolve(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "cache" {
		t.Errorf("expected source=cache, got %s", source)
	}
	if len(result.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(result.Answer))
	}
}

func TestResolve_DoH(t *testing.T) {
	// Create a mock DoH server
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}

		// Read the DNS query
		body := make([]byte, 512)
		n, _ := r.Body.Read(body)
		query := new(dns.Msg)
		if err := query.Unpack(body[:n]); err != nil {
			http.Error(w, "bad dns message", http.StatusBadRequest)
			return
		}

		// Build response
		resp := new(dns.Msg)
		resp.SetReply(query)
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("10.0.0.1"),
		})

		packed, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(packed)
	}))
	defer dohServer.Close()

	r := newTestResolver(t, dohServer.URL, nil)

	query := new(dns.Msg)
	query.SetQuestion("doh.example.com.", dns.TypeA)

	result, source, err := r.Resolve(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "upstream_doh" {
		t.Errorf("expected source=upstream_doh, got %s", source)
	}
	if len(result.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(result.Answer))
	}

	// Verify it was cached
	cached, found := r.cache.Get("doh.example.com.", dns.TypeA)
	if !found {
		t.Error("expected response to be cached")
	}
	if cached == nil {
		t.Error("expected non-nil cached response")
	}
}

func TestResolve_DoHError(t *testing.T) {
	// DoH server that returns errors
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer dohServer.Close()

	r := newTestResolver(t, dohServer.URL, nil)

	query := new(dns.Msg)
	query.SetQuestion("fail.example.com.", dns.TypeA)

	// With no fallback servers either, should fail
	result, _, err := r.Resolve(context.Background(), query)
	if err == nil {
		t.Fatal("expected error when DoH and all fallbacks fail")
	}
	if result == nil {
		t.Fatal("expected SERVFAIL response, got nil")
	}
	if result.Rcode != dns.RcodeServerFailure {
		t.Errorf("expected SERVFAIL rcode, got %d", result.Rcode)
	}
}

func TestResolve_FallbackServer(t *testing.T) {
	// Start a local DNS server as fallback
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer pc.Close()

	addr := pc.LocalAddr().String()

	// Handle DNS queries in a goroutine
	go func() {
		buf := make([]byte, 512)
		for {
			n, rAddr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			query := new(dns.Msg)
			if err := query.Unpack(buf[:n]); err != nil {
				continue
			}

			resp := new(dns.Msg)
			resp.SetReply(query)
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120},
				A:   net.ParseIP("10.0.0.2"),
			})

			packed, _ := resp.Pack()
			pc.WriteTo(packed, rAddr)
		}
	}()

	r := newTestResolver(t, "", []string{addr})

	query := new(dns.Msg)
	query.SetQuestion("fallback.example.com.", dns.TypeA)

	result, source, err := r.Resolve(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "upstream_fallback" {
		t.Errorf("expected source=upstream_fallback, got %s", source)
	}
	if len(result.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(result.Answer))
	}
}

func TestResolve_AllFail(t *testing.T) {
	// No DoH, no fallback servers
	r := newTestResolver(t, "", nil)

	query := new(dns.Msg)
	query.SetQuestion("nope.example.com.", dns.TypeA)

	result, source, err := r.Resolve(context.Background(), query)
	if err == nil {
		t.Fatal("expected error when all methods fail")
	}
	if source != "" {
		t.Errorf("expected empty source, got %s", source)
	}
	if result.Rcode != dns.RcodeServerFailure {
		t.Errorf("expected SERVFAIL, got %d", result.Rcode)
	}
}

func TestResolve_ContextCancelled(t *testing.T) {
	// DoH server that is slow
	dohServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer dohServer.Close()

	r := newTestResolver(t, dohServer.URL, nil)

	query := new(dns.Msg)
	query.SetQuestion("slow.example.com.", dns.TypeA)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := r.Resolve(ctx, query)
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

func TestExtractMinTTL_NilMessage(t *testing.T) {
	ttl := extractMinTTL(nil)
	if ttl != 300 {
		t.Errorf("expected default TTL 300, got %d", ttl)
	}
}

func TestExtractMinTTL_NoAnswers(t *testing.T) {
	msg := new(dns.Msg)
	ttl := extractMinTTL(msg)
	if ttl != 300 {
		t.Errorf("expected default TTL 300, got %d", ttl)
	}
}

func TestExtractMinTTL_SingleAnswer(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = append(msg.Answer, &dns.A{
		Hdr: dns.RR_Header{Ttl: 120},
		A:   net.ParseIP("1.2.3.4"),
	})
	ttl := extractMinTTL(msg)
	if ttl != 120 {
		t.Errorf("expected TTL 120, got %d", ttl)
	}
}

func TestExtractMinTTL_MultipleAnswers(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = append(msg.Answer,
		&dns.A{Hdr: dns.RR_Header{Ttl: 300}, A: net.ParseIP("1.2.3.4")},
		&dns.A{Hdr: dns.RR_Header{Ttl: 60}, A: net.ParseIP("5.6.7.8")},
		&dns.A{Hdr: dns.RR_Header{Ttl: 180}, A: net.ParseIP("9.10.11.12")},
	)
	ttl := extractMinTTL(msg)
	if ttl != 60 {
		t.Errorf("expected minimum TTL 60, got %d", ttl)
	}
}

func TestExtractMinTTL_ZeroTTL(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = append(msg.Answer, &dns.A{
		Hdr: dns.RR_Header{Ttl: 0},
		A:   net.ParseIP("1.2.3.4"),
	})
	ttl := extractMinTTL(msg)
	if ttl != 1 {
		t.Errorf("expected clamped TTL 1, got %d", ttl)
	}
}

func TestExtractMinTTL_VeryHighTTL(t *testing.T) {
	msg := new(dns.Msg)
	msg.Answer = append(msg.Answer, &dns.A{
		Hdr: dns.RR_Header{Ttl: 172800}, // 48 hours — should be capped to 24h
		A:   net.ParseIP("1.2.3.4"),
	})
	ttl := extractMinTTL(msg)
	if ttl != 86400 {
		t.Errorf("expected capped TTL 86400, got %d", ttl)
	}
}

func TestCacheResponse_NilMessage(t *testing.T) {
	r := newTestResolver(t, "", nil)
	// Should not panic
	r.cacheResponse(nil)
}

func TestCacheResponse_EmptyQuestion(t *testing.T) {
	r := newTestResolver(t, "", nil)
	msg := new(dns.Msg)
	// Should not panic
	r.cacheResponse(msg)
}

func TestLookupCache_Miss(t *testing.T) {
	r := newTestResolver(t, "", nil)
	result, found := r.lookupCache("missing.example.com.", dns.TypeA)
	if found {
		t.Error("expected cache miss")
	}
	if result != nil {
		t.Error("expected nil result for cache miss")
	}
}

func TestResolvePlainDNS_AddsPort(t *testing.T) {
	// Start a local DNS server
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer pc.Close()

	_, port, _ := net.SplitHostPort(pc.LocalAddr().String())

	go func() {
		buf := make([]byte, 512)
		for {
			n, rAddr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			query := new(dns.Msg)
			if err := query.Unpack(buf[:n]); err != nil {
				continue
			}
			resp := new(dns.Msg)
			resp.SetReply(query)
			resp.Answer = append(resp.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("10.0.0.3"),
			})
			packed, _ := resp.Pack()
			pc.WriteTo(packed, rAddr)
		}
	}()

	r := newTestResolver(t, "", nil)
	query := new(dns.Msg)
	query.SetQuestion("porttest.example.com.", dns.TypeA)

	// Pass server with port already included
	result, err := r.resolvePlainDNS(context.Background(), query, "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Answer) != 1 {
		t.Errorf("expected 1 answer, got %d", len(result.Answer))
	}
}
