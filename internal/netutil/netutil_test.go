package netutil

import (
	"net"
	"testing"
)

// Test IPToUint32
func TestIPToUint32(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected uint32
	}{
		{
			name:     "normal IP",
			ip:       net.ParseIP("192.168.1.100"),
			expected: 3232235876,
		},
		{
			name:     "zero IP",
			ip:       net.ParseIP("0.0.0.0"),
			expected: 0,
		},
		{
			name:     "max IP",
			ip:       net.ParseIP("255.255.255.255"),
			expected: 4294967295,
		},
		{
			name:     "localhost",
			ip:       net.ParseIP("127.0.0.1"),
			expected: 2130706433,
		},
		{
			name:     "nil IP",
			ip:       nil,
			expected: 0,
		},
		{
			name:     "IPv6 address",
			ip:       net.ParseIP("::1"),
			expected: 0,
		},
		{
			name:     "first octet 1",
			ip:       net.ParseIP("1.0.0.0"),
			expected: 16777216,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IPToUint32(tt.ip)
			if result != tt.expected {
				t.Errorf("IPToUint32(%v) = %d, want %d", tt.ip, result, tt.expected)
			}
		})
	}
}

// Test Uint32ToIP
func TestUint32ToIP(t *testing.T) {
	tests := []struct {
		name     string
		n        uint32
		expected string
	}{
		{
			name:     "zero",
			n:        0,
			expected: "0.0.0.0",
		},
		{
			name:     "localhost",
			n:        2130706433,
			expected: "127.0.0.1",
		},
		{
			name:     "max uint32",
			n:        4294967295,
			expected: "255.255.255.255",
		},
		{
			name:     "192.168.1.100",
			n:        3232235876,
			expected: "192.168.1.100",
		},
		{
			name:     "1.0.0.0",
			n:        16777216,
			expected: "1.0.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Uint32ToIP(tt.n)
			if result.String() != tt.expected {
				t.Errorf("Uint32ToIP(%d) = %s, want %s", tt.n, result.String(), tt.expected)
			}
		})
	}
}

// Test round-trip conversion
func TestIPUint32RoundTrip(t *testing.T) {
	tests := []string{
		"0.0.0.0",
		"127.0.0.1",
		"192.168.1.1",
		"192.168.1.255",
		"10.0.0.1",
		"255.255.255.255",
		"172.16.0.1",
	}

	for _, ipStr := range tests {
		t.Run(ipStr, func(t *testing.T) {
			original := net.ParseIP(ipStr)
			converted := IPToUint32(original)
			roundTrip := Uint32ToIP(converted)
			if roundTrip.String() != ipStr {
				t.Errorf("round-trip %s -> %d -> %s failed", ipStr, converted, roundTrip.String())
			}
		})
	}
}

// Test NextIP
func TestNextIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected string
		isNil    bool
	}{
		{
			name:     "normal IP",
			ip:       net.ParseIP("192.168.1.100"),
			expected: "192.168.1.101",
		},
		{
			name:     "octet wraparound",
			ip:       net.ParseIP("192.168.1.255"),
			expected: "192.168.2.0",
		},
		{
			name:     "zero IP",
			ip:       net.ParseIP("0.0.0.0"),
			expected: "0.0.0.1",
		},
		{
			name:  "nil IP",
			ip:    nil,
			isNil: true,
		},
		{
			name:  "IPv6 address",
			ip:    net.ParseIP("::1"),
			isNil: true,
		},
		{
			name:     "255.255.255.254",
			ip:       net.ParseIP("255.255.255.254"),
			expected: "255.255.255.255",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NextIP(tt.ip)
			if tt.isNil {
				if result != nil {
					t.Errorf("NextIP(%v) = %v, want nil", tt.ip, result)
				}
			} else {
				if result == nil {
					t.Errorf("NextIP(%v) = nil, want %s", tt.ip, tt.expected)
				} else if result.String() != tt.expected {
					t.Errorf("NextIP(%v) = %s, want %s", tt.ip, result.String(), tt.expected)
				}
			}
		})
	}
}

// Test PrevIP
func TestPrevIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected string
		isNil    bool
	}{
		{
			name:     "normal IP",
			ip:       net.ParseIP("192.168.1.100"),
			expected: "192.168.1.99",
		},
		{
			name:     "octet wraparound",
			ip:       net.ParseIP("192.168.2.0"),
			expected: "192.168.1.255",
		},
		{
			name:     "one IP",
			ip:       net.ParseIP("0.0.0.1"),
			expected: "0.0.0.0",
		},
		{
			name:  "zero IP (cannot go lower)",
			ip:    net.ParseIP("0.0.0.0"),
			isNil: true,
		},
		{
			name:  "nil IP",
			ip:    nil,
			isNil: true,
		},
		{
			name:  "IPv6 address",
			ip:    net.ParseIP("::1"),
			isNil: true,
		},
		{
			name:     "255.255.255.255",
			ip:       net.ParseIP("255.255.255.255"),
			expected: "255.255.255.254",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PrevIP(tt.ip)
			if tt.isNil {
				if result != nil {
					t.Errorf("PrevIP(%v) = %v, want nil", tt.ip, result)
				}
			} else {
				if result == nil {
					t.Errorf("PrevIP(%v) = nil, want %s", tt.ip, tt.expected)
				} else if result.String() != tt.expected {
					t.Errorf("PrevIP(%v) = %s, want %s", tt.ip, result.String(), tt.expected)
				}
			}
		})
	}
}

// Test IPInRange
func TestIPInRange(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		start    net.IP
		end      net.IP
		expected bool
	}{
		{
			name:     "IP in range",
			ip:       net.ParseIP("192.168.1.100"),
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: true,
		},
		{
			name:     "IP at start boundary",
			ip:       net.ParseIP("192.168.1.1"),
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: true,
		},
		{
			name:     "IP at end boundary",
			ip:       net.ParseIP("192.168.1.254"),
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: true,
		},
		{
			name:     "IP before range",
			ip:       net.ParseIP("192.168.0.255"),
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: false,
		},
		{
			name:     "IP after range",
			ip:       net.ParseIP("192.168.2.0"),
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: false,
		},
		{
			name:     "nil IP",
			ip:       nil,
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: false,
		},
		{
			name:     "nil start",
			ip:       net.ParseIP("192.168.1.100"),
			start:    nil,
			end:      net.ParseIP("192.168.1.254"),
			expected: false,
		},
		{
			name:     "nil end",
			ip:       net.ParseIP("192.168.1.100"),
			start:    net.ParseIP("192.168.1.1"),
			end:      nil,
			expected: false,
		},
		{
			name:     "IPv6 input",
			ip:       net.ParseIP("::1"),
			start:    net.ParseIP("192.168.1.1"),
			end:      net.ParseIP("192.168.1.254"),
			expected: false,
		},
		{
			name:     "single IP range",
			ip:       net.ParseIP("10.0.0.1"),
			start:    net.ParseIP("10.0.0.1"),
			end:      net.ParseIP("10.0.0.1"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IPInRange(tt.ip, tt.start, tt.end)
			if result != tt.expected {
				t.Errorf("IPInRange(%v, %v, %v) = %v, want %v", tt.ip, tt.start, tt.end, result, tt.expected)
			}
		})
	}
}

// Test LastOctet
func TestLastOctet(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected byte
	}{
		{
			name:     "normal IP",
			ip:       net.ParseIP("192.168.1.100"),
			expected: 100,
		},
		{
			name:     "zero octet",
			ip:       net.ParseIP("192.168.1.0"),
			expected: 0,
		},
		{
			name:     "max octet",
			ip:       net.ParseIP("192.168.1.255"),
			expected: 255,
		},
		{
			name:     "nil IP",
			ip:       nil,
			expected: 0,
		},
		{
			name:     "IPv6 address",
			ip:       net.ParseIP("::1"),
			expected: 0,
		},
		{
			name:     "localhost",
			ip:       net.ParseIP("127.0.0.1"),
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LastOctet(tt.ip)
			if result != tt.expected {
				t.Errorf("LastOctet(%v) = %d, want %d", tt.ip, result, tt.expected)
			}
		})
	}
}

// Test FormatLastOctet
func TestFormatLastOctet(t *testing.T) {
	tests := []struct {
		name     string
		ip       net.IP
		expected string
	}{
		{
			name:     "zero padded",
			ip:       net.ParseIP("192.168.1.5"),
			expected: "005",
		},
		{
			name:     "two digit",
			ip:       net.ParseIP("192.168.1.55"),
			expected: "055",
		},
		{
			name:     "three digit",
			ip:       net.ParseIP("192.168.1.255"),
			expected: "255",
		},
		{
			name:     "zero octet",
			ip:       net.ParseIP("192.168.1.0"),
			expected: "000",
		},
		{
			name:     "nil IP",
			ip:       nil,
			expected: "000",
		},
		{
			name:     "IPv6 address",
			ip:       net.ParseIP("::1"),
			expected: "000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatLastOctet(tt.ip)
			if result != tt.expected {
				t.Errorf("FormatLastOctet(%v) = %s, want %s", tt.ip, result, tt.expected)
			}
		})
	}
}

// Test ParseMAC
func TestParseMAC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
		expected string
	}{
		{
			name:     "colon format",
			input:    "aa:bb:cc:dd:ee:ff",
			wantErr:  false,
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "hyphen format",
			input:    "aa-bb-cc-dd-ee-ff",
			wantErr:  false,
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:    "no separator",
			input:   "aabbccddeeff",
			wantErr: true,
		},
		{
			name:     "uppercase",
			input:    "AA:BB:CC:DD:EE:FF",
			wantErr:  false,
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:    "invalid format",
			input:   "invalid",
			wantErr: true,
		},
		{
			name:    "incomplete",
			input:   "aa:bb:cc:dd",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseMAC(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMAC(%s) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && result.String() != tt.expected {
				t.Errorf("ParseMAC(%s) = %s, want %s", tt.input, result.String(), tt.expected)
			}
		})
	}
}

// Test NormalizeMAC
func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		name     string
		mac      net.HardwareAddr
		expected string
	}{
		{
			name:     "standard MAC",
			mac:      net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "all zeros",
			mac:      net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			expected: "00:00:00:00:00:00",
		},
		{
			name:     "all ones",
			mac:      net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			expected: "ff:ff:ff:ff:ff:ff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeMAC(tt.mac)
			if result != tt.expected {
				t.Errorf("NormalizeMAC(%v) = %s, want %s", tt.mac, result, tt.expected)
			}
		})
	}
}

// Test SubnetContains
func TestSubnetContains(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("192.168.1.0/24")

	tests := []struct {
		name     string
		subnet   net.IPNet
		ip       net.IP
		expected bool
	}{
		{
			name:     "IP in subnet",
			subnet:   *subnet,
			ip:       net.ParseIP("192.168.1.100"),
			expected: true,
		},
		{
			name:     "network address",
			subnet:   *subnet,
			ip:       net.ParseIP("192.168.1.0"),
			expected: true,
		},
		{
			name:     "broadcast address",
			subnet:   *subnet,
			ip:       net.ParseIP("192.168.1.255"),
			expected: true,
		},
		{
			name:     "IP outside subnet",
			subnet:   *subnet,
			ip:       net.ParseIP("192.168.2.1"),
			expected: false,
		},
		{
			name:     "nil IP",
			subnet:   *subnet,
			ip:       nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SubnetContains(tt.subnet, tt.ip)
			if result != tt.expected {
				t.Errorf("SubnetContains(%v, %v) = %v, want %v", tt.subnet, tt.ip, result, tt.expected)
			}
		})
	}
}

// Test BroadcastAddr
func TestBroadcastAddr(t *testing.T) {
	tests := []struct {
		name     string
		cidr     string
		expected string
	}{
		{
			name:     "/24 subnet",
			cidr:     "192.168.1.0/24",
			expected: "192.168.1.255",
		},
		{
			name:     "/25 subnet",
			cidr:     "192.168.1.0/25",
			expected: "192.168.1.127",
		},
		{
			name:     "/30 subnet",
			cidr:     "192.168.1.0/30",
			expected: "192.168.1.3",
		},
		{
			name:     "/32 subnet",
			cidr:     "192.168.1.5/32",
			expected: "192.168.1.5",
		},
		{
			name:     "/16 subnet",
			cidr:     "10.0.0.0/16",
			expected: "10.0.255.255",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, subnet, _ := net.ParseCIDR(tt.cidr)
			result := BroadcastAddr(*subnet)
			if result == nil {
				t.Errorf("BroadcastAddr(%s) = nil, want %s", tt.cidr, tt.expected)
			} else if result.String() != tt.expected {
				t.Errorf("BroadcastAddr(%s) = %s, want %s", tt.cidr, result.String(), tt.expected)
			}
		})
	}
}

// Test NetworkAddr
func TestNetworkAddr(t *testing.T) {
	tests := []struct {
		name     string
		cidr     string
		expected string
	}{
		{
			name:     "/24 subnet",
			cidr:     "192.168.1.0/24",
			expected: "192.168.1.0",
		},
		{
			name:     "/25 subnet",
			cidr:     "192.168.1.128/25",
			expected: "192.168.1.128",
		},
		{
			name:     "/30 subnet",
			cidr:     "192.168.1.4/30",
			expected: "192.168.1.4",
		},
		{
			name:     "/16 subnet",
			cidr:     "10.5.0.0/16",
			expected: "10.5.0.0",
		},
		{
			name:     "host within subnet",
			cidr:     "192.168.1.100/24",
			expected: "192.168.1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, subnet, _ := net.ParseCIDR(tt.cidr)
			result := NetworkAddr(*subnet)
			if result == nil {
				t.Errorf("NetworkAddr(%s) = nil, want %s", tt.cidr, tt.expected)
			} else if result.String() != tt.expected {
				t.Errorf("NetworkAddr(%s) = %s, want %s", tt.cidr, result.String(), tt.expected)
			}
		})
	}
}

// Test edge cases with nil subnet
func TestBroadcastAddrNilSubnet(t *testing.T) {
	result := BroadcastAddr(net.IPNet{})
	if result != nil {
		t.Errorf("BroadcastAddr with empty subnet = %v, want nil", result)
	}
}

func TestNetworkAddrNilSubnet(t *testing.T) {
	result := NetworkAddr(net.IPNet{})
	if result != nil {
		t.Errorf("NetworkAddr with empty subnet = %v, want nil", result)
	}
}
