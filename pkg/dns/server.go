package dns

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/events"
	"github.com/phubbard/lantern/pkg/metrics"
	"github.com/phubbard/lantern/pkg/model"
)

// Zone holds the local DNS zone data
type Zone struct {
	mu      sync.RWMutex
	domain  string                 // e.g. "home.lab."
	records map[string][]dns.RR    // forward records (A, TXT) keyed by FQDN
	reverse map[string]*dns.PTR    // reverse records keyed by in-addr.arpa name
}

// NewZone creates a new Zone with the given domain
func NewZone(domain string) *Zone {
	return &Zone{
		domain:  domain,
		records: make(map[string][]dns.RR),
		reverse: make(map[string]*dns.PTR),
	}
}

// UpdateFromLease creates/updates DNS records from a lease
// Creates A record, PTR record, and TXT record (if fingerprint exists)
func (z *Zone) UpdateFromLease(lease *model.Lease) {
	if lease == nil || lease.DNSName == "" || lease.IP == nil {
		return
	}

	z.mu.Lock()
	defer z.mu.Unlock()

	// Ensure FQDN ends with dot
	fqdn := lease.DNSName
	if fqdn[len(fqdn)-1] != '.' {
		fqdn = fqdn + "."
	}

	// Create/update A or AAAA record based on IP version
	var rrA dns.RR
	ttl := uint32(3600)
	if lease.TTL > 0 {
		ttl = uint32(lease.TTL.Seconds())
	}

	if ipv4 := lease.IP.To4(); ipv4 != nil {
		rrA = &dns.A{
			Hdr: dns.RR_Header{
				Name:   fqdn,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			A: ipv4,
		}
	} else {
		rrA = &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   fqdn,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			AAAA: lease.IP,
		}
	}

	// Store A/AAAA record
	z.records[fqdn] = []dns.RR{rrA}

	// Create/update TXT record if fingerprint exists
	if lease.Fingerprint != nil && lease.Fingerprint.OS != "" {
		txtValue := fmt.Sprintf("os=%s;type=%s;vendor=%s",
			lease.Fingerprint.OS,
			lease.Fingerprint.DeviceType,
			lease.Fingerprint.Vendor,
		)
		rrTXT := &dns.TXT{
			Hdr: dns.RR_Header{
				Name:   fqdn,
				Rrtype: dns.TypeTXT,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			Txt: []string{txtValue},
		}
		z.records[fqdn] = append(z.records[fqdn], rrTXT)
	}

	// Create/update reverse PTR record
	reverseAddr := z.ReverseIP(lease.IP)
	rrPTR := &dns.PTR{
		Hdr: dns.RR_Header{
			Name:   reverseAddr,
			Rrtype: dns.TypePTR,
			Class:  dns.ClassINET,
			Ttl:    ttl,
		},
		Ptr: fqdn,
	}
	z.reverse[reverseAddr] = rrPTR
}

// RemoveLease removes DNS records associated with a lease
func (z *Zone) RemoveLease(lease *model.Lease) {
	if lease == nil || lease.DNSName == "" || lease.IP == nil {
		return
	}

	z.mu.Lock()
	defer z.mu.Unlock()

	// Remove forward record
	fqdn := lease.DNSName
	if fqdn[len(fqdn)-1] != '.' {
		fqdn = fqdn + "."
	}
	delete(z.records, fqdn)

	// Remove reverse record
	reverseAddr := z.ReverseIP(lease.IP)
	delete(z.reverse, reverseAddr)
}

// Lookup performs a thread-safe lookup of records in the zone
func (z *Zone) Lookup(name string, qtype uint16) []dns.RR {
	z.mu.RLock()
	defer z.mu.RUnlock()

	// Normalize name to FQDN
	if name[len(name)-1] != '.' {
		name = name + "."
	}

	switch qtype {
	case dns.TypeA, dns.TypeAAAA:
		if records, ok := z.records[name]; ok {
			result := []dns.RR{}
			for _, rr := range records {
				if rr.Header().Rrtype == qtype {
					result = append(result, rr)
				}
			}
			return result
		}
	case dns.TypeTXT:
		if records, ok := z.records[name]; ok {
			result := []dns.RR{}
			for _, rr := range records {
				if rr.Header().Rrtype == dns.TypeTXT {
					result = append(result, rr)
				}
			}
			return result
		}
	case dns.TypePTR:
		if ptr, ok := z.reverse[name]; ok {
			return []dns.RR{ptr}
		}
	case dns.TypeSOA:
		// Return SOA for zone apex queries
		if name == z.domain || name == "." {
			return []dns.RR{z.buildSOA()}
		}
	case dns.TypeNS:
		// Return NS for zone apex queries
		if name == z.domain || name == "." {
			return []dns.RR{z.buildNS()}
		}
	}

	return nil
}

// ReverseIP converts an IP address to in-addr.arpa format
// 192.168.1.42 -> "42.1.168.192.in-addr.arpa."
// 2001:db8::1 -> "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."
func (z *Zone) ReverseIP(ip net.IP) string {
	if ipv4 := ip.To4(); ipv4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", ipv4[3], ipv4[2], ipv4[1], ipv4[0])
	}

	// IPv6 reverse
	return dns.ReverseAddr(ip)
}

// buildSOA creates an SOA record for the local zone
func (z *Zone) buildSOA() *dns.SOA {
	return &dns.SOA{
		Hdr: dns.RR_Header{
			Name:   z.domain,
			Rrtype: dns.TypeSOA,
			Class:  dns.ClassINET,
			Ttl:    3600,
		},
		Ns:      "ns1." + z.domain,
		Mbox:    "admin." + z.domain,
		Serial:  uint32(time.Now().Unix()),
		Refresh: 3600,
		Retry:   1800,
		Expire:  604800,
		Minttl:  3600,
	}
}

// buildNS creates an NS record for the local zone
func (z *Zone) buildNS() *dns.NS {
	return &dns.NS{
		Hdr: dns.RR_Header{
			Name:   z.domain,
			Rrtype: dns.TypeNS,
			Class:  dns.ClassINET,
			Ttl:    3600,
		},
		Ns: "ns1." + z.domain,
	}
}

// Resolver interface for upstream resolution
type Resolver interface {
	Resolve(ctx context.Context, msg *dns.Msg) (*dns.Msg, string, error)
}

// Blocker interface for blocklist checking
type Blocker interface {
	IsBlocked(name string) bool
}

// Server implements a full-featured DNS server with local zone, blocklist, and upstream resolution
type Server struct {
	cfg       *config.Config
	zone      *Zone
	upstream  Resolver
	blocker   Blocker
	metrics   *metrics.Collector
	events    *events.Store
	udpServer *dns.Server
	tcpServer *dns.Server
	logger    *slog.Logger
}

// New creates a new DNS server with the given configuration and dependencies
func New(cfg *config.Config, upstream Resolver, blocker Blocker, m *metrics.Collector, e *events.Store) *Server {
	logger := slog.Default()
	return &Server{
		cfg:      cfg,
		zone:     NewZone(cfg.DNS.Zone),
		upstream: upstream,
		blocker:  blocker,
		metrics:  m,
		events:   e,
		logger:   logger,
	}
}

// Zone returns the internal DNS zone for external updates (e.g., from DHCP)
func (s *Server) Zone() *Zone {
	return s.zone
}

// Start starts both UDP and TCP DNS servers
func (s *Server) Start(ctx context.Context) error {
	// Get listen address
	addr := s.cfg.DNS.Listen
	if addr == "" {
		addr = ":53"
	}

	// Register the main query handler
	dns.HandleFunc(".", s.handleQuery)

	// Create UDP server
	s.udpServer = &dns.Server{
		Addr:       addr,
		Net:        "udp",
		Handler:    dns.DefaultServeMux,
		ReusePort:  true,
		MaxTCPConnections: -1,
	}

	// Create TCP server
	s.tcpServer = &dns.Server{
		Addr:       addr,
		Net:        "tcp",
		Handler:    dns.DefaultServeMux,
		ReusePort:  true,
		MaxTCPConnections: -1,
	}

	// Start UDP server in a goroutine
	go func() {
		s.logger.Info("Starting DNS UDP server", "addr", addr)
		if err := s.udpServer.ListenAndServe(); err != nil {
			s.logger.Error("DNS UDP server error", "err", err)
		}
	}()

	// Start TCP server in a goroutine
	go func() {
		s.logger.Info("Starting DNS TCP server", "addr", addr)
		if err := s.tcpServer.ListenAndServe(); err != nil {
			s.logger.Error("DNS TCP server error", "err", err)
		}
	}()

	return nil
}

// Stop gracefully stops both DNS servers
func (s *Server) Stop() error {
	if s.udpServer != nil {
		if err := s.udpServer.Shutdown(); err != nil {
			s.logger.Error("Error shutting down UDP server", "err", err)
			return err
		}
	}

	if s.tcpServer != nil {
		if err := s.tcpServer.Shutdown(); err != nil {
			s.logger.Error("Error shutting down TCP server", "err", err)
			return err
		}
	}

	return nil
}

// handleQuery is the main DNS query handler
func (s *Server) handleQuery(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()

	// Extract question
	if len(r.Question) == 0 {
		// Invalid query, return as-is with no questions
		dns.WriteMsg(w, r)
		return
	}

	q := r.Question[0]
	qname := q.Name
	qtype := q.Qtype

	s.logger.Debug("DNS query received", "name", qname, "type", dns.TypeToString[qtype])

	// Check blocklist
	if s.blocker != nil && s.blocker.IsBlocked(qname) {
		s.logger.Debug("Query blocked by blocklist", "name", qname)
		blocked := s.buildBlockedResponse(r)
		s.metrics.RecordQuery(qname, qtype, "blocked", time.Since(start))
		s.events.Record(&events.DNSEvent{
			Name:      qname,
			Type:      dns.TypeToString[qtype],
			Source:    "blocked",
			Timestamp: time.Now(),
		})
		dns.WriteMsg(w, blocked)
		return
	}

	// Check local zone
	records := s.zone.Lookup(qname, qtype)
	if len(records) > 0 {
		s.logger.Debug("Query answered from local zone", "name", qname, "records", len(records))
		resp := s.buildLocalResponse(r, records)
		s.metrics.RecordQuery(qname, qtype, "local", time.Since(start))
		s.events.Record(&events.DNSEvent{
			Name:      qname,
			Type:      dns.TypeToString[qtype],
			Source:    "local",
			Timestamp: time.Now(),
		})
		dns.WriteMsg(w, resp)
		return
	}

	// Check for SOA query on zone apex
	if qtype == dns.TypeSOA && (qname == s.zone.domain || qname == ".") {
		resp := s.buildLocalResponse(r, []dns.RR{s.zone.buildSOA()})
		s.metrics.RecordQuery(qname, qtype, "local", time.Since(start))
		s.events.Record(&events.DNSEvent{
			Name:      qname,
			Type:      dns.TypeToString[qtype],
			Source:    "local",
			Timestamp: time.Now(),
		})
		dns.WriteMsg(w, resp)
		return
	}

	// Check for NS query on zone apex
	if qtype == dns.TypeNS && (qname == s.zone.domain || qname == ".") {
		resp := s.buildLocalResponse(r, []dns.RR{s.zone.buildNS()})
		s.metrics.RecordQuery(qname, qtype, "local", time.Since(start))
		s.events.Record(&events.DNSEvent{
			Name:      qname,
			Type:      dns.TypeToString[qtype],
			Source:    "local",
			Timestamp: time.Now(),
		})
		dns.WriteMsg(w, resp)
		return
	}

	// Forward to upstream resolver
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, source, err := s.upstream.Resolve(ctx, r)
	if err != nil {
		s.logger.Error("Upstream resolution error", "name", qname, "err", err)
		// Return SERVFAIL
		resp = r.Copy()
		resp.SetRcode(r, dns.RcodeServerFailure)
		s.metrics.RecordQuery(qname, qtype, "error", time.Since(start))
		s.events.Record(&events.DNSEvent{
			Name:      qname,
			Type:      dns.TypeToString[qtype],
			Source:    "error",
			Timestamp: time.Now(),
		})
		dns.WriteMsg(w, resp)
		return
	}

	s.logger.Debug("Query answered from upstream", "name", qname, "source", source)
	s.metrics.RecordQuery(qname, qtype, source, time.Since(start))
	s.events.Record(&events.DNSEvent{
		Name:      qname,
		Type:      dns.TypeToString[qtype],
		Source:    source,
		Timestamp: time.Now(),
	})
	dns.WriteMsg(w, resp)
}

// buildBlockedResponse builds a DNS response for blocked queries
// Returns 0.0.0.0 for A queries, :: for AAAA, NXDOMAIN for others
func (s *Server) buildBlockedResponse(r *dns.Msg) *dns.Msg {
	resp := r.Copy()
	resp.Answer = []dns.RR{}

	if len(r.Question) == 0 {
		return resp
	}

	q := r.Question[0]

	switch q.Qtype {
	case dns.TypeA:
		rr := &dns.A{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    3600,
			},
			A: net.ParseIP("0.0.0.0").To4(),
		}
		resp.Answer = append(resp.Answer, rr)
	case dns.TypeAAAA:
		rr := &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    3600,
			},
			AAAA: net.ParseIP("::"),
		}
		resp.Answer = append(resp.Answer, rr)
	default:
		// Return NXDOMAIN for other query types
		resp.SetRcode(r, dns.RcodeNameError)
	}

	return resp
}

// buildLocalResponse builds a DNS response from local zone records
func (s *Server) buildLocalResponse(r *dns.Msg, records []dns.RR) *dns.Msg {
	resp := r.Copy()
	resp.Answer = records
	resp.SetRcode(r, dns.RcodeSuccess)
	return resp
}
