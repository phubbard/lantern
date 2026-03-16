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
	"github.com/pfh/lantern/pkg/config"
	"github.com/pfh/lantern/pkg/events"
	"github.com/pfh/lantern/pkg/metrics"
	"github.com/pfh/lantern/pkg/model"
)

// Server implements a DHCPv4 server for home DNS.
type Server struct {
	cfg       *config.Config
	pool      *model.LeasePool
	metrics   *metrics.Collector
	events    *events.Store
	onLease   func(lease *model.Lease)
	logger    *slog.Logger
	mu        sync.RWMutex
	server    *server4.Server
	cancel    context.CancelFunc
	done      chan struct{}
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
	s.logger.InfoContext(ctx, "starting DHCP server", "port", 67, "network", cfg.DHCP.Network)

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
	srv, err := server4.NewServer(
		s.cfg.DHCP.Network,
		net.ParseIP(s.cfg.DHCP.Address),
		s.handler,
		server4.WithDebugLogger(),
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

	s.metrics.RecordDHCPRequest(msg.MessageType().String())

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

	// Extract client information
	clientMAC := msg.ClientHWAddr.String()
	hostname := msg.HostName()
	clientID := s.extractClientID(msg)

	s.logger.DebugContext(ctx, "handling DISCOVER",
		"mac", clientMAC,
		"hostname", hostname,
		"client_id", clientID,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for existing lease
	var lease *model.Lease
	existingLease, found := s.pool.GetByMAC(clientMAC)
	if found && existingLease != nil {
		lease = existingLease
		s.logger.DebugContext(ctx, "found existing lease for DISCOVER",
			"mac", clientMAC,
			"ip", lease.IPAddress,
		)
	} else {
		// Allocate new IP
		ip, err := s.pool.Allocate(clientMAC, hostname)
		if err != nil {
			s.logger.ErrorContext(ctx, "failed to allocate IP for DISCOVER",
				"mac", clientMAC,
				"error", err,
			)
			s.metrics.RecordDHCPError("discover_allocation_failed")
			return
		}

		lease = &model.Lease{
			ClientMAC:   clientMAC,
			IPAddress:   ip.String(),
			Hostname:    hostname,
			ClientID:    clientID,
			LeaseStart:  time.Now(),
			LeaseExpiry: time.Now().Add(time.Duration(s.cfg.DHCP.LeaseTime) * time.Second),
		}

		s.logger.InfoContext(ctx, "allocated new IP for DISCOVER",
			"mac", clientMAC,
			"ip", ip.String(),
			"hostname", hostname,
		)
	}

	// Build OFFER response
	reply, err := dhcpv4.NewReplyFromRequest(msg)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create OFFER reply",
			"error", err,
			"mac", clientMAC,
		)
		s.metrics.RecordDHCPError("offer_creation_failed")
		return
	}

	// Set OFFER parameters
	reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	reply.YourIPAddr = net.ParseIP(lease.IPAddress)
	reply.ServerIPAddr = net.ParseIP(s.cfg.DHCP.Address)

	// Build and add options
	options := s.buildOptions(lease)
	for _, opt := range options {
		reply.UpdateOption(opt)
	}

	// Send response
	s.sendResponse(conn, peer, reply)

	// Record event
	s.events.Record(events.Event{
		Type:      events.EventTypeDHCPOffer,
		Timestamp: time.Now(),
		ClientMAC: clientMAC,
		Hostname:  hostname,
		IPAddress: lease.IPAddress,
		Details:   map[string]string{"client_id": clientID},
	})

	s.metrics.RecordDHCPOffer()

	s.logger.InfoContext(ctx, "sent DHCP OFFER",
		"mac", clientMAC,
		"ip", lease.IPAddress,
		"hostname", hostname,
	)
}

// handleRequest processes DHCP REQUEST messages and sends ACK or NAK.
func (s *Server) handleRequest(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr.String()
	hostname := msg.HostName()
	clientID := s.extractClientID(msg)

	// Extract requested IP from Option 50 or use ClientIPAddr for renewal
	var requestedIP net.IP
	if requestedIPOpt := msg.GetOneOption(dhcpv4.OptionRequestedIPAddress); requestedIPOpt != nil {
		requestedIP = requestedIPOpt.(*dhcpv4.OptRequestedIPAddress).RequestedIPAddr
	} else if !msg.ClientIPAddr.IsUnspecified() {
		requestedIP = msg.ClientIPAddr
	}

	s.logger.DebugContext(ctx, "handling REQUEST",
		"mac", clientMAC,
		"hostname", hostname,
		"requested_ip", requestedIP.String(),
		"client_id", clientID,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the request
	existingLease, found := s.pool.GetByIP(requestedIP.String())
	if !found || existingLease == nil || existingLease.ClientMAC != clientMAC {
		// Invalid request - send NAK
		s.logger.WarnContext(ctx, "rejecting REQUEST - lease validation failed",
			"mac", clientMAC,
			"requested_ip", requestedIP.String(),
			"found", found,
		)

		reply, err := dhcpv4.NewReplyFromRequest(msg)
		if err != nil {
			s.logger.ErrorContext(ctx, "failed to create NAK reply", "error", err)
			s.metrics.RecordDHCPError("nak_creation_failed")
			return
		}

		reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeNak))
		reply.ServerIPAddr = net.ParseIP(s.cfg.DHCP.Address)
		s.sendResponse(conn, peer, reply)

		s.events.Record(events.Event{
			Type:      events.EventTypeDHCPNak,
			Timestamp: time.Now(),
			ClientMAC: clientMAC,
			Hostname:  hostname,
			IPAddress: requestedIP.String(),
			Details:   map[string]string{"reason": "lease_validation_failed"},
		})

		s.metrics.RecordDHCPNak()
		return
	}

	// Update lease with current information
	lease := existingLease
	lease.Hostname = hostname
	lease.ClientID = clientID
	lease.LeaseStart = time.Now()
	lease.LeaseExpiry = time.Now().Add(time.Duration(s.cfg.DHCP.LeaseTime) * time.Second)

	// Build ACK response
	reply, err := dhcpv4.NewReplyFromRequest(msg)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create ACK reply",
			"error", err,
			"mac", clientMAC,
		)
		s.metrics.RecordDHCPError("ack_creation_failed")
		return
	}

	reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	reply.YourIPAddr = requestedIP
	reply.ServerIPAddr = net.ParseIP(s.cfg.DHCP.Address)

	// Build and add options
	options := s.buildOptions(lease)
	for _, opt := range options {
		reply.UpdateOption(opt)
	}

	// Send response
	s.sendResponse(conn, peer, reply)

	// Call the lease callback to update DNS
	if s.onLease != nil {
		s.onLease(lease)
	}

	// Record event
	s.events.Record(events.Event{
		Type:      events.EventTypeDHCPAck,
		Timestamp: time.Now(),
		ClientMAC: clientMAC,
		Hostname:  hostname,
		IPAddress: lease.IPAddress,
		Details:   map[string]string{"client_id": clientID},
	})

	s.metrics.RecordDHCPAck()

	s.logger.InfoContext(ctx, "sent DHCP ACK",
		"mac", clientMAC,
		"ip", lease.IPAddress,
		"hostname", hostname,
	)
}

// handleRelease processes DHCP RELEASE messages.
func (s *Server) handleRelease(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr.String()
	ipAddr := msg.ClientIPAddr.String()

	s.logger.DebugContext(ctx, "handling RELEASE",
		"mac", clientMAC,
		"ip", ipAddr,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Release the lease
	if err := s.pool.Release(ipAddr); err != nil {
		s.logger.WarnContext(ctx, "failed to release lease",
			"ip", ipAddr,
			"mac", clientMAC,
			"error", err,
		)
		s.metrics.RecordDHCPError("release_failed")
		return
	}

	// Record event
	s.events.Record(events.Event{
		Type:      events.EventTypeDHCPRelease,
		Timestamp: time.Now(),
		ClientMAC: clientMAC,
		IPAddress: ipAddr,
	})

	s.metrics.RecordDHCPRelease()

	s.logger.InfoContext(ctx, "released DHCP lease",
		"mac", clientMAC,
		"ip", ipAddr,
	)
}

// handleDecline processes DHCP DECLINE messages.
func (s *Server) handleDecline(conn net.PacketConn, peer net.Addr, msg *dhcpv4.DHCPv4) {
	ctx := context.Background()

	clientMAC := msg.ClientHWAddr.String()
	ipAddr := msg.ClientIPAddr.String()

	s.logger.WarnContext(ctx, "received DHCP DECLINE",
		"mac", clientMAC,
		"ip", ipAddr,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Mark the IP as declined/unavailable
	if err := s.pool.Release(ipAddr); err != nil {
		s.logger.ErrorContext(ctx, "failed to release declined IP",
			"ip", ipAddr,
			"error", err,
		)
	}

	// Record event
	s.events.Record(events.Event{
		Type:      events.EventTypeDHCPDecline,
		Timestamp: time.Now(),
		ClientMAC: clientMAC,
		IPAddress: ipAddr,
	})

	s.metrics.RecordDHCPDecline()
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
		s.metrics.RecordDHCPError("send_failed")
		return
	}

	s.logger.DebugContext(ctx, "sent DHCP response",
		"peer", peer.String(),
		"size_bytes", len(respBytes),
	)
}

// buildOptions constructs standard DHCP options for a lease.
func (s *Server) buildOptions(lease *model.Lease) []dhcpv4.Option {
	var options []dhcpv4.Option

	// Subnet mask
	if s.cfg.DHCP.SubnetMask != "" {
		options = append(options, dhcpv4.OptSubnetMask(net.ParseIP(s.cfg.DHCP.SubnetMask)))
	}

	// Router (gateway)
	if s.cfg.DHCP.Router != "" {
		options = append(options, dhcpv4.OptRouter(net.ParseIP(s.cfg.DHCP.Router)))
	}

	// DNS servers
	if len(s.cfg.DHCP.DNSServers) > 0 {
		dnsIPs := make([]net.IP, 0, len(s.cfg.DHCP.DNSServers))
		for _, dns := range s.cfg.DHCP.DNSServers {
			dnsIPs = append(dnsIPs, net.ParseIP(dns))
		}
		options = append(options, dhcpv4.OptDNS(dnsIPs...))
	}

	// Lease time
	leaseTime := time.Duration(s.cfg.DHCP.LeaseTime) * time.Second
	options = append(options, dhcpv4.OptIPAddressLeaseTime(leaseTime))

	// DHCP Server Identifier
	options = append(options, dhcpv4.OptServerIdentifier(net.ParseIP(s.cfg.DHCP.Address)))

	// Renewal time (T1) - typically 50% of lease time
	t1 := leaseTime / 2
	options = append(options, dhcpv4.OptRebindingTimeValue(t1))

	// Rebinding time (T2) - typically 87.5% of lease time
	t2 := (leaseTime * 7) / 8
	options = append(options, dhcpv4.OptRebindingTimeValue(t2))

	// Domain name
	if s.cfg.Domain != "" {
		options = append(options, dhcpv4.OptDomainName(s.cfg.Domain))
	}

	return options
}

// extractClientID extracts the client ID from DHCP options.
func (s *Server) extractClientID(msg *dhcpv4.DHCPv4) string {
	if clientIDOpt := msg.GetOneOption(dhcpv4.OptionClientIdentifier); clientIDOpt != nil {
		if opt, ok := clientIDOpt.(*dhcpv4.OptClientIdentifier); ok {
			return opt.ClientID
		}
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

	expired := s.pool.ExpireLeases(time.Now())
	for _, lease := range expired {
		s.logger.InfoContext(ctx, "lease expired",
			"mac", lease.ClientMAC,
			"ip", lease.IPAddress,
			"hostname", lease.Hostname,
		)

		s.events.Record(events.Event{
			Type:      events.EventTypeDHCPExpired,
			Timestamp: time.Now(),
			ClientMAC: lease.ClientMAC,
			IPAddress: lease.IPAddress,
			Hostname:  lease.Hostname,
		})

		s.metrics.RecordDHCPExpired()
	}
}

// SaveLeases persists all current leases to disk as JSON.
func (s *Server) SaveLeases() error {
	ctx := context.Background()

	s.mu.RLock()
	leases := s.pool.GetAll()
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
		if lease.LeaseExpiry.Before(now) {
			expiredCount++
			continue
		}

		// Restore lease to pool
		if err := s.pool.Restore(lease); err != nil {
			s.logger.WarnContext(ctx, "failed to restore lease",
				"mac", lease.ClientMAC,
				"ip", lease.IPAddress,
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
