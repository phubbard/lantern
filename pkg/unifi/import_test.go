package unifi

import (
	"strings"
	"testing"
)

func TestImportFromReader(t *testing.T) {
	configJSON := `{
		"user": [
			{
				"mac": "aa:bb:cc:dd:ee:01",
				"name": "Living Room TV",
				"fixed_ip": "192.168.1.100",
				"use_fixedip": true,
				"hostname": "lg-tv"
			},
			{
				"mac": "aa:bb:cc:dd:ee:02",
				"name": "Phone",
				"use_fixedip": false
			},
			{
				"mac": "invalid-mac",
				"name": "Bad Client",
				"fixed_ip": "192.168.1.101",
				"use_fixedip": true
			},
			{
				"mac": "aa:bb:cc:dd:ee:03",
				"name": "",
				"hostname": "server-rack",
				"fixed_ip": "192.168.1.50",
				"use_fixedip": true
			}
		],
		"networkconf": [
			{
				"_id": "abc123",
				"name": "LAN",
				"purpose": "corporate",
				"ip_subnet": "192.168.1.0/24",
				"gateway": "192.168.1.1",
				"dhcpd_enabled": true,
				"dhcpd_start": "192.168.1.100",
				"dhcpd_stop": "192.168.1.250",
				"domain_name": "home.lan"
			},
			{
				"_id": "def456",
				"name": "Guest",
				"purpose": "guest",
				"ip_subnet": "192.168.2.0/24"
			}
		]
	}`

	result, err := ImportFromReader(strings.NewReader(configJSON), nil)
	if err != nil {
		t.Fatalf("ImportFromReader error: %v", err)
	}

	// Should have 2 static hosts (the one without fixed IP and the one with invalid MAC are skipped)
	if len(result.StaticHosts) != 2 {
		t.Errorf("expected 2 static hosts, got %d", len(result.StaticHosts))
	}

	// Check first host
	if result.StaticHosts[0].Name != "living-room-tv" {
		t.Errorf("expected name 'living-room-tv', got %q", result.StaticHosts[0].Name)
	}
	if result.StaticHosts[0].IP != "192.168.1.100" {
		t.Errorf("expected IP '192.168.1.100', got %q", result.StaticHosts[0].IP)
	}

	// Second host should use hostname since name is empty
	if result.StaticHosts[1].Name != "server-rack" {
		t.Errorf("expected name 'server-rack', got %q", result.StaticHosts[1].Name)
	}

	// Should have 1 network (guest is skipped)
	if len(result.Networks) != 1 {
		t.Errorf("expected 1 network, got %d", len(result.Networks))
	}

	if result.Networks[0].Name != "LAN" {
		t.Errorf("expected network name 'LAN', got %q", result.Networks[0].Name)
	}
	if result.Networks[0].Domain != "home.lan" {
		t.Errorf("expected domain 'home.lan', got %q", result.Networks[0].Domain)
	}

	// Should have 1 warning about the invalid MAC
	if len(result.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
}

func TestImportFromReaderEmptyConfig(t *testing.T) {
	result, err := ImportFromReader(strings.NewReader(`{}`), nil)
	if err != nil {
		t.Fatalf("ImportFromReader error: %v", err)
	}
	if len(result.StaticHosts) != 0 {
		t.Errorf("expected 0 static hosts, got %d", len(result.StaticHosts))
	}
	if len(result.Networks) != 0 {
		t.Errorf("expected 0 networks, got %d", len(result.Networks))
	}
}

func TestToLanternConfig(t *testing.T) {
	result := &ImportResult{
		StaticHosts: []StaticHost{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.100", Name: "tv"},
		},
		Networks: []ParsedNetwork{
			{
				Name:       "LAN",
				Subnet:     "192.168.1.0/24",
				Gateway:    "192.168.1.1",
				RangeStart: "192.168.1.100",
				RangeEnd:   "192.168.1.250",
				Domain:     "home.lan",
			},
		},
	}

	data, err := result.ToLanternConfig()
	if err != nil {
		t.Fatalf("ToLanternConfig error: %v", err)
	}

	json := string(data)
	if !strings.Contains(json, "192.168.1.100") {
		t.Error("expected output to contain static host IP")
	}
	if !strings.Contains(json, "home.lan") {
		t.Error("expected output to contain domain")
	}
}

func TestSanitizeDNSName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Living Room TV", "living-room-tv"},
		{"server_01", "server-01"},
		{"My.Device", "my-device"},
		{"---test---", "test"},
		{"a b  c", "a-b-c"},
		{"UPPERCASE", "uppercase"},
		{"special!@#chars", "specialchars"},
		{strings.Repeat("a", 100), strings.Repeat("a", 63)},
	}

	for _, tt := range tests {
		got := sanitizeDNSName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeDNSName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
