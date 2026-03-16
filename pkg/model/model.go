package model

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// EventType constants
const (
	EventDHCPDiscover EventType = "dhcp_discover"
	EventDHCPOffer    EventType = "dhcp_offer"
	EventDHCPRequest  EventType = "dhcp_request"
	EventDHCPAck      EventType = "dhcp_ack"
	EventDHCPNak      EventType = "dhcp_nak"
	EventDHCPRelease  EventType = "dhcp_release"
	EventDNSQuery     EventType = "dns_query"
	EventFingerprint  EventType = "fingerprint"
	EventLeaseExpired EventType = "lease_expired"
	EventStaticSet    EventType = "static_set"
	EventStaticRemove EventType = "static_remove"
)

// EventType represents a type of network event
type EventType string

// HostFingerprint contains OS and device type identification info
type HostFingerprint struct {
	OS         string    `json:"os"`
	OSVersion  string    `json:"os_version"`
	DeviceType string    `json:"device_type"` // "desktop", "mobile", "iot", "server", etc.
	RawSig     string    `json:"raw_sig"`     // raw p0f signature string for debugging
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Confidence float64   `json:"confidence"` // 0.0-1.0
}

// Lease represents a DHCP lease or static assignment
type Lease struct {
	MAC         net.HardwareAddr `json:"mac"`
	IP          net.IP           `json:"ip"`
	Hostname    string           `json:"hostname"` // from DHCP option 12/81
	DNSName     string           `json:"dns_name"` // computed full DNS name
	Static      bool             `json:"static"`
	TTL         time.Duration    `json:"ttl"`
	GrantedAt   time.Time        `json:"granted_at"`
	ExpiresAt   time.Time        `json:"expires_at"`
	ClientID    string           `json:"client_id"` // DHCP option 61
	Fingerprint *HostFingerprint `json:"fingerprint,omitempty"`
}

// LeaseJSON is a JSON-serializable representation of Lease
type LeaseJSON struct {
	MAC         string           `json:"mac"`
	IP          string           `json:"ip"`
	Hostname    string           `json:"hostname"`
	DNSName     string           `json:"dns_name"`
	Static      bool             `json:"static"`
	TTLSeconds  int              `json:"ttl_seconds"`
	GrantedAt   time.Time        `json:"granted_at"`
	ExpiresAt   time.Time        `json:"expires_at"`
	ClientID    string           `json:"client_id"`
	Fingerprint *HostFingerprint `json:"fingerprint,omitempty"`
}

// MarshalJSON converts a Lease to JSON format
func (l *Lease) MarshalJSON() ([]byte, error) {
	lj := LeaseJSON{
		MAC:         l.MAC.String(),
		IP:          l.IP.String(),
		Hostname:    l.Hostname,
		DNSName:     l.DNSName,
		Static:      l.Static,
		TTLSeconds:  int(l.TTL.Seconds()),
		GrantedAt:   l.GrantedAt,
		ExpiresAt:   l.ExpiresAt,
		ClientID:    l.ClientID,
		Fingerprint: l.Fingerprint,
	}
	return json.Marshal(lj)
}

// UnmarshalJSON converts JSON data into a Lease
func (l *Lease) UnmarshalJSON(data []byte) error {
	var lj LeaseJSON
	if err := json.Unmarshal(data, &lj); err != nil {
		return err
	}

	mac, err := net.ParseMAC(lj.MAC)
	if err != nil {
		return fmt.Errorf("invalid MAC address: %w", err)
	}

	ip := net.ParseIP(lj.IP)
	if ip == nil {
		return fmt.Errorf("invalid IP address: %s", lj.IP)
	}

	l.MAC = mac
	l.IP = ip
	l.Hostname = lj.Hostname
	l.DNSName = lj.DNSName
	l.Static = lj.Static
	l.TTL = time.Duration(lj.TTLSeconds) * time.Second
	l.GrantedAt = lj.GrantedAt
	l.ExpiresAt = lj.ExpiresAt
	l.ClientID = lj.ClientID
	l.Fingerprint = lj.Fingerprint

	return nil
}

// ToJSON converts a Lease to LeaseJSON for manual serialization
func (l *Lease) ToJSON() *LeaseJSON {
	return &LeaseJSON{
		MAC:         l.MAC.String(),
		IP:          l.IP.String(),
		Hostname:    l.Hostname,
		DNSName:     l.DNSName,
		Static:      l.Static,
		TTLSeconds:  int(l.TTL.Seconds()),
		GrantedAt:   l.GrantedAt,
		ExpiresAt:   l.ExpiresAt,
		ClientID:    l.ClientID,
		Fingerprint: l.Fingerprint,
	}
}

// FromJSON creates a Lease from LeaseJSON
func (l *Lease) FromJSON(lj *LeaseJSON) error {
	mac, err := net.ParseMAC(lj.MAC)
	if err != nil {
		return fmt.Errorf("invalid MAC address: %w", err)
	}

	ip := net.ParseIP(lj.IP)
	if ip == nil {
		return fmt.Errorf("invalid IP address: %s", lj.IP)
	}

	l.MAC = mac
	l.IP = ip
	l.Hostname = lj.Hostname
	l.DNSName = lj.DNSName
	l.Static = lj.Static
	l.TTL = time.Duration(lj.TTLSeconds) * time.Second
	l.GrantedAt = lj.GrantedAt
	l.ExpiresAt = lj.ExpiresAt
	l.ClientID = lj.ClientID
	l.Fingerprint = lj.Fingerprint

	return nil
}

// HostEvent represents a single network event for a host
type HostEvent struct {
	Timestamp time.Time `json:"timestamp"`
	MAC       string    `json:"mac"`
	IP        string    `json:"ip"`
	ClientID  string    `json:"client_id"`
	Type      EventType `json:"type"`
	Detail    string    `json:"detail"`
}

// DNSQueryLog records a single DNS query for the query log
type DNSQueryLog struct {
	Timestamp  time.Time     `json:"timestamp"`
	ClientIP   string        `json:"client_ip"`
	QueryName  string        `json:"query_name"`
	QueryType  string        `json:"query_type"` // "A", "AAAA", "PTR", etc.
	ResponseIP string        `json:"response_ip,omitempty"`
	Source     string        `json:"source"` // "local", "cache", "upstream", "blocked"
	Latency    time.Duration `json:"latency_ns"`
	Blocked    bool          `json:"blocked"`
}

// LeasePool manages IP address allocation within a subnet
type LeasePool struct {
	mu         sync.RWMutex
	Subnet     net.IPNet
	RangeStart net.IP
	RangeEnd   net.IP
	Domain     string
	DefaultTTL time.Duration
	StaticTTL  time.Duration
	leases     map[string]*Lease // key: MAC string
	byIP       map[string]*Lease // key: IP string
	byName     map[string]*Lease // key: DNS name
}

// NewLeasePool creates a new LeasePool
func NewLeasePool(subnet net.IPNet, rangeStart, rangeEnd net.IP, domain string, defaultTTL, staticTTL time.Duration) *LeasePool {
	return &LeasePool{
		Subnet:     subnet,
		RangeStart: rangeStart,
		RangeEnd:   rangeEnd,
		Domain:     domain,
		DefaultTTL: defaultTTL,
		StaticTTL:  staticTTL,
		leases:     make(map[string]*Lease),
		byIP:       make(map[string]*Lease),
		byName:     make(map[string]*Lease),
	}
}

// FindByMAC finds a lease by MAC address
func (lp *LeasePool) FindByMAC(mac net.HardwareAddr) *Lease {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.leases[mac.String()]
}

// FindByIP finds a lease by IP address
func (lp *LeasePool) FindByIP(ip net.IP) *Lease {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.byIP[ip.String()]
}

// FindByName finds a lease by DNS name
func (lp *LeasePool) FindByName(name string) *Lease {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.byName[strings.ToLower(name)]
}

// AllocateIP finds and reserves the first available IP in the range
func (lp *LeasePool) AllocateIP() (net.IP, error) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	// Convert IPs to uint32 for comparison
	start := ipToUint32(lp.RangeStart)
	end := ipToUint32(lp.RangeEnd)

	for ip := start; ip <= end; ip++ {
		candidate := uint32ToIP(ip)
		if lp.byIP[candidate.String()] == nil {
			return candidate, nil
		}
	}

	return nil, fmt.Errorf("no available IP addresses in range")
}

// SetLease adds or updates a lease in the pool
func (lp *LeasePool) SetLease(lease *Lease) error {
	if lease == nil || lease.MAC == nil || lease.IP == nil {
		return fmt.Errorf("invalid lease: MAC and IP are required")
	}

	lp.mu.Lock()
	defer lp.mu.Unlock()

	// Remove old entries if MAC already exists
	if oldLease, exists := lp.leases[lease.MAC.String()]; exists {
		delete(lp.byIP, oldLease.IP.String())
		if oldLease.DNSName != "" {
			delete(lp.byName, strings.ToLower(oldLease.DNSName))
		}
	}

	// Remove old IP entry if IP is being reused
	if oldLease, exists := lp.byIP[lease.IP.String()]; exists && oldLease.MAC.String() != lease.MAC.String() {
		delete(lp.leases, oldLease.MAC.String())
		if oldLease.DNSName != "" {
			delete(lp.byName, strings.ToLower(oldLease.DNSName))
		}
	}

	// Add new entries
	lp.leases[lease.MAC.String()] = lease
	lp.byIP[lease.IP.String()] = lease
	if lease.DNSName != "" {
		lp.byName[strings.ToLower(lease.DNSName)] = lease
	}

	return nil
}

// ReleaseLease removes a lease from the pool
func (lp *LeasePool) ReleaseLease(mac net.HardwareAddr) error {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	lease, exists := lp.leases[mac.String()]
	if !exists {
		return fmt.Errorf("lease not found for MAC %s", mac.String())
	}

	delete(lp.leases, mac.String())
	delete(lp.byIP, lease.IP.String())
	if lease.DNSName != "" {
		delete(lp.byName, strings.ToLower(lease.DNSName))
	}

	return nil
}

// GetAllLeases returns a copy of all leases in the pool
func (lp *LeasePool) GetAllLeases() []*Lease {
	lp.mu.RLock()
	defer lp.mu.RUnlock()

	leases := make([]*Lease, 0, len(lp.leases))
	for _, lease := range lp.leases {
		leases = append(leases, lease)
	}
	return leases
}

// SetStaticLease sets a lease as static
func (lp *LeasePool) SetStaticLease(lease *Lease) error {
	if lease == nil {
		return fmt.Errorf("lease cannot be nil")
	}

	lease.Static = true
	lease.TTL = lp.StaticTTL
	lease.ExpiresAt = time.Now().Add(lp.StaticTTL)

	return lp.SetLease(lease)
}

// RemoveStaticLease removes a static lease by MAC address
func (lp *LeasePool) RemoveStaticLease(mac net.HardwareAddr) error {
	return lp.ReleaseLease(mac)
}

// IsExpired checks if a lease has expired
func (lp *LeasePool) IsExpired(lease *Lease) bool {
	if lease.Static {
		return false // Static leases don't expire
	}
	return time.Now().After(lease.ExpiresAt)
}

// GenerateDNSName generates a DNS name using the priority chain:
// 1. Static config with explicit DNS name -> use that
// 2. Client-provided hostname (sanitized) -> hostname.domain
// 3. Fallback -> dhcp-{last_octet:03d}.domain
func (lp *LeasePool) GenerateDNSName(lease *Lease, staticDNSName string) string {
	// Priority 1: Static DNS name from config
	if staticDNSName != "" {
		return staticDNSName
	}

	// Priority 2: Client-provided hostname
	if lease.Hostname != "" {
		sanitized := SanitizeHostname(lease.Hostname)
		if sanitized != "" {
			return sanitized + "." + lp.Domain
		}
	}

	// Priority 3: Fallback to dhcp-{last_octet:03d}.domain
	lastOctet := getLastOctet(lease.IP)
	return fmt.Sprintf("dhcp-%03d.%s", lastOctet, lp.Domain)
}

// SanitizeHostname sanitizes a hostname according to rules:
// - lowercase
// - replace non-alphanumeric (except -) with -
// - collapse consecutive dashes
// - trim dashes from start/end
// - truncate to 63 chars
func SanitizeHostname(hostname string) string {
	// Lowercase
	hostname = strings.ToLower(hostname)

	// Replace non-alphanumeric (except -) with -
	re := regexp.MustCompile(`[^a-z0-9-]`)
	hostname = re.ReplaceAllString(hostname, "-")

	// Collapse consecutive dashes
	re = regexp.MustCompile(`-+`)
	hostname = re.ReplaceAllString(hostname, "-")

	// Trim dashes from start/end
	hostname = strings.Trim(hostname, "-")

	// Truncate to 63 chars (DNS label max)
	if len(hostname) > 63 {
		hostname = hostname[:63]
	}

	return hostname
}

// Helper functions

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

func getLastOctet(ip net.IP) byte {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return ip[3]
}
