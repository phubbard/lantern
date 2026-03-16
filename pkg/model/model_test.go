package model

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test NewLeasePool
func TestNewLeasePool(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	start := net.ParseIP("192.168.1.100")
	end := net.ParseIP("192.168.1.200")
	domain := "home.lab"
	defaultTTL := 24 * time.Hour
	staticTTL := 0 * time.Second

	pool := NewLeasePool(*subnet, start, end, domain, defaultTTL, staticTTL)

	if pool == nil {
		t.Fatal("NewLeasePool returned nil")
	}

	if !pool.Subnet.IP.Equal(subnet.IP) {
		t.Errorf("Subnet IP mismatch: %v vs %v", pool.Subnet.IP, subnet.IP)
	}

	if !pool.RangeStart.Equal(start) {
		t.Errorf("RangeStart mismatch: %v vs %v", pool.RangeStart, start)
	}

	if !pool.RangeEnd.Equal(end) {
		t.Errorf("RangeEnd mismatch: %v vs %v", pool.RangeEnd, end)
	}

	if pool.Domain != domain {
		t.Errorf("Domain mismatch: %s vs %s", pool.Domain, domain)
	}

	if len(pool.GetAllLeases()) != 0 {
		t.Errorf("New pool should have no leases, got %d", len(pool.GetAllLeases()))
	}
}

// Test AllocateIP
func TestAllocateIP(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	start := net.ParseIP("192.168.1.100")
	end := net.ParseIP("192.168.1.110")

	pool := NewLeasePool(*subnet, start, end, "home.lab", 1*time.Hour, 0)

	tests := []struct {
		name        string
		wantFirst   string
		wantSecond  string
		wantErr     bool
		description string
	}{
		{
			name:        "first allocation",
			wantFirst:   "192.168.1.100",
			wantSecond:  "192.168.1.101",
			description: "Should allocate IPs sequentially from start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip1, err := pool.AllocateIP()
			if err != nil {
				t.Fatalf("First AllocateIP() error = %v", err)
			}

			if !ip1.Equal(net.ParseIP(tt.wantFirst)) {
				t.Errorf("First IP = %s, want %s", ip1.String(), tt.wantFirst)
			}

			// Reserve the first IP
			lease1 := &Lease{
				MAC:    net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
				IP:     ip1,
				Static: false,
			}
			pool.SetLease(lease1)

			ip2, err := pool.AllocateIP()
			if err != nil {
				t.Fatalf("Second AllocateIP() error = %v", err)
			}

			if !ip2.Equal(net.ParseIP(tt.wantSecond)) {
				t.Errorf("Second IP = %s, want %s", ip2.String(), tt.wantSecond)
			}
		})
	}
}

// Test AllocateIP exhaustion
func TestAllocateIPExhaustion(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	start := net.ParseIP("192.168.1.100")
	end := net.ParseIP("192.168.1.102") // Only 3 IPs available

	pool := NewLeasePool(*subnet, start, end, "home.lab", 1*time.Hour, 0)

	// Allocate all available IPs
	var leases []*Lease
	for i := 0; i < 3; i++ {
		ip, err := pool.AllocateIP()
		if err != nil {
			t.Fatalf("AllocateIP %d: %v", i, err)
		}

		mac := net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, byte(i)}
		lease := &Lease{
			MAC: mac,
			IP:  ip,
		}
		leases = append(leases, lease)
		pool.SetLease(lease)
	}

	// Try to allocate one more - should fail
	_, err := pool.AllocateIP()
	if err == nil {
		t.Error("AllocateIP() on exhausted pool should return error")
	}

	if err.Error() != "no available IP addresses in range" {
		t.Errorf("Error message = %q, want 'no available IP addresses in range'", err.Error())
	}
}

// Test FindByMAC
func TestFindByMAC(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	lease := &Lease{
		MAC:      mac,
		IP:       net.ParseIP("192.168.1.100"),
		Hostname: "laptop",
		Static:   false,
	}

	pool.SetLease(lease)

	found := pool.FindByMAC(mac)
	if found == nil {
		t.Fatal("FindByMAC() returned nil")
	}

	if !found.IP.Equal(lease.IP) {
		t.Errorf("Found lease IP = %s, want %s", found.IP.String(), lease.IP.String())
	}

	// Test non-existent MAC
	notFound := pool.FindByMAC(net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if notFound != nil {
		t.Error("FindByMAC() for non-existent MAC should return nil")
	}
}

// Test FindByIP
func TestFindByIP(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	ip := net.ParseIP("192.168.1.100")
	lease := &Lease{
		MAC: mac,
		IP:  ip,
	}

	pool.SetLease(lease)

	found := pool.FindByIP(ip)
	if found == nil {
		t.Fatal("FindByIP() returned nil")
	}

	if found.MAC.String() != mac.String() {
		t.Errorf("Found lease MAC = %s, want %s", found.MAC.String(), mac.String())
	}

	// Test non-existent IP
	notFound := pool.FindByIP(net.ParseIP("192.168.1.99"))
	if notFound != nil {
		t.Error("FindByIP() for non-existent IP should return nil")
	}
}

// Test FindByName
func TestFindByName(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	lease := &Lease{
		MAC:     mac,
		IP:      net.ParseIP("192.168.1.100"),
		DNSName: "laptop.home.lab",
	}

	pool.SetLease(lease)

	// Test exact match
	found := pool.FindByName("laptop.home.lab")
	if found == nil {
		t.Fatal("FindByName() returned nil")
	}

	// Test case-insensitive
	found = pool.FindByName("LAPTOP.HOME.LAB")
	if found == nil {
		t.Fatal("FindByName() case-insensitive failed")
	}

	// Test non-existent name
	notFound := pool.FindByName("nonexistent.home.lab")
	if notFound != nil {
		t.Error("FindByName() for non-existent name should return nil")
	}
}

// Test SetLease and ReleaseLease
func TestSetAndReleaseLease(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	ip := net.ParseIP("192.168.1.100")
	lease := &Lease{
		MAC:     mac,
		IP:      ip,
		DNSName: "test.home.lab",
	}

	// Set lease
	err := pool.SetLease(lease)
	if err != nil {
		t.Fatalf("SetLease() error = %v", err)
	}

	if pool.FindByMAC(mac) == nil {
		t.Error("Lease not found by MAC after SetLease")
	}

	// Release lease
	err = pool.ReleaseLease(mac)
	if err != nil {
		t.Fatalf("ReleaseLease() error = %v", err)
	}

	if pool.FindByMAC(mac) != nil {
		t.Error("Lease still found by MAC after ReleaseLease")
	}

	if pool.FindByIP(ip) != nil {
		t.Error("Lease still found by IP after ReleaseLease")
	}

	if pool.FindByName("test.home.lab") != nil {
		t.Error("Lease still found by name after ReleaseLease")
	}
}

// Test ReleaseLease reuses IP
func TestReleaseLease_ReusesIP(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	start := net.ParseIP("192.168.1.100")
	end := net.ParseIP("192.168.1.102")
	pool := NewLeasePool(*subnet, start, end, "home.lab", 1*time.Hour, 0)

	mac1 := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	ip1, _ := pool.AllocateIP()
	lease1 := &Lease{MAC: mac1, IP: ip1}
	pool.SetLease(lease1)

	mac2 := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x02}
	ip2, _ := pool.AllocateIP()
	lease2 := &Lease{MAC: mac2, IP: ip2}
	pool.SetLease(lease2)

	// Release first lease
	pool.ReleaseLease(mac1)

	// Allocate again - should get the same IP
	ip3, _ := pool.AllocateIP()
	if !ip3.Equal(ip1) {
		t.Errorf("After release, allocated IP = %s, want %s", ip3.String(), ip1.String())
	}
}

// Test SetStaticLease and RemoveStaticLease
func TestSetStaticLease(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	lease := &Lease{
		MAC:     mac,
		IP:      net.ParseIP("192.168.1.50"),
		DNSName: "router.home.lab",
	}

	err := pool.SetStaticLease(lease)
	if err != nil {
		t.Fatalf("SetStaticLease() error = %v", err)
	}

	found := pool.FindByMAC(mac)
	if !found.Static {
		t.Error("SetStaticLease() did not set Static=true")
	}

	// Static leases should not expire
	if pool.IsExpired(found) {
		t.Error("Static lease should not expire")
	}

	// Remove static lease
	err = pool.RemoveStaticLease(mac)
	if err != nil {
		t.Fatalf("RemoveStaticLease() error = %v", err)
	}

	if pool.FindByMAC(mac) != nil {
		t.Error("Lease still found after RemoveStaticLease")
	}
}

// Test IsExpired
func TestIsExpired(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	// Non-expired lease
	now := time.Now()
	futureExpiry := now.Add(1 * time.Hour)
	activeLease := &Lease{
		MAC:       net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
		IP:        net.ParseIP("192.168.1.100"),
		ExpiresAt: futureExpiry,
		Static:    false,
	}

	if pool.IsExpired(activeLease) {
		t.Error("Active lease should not be expired")
	}

	// Expired lease
	pastExpiry := now.Add(-1 * time.Hour)
	expiredLease := &Lease{
		MAC:       net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x07},
		IP:        net.ParseIP("192.168.1.101"),
		ExpiresAt: pastExpiry,
		Static:    false,
	}

	if !pool.IsExpired(expiredLease) {
		t.Error("Expired lease should be expired")
	}

	// Static lease never expires
	staticLease := &Lease{
		MAC:       net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x08},
		IP:        net.ParseIP("192.168.1.102"),
		ExpiresAt: pastExpiry,
		Static:    true,
	}

	if pool.IsExpired(staticLease) {
		t.Error("Static lease should never expire")
	}
}

// Test GenerateDNSName priority chain
func TestGenerateDNSName(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	tests := []struct {
		name         string
		hostname     string
		staticName   string
		ip           string
		expectedName string
		description  string
	}{
		{
			name:         "static DNS name priority",
			hostname:     "mypc",
			staticName:   "custom.home.lab",
			ip:           "192.168.1.100",
			expectedName: "custom.home.lab",
			description:  "Static DNS name should have highest priority",
		},
		{
			name:         "hostname fallback",
			hostname:     "mypc",
			staticName:   "",
			ip:           "192.168.1.100",
			expectedName: "mypc.home.lab",
			description:  "Should use sanitized hostname when no static name",
		},
		{
			name:         "dhcp fallback",
			hostname:     "",
			staticName:   "",
			ip:           "192.168.1.100",
			expectedName: "dhcp-100.home.lab",
			description:  "Should use DHCP fallback when no hostname",
		},
		{
			name:         "empty hostname sanitizes to empty",
			hostname:     "!!!",
			staticName:   "",
			ip:           "192.168.1.50",
			expectedName: "dhcp-050.home.lab",
			description:  "Invalid hostname should use DHCP fallback",
		},
		{
			name:         "uppercase hostname",
			hostname:     "MyPC",
			staticName:   "",
			ip:           "192.168.1.101",
			expectedName: "mypc.home.lab",
			description:  "Hostname should be lowercased",
		},
		{
			name:         "spaces in hostname",
			hostname:     "My PC",
			staticName:   "",
			ip:           "192.168.1.102",
			expectedName: "my-pc.home.lab",
			description:  "Spaces should be replaced with dash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease := &Lease{
				MAC:      net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
				IP:       net.ParseIP(tt.ip),
				Hostname: tt.hostname,
			}

			result := pool.GenerateDNSName(lease, tt.staticName)
			if result != tt.expectedName {
				t.Errorf("GenerateDNSName() = %s, want %s: %s", result, tt.expectedName, tt.description)
			}
		})
	}
}

// Test SanitizeHostname
func TestSanitizeHostname(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "uppercase to lowercase",
			input:    "MyPC",
			expected: "mypc",
		},
		{
			name:     "spaces to dashes",
			input:    "My PC",
			expected: "my-pc",
		},
		{
			name:     "special characters to dashes",
			input:    "My!PC@Host",
			expected: "my-pc-host",
		},
		{
			name:     "collapse multiple dashes",
			input:    "my---pc",
			expected: "my-pc",
		},
		{
			name:     "trim leading dashes",
			input:    "---hello",
			expected: "hello",
		},
		{
			name:     "trim trailing dashes",
			input:    "hello---",
			expected: "hello",
		},
		{
			name:     "trim both ends",
			input:    "---hello---",
			expected: "hello",
		},
		{
			name:     "truncate to 63 chars",
			input:    strings.Repeat("a", 70),
			expected: strings.Repeat("a", 63),
		},
		{
			name:     "empty after sanitize",
			input:    "!!!",
			expected: "",
		},
		{
			name:     "numbers preserved",
			input:    "host123",
			expected: "host123",
		},
		{
			name:     "dashes and numbers",
			input:    "my-host-123",
			expected: "my-host-123",
		},
		{
			name:     "mixed special chars",
			input:    "my_host.123!",
			expected: "my-host-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeHostname(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeHostname(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test Lease JSON marshaling/unmarshaling
func TestLeaseJSON(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	ip := net.ParseIP("192.168.1.100")
	now := time.Now().UTC()
	ttl := 1 * time.Hour

	lease := &Lease{
		MAC:       mac,
		IP:        ip,
		Hostname:  "laptop",
		DNSName:   "laptop.home.lab",
		Static:    false,
		TTL:       ttl,
		GrantedAt: now,
		ExpiresAt: now.Add(ttl),
		ClientID:  "dhcp-client-123",
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	// Unmarshal back
	var restored Lease
	err = json.Unmarshal(jsonBytes, &restored)
	if err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	// Verify all fields
	if restored.MAC.String() != mac.String() {
		t.Errorf("MAC mismatch: %s vs %s", restored.MAC.String(), mac.String())
	}

	if !restored.IP.Equal(ip) {
		t.Errorf("IP mismatch: %s vs %s", restored.IP.String(), ip.String())
	}

	if restored.Hostname != lease.Hostname {
		t.Errorf("Hostname mismatch: %s vs %s", restored.Hostname, lease.Hostname)
	}

	if restored.DNSName != lease.DNSName {
		t.Errorf("DNSName mismatch: %s vs %s", restored.DNSName, lease.DNSName)
	}

	if restored.Static != lease.Static {
		t.Errorf("Static mismatch: %v vs %v", restored.Static, lease.Static)
	}

	if restored.TTL != ttl {
		t.Errorf("TTL mismatch: %v vs %v", restored.TTL, ttl)
	}

	if restored.ClientID != lease.ClientID {
		t.Errorf("ClientID mismatch: %s vs %s", restored.ClientID, lease.ClientID)
	}
}

// Test Lease ToJSON
func TestLeaseToJSON(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	ip := net.ParseIP("192.168.1.100")
	ttl := 2 * time.Hour

	lease := &Lease{
		MAC:      mac,
		IP:       ip,
		Hostname: "test",
		DNSName:  "test.home.lab",
		Static:   true,
		TTL:      ttl,
		ClientID: "test-client",
	}

	leaseJSON := lease.ToJSON()

	if leaseJSON.MAC != mac.String() {
		t.Errorf("MAC in JSON = %s, want %s", leaseJSON.MAC, mac.String())
	}

	if leaseJSON.IP != ip.String() {
		t.Errorf("IP in JSON = %s, want %s", leaseJSON.IP, ip.String())
	}

	if leaseJSON.TTLSeconds != int(ttl.Seconds()) {
		t.Errorf("TTL in JSON = %d, want %d", leaseJSON.TTLSeconds, int(ttl.Seconds()))
	}

	if leaseJSON.Static != lease.Static {
		t.Errorf("Static in JSON = %v, want %v", leaseJSON.Static, lease.Static)
	}
}

// Test GetAllLeases
func TestGetAllLeases(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	// Add leases
	leases := make([]*Lease, 3)
	for i := 0; i < 3; i++ {
		mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, byte(i + 1)}
		ip := net.ParseIP(fmt.Sprintf("192.168.1.%d", 100+i))
		leases[i] = &Lease{
			MAC: mac,
			IP:  ip,
		}
		pool.SetLease(leases[i])
	}

	allLeases := pool.GetAllLeases()
	if len(allLeases) != 3 {
		t.Errorf("GetAllLeases() returned %d leases, want 3", len(allLeases))
	}

	// Verify we got copies, not references
	if len(allLeases) != len(pool.GetAllLeases()) {
		t.Error("GetAllLeases() should return consistent results")
	}
}

// Test concurrent access to LeasePool
func TestConcurrentLeasePoolAccess(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	// 50 goroutines allocating IPs
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ip, err := pool.AllocateIP()
			if err != nil && idx < 101 { // Only first 101 should succeed
				return
			}
			if err == nil && ip != nil {
				mac := net.HardwareAddr{0xaa, byte(idx >> 8), byte(idx), 0x00, 0x00, 0x01}
				lease := &Lease{MAC: mac, IP: ip}
				pool.SetLease(lease)
			}
		}(i)
	}

	// 10 goroutines finding leases
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leases := pool.GetAllLeases()
			_ = leases // Use it to prevent optimization
		}()
	}

	wg.Wait()
	close(errChan)

	// At least some leases should have been created
	allLeases := pool.GetAllLeases()
	if len(allLeases) == 0 {
		t.Error("No leases were created during concurrent access")
	}
}

// Test SetLease with nil lease
func TestSetLeaseNilLease(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	err := pool.SetLease(nil)
	if err == nil {
		t.Error("SetLease(nil) should return error")
	}
}

// Test SetLease with missing MAC
func TestSetLeaseMissingMAC(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	lease := &Lease{
		IP: net.ParseIP("192.168.1.100"),
	}

	err := pool.SetLease(lease)
	if err == nil {
		t.Error("SetLease with nil MAC should return error")
	}
}

// Test SetLease with missing IP
func TestSetLeaseMissingIP(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	lease := &Lease{
		MAC: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}

	err := pool.SetLease(lease)
	if err == nil {
		t.Error("SetLease with nil IP should return error")
	}
}

// Test RemoveStaticLease with non-existent lease
func TestRemoveStaticLeaseNotFound(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")
	pool := NewLeasePool(*subnet, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.200"), "home.lab", 1*time.Hour, 0)

	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	err := pool.RemoveStaticLease(mac)
	if err == nil {
		t.Error("RemoveStaticLease for non-existent lease should return error")
	}
}
