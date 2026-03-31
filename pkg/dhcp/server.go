package dhcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/events"
	"github.com/phubbard/lantern/pkg/metrics"
	"github.com/phubbard/lantern/pkg/model"
)

// Server implements a DHCPv4 server for home DNS.
type Server struct {
	cfg     *config.Config
	pool    *model.LeasePool
	metrics *metrics.Collector
	events  *events.Store
	onLease func(lease *model.Lease)
	logger  *slog.Logger
	mu      sync.RWMutex
	server  *server4.Server
	cancel  context.CancelFunc
	done    chan struct{}
}

// New creates a new DHCP server instance.
func New(cfg *config.Config, pool *model.LeasePool, m *metrics.Collector, e *events.Store, onLease func(*model.Lease)) *Server {
	return &Server{
		cfg:     cfg,
		pool:    pool,
		metrics: m,
		events:  e,
		onLease: onLease,
		logger:  slog.Default().With("component", "dhcp.server"),
		done:    make(chan struct{}),
	}
}

// Start begins listening on UDP port 67 for DHCP requests.
func (s *Server) Start(ctx context.Context) error {
	iface := s.cfg.DHCP.Interface
	if iface == "" {
		iface = s.cfg.Interface
	}
	s.logger.InfoContext(ctx, "starting DHCP server", "port", 67, "interface", iface)

	// Load existing leases from disk if available
	if err := s.LoadLeases(); err != nil {
		s.logger.WarnContext(ctx, "failed to load leases from disk", "error", err)
	}

	// Create cancellable context for the server
	serverCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Start the lease reaper goroutine
	go s.leaseReaper(serverCtx)

	// Create the DHCP server with our handler
	laddr := &net.UDPAddr{IP: net.IPv4zero, Port: 67}
	srv, err := server4.NewServer(
		iface,
		laddr,
		s.handler,
	)
	if err != nil {
		return fmt.Errorf("failed to create DHCP server: %w", err)
	}

	s.server = srv
	s.logger.InfoContext(ctx, "DHCP server created successfully")

	// Run the server in a goroutine
	go func() {
		defer close(s.done)
		if err := srv.Serve(); err != nil {
			s.logger.ErrorContext(ctx, "DHCP server error", "error", err)
		}
	}()

	s.logger.InfoContext(ctx, "DHCP server started")
	return nil
}

// Stop gracefully shuts down the DHCP server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.InfoContext(ctx, "stopping DHCP server")

	if s.cancel != nil {
		s.cancel()
	}

	if s.server != nil {
		s.server.Close()
	}

	// Wait for server to finish with timeout
	select {
	case <-s.done:
		s.logger.InfoContext(ctx, "DHCP server stopped")
	case <-time.After(5 * time.Second):
		s.logger.WarnContext(ctx, "DHCP server shutdown timeout")
	}

	// Save leases before shutdown
	if err := s.SaveLeases(); err != nil {
		s.logger.ErrorContext(ctx, "failed to save leases on shutdown", "error", err)
		return err
	}

	return nil
}

// handler is the main DHCP message handler.
func (s *Server) handler(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	s.logger.DebugContext(ctx, "received DHCP packet",
		"peer", peer.String(),
		"message_type", msg.MessageType().String(),
		"xid", msg.TransactionID,
		"client_hw_addr", msg.ClientHWAddr.String(),
	)

	s.metrics.IncrCounter("dhcp_requests")

	switch msg.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		s.handleDiscover(conn, peer, msg)
	case dhcpv4.MessageTypeRequest:
		s.handleRequest(conn, peer, msg)
	case dhcpv4.MessageTypeRelease:
		s.handleRelease(conn, peer, msg)
	case dhcpv4.MessageTypeDecline:
		s.handleDecline(conn, peer, msg)
	default:
		s.logger.DebugContext(ctx, "ignoring unsupported DHCP message type", "type", msg.MessageType().String())
	}
}

// handleDiscover processes DHCP DISCOVER messages and sends OFFER.
func (s *Server) handleDiscover(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr
	hostname := msg.HostName()
	clientID := s.extractClientID(msg)

	s.logger.DebugContext(ctx, "handling DISCOVER",
		"mac", clientMAC.String(),
		"hostname", hostname,
		"client_id", clientID,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.metrics.IncrCounter("dhcp_discovers")

	// Check for existing lease
	var lease *model.Lease
	existingLease := s.pool.FindByMAC(clientMAC)
	if existingLease != nil {
		lease = existingLease
		s.logger.DebugContext(ctx, "found existing lease for DISCOVER",
			"mac", clientMAC.String(),
			"ip", lease.IP.String(),
		)
	} else {
		// Allocate new IP
		ip, err := s.pool.AllocateIP()
		if err != nil {
			s.logger.ErrorContext(ctx, "failed to allocate IP for DISCOVER",
				"mac", clientMAC.String(),
				"error", err,
			)
			return
		}

		leaseDuration := time.Duration(s.cfg.DHCP.DefaultTTL)
		lease = &model.Lease{
			MAC:       clientMAC,
			IP:        ip,
			Hostname:  hostname,
			ClientID:  clientID,
			TTL:       leaseDuration,
			GrantedAt: time.Now(),
			ExpiresAt: time.Now().Add(leaseDuration),
		}

		// Generate DNS name
		lease.DNSName = s.pool.GenerateDNSName(lease, "")

		// Store the lease
		if err := s.pool.SetLease(lease); err != nil {
			s.logger.ErrorContext(ctx, "failed to store lease", "error", err)
			return
		}

		s.logger.InfoContext(ctx, "allocated new IP for DISCOVER",
			"mac", clientMAC.String(),
			"ip", ip.String(),
			"hostname", hostname,
		)
	}

	// Build OFFER response
	reply, err := dhcpv4.NewReplyFromRequest(msg)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create OFFER reply",
			"error", err,
			"mac", clientMAC.String(),
		)
		return
	}

	// Set OFFER parameters
	reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	reply.YourIPAddr = lease.IP
	reply.ServerIPAddr = net.ParseIP(s.cfg.DHCP.Gateway)

	// Build and add options
	s.setDHCPOptions(reply)

	// Send response
	s.sendResponse(conn, peer, reply)

	// Record event
	s.events.Record(model.HostEvent{
		Timestamp: time.Now(),
		MAC:       clientMAC.String(),
		IP:        lease.IP.String(),
		ClientID:  clientID,
		Type:      model.EventDHCPOffer,
		Detail:    fmt.Sprintf("hostname=%s", hostname),
	})

	s.metrics.IncrCounter("dhcp_offers")

	s.logger.InfoContext(ctx, "sent DHCP OFFER",
		"mac", clientMAC.String(),
		"ip", lease.IP.String(),
		"hostname", hostname,
	)
}

// handleRequest processes DHCP REQUEST messages and sends ACK or NAK.
func (s *Server) handleRequest(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr
	hostname := msg.HostName()
	clientID := s.extractClientID(msg)

	// Extract requested IP from Option 50 or use ClientIPAddr for renewal
	requestedIP := msg.RequestedIPAddress()
	if requestedIP == nil || requestedIP.IsUnspecified() {
		if !msg.ClientIPAddr.IsUnspecified() {
			requestedIP = msg.ClientIPAddr
		}
	}

	s.logger.DebugContext(ctx, "handling REQUEST",
		"mac", clientMAC.String(),
		"hostname", hostname,
		"requested_ip", requestedIP,
		"client_id", clientID,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the request — look up by the requested IP
	if requestedIP == nil {
		s.sendNAK(conn, peer, msg, clientMAC, hostname)
		return
	}

	existingLease := s.pool.FindByIP(requestedIP)
	if existingLease == nil || existingLease.MAC.String() != clientMAC.String() {
		// Invalid request - send NAK
		s.logger.WarnContext(ctx, "rejecting REQUEST - lease validation failed",
			"mac", clientMAC.String(),
			"requested_ip", requestedIP.String(),
		)
		s.sendNAK(conn, peer, msg, clientMAC, hostname)
		return
	}

	// Update lease with current information
	leaseDuration := time.Duration(s.cfg.DHCP.DefaultTTL)
	existingLease.Hostname = hostname
	existingLease.ClientID = clientID
	existingLease.GrantedAt = time.Now()
	existingLease.ExpiresAt = time.Now().Add(leaseDuration)
	existingLease.DNSName = s.pool.GenerateDNSName(existingLease, "")

	// Build ACK response
	reply, err := dhcpv4.NewReplyFromRequest(msg)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create ACK reply",
			"error", err,
			"mac", clientMAC.String(),
		)
		return
	}

	reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	reply.YourIPAddr = requestedIP
	reply.ServerIPAddr = net.ParseIP(s.cfg.DHCP.Gateway)

	// Build and add options
	s.setDHCPOptions(reply)

	// Send response
	s.sendResponse(conn, peer, reply)

	// Call the lease callback to update DNS
	if s.onLease != nil {
		s.onLease(existingLease)
	}

	// Record event
	s.events.Record(model.HostEvent{
		Timestamp: time.Now(),
		MAC:       clientMAC.String(),
		IP:        existingLease.IP.String(),
		ClientID:  clientID,
		Type:      model.EventDHCPAck,
		Detail:    fmt.Sprintf("hostname=%s", hostname),
	})

	s.metrics.IncrCounter("dhcp_acks")

	s.logger.InfoContext(ctx, "sent DHCP ACK",
		"mac", clientMAC.String(),
		"ip", existingLease.IP.String(),
		"hostname", hostname,
	)
}

// sendNAK sends a DHCP NAK response.
func (s *Server) sendNAK(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4, clientMAC net.HardwareAddr, hostname string) {
	reply, err := dhcpv4.NewReplyFromRequest(msg)
	if err != nil {
		s.logger.Error("failed to create NAK reply", "error", err)
		return
	}

	reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeNak))
	reply.ServerIPAddr = net.ParseIP(s.cfg.DHCP.Gateway)
	s.sendResponse(conn, peer, reply)

	s.events.Record(model.HostEvent{
		Timestamp: time.Now(),
		MAC:       clientMAC.String(),
		Type:      model.EventDHCPNak,
		Detail:    fmt.Sprintf("hostname=%s reason=lease_validation_failed", hostname),
	})

	s.metrics.IncrCounter("dhcp_naks")
}

// handleRelease processes DHCP RELEASE messages.
func (s *Server) handleRelease(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr
	ipAddr := msg.ClientIPAddr

	s.logger.DebugContext(ctx, "handling RELEASE",
		"mac", clientMAC.String(),
		"ip", ipAddr.String(),
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Release the lease by MAC
	if err := s.pool.ReleaseLease(clientMAC); err != nil {
		s.logger.WarnContext(ctx, "failed to release lease",
			"ip", ipAddr.String(),
			"mac", clientMAC.String(),
			"error", err,
		)
		return
	}

	// Record event
	s.events.Record(model.HostEvent{
		Timestamp: time.Now(),
		MAC:       clientMAC.String(),
		IP:        ipAddr.String(),
		Type:      model.EventDHCPRelease,
	})

	s.metrics.IncrCounter("dhcp_releases")

	s.logger.InfoContext(ctx, "released DHCP lease",
		"mac", clientMAC.String(),
		"ip", ipAddr.String(),
	)
}

// handleDecline processes DHCP DECLINE messages.
func (s *Server) handleDecline(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr
	ipAddr := msg.ClientIPAddr

	s.logger.WarnContext(ctx, "received DHCP DECLINE",
		"mac", clientMAC.String(),
		"ip", ipAddr.String(),
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Mark the IP as declined by releasing the lease
	if err := s.pool.ReleaseLease(clientMAC); err != nil {
		s.logger.ErrorContext(ctx, "failed to release declined IP",
			"ip", ipAddr.String(),
			"error", err,
		)
	}

	// Record event
	s.events.Record(model.HostEvent{
		Timestamp: time.Now(),
		MAC:       clientMAC.String(),
		IP:        ipAddr.String(),
		Type:      model.EventDHCPRequest, // closest match for decline
		Detail:    "decline",
	})
}

// sendResponse sends a DHCP response packet to the client.
func (s *Server) sendResponse(conn net.PacketConn, peer net.Addr, resp *dhcpv4.DHCPv4) {
	ctx := context.Background()

	respBytes := resp.ToBytes()
	_, err := conn.WriteTo(respBytes, peer)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to send DHCP response",
			"peer", peer.String(),
			"error", err,
		)
		return
	}

	s.logger.DebugContext(ctx, "sent DHCP response",
		"peer", peer.String(),
		"size_bytes", len(respBytes),
	)
}

// setDHCPOptions applies standard DHCP options to a reply message.
func (s *Server) setDHCPOptions(reply *dhcpv4.DHCPv4) {
	// Subnet mask — derive from CIDR
	_, ipnet, err := net.ParseCIDR(s.cfg.DHCP.Subnet)
	if err == nil && ipnet != nil {
		reply.UpdateOption(dhcpv4.OptSubnetMask(ipnet.Mask))
	}

	// Router (gateway)
	gateway := net.ParseIP(s.cfg.DHCP.Gateway)
	if gateway != nil {
		reply.UpdateOption(dhcpv4.OptRouter(gateway))
	}

	// DNS servers
	if len(s.cfg.DHCP.DNSServers) > 0 {
		dnsIPs := make([]net.IP, 0, len(s.cfg.DHCP.DNSServers))
		for _, dnsStr := range s.cfg.DHCP.DNSServers {
			if ip := net.ParseIP(dnsStr); ip != nil {
				dnsIPs = append(dnsIPs, ip)
			}
		}
		if len(dnsIPs) > 0 {
			reply.UpdateOption(dhcpv4.OptDNS(dnsIPs...))
		}
	}

	// Lease time
	leaseTime := time.Duration(s.cfg.DHCP.DefaultTTL)
	reply.UpdateOption(dhcpv4.OptIPAddressLeaseTime(leaseTime))

	// DHCP Server Identifier
	if gateway != nil {
		reply.UpdateOption(dhcpv4.OptServerIdentifier(gateway))
	}

	// Domain name
	if s.cfg.Domain != "" {
		reply.UpdateOption(dhcpv4.OptDomainName(s.cfg.Domain))
	}
}

// extractClientID extracts the client ID from DHCP options.
// Option 61 is typically raw bytes (hardware type + MAC), so we
// hex-encode it for safe display and storage.
func (s *Server) extractClientID(msg *dhcpv4.DHCPv4) string {
	if opt := msg.Options.Get(dhcpv4.OptionClientIdentifier); opt != nil {
		return fmt.Sprintf("%x", opt)
	}
	return ""
}

// leaseReaper periodically scans for and expires old leases.
func (s *Server) leaseReaper(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	s.logger.InfoContext(ctx, "lease reaper started")

	for {
		select {
		case <-ctx.Done():
			s.logger.InfoContext(ctx, "lease reaper stopped")
			return
		case <-ticker.C:
			s.expireLeases()
		}
	}
}

// expireLeases checks for and removes expired leases.
func (s *Server) expireLeases() {
	ctx := context.Background()

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	allLeases := s.pool.GetAllLeases()
	for _, lease := range allLeases {
		if lease.Static {
			continue // Static leases don't expire
		}
		if now.After(lease.ExpiresAt) {
			s.logger.InfoContext(ctx, "lease expired",
				"mac", lease.MAC.String(),
				"ip", lease.IP.String(),
				"hostname", lease.Hostname,
			)

			s.events.Record(model.HostEvent{
				Timestamp: now,
				MAC:       lease.MAC.String(),
				IP:        lease.IP.String(),
				Type:      model.EventLeaseExpired,
				Detail:    fmt.Sprintf("hostname=%s", lease.Hostname),
			})

			_ = s.pool.ReleaseLease(lease.MAC)
		}
	}
}

// SaveLeases persists all current leases to disk as JSON.
func (s *Server) SaveLeases() error {
	ctx := context.Background()

	s.mu.RLock()
	leases := s.pool.GetAllLeases()
	s.mu.RUnlock()

	if s.cfg.DHCP.LeaseFile == "" {
		s.logger.DebugContext(ctx, "lease file not configured, skipping save")
		return nil
	}

	data, err := json.MarshalIndent(leases, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal leases: %w", err)
	}

	if err := os.WriteFile(s.cfg.DHCP.LeaseFile, data, 0o644); err != nil {
		return fmt.Errorf("failed to write lease file: %w", err)
	}

	s.logger.DebugContext(ctx, "saved leases to disk",
		"file", s.cfg.DHCP.LeaseFile,
		"count", len(leases),
	)

	return nil
}

// LoadLeases restores leases from disk if the lease file exists.
func (s *Server) LoadLeases() error {
	ctx := context.Background()

	if s.cfg.DHCP.LeaseFile == "" {
		s.logger.DebugContext(ctx, "lease file not configured, skipping load")
		return nil
	}

	data, err := os.ReadFile(s.cfg.DHCP.LeaseFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.logger.DebugContext(ctx, "lease file does not exist, starting with empty pool",
				"file", s.cfg.DHCP.LeaseFile,
			)
			return nil
		}
		return fmt.Errorf("failed to read lease file: %w", err)
	}

	var leases []*model.Lease
	if err := json.Unmarshal(data, &leases); err != nil {
		return fmt.Errorf("failed to unmarshal leases: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	loadedCount := 0
	expiredCount := 0

	for _, lease := range leases {
		// Skip expired leases
		if !lease.Static && lease.ExpiresAt.Before(now) {
			expiredCount++
			continue
		}

		// Restore lease to pool
		if err := s.pool.SetLease(lease); err != nil {
			s.logger.WarnContext(ctx, "failed to restore lease",
				"mac", lease.MAC.String(),
				"ip", lease.IP.String(),
				"error", err,
			)
			continue
		}

		loadedCount++
	}

	s.logger.InfoContext(ctx, "loaded leases from disk",
		"file", s.cfg.DHCP.LeaseFile,
		"loaded", loadedCount,
		"expired", expiredCount,
	)

	return nil
}
