package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var Version string

// Duration is a custom type that unmarshals duration strings like "5m", "24h"
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}

	*d = Duration(dur)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Config represents the complete lantern configuration
type Config struct {
	Domain      string            `json:"domain"`
	Interface   string            `json:"interface"`
	DHCP        DHCPConfig        `json:"dhcp"`
	DNS         DNSConfig         `json:"dns"`
	Upstream    UpstreamConfig    `json:"upstream"`
	Blocklists  []BlocklistConfig `json:"blocklists"`
	StaticHosts []StaticHost      `json:"static_hosts"`
	Fingerprint FingerprintConfig `json:"fingerprint"`
	Web         WebConfig         `json:"web"`
	Events      EventsConfig      `json:"events"`
	Logging     LoggingConfig     `json:"logging"`
}

// DHCPConfig contains DHCP server configuration
type DHCPConfig struct {
	Enabled      *bool      `json:"enabled,omitempty"` // default true; set false for DNS-only mode
	Interface    string     `json:"interface,omitempty"` // override top-level interface
	Subnet       string     `json:"subnet"`
	RangeStart   string     `json:"range_start"`
	RangeEnd     string     `json:"range_end"`
	Gateway      string     `json:"gateway"`
	DNSServers   []string   `json:"dns_servers"`
	DefaultTTL   Duration   `json:"default_ttl"`
	StaticTTL    Duration   `json:"static_ttl"`
	LeaseFile    string     `json:"lease_file"`
	subnetIPNet  *net.IPNet // parsed subnet
	rangeStartIP net.IP     // parsed range start
	rangeEndIP   net.IP     // parsed range end
	gatewayIP    net.IP     // parsed gateway
}

// DNSConfig contains DNS server configuration
type DNSConfig struct {
	Listen     string        `json:"listen"`
	NameFormat NameFormatCfg `json:"name_format"`
}

// NameFormatCfg contains name format templates
type NameFormatCfg struct {
	WithHostname string `json:"with_hostname"`
	Fallback     string `json:"fallback"`
}

// UpstreamConfig contains upstream DNS configuration
type UpstreamConfig struct {
	DOHURL          string   `json:"doh_url"`
	FallbackServers []string `json:"fallback_servers"`
	CacheMaxEntries int      `json:"cache_max_entries"`
	CacheHotSetSize int      `json:"cache_hot_set_size"` // in-memory LRU capacity (default 5000)
	CacheDB         string   `json:"cache_db"`
}

// BlocklistConfig represents a blocklist configuration.
// Either Path (local file) or URL (remote) must be set, not both.
type BlocklistConfig struct {
	Path           string   `json:"path"`
	URL            string   `json:"url"`
	Enabled        bool     `json:"enabled"`
	UpdateInterval Duration `json:"update_interval"`
}

// StaticHost represents a static host mapping
type StaticHost struct {
	MAC  string           `json:"mac"`
	IP   string           `json:"ip"`
	Name string           `json:"name"`
	ip   net.IP           // parsed IP
	mac  net.HardwareAddr // parsed MAC
}

// FingerprintConfig contains device fingerprinting configuration
type FingerprintConfig struct {
	Enabled        bool   `json:"enabled"`
	Interface      string `json:"interface"`
	SignaturesFile string `json:"signatures_file"`
}

// WebConfig contains web UI configuration
type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
}

// EventsConfig contains events configuration
type EventsConfig struct {
	PerHostLimit int    `json:"per_host_limit"`
	Persist      bool   `json:"persist"`
	DB           string `json:"db"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// Load loads configuration from a JSON file
func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	return LoadFromReader(file)
}

// LoadFromReader loads configuration from an io.Reader
func LoadFromReader(r io.Reader) (*Config, error) {
	var cfg Config
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	cfg.SetDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// SetDefaults sets sensible defaults for optional configuration fields
func (c *Config) SetDefaults() {
	if c.Domain == "" {
		c.Domain = "home.local"
	}

	if c.Interface == "" {
		c.Interface = "eth0"
	}

	// DHCP defaults
	if c.DHCP.Enabled == nil {
		t := true
		c.DHCP.Enabled = &t
	}
	if c.DHCP.Interface == "" {
		c.DHCP.Interface = c.Interface
	}
	if c.DHCP.DefaultTTL == 0 {
		c.DHCP.DefaultTTL = Duration(5 * time.Minute)
	}
	if c.DHCP.StaticTTL == 0 {
		c.DHCP.StaticTTL = Duration(24 * time.Hour)
	}
	if c.DHCP.LeaseFile == "" {
		c.DHCP.LeaseFile = "/var/lib/lantern/leases.json"
	}

	// DNS defaults
	if c.DNS.Listen == "" {
		c.DNS.Listen = ":53"
	}
	if c.DNS.NameFormat.WithHostname == "" {
		c.DNS.NameFormat.WithHostname = "{hostname}.{domain}"
	}
	if c.DNS.NameFormat.Fallback == "" {
		c.DNS.NameFormat.Fallback = "dhcp-{octet:03d}.{domain}"
	}

	// Upstream defaults
	if c.Upstream.DOHURL == "" {
		c.Upstream.DOHURL = "https://1.1.1.1/dns-query"
	}
	if len(c.Upstream.FallbackServers) == 0 {
		c.Upstream.FallbackServers = []string{"8.8.8.8:53", "1.1.1.1:53"}
	}
	if c.Upstream.CacheMaxEntries == 0 {
		c.Upstream.CacheMaxEntries = 50000
	}
	if c.Upstream.CacheHotSetSize == 0 {
		c.Upstream.CacheHotSetSize = 5000
	}
	if c.Upstream.CacheDB == "" {
		c.Upstream.CacheDB = "/var/lib/lantern/cache.db"
	}

	// Web defaults
	if c.Web.Listen == "" {
		c.Web.Listen = ":8080"
	}

	// Events defaults
	if c.Events.PerHostLimit == 0 {
		c.Events.PerHostLimit = 100
	}
	if c.Events.DB == "" {
		c.Events.DB = "/var/lib/lantern/events.db"
	}

	// Logging defaults
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}

	// Fingerprint defaults
	if c.Fingerprint.Interface == "" {
		c.Fingerprint.Interface = c.Interface
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Domain == "" {
		return fmt.Errorf("domain is required")
	}

	if c.Interface == "" {
		return fmt.Errorf("interface is required")
	}

	// Validate DHCP config
	if err := c.validateDHCP(); err != nil {
		return err
	}

	// Validate DNS config
	if err := c.validateDNS(); err != nil {
		return err
	}

	// Validate Upstream config
	if err := c.validateUpstream(); err != nil {
		return err
	}

	// Validate Blocklists
	if err := c.validateBlocklists(); err != nil {
		return err
	}

	// Validate Static Hosts
	if err := c.validateStaticHosts(); err != nil {
		return err
	}

	// Validate Fingerprint config
	if err := c.validateFingerprint(); err != nil {
		return err
	}

	// Validate Web config
	if err := c.validateWeb(); err != nil {
		return err
	}

	// Validate Events config
	if err := c.validateEvents(); err != nil {
		return err
	}

	// Validate Logging config
	if err := c.validateLogging(); err != nil {
		return err
	}

	return nil
}

func (c *Config) validateDHCP() error {
	if !c.DHCP.IsEnabled() {
		return nil // skip validation when DHCP is disabled
	}

	if c.DHCP.Subnet == "" {
		return fmt.Errorf("dhcp.subnet is required")
	}

	// Parse subnet
	_, ipnet, err := net.ParseCIDR(c.DHCP.Subnet)
	if err != nil {
		return fmt.Errorf("dhcp.subnet invalid CIDR: %w", err)
	}
	c.DHCP.subnetIPNet = ipnet

	// Parse range start
	if c.DHCP.RangeStart == "" {
		return fmt.Errorf("dhcp.range_start is required")
	}
	rangeStart := net.ParseIP(c.DHCP.RangeStart)
	if rangeStart == nil {
		return fmt.Errorf("dhcp.range_start is not a valid IP: %s", c.DHCP.RangeStart)
	}
	c.DHCP.rangeStartIP = rangeStart

	// Parse range end
	if c.DHCP.RangeEnd == "" {
		return fmt.Errorf("dhcp.range_end is required")
	}
	rangeEnd := net.ParseIP(c.DHCP.RangeEnd)
	if rangeEnd == nil {
		return fmt.Errorf("dhcp.range_end is not a valid IP: %s", c.DHCP.RangeEnd)
	}
	c.DHCP.rangeEndIP = rangeEnd

	// Validate range is within subnet and ordered
	if !ipnet.Contains(rangeStart) {
		return fmt.Errorf("dhcp.range_start %s is not in subnet %s", c.DHCP.RangeStart, c.DHCP.Subnet)
	}
	if !ipnet.Contains(rangeEnd) {
		return fmt.Errorf("dhcp.range_end %s is not in subnet %s", c.DHCP.RangeEnd, c.DHCP.Subnet)
	}

	// Compare IPs as integers
	if ipToInt(rangeStart) > ipToInt(rangeEnd) {
		return fmt.Errorf("dhcp.range_start %s must be <= dhcp.range_end %s", c.DHCP.RangeStart, c.DHCP.RangeEnd)
	}

	// Parse gateway
	if c.DHCP.Gateway == "" {
		return fmt.Errorf("dhcp.gateway is required")
	}
	gateway := net.ParseIP(c.DHCP.Gateway)
	if gateway == nil {
		return fmt.Errorf("dhcp.gateway is not a valid IP: %s", c.DHCP.Gateway)
	}
	if !ipnet.Contains(gateway) {
		return fmt.Errorf("dhcp.gateway %s is not in subnet %s", c.DHCP.Gateway, c.DHCP.Subnet)
	}
	c.DHCP.gatewayIP = gateway

	// Validate DNS servers (optional but if provided must be valid)
	for _, dnsServer := range c.DHCP.DNSServers {
		ip := net.ParseIP(dnsServer)
		if ip == nil {
			return fmt.Errorf("dhcp.dns_servers contains invalid IP: %s", dnsServer)
		}
	}

	// Validate TTLs
	if c.DHCP.DefaultTTL <= 0 {
		return fmt.Errorf("dhcp.default_ttl must be positive")
	}
	if c.DHCP.StaticTTL <= 0 {
		return fmt.Errorf("dhcp.static_ttl must be positive")
	}

	return nil
}

func (c *Config) validateDNS() error {
	if c.DNS.Listen == "" {
		return fmt.Errorf("dns.listen is required")
	}

	// Validate listen address format (could be :53 or 0.0.0.0:53 etc)
	parts := strings.Split(c.DNS.Listen, ":")
	if len(parts) != 2 {
		return fmt.Errorf("dns.listen must be in format 'host:port': %s", c.DNS.Listen)
	}

	// Validate port
	port := parts[1]
	if port == "" {
		return fmt.Errorf("dns.listen port cannot be empty")
	}

	if c.DNS.NameFormat.WithHostname == "" {
		return fmt.Errorf("dns.name_format.with_hostname is required")
	}

	if c.DNS.NameFormat.Fallback == "" {
		return fmt.Errorf("dns.name_format.fallback is required")
	}

	return nil
}

func (c *Config) validateUpstream() error {
	if c.Upstream.DOHURL == "" {
		return fmt.Errorf("upstream.doh_url is required")
	}

	if len(c.Upstream.FallbackServers) == 0 {
		return fmt.Errorf("upstream.fallback_servers must not be empty")
	}

	// Validate fallback servers
	for _, server := range c.Upstream.FallbackServers {
		// Should be host:port format
		parts := strings.Split(server, ":")
		if len(parts) != 2 {
			return fmt.Errorf("upstream.fallback_servers entry %q must be in host:port format", server)
		}
		host := parts[0]
		// Try to parse as IP
		if net.ParseIP(host) == nil {
			// Could be a hostname, that's ok for fallback
		}
	}

	if c.Upstream.CacheMaxEntries <= 0 {
		return fmt.Errorf("upstream.cache_max_entries must be positive")
	}

	if c.Upstream.CacheDB == "" {
		return fmt.Errorf("upstream.cache_db is required")
	}

	return nil
}

func (c *Config) validateBlocklists() error {
	for i, bl := range c.Blocklists {
		if bl.Path == "" && bl.URL == "" {
			return fmt.Errorf("blocklists[%d] must have either path or url", i)
		}
	}
	return nil
}

func (c *Config) validateStaticHosts() error {
	// Parse the DHCP subnet so we can check containment
	var subnet *net.IPNet
	if c.DHCP.Subnet != "" {
		_, subnet, _ = net.ParseCIDR(c.DHCP.Subnet)
	}

	for i, host := range c.StaticHosts {
		if host.Name == "" {
			return fmt.Errorf("static_hosts[%d].name is required", i)
		}

		if host.IP == "" {
			return fmt.Errorf("static_hosts[%d].ip is required", i)
		}

		ip := net.ParseIP(host.IP)
		if ip == nil {
			return fmt.Errorf("static_hosts[%d].ip is not valid: %s", i, host.IP)
		}

		// Check that static IP is within the DHCP subnet
		if subnet != nil && !subnet.Contains(ip) {
			return fmt.Errorf("static_hosts[%d].ip %s is not in subnet %s", i, host.IP, c.DHCP.Subnet)
		}
		c.StaticHosts[i].ip = ip

		if host.MAC == "" {
			return fmt.Errorf("static_hosts[%d].mac is required", i)
		}

		mac, err := net.ParseMAC(host.MAC)
		if err != nil {
			return fmt.Errorf("static_hosts[%d].mac is not valid: %w", i, err)
		}
		c.StaticHosts[i].mac = mac
	}
	return nil
}

func (c *Config) validateFingerprint() error {
	if c.Fingerprint.Enabled && c.Fingerprint.Interface == "" {
		return fmt.Errorf("fingerprint.interface is required when fingerprint is enabled")
	}
	return nil
}

func (c *Config) validateWeb() error {
	if c.Web.Enabled && c.Web.Listen == "" {
		return fmt.Errorf("web.listen is required when web is enabled")
	}
	return nil
}

func (c *Config) validateEvents() error {
	if c.Events.PerHostLimit <= 0 {
		return fmt.Errorf("events.per_host_limit must be positive")
	}

	if c.Events.Persist && c.Events.DB == "" {
		return fmt.Errorf("events.db is required when events.persist is true")
	}

	return nil
}

func (c *Config) validateLogging() error {
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error")
	}

	validFormats := map[string]bool{"json": true, "text": true}
	if !validFormats[c.Logging.Format] {
		return fmt.Errorf("logging.format must be one of: json, text")
	}

	return nil
}

// Watch watches the configuration file for changes and reloads it
// It polls the file every 2 seconds using os.Stat to detect modifications
// When a change is detected, the callback is called with the new config
// The callback should handle the config update and return any errors
func Watch(path string, callback func(*Config) error) error {
	logger := slog.Default()

	// Get initial file info
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat config file: %w", err)
	}

	lastModTime := fi.ModTime()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		fi, err := os.Stat(path)
		if err != nil {
			logger.Error("failed to stat config file", slog.Any("error", err))
			continue
		}

		// Check if file was modified
		if fi.ModTime() != lastModTime {
			logger.Info("config file changed, reloading", slog.String("path", path))

			cfg, err := Load(path)
			if err != nil {
				logger.Error("failed to load config", slog.Any("error", err))
				continue
			}

			if err := callback(cfg); err != nil {
				logger.Error("callback failed to process config", slog.Any("error", err))
				continue
			}

			lastModTime = fi.ModTime()
			logger.Info("config reloaded successfully")
		}
	}

	return nil
}

// IsEnabled returns whether DHCP is enabled (defaults to true)
func (d *DHCPConfig) IsEnabled() bool {
	return d.Enabled == nil || *d.Enabled
}

// GetSubnet returns the parsed subnet IPNet
func (d *DHCPConfig) GetSubnet() *net.IPNet {
	return d.subnetIPNet
}

// GetRangeStart returns the parsed range start IP
func (d *DHCPConfig) GetRangeStart() net.IP {
	return d.rangeStartIP
}

// GetRangeEnd returns the parsed range end IP
func (d *DHCPConfig) GetRangeEnd() net.IP {
	return d.rangeEndIP
}

// GetGateway returns the parsed gateway IP
func (d *DHCPConfig) GetGateway() net.IP {
	return d.gatewayIP
}

// GetIP returns the parsed IP from StaticHost
func (sh *StaticHost) GetIP() net.IP {
	return sh.ip
}

// GetMAC returns the parsed MAC from StaticHost
func (sh *StaticHost) GetMAC() net.HardwareAddr {
	return sh.mac
}

// ipToInt converts an IPv4 address to a 32-bit integer for comparison
func ipToInt(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// ConfigPath returns the path to the config file
// This is a helper that can be used to resolve relative or default paths
func ConfigPath(path string) (string, error) {
	if path == "" {
		// Try common default locations
		defaults := []string{
			"/etc/lantern/config.json",
			"/etc/lantern.json",
			"./config.json",
		}
		for _, p := range defaults {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		return "", fmt.Errorf("no config file found in default locations")
	}

	// Expand home directory if needed
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}

	return filepath.Abs(path)
}
