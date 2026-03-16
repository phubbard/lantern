package dns

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/pfh/lantern/pkg/config"
	"github.com/pfh/lantern/pkg/events"
	"github.com/pfh/lantern/pkg/metrics"
	"github.com/pfh/lantern/pkg/model"
)

// Zone Tests

func TestNewZone(t *testing.T) {
	domain := "home.lab."
	z := NewZone(domain)

	if z.domain != domain {
		t.Errorf("expected domain %q, got %q", domain, z.domain)
	}
	if len(z.records) != 0 {
		t.Errorf("expected empty records map, got %d entries", len(z.records))
	}
	if len(z.reverse) != 0 {
		t.Errorf("expected empty reverse map, got %d entries", len(z.reverse))
	}
}

func TestZone_UpdateFromLease_A_Record(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
	}

	z.UpdateFromLease(lease)

	// Verify A record is stored
	fqdn := "myhost.home.lab."
	records := z.Lookup(fqdn, dns.TypeA)
	if len(records) != 1 {
		t.Fatalf("expected 1 A record, got %d", len(records))
	}

	aRecord, ok := records[0].(*dns.A)
	if !ok {
		t.Fatal("expected *dns.A record")
	}

	if !aRecord.A.Equal(net.ParseIP("192.168.1.100")) {
		t.Errorf("expected IP 192.168.1.100, got %s", aRecord.A.String())
	}
}

func TestZone_UpdateFromLease_PTR_Record(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.42"),
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
	}

	z.UpdateFromLease(lease)

	// Verify PTR record
	reverseAddr := z.ReverseIP(lease.IP)
	records := z.Lookup(reverseAddr, dns.TypePTR)
	if len(records) != 1 {
		t.Fatalf("expected 1 PTR record, got %d", len(records))
	}

	ptrRecord, ok := records[0].(*dns.PTR)
	if !ok {
		t.Fatal("expected *dns.PTR record")
	}

	if ptrRecord.Ptr != "myhost.home.lab." {
		t.Errorf("expected PTR myhost.home.lab., got %s", ptrRecord.Ptr)
	}
}

func TestZone_UpdateFromLease_TXT_With_Fingerprint(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
		Fingerprint: &model.HostFingerprint{
			OS:         "Linux",
			DeviceType: "Desktop",
			Vendor:     "Generic",
		},
	}

	z.UpdateFromLease(lease)

	// Verify TXT record is present
	fqdn := "myhost.home.lab."
	records := z.Lookup(fqdn, dns.TypeTXT)
	if len(records) != 1 {
		t.Fatalf("expected 1 TXT record, got %d", len(records))
	}

	txtRecord, ok := records[0].(*dns.TXT)
	if !ok {
		t.Fatal("expected *dns.TXT record")
	}

	if len(txtRecord.Txt) != 1 {
		t.Fatalf("expected 1 TXT string, got %d", len(txtRecord.Txt))
	}

	expected := "os=Linux;type=Desktop;vendor=Generic"
	if txtRecord.Txt[0] != expected {
		t.Errorf("expected TXT %q, got %q", expected, txtRecord.Txt[0])
	}
}

func TestZone_UpdateFromLease_NoFingerprint(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
		// No fingerprint
	}

	z.UpdateFromLease(lease)

	fqdn := "myhost.home.lab."

	// Verify A record exists
	aRecords := z.Lookup(fqdn, dns.TypeA)
	if len(aRecords) != 1 {
		t.Fatalf("expected 1 A record, got %d", len(aRecords))
	}

	// Verify TXT record does NOT exist
	txtRecords := z.Lookup(fqdn, dns.TypeTXT)
	if len(txtRecords) != 0 {
		t.Errorf("expected 0 TXT records, got %d", len(txtRecords))
	}
}

func TestZone_RemoveLease(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
	}

	// Add the lease
	z.UpdateFromLease(lease)
	fqdn := "myhost.home.lab."
	if records := z.Lookup(fqdn, dns.TypeA); len(records) == 0 {
		t.Fatal("lease not added successfully")
	}

	// Remove the lease
	z.RemoveLease(lease)

	// Verify records are gone
	if records := z.Lookup(fqdn, dns.TypeA); len(records) != 0 {
		t.Errorf("expected 0 A records after removal, got %d", len(records))
	}

	reverseAddr := z.ReverseIP(lease.IP)
	if records := z.Lookup(reverseAddr, dns.TypePTR); len(records) != 0 {
		t.Errorf("expected 0 PTR records after removal, got %d", len(records))
	}
}

func TestZone_Lookup_A(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("10.0.0.50"),
		DNSName: "testhost.home.lab",
		TTL:     10 * time.Minute,
	}

	z.UpdateFromLease(lease)

	// Test with FQDN
	records := z.Lookup("testhost.home.lab.", dns.TypeA)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	aRecord := records[0].(*dns.A)
	if !aRecord.A.Equal(net.ParseIP("10.0.0.50")) {
		t.Errorf("expected 10.0.0.50, got %s", aRecord.A.String())
	}

	// Test without trailing dot (should be normalized)
	records = z.Lookup("testhost.home.lab", dns.TypeA)
	if len(records) != 1 {
		t.Fatalf("expected 1 record with normalized name, got %d", len(records))
	}
}

func TestZone_Lookup_PTR(t *testing.T) {
	z := NewZone("home.lab.")

	ip := net.ParseIP("192.168.1.42")
	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      ip,
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
	}

	z.UpdateFromLease(lease)

	reverseAddr := z.ReverseIP(ip)
	records := z.Lookup(reverseAddr, dns.TypePTR)
	if len(records) != 1 {
		t.Fatalf("expected 1 PTR record, got %d", len(records))
	}

	ptrRecord := records[0].(*dns.PTR)
	if ptrRecord.Ptr != "myhost.home.lab." {
		t.Errorf("expected myhost.home.lab., got %s", ptrRecord.Ptr)
	}
}

func TestZone_Lookup_TXT(t *testing.T) {
	z := NewZone("home.lab.")

	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
		Fingerprint: &model.HostFingerprint{
			OS:         "macOS",
			DeviceType: "Laptop",
			Vendor:     "Apple",
		},
	}

	z.UpdateFromLease(lease)

	records := z.Lookup("myhost.home.lab.", dns.TypeTXT)
	if len(records) != 1 {
		t.Fatalf("expected 1 TXT record, got %d", len(records))
	}

	txtRecord := records[0].(*dns.TXT)
	if len(txtRecord.Txt[0]) == 0 {
		t.Error("expected non-empty TXT data")
	}
}

func TestZone_Lookup_NotFound(t *testing.T) {
	z := NewZone("home.lab.")

	records := z.Lookup("nonexistent.home.lab.", dns.TypeA)
	if len(records) != 0 {
		t.Errorf("expected 0 records for non-existent name, got %d", len(records))
	}
}

func TestZone_ReverseIP_IPv4(t *testing.T) {
	z := NewZone("home.lab.")

	tests := []struct {
		ip       string
		expected string
	}{
		{"192.168.1.42", "42.1.168.192.in-addr.arpa."},
		{"10.0.0.1", "1.0.0.10.in-addr.arpa."},
		{"127.0.0.1", "1.0.0.127.in-addr.arpa."},
	}

	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		result := z.ReverseIP(ip)
		if result != tc.expected {
			t.Errorf("for %s: expected %q, got %q", tc.ip, tc.expected, result)
		}
	}
}

func TestZone_ConcurrentAccess(t *testing.T) {
	z := NewZone("home.lab.")

	// Run concurrent updates and lookups
	done := make(chan bool, 10)

	// Goroutines to update
	for i := 0; i < 5; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				ip := net.IPv4(192, 168, 1, byte(10+idx*5+j))
				lease := &model.Lease{
					MAC:     net.HardwareAddr{byte(idx), byte(j), 0, 0, 0, 0},
					IP:      ip,
					DNSName: "host.home.lab",
					TTL:     5 * time.Minute,
				}
				z.UpdateFromLease(lease)
			}
			done <- true
		}(i)
	}

	// Goroutines to lookup
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				z.Lookup("host.home.lab.", dns.TypeA)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify zone is in a consistent state
	records := z.Lookup("host.home.lab.", dns.TypeA)
	if len(records) == 0 {
		t.Fatal("expected at least one A record after concurrent updates")
	}
}

// Mock implementations for testing

type MockResolver struct {
	resolveFunc func(ctx context.Context, msg *dns.Msg) (*dns.Msg, string, error)
}

func (m *MockResolver) Resolve(ctx context.Context, msg *dns.Msg) (*dns.Msg, string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, msg)
	}
	// Default: return SERVFAIL
	resp := msg.Copy()
	resp.SetRcode(msg, dns.RcodeServerFailure)
	return resp, "error", nil
}

type MockBlocker struct {
	blockedDomains map[string]bool
}

func NewMockBlocker() *MockBlocker {
	return &MockBlocker{
		blockedDomains: make(map[string]bool),
	}
}

func (m *MockBlocker) IsBlocked(name string) bool {
	return m.blockedDomains[name]
}

func (m *MockBlocker) Block(name string) {
	m.blockedDomains[name] = true
}

// Server Integration Tests

func TestServer_LocalResolution(t *testing.T) {
	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "127.0.0.1:15353",
		},
	}

	resolver := &MockResolver{}
	blocker := NewMockBlocker()
	metrics := metrics.NewCollector()
	eventStore := events.NewStore(100)

	server := New(cfg, resolver, blocker, metrics, eventStore)

	// Add a lease to the zone
	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "test.home.lab",
		TTL:     5 * time.Minute,
	}
	server.Zone().UpdateFromLease(lease)

	// Start the server
	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Logf("error stopping server: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Query the local zone
	client := &dns.Client{}
	msg := &dns.Msg{}
	msg.SetQuestion("test.home.lab.", dns.TypeA)

	resp, _, err := client.Exchange(msg, "127.0.0.1:15353")
	if err != nil {
		t.Fatalf("DNS query failed: %v", err)
	}

	if len(resp.Answer) == 0 {
		t.Fatal("expected answer in response")
	}

	aRecord, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	if !aRecord.A.Equal(net.ParseIP("192.168.1.100")) {
		t.Errorf("expected 192.168.1.100, got %s", aRecord.A.String())
	}
}

func TestServer_BlockedDomain(t *testing.T) {
	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "127.0.0.1:15354",
		},
	}

	resolver := &MockResolver{}
	blocker := NewMockBlocker()
	blocker.Block("ads.example.com.")
	metrics := metrics.NewCollector()
	eventStore := events.NewStore(100)

	server := New(cfg, resolver, blocker, metrics, eventStore)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	// Query blocked domain
	client := &dns.Client{}
	msg := &dns.Msg{}
	msg.SetQuestion("ads.example.com.", dns.TypeA)

	resp, _, err := client.Exchange(msg, "127.0.0.1:15354")
	if err != nil {
		t.Fatalf("DNS query failed: %v", err)
	}

	// Should return 0.0.0.0
	if len(resp.Answer) == 0 {
		t.Fatal("expected answer in blocked response")
	}

	aRecord, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}

	if !aRecord.A.Equal(net.ParseIP("0.0.0.0")) {
		t.Errorf("expected 0.0.0.0 for blocked domain, got %s", aRecord.A.String())
	}
}

func TestServer_UpstreamForwarding(t *testing.T) {
	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "127.0.0.1:15355",
		},
	}

	upstreamCalled := false
	resolver := &MockResolver{
		resolveFunc: func(ctx context.Context, msg *dns.Msg) (*dns.Msg, string, error) {
			upstreamCalled = true
			// Return a response with a synthetic A record
			resp := msg.Copy()
			rr := &dns.A{
				Hdr: dns.RR_Header{
					Name:   "example.com.",
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP("93.184.216.34"),
			}
			resp.Answer = append(resp.Answer, rr)
			return resp, "upstream", nil
		},
	}

	blocker := NewMockBlocker()
	metrics := metrics.NewCollector()
	eventStore := events.NewStore(100)

	server := New(cfg, resolver, blocker, metrics, eventStore)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	// Query for external domain (not in local zone)
	client := &dns.Client{}
	msg := &dns.Msg{}
	msg.SetQuestion("example.com.", dns.TypeA)

	resp, _, err := client.Exchange(msg, "127.0.0.1:15355")
	if err != nil {
		t.Fatalf("DNS query failed: %v", err)
	}

	if !upstreamCalled {
		t.Error("upstream resolver was not called")
	}

	if len(resp.Answer) == 0 {
		t.Fatal("expected answer in upstream response")
	}
}

func TestServer_PTRQuery(t *testing.T) {
	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "127.0.0.1:15356",
		},
	}

	resolver := &MockResolver{}
	blocker := NewMockBlocker()
	metrics := metrics.NewCollector()
	eventStore := events.NewStore(100)

	server := New(cfg, resolver, blocker, metrics, eventStore)

	// Add a lease with the IP we'll query
	ip := net.ParseIP("192.168.1.50")
	lease := &model.Lease{
		MAC:     net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		IP:      ip,
		DNSName: "myhost.home.lab",
		TTL:     5 * time.Minute,
	}
	server.Zone().UpdateFromLease(lease)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	// Query PTR for the IP
	client := &dns.Client{}
	msg := &dns.Msg{}
	reverseAddr := server.Zone().ReverseIP(ip)
	msg.SetQuestion(reverseAddr, dns.TypePTR)

	resp, _, err := client.Exchange(msg, "127.0.0.1:15356")
	if err != nil {
		t.Fatalf("DNS query failed: %v", err)
	}

	if len(resp.Answer) == 0 {
		t.Fatal("expected answer in PTR response")
	}

	ptrRecord, ok := resp.Answer[0].(*dns.PTR)
	if !ok {
		t.Fatalf("expected PTR record, got %T", resp.Answer[0])
	}

	if ptrRecord.Ptr != "myhost.home.lab." {
		t.Errorf("expected myhost.home.lab., got %s", ptrRecord.Ptr)
	}
}
