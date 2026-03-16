package unifi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
)

// ClientEntry represents a UniFi client/device entry from a config backup.
type ClientEntry struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	IP       string `json:"fixed_ip,omitempty"`  // fixed IP assignment
	UseFixIP bool   `json:"use_fixedip"`         // whether fixed IP is active
	Hostname string `json:"hostname,omitempty"`   // discovered hostname
	Network  string `json:"network_id,omitempty"` // which network the client belongs to
	Note     string `json:"note,omitempty"`
	Noted    bool   `json:"noted"`
}

// NetworkEntry represents a UniFi network definition.
type NetworkEntry struct {
	ID            string `json:"_id"`
	Name          string `json:"name"`
	Purpose       string `json:"purpose"` // "corporate", "guest", etc.
	Subnet        string `json:"ip_subnet"`
	Gateway       string `json:"gateway,omitempty"`
	DHCPEnabled   bool   `json:"dhcpd_enabled"`
	DHCPStart     string `json:"dhcpd_start,omitempty"`
	DHCPStop      string `json:"dhcpd_stop,omitempty"`
	DHCPDNSServers []string `json:"dhcpd_dns_1,omitempty"`
	DomainName    string `json:"domain_name,omitempty"`
	VLAN          int    `json:"vlan,omitempty"`
}

// ImportResult holds the parsed data ready for Lantern config generation.
type ImportResult struct {
	StaticHosts []StaticHost
	Networks    []ParsedNetwork
	Warnings    []string
}

// StaticHost is a Lantern-compatible static host entry.
type StaticHost struct {
	MAC  string `json:"mac"`
	IP   string `json:"ip"`
	Name string `json:"name"`
}

// ParsedNetwork is a Lantern-compatible network definition.
type ParsedNetwork struct {
	Name       string `json:"name"`
	Subnet     string `json:"subnet"`
	Gateway    string `json:"gateway"`
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
	Domain     string `json:"domain"`
}

// ImportFromFile reads a UniFi config backup JSON and extracts static hosts
// and network settings that can be used in a Lantern config.
func ImportFromFile(path string, logger *slog.Logger) (*ImportResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open UniFi config: %w", err)
	}
	defer f.Close()

	return ImportFromReader(f, logger)
}

// ImportFromReader reads a UniFi config backup from an io.Reader.
func ImportFromReader(r io.Reader, logger *slog.Logger) (*ImportResult, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode UniFi config: %w", err)
	}

	result := &ImportResult{}

	// Extract clients / user entries (static hosts with fixed IPs)
	if clientsRaw, ok := raw["user"]; ok {
		result.parseClients(clientsRaw, logger)
	}

	// Some UniFi exports use "clients" instead of "user"
	if clientsRaw, ok := raw["clients"]; ok {
		result.parseClients(clientsRaw, logger)
	}

	// Extract network definitions
	if networksRaw, ok := raw["networkconf"]; ok {
		result.parseNetworks(networksRaw, logger)
	}

	logger.Info("UniFi import completed",
		"static_hosts", len(result.StaticHosts),
		"networks", len(result.Networks),
		"warnings", len(result.Warnings),
	)

	return result, nil
}

// parseClients extracts static host entries from client/user data.
func (r *ImportResult) parseClients(data json.RawMessage, logger *slog.Logger) {
	var clients []ClientEntry
	if err := json.Unmarshal(data, &clients); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("failed to parse clients: %v", err))
		return
	}

	for _, client := range clients {
		// Only import clients with fixed IP assignments
		if !client.UseFixIP || client.IP == "" {
			continue
		}

		// Validate MAC
		mac := strings.ToLower(client.MAC)
		if _, err := net.ParseMAC(mac); err != nil {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("skipping client with invalid MAC %q: %v", mac, err))
			continue
		}

		// Validate IP
		ip := net.ParseIP(client.IP)
		if ip == nil {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("skipping client %q with invalid IP %q", mac, client.IP))
			continue
		}

		// Determine the best name
		name := client.Name
		if name == "" {
			name = client.Hostname
		}
		if name == "" {
			name = fmt.Sprintf("host-%s", strings.ReplaceAll(mac, ":", ""))
		}

		// Sanitize name for DNS: lowercase, replace spaces/underscores with hyphens
		name = sanitizeDNSName(name)

		r.StaticHosts = append(r.StaticHosts, StaticHost{
			MAC:  mac,
			IP:   client.IP,
			Name: name,
		})

		logger.Debug("imported static host", "mac", mac, "ip", client.IP, "name", name)
	}
}

// parseNetworks extracts network definitions.
func (r *ImportResult) parseNetworks(data json.RawMessage, logger *slog.Logger) {
	var networks []NetworkEntry
	if err := json.Unmarshal(data, &networks); err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("failed to parse networks: %v", err))
		return
	}

	for _, network := range networks {
		// Only import corporate/LAN networks with DHCP
		if network.Purpose != "corporate" && network.Purpose != "vlan-only" {
			continue
		}

		if network.Subnet == "" {
			continue
		}

		parsed := ParsedNetwork{
			Name:       network.Name,
			Subnet:     network.Subnet,
			Gateway:    network.Gateway,
			RangeStart: network.DHCPStart,
			RangeEnd:   network.DHCPStop,
			Domain:     network.DomainName,
		}

		r.Networks = append(r.Networks, parsed)
		logger.Debug("imported network", "name", network.Name, "subnet", network.Subnet)
	}
}

// ToLanternConfig generates a partial Lantern config JSON from the import result.
// The caller can merge this into their existing config.
func (r *ImportResult) ToLanternConfig() ([]byte, error) {
	cfg := map[string]interface{}{
		"static_hosts": r.StaticHosts,
	}

	// Use the first network as the primary DHCP config
	if len(r.Networks) > 0 {
		net := r.Networks[0]
		dhcp := map[string]interface{}{}
		if net.Subnet != "" {
			dhcp["subnet"] = net.Subnet
		}
		if net.Gateway != "" {
			dhcp["gateway"] = net.Gateway
		}
		if net.RangeStart != "" {
			dhcp["range_start"] = net.RangeStart
		}
		if net.RangeEnd != "" {
			dhcp["range_end"] = net.RangeEnd
		}
		cfg["dhcp"] = dhcp

		if net.Domain != "" {
			cfg["domain"] = net.Domain
		}
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// sanitizeDNSName converts a human-readable name to a valid DNS label.
func sanitizeDNSName(name string) string {
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r == ' ' || r == '_' || r == '.' {
			return '-'
		}
		return -1
	}, name)
	// Remove leading/trailing hyphens
	name = strings.Trim(name, "-")
	// Collapse multiple hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	// Truncate to 63 chars (DNS label limit)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}
