package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Duration Marshal/Unmarshal Tests

func TestDuration_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"5 minutes", `"5m"`, 5 * time.Minute, false},
		{"24 hours", `"24h"`, 24 * time.Hour, false},
		{"500 milliseconds", `"500ms"`, 500 * time.Millisecond, false},
		{"1 second", `"1s"`, 1 * time.Second, false},
		{"30 seconds", `"30s"`, 30 * time.Second, false},
		{"invalid string", `"not-a-duration"`, 0, true},
		{"empty string", `""`, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Duration
			err := d.UnmarshalJSON([]byte(tc.input))

			if (err != nil) != tc.wantErr {
				t.Errorf("expected error=%v, got %v", tc.wantErr, err)
			}

			if !tc.wantErr && time.Duration(d) != tc.want {
				t.Errorf("expected %v, got %v", tc.want, time.Duration(d))
			}
		})
	}
}

func TestDuration_MarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input time.Duration
		want  string
	}{
		{"5 minutes", 5 * time.Minute, `"5m0s"`},
		{"24 hours", 24 * time.Hour, `"24h0m0s"`},
		{"500 milliseconds", 500 * time.Millisecond, `"500ms"`},
		{"1 second", 1 * time.Second, `"1s"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Duration(tc.input)
			data, err := d.MarshalJSON()
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Compare the duration string representation instead of exact format
			var result string
			if err := json.Unmarshal(data, &result); err != nil {
				t.Errorf("failed to unmarshal result: %v", err)
			}

			// Parse both to compare
			originalDur, _ := time.ParseDuration(result)
			if originalDur != tc.input {
				t.Errorf("round-trip failed: expected %v, got %v", tc.input, originalDur)
			}
		})
	}
}

func TestDuration_RoundTrip(t *testing.T) {
	original := 5*time.Minute + 30*time.Second

	// Marshal
	d := Duration(original)
	data, err := d.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Unmarshal
	var d2 Duration
	if err := d2.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if time.Duration(d2) != original {
		t.Errorf("round-trip failed: expected %v, got %v", original, time.Duration(d2))
	}
}

// Config Load Tests

func createValidConfigJSON() string {
	return `{
  "domain": "test.lab",
  "interface": "eth0",
  "dhcp": {
    "subnet": "10.0.0.0/24",
    "range_start": "10.0.0.100",
    "range_end": "10.0.0.200",
    "gateway": "10.0.0.1",
    "dns_servers": ["10.0.0.1"],
    "default_ttl": "5m",
    "static_ttl": "24h",
    "lease_file": "/tmp/test-leases.json"
  },
  "dns": { "listen": ":5353" },
  "upstream": {
    "doh_url": "https://1.1.1.1/dns-query",
    "fallback_servers": ["8.8.8.8:53"],
    "cache_max_entries": 1000,
    "cache_db": "/tmp/test-cache.db"
  },
  "web": { "enabled": false, "listen": ":18080" },
  "logging": { "level": "info", "format": "text" }
}`
}

func TestLoad_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write valid config to file
	configJSON := createValidConfigJSON()
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify key fields
	if cfg.Domain != "test.lab" {
		t.Errorf("expected domain 'test.lab', got '%s'", cfg.Domain)
	}
	if cfg.Interface != "eth0" {
		t.Errorf("expected interface 'eth0', got '%s'", cfg.Interface)
	}
	if cfg.DHCP.Subnet != "10.0.0.0/24" {
		t.Errorf("expected subnet '10.0.0.0/24', got '%s'", cfg.DHCP.Subnet)
	}
	if cfg.Upstream.DOHURL != "https://1.1.1.1/dns-query" {
		t.Errorf("expected DoH URL 'https://1.1.1.1/dns-query', got '%s'", cfg.Upstream.DOHURL)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write invalid JSON
	if err := os.WriteFile(configPath, []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadFromReader(t *testing.T) {
	configJSON := createValidConfigJSON()
	reader := strings.NewReader(configJSON)

	cfg, err := LoadFromReader(reader)
	if err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	if cfg.Domain != "test.lab" {
		t.Errorf("expected domain 'test.lab', got '%s'", cfg.Domain)
	}
}

func TestConfig_SetDefaults(t *testing.T) {
	cfg := &Config{
		Domain:    "",
		Interface: "",
		DNS: DNSConfig{
			Listen: "",
		},
		Upstream: UpstreamConfig{
			DOHURL: "",
		},
		DHCP: DHCPConfig{
			DefaultTTL: 0,
			StaticTTL:  0,
		},
		Logging: LoggingConfig{
			Level:  "",
			Format: "",
		},
	}

	cfg.SetDefaults()

	if cfg.Domain != "home.local" {
		t.Errorf("expected default domain 'home.local', got '%s'", cfg.Domain)
	}
	if cfg.Interface != "eth0" {
		t.Errorf("expected default interface 'eth0', got '%s'", cfg.Interface)
	}
	if cfg.DNS.Listen != ":53" {
		t.Errorf("expected default DNS listen ':53', got '%s'", cfg.DNS.Listen)
	}
	if cfg.Upstream.DOHURL != "https://1.1.1.1/dns-query" {
		t.Errorf("expected default DoH URL 'https://1.1.1.1/dns-query'")
	}
	if cfg.Upstream.CacheMaxEntries != 50000 {
		t.Errorf("expected default cache max entries 50000, got %d", cfg.Upstream.CacheMaxEntries)
	}
	if cfg.DHCP.DefaultTTL != Duration(5*time.Minute) {
		t.Errorf("expected default TTL 5m, got %v", cfg.DHCP.DefaultTTL)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected default log level 'info', got '%s'", cfg.Logging.Level)
	}
}

func TestConfig_Validate_Valid(t *testing.T) {
	reader := strings.NewReader(createValidConfigJSON())
	cfg, err := LoadFromReader(reader)
	if err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	// Should be valid after loading
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config failed validation: %v", err)
	}
}

func TestConfig_Validate_MissingDomain(t *testing.T) {
	cfg := &Config{
		Domain:    "",
		Interface: "eth0",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for missing domain")
	}
}

func TestConfig_Validate_InvalidSubnet(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "invalid-subnet",
			RangeStart: "10.0.0.1",
			RangeEnd:   "10.0.0.254",
			Gateway:    "10.0.0.1",
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid subnet")
	}
}

func TestConfig_Validate_RangeOutsideSubnet(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "192.168.1.1", // Outside subnet
			RangeEnd:   "10.0.0.254",
			Gateway:    "10.0.0.1",
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for range start outside subnet")
	}
}

func TestConfig_Validate_RangeOrdering(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.200", // Start > end
			RangeEnd:   "10.0.0.100",
			Gateway:    "10.0.0.1",
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for range start > range end")
	}
}

func TestConfig_Validate_InvalidMAC(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		StaticHosts: []StaticHost{
			{
				MAC:  "invalid-mac",
				IP:   "10.0.0.50",
				Name: "testhost",
			},
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid MAC address")
	}
}

func TestConfig_Validate_StaticIPOutsideSubnet(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		StaticHosts: []StaticHost{
			{
				MAC:  "aa:bb:cc:dd:ee:ff",
				IP:   "192.168.1.50", // Outside subnet
				Name: "testhost",
			},
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for static IP outside subnet")
	}
}

func TestConfig_Validate_ValidStaticHost(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		StaticHosts: []StaticHost{
			{
				MAC:  "aa:bb:cc:dd:ee:ff",
				IP:   "10.0.0.50",
				Name: "testhost",
			},
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err != nil {
		t.Errorf("valid static host failed validation: %v", err)
	}
}

func TestConfig_Validate_InvalidLogLevel(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		Logging: LoggingConfig{
			Level:  "invalid_level",
			Format: "text",
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid log level")
	}
}

func TestConfig_Validate_InvalidLogFormat(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "invalid_format",
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid log format")
	}
}

func TestConfig_Validate_CacheBoundaries(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		Upstream: UpstreamConfig{
			DOHURL:          "https://1.1.1.1/dns-query",
			FallbackServers: []string{"8.8.8.8:53"},
			CacheMaxEntries: 0, // Invalid
			CacheDB:         "/tmp/cache.db",
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid cache max entries")
	}
}

func TestConfig_LoadFromReader_WithDefaults(t *testing.T) {
	// Minimal valid config
	minimalJSON := `{
  "domain": "test.lab",
  "interface": "eth0",
  "dhcp": {
    "subnet": "10.0.0.0/24",
    "range_start": "10.0.0.100",
    "range_end": "10.0.0.200",
    "gateway": "10.0.0.1"
  },
  "upstream": {
    "fallback_servers": ["8.8.8.8:53"]
  }
}`

	reader := strings.NewReader(minimalJSON)
	cfg, err := LoadFromReader(reader)
	if err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	// Verify defaults were applied
	if cfg.DNS.Listen == "" {
		t.Error("DNS listen default not applied")
	}
	if cfg.Upstream.DOHURL == "" {
		t.Error("DoH URL default not applied")
	}
	if cfg.Upstream.CacheMaxEntries == 0 {
		t.Error("cache max entries default not applied")
	}
}

func TestConfig_DHCPGetters(t *testing.T) {
	reader := strings.NewReader(createValidConfigJSON())
	cfg, err := LoadFromReader(reader)
	if err != nil {
		t.Fatalf("LoadFromReader failed: %v", err)
	}

	// Test GetSubnet
	subnet := cfg.DHCP.GetSubnet()
	if subnet == nil {
		t.Error("GetSubnet returned nil")
	}

	// Test GetRangeStart
	rangeStart := cfg.DHCP.GetRangeStart()
	if rangeStart.String() != "10.0.0.100" {
		t.Errorf("expected range start 10.0.0.100, got %s", rangeStart.String())
	}

	// Test GetRangeEnd
	rangeEnd := cfg.DHCP.GetRangeEnd()
	if rangeEnd.String() != "10.0.0.200" {
		t.Errorf("expected range end 10.0.0.200, got %s", rangeEnd.String())
	}

	// Test GetGateway
	gateway := cfg.DHCP.GetGateway()
	if gateway.String() != "10.0.0.1" {
		t.Errorf("expected gateway 10.0.0.1, got %s", gateway.String())
	}
}

func TestConfig_StaticHostGetters(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		StaticHosts: []StaticHost{
			{
				MAC:  "aa:bb:cc:dd:ee:ff",
				IP:   "10.0.0.50",
				Name: "testhost",
			},
		},
	}

	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	host := cfg.StaticHosts[0]

	// Test GetIP
	ip := host.GetIP()
	if ip.String() != "10.0.0.50" {
		t.Errorf("expected IP 10.0.0.50, got %s", ip.String())
	}

	// Test GetMAC
	mac := host.GetMAC()
	if mac.String() != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("expected MAC aa:bb:cc:dd:ee:ff, got %s", mac.String())
	}
}

func TestConfig_EventsValidation(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		Events: EventsConfig{
			PerHostLimit: 0, // Invalid
			Persist:      false,
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid per_host_limit")
	}
}

func TestConfig_PersistentEventsWithoutDB(t *testing.T) {
	cfg := &Config{
		Domain:    "test.lab",
		Interface: "eth0",
		DHCP: DHCPConfig{
			Subnet:     "10.0.0.0/24",
			RangeStart: "10.0.0.100",
			RangeEnd:   "10.0.0.200",
			Gateway:    "10.0.0.1",
		},
		Events: EventsConfig{
			PerHostLimit: 100,
			Persist:      true,
			DB:           "", // Missing DB path
		},
	}

	cfg.SetDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for persistent events without DB path")
	}
}
