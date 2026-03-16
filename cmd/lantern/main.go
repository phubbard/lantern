package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/phubbard/lantern/pkg/blocker"
	"github.com/phubbard/lantern/pkg/cache"
	"github.com/phubbard/lantern/pkg/config"
	"github.com/phubbard/lantern/pkg/control"
	"github.com/phubbard/lantern/pkg/dhcp"
	lanterndns "github.com/phubbard/lantern/pkg/dns"
	"github.com/phubbard/lantern/pkg/events"
	"github.com/phubbard/lantern/pkg/metrics"
	"github.com/phubbard/lantern/pkg/model"
	"github.com/phubbard/lantern/pkg/upstream"
	"github.com/phubbard/lantern/pkg/web"
	"github.com/spf13/cobra"
)

var (
	configPath string
	socketPath string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "lantern",
		Short: "Home DNS and DHCP server",
		Long:  "A lightweight DNS and DHCP server for home networks with ad-blocking and local zone management",
	}

	// Global flags
	rootCmd.PersistentFlags().StringVar(&socketPath, "socket", "/var/run/lantern/lantern.sock",
		"Path to lantern control socket")

	// Serve command
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the lantern server",
		RunE:  runServe,
	}
	serveCmd.Flags().StringVarP(&configPath, "config", "c", "/etc/lantern/config.json",
		"Path to config file")
	rootCmd.AddCommand(serveCmd)

	// Reload command
	reloadCmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload config on running server",
		RunE:  runReload,
	}
	rootCmd.AddCommand(reloadCmd)

	// Status command
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show server status",
		RunE:  runStatus,
	}
	rootCmd.AddCommand(statusCmd)

	// Leases command
	leasesCmd := &cobra.Command{
		Use:   "leases",
		Short: "List current DHCP leases",
		RunE:  runLeases,
	}
	rootCmd.AddCommand(leasesCmd)

	// Static command group
	staticCmd := &cobra.Command{
		Use:   "static",
		Short: "Manage static IP reservations",
	}

	staticAddCmd := &cobra.Command{
		Use:   "add <mac> <ip> [name]",
		Short: "Add a static IP reservation",
		Args:  cobra.RangeArgs(2, 3),
		RunE:  runStaticAdd,
	}
	staticCmd.AddCommand(staticAddCmd)

	staticRemoveCmd := &cobra.Command{
		Use:   "remove <mac>",
		Short: "Remove a static IP reservation",
		Args:  cobra.ExactArgs(1),
		RunE:  runStaticRemove,
	}
	staticCmd.AddCommand(staticRemoveCmd)

	rootCmd.AddCommand(staticCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runServe starts the complete lantern server
func runServe(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Load config from file
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// 2. Set up slog logger
	var logHandler slog.Handler
	if cfg.Logging.Format == "json" {
		logHandler = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, nil)
	}
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.Info("lantern starting",
		"config_path", configPath,
		"version", config.Version,
	)

	// 3. Create metrics collector
	metricsCollector := metrics.NewCollector()

	// 4. Create events store
	eventsStore := events.NewStore(cfg.Events.PerHostLimit)

	// 5. Parse CIDR and IP addresses from config
	_, subnetIPNet, err := net.ParseCIDR(cfg.DHCP.Subnet)
	if err != nil {
		return fmt.Errorf("failed to parse DHCP subnet: %w", err)
	}

	rangeStart := net.ParseIP(cfg.DHCP.RangeStart)
	if rangeStart == nil {
		return fmt.Errorf("failed to parse DHCP range start: %s", cfg.DHCP.RangeStart)
	}

	rangeEnd := net.ParseIP(cfg.DHCP.RangeEnd)
	if rangeEnd == nil {
		return fmt.Errorf("failed to parse DHCP range end: %s", cfg.DHCP.RangeEnd)
	}

	// 6. Create lease pool from config
	leasePool := model.NewLeasePool(
		*subnetIPNet,
		rangeStart,
		rangeEnd,
		cfg.Domain,
		time.Duration(cfg.DHCP.DefaultTTL),
		time.Duration(cfg.DHCP.StaticTTL),
	)

	// 7. Initialize static hosts from config into the lease pool
	for _, staticHost := range cfg.StaticHosts {
		mac, err := net.ParseMAC(staticHost.MAC)
		if err != nil {
			logger.Warn("failed to parse static host MAC", "mac", staticHost.MAC, "error", err)
			continue
		}
		ip := net.ParseIP(staticHost.IP)
		if ip == nil {
			logger.Warn("failed to parse static host IP", "ip", staticHost.IP)
			continue
		}

		lease := &model.Lease{
			MAC:       mac,
			IP:        ip,
			Hostname:  staticHost.Name,
			DNSName:   leasePool.GenerateDNSName(&model.Lease{Hostname: staticHost.Name, IP: ip}, ""),
			Static:    true,
			TTL:       time.Duration(cfg.DHCP.StaticTTL),
			GrantedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Duration(cfg.DHCP.StaticTTL)),
		}
		if err := leasePool.SetStaticLease(lease); err != nil {
			logger.Warn("failed to add static host", "mac", staticHost.MAC, "error", err)
		}
	}

	// 8. Create SQLite cache
	cacheDir := filepath.Dir(cfg.Upstream.CacheDB)
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		logger.Warn("failed to create cache directory", "dir", cacheDir, "error", err)
	}
	dnsCache, err := cache.New(cfg.Upstream.CacheDB, cfg.Upstream.CacheMaxEntries, logger)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	defer dnsCache.Close()

	// Start cache pruner
	dnsCache.StartPruner(ctx, 5*time.Minute)

	// 9. Create upstream resolver
	upstreamResolver := upstream.New(cfg, dnsCache, metricsCollector, logger)

	// 10. Create blocker, load blocklist files
	blockMgr := blocker.New(logger)
	for _, bl := range cfg.Blocklists {
		if !bl.Enabled {
			continue
		}
		count, err := blockMgr.LoadFile(bl.Path)
		if err != nil {
			logger.Warn("failed to load blocklist", "file", bl.Path, "error", err)
		} else {
			logger.Info("loaded blocklist", "file", bl.Path, "count", count)
		}
	}

	// 11. Create DNS server with upstream and blocker
	dnsServer := lanterndns.New(cfg, upstreamResolver, blockMgr, metricsCollector, eventsStore)

	// 11b. Register static hosts in the DNS zone
	for _, lease := range leasePool.GetAllLeases() {
		if lease.Static {
			dnsServer.Zone().UpdateFromLease(lease)
			logger.Info("registered static host in DNS", "name", lease.DNSName, "ip", lease.IP)
		}
	}

	// 12. Create DHCP server with onLease callback that updates the DNS zone
	onLease := func(lease *model.Lease) {
		logger.Info("new lease granted",
			"mac", lease.MAC,
			"ip", lease.IP,
			"hostname", lease.Hostname,
			"dns_name", lease.DNSName,
		)
		// Update DNS zone with the new lease
		dnsServer.Zone().UpdateFromLease(lease)
	}

	dhcpServer := dhcp.New(cfg, leasePool, metricsCollector, eventsStore, onLease)

	// 13. Create web server if enabled
	var webServer *web.Server
	if cfg.Web.Enabled {
		webServer = web.New(cfg, leasePool, metricsCollector, eventsStore, blockMgr, logger)
	}

	// 14. Create control server, register RPC handlers
	ctrlServer := control.NewServer(socketPath, logger)

	// Register RPC handlers using generic Handle()
	ctrlServer.Handle("status", func(params json.RawMessage) (any, error) {
		snap := metricsCollector.Snapshot()
		total, expired := dnsCache.Stats()
		return map[string]any{
			"version":          config.Version,
			"queries_total":    snap.QueriesTotal,
			"queries_blocked":  snap.QueriesBlocked,
			"queries_cached":   snap.QueriesCached,
			"queries_upstream": snap.QueriesUpstream,
			"cache_total":      total,
			"cache_expired":    expired,
			"blocked_domains":  blockMgr.Count(),
		}, nil
	})

	ctrlServer.Handle("leases", func(params json.RawMessage) (any, error) {
		return leasePool.GetAllLeases(), nil
	})

	ctrlServer.Handle("static.add", func(params json.RawMessage) (any, error) {
		var p struct {
			MAC      string `json:"mac"`
			IP       string `json:"ip"`
			Hostname string `json:"hostname"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}

		mac, err := net.ParseMAC(p.MAC)
		if err != nil {
			return nil, fmt.Errorf("invalid MAC: %w", err)
		}
		ip := net.ParseIP(p.IP)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP: %s", p.IP)
		}

		lease := &model.Lease{
			MAC:       mac,
			IP:        ip,
			Hostname:  p.Hostname,
			DNSName:   leasePool.GenerateDNSName(&model.Lease{Hostname: p.Hostname, IP: ip}, ""),
			Static:    true,
			TTL:       time.Duration(cfg.DHCP.StaticTTL),
			GrantedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Duration(cfg.DHCP.StaticTTL)),
		}
		if err := leasePool.SetStaticLease(lease); err != nil {
			return nil, err
		}
		dnsServer.Zone().UpdateFromLease(lease)
		return map[string]string{"message": "static reservation added"}, nil
	})

	ctrlServer.Handle("static.remove", func(params json.RawMessage) (any, error) {
		var p struct {
			MAC string `json:"mac"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}

		mac, err := net.ParseMAC(p.MAC)
		if err != nil {
			return nil, fmt.Errorf("invalid MAC: %w", err)
		}

		if err := leasePool.RemoveStaticLease(mac); err != nil {
			return nil, err
		}
		return map[string]string{"message": "static reservation removed"}, nil
	})

	ctrlServer.Handle("reload", func(params json.RawMessage) (any, error) {
		newCfg, err := config.Load(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		_ = newCfg // TODO: apply new config to running components
		logger.Info("config reloaded")
		return map[string]string{"message": "config reloaded"}, nil
	})

	ctrlServer.Handle("blocklist.reload", func(params json.RawMessage) (any, error) {
		if err := blockMgr.Reload(); err != nil {
			return nil, err
		}
		return map[string]any{
			"message": "blocklists reloaded",
			"count":   blockMgr.Count(),
		}, nil
	})

	ctrlServer.Handle("import.hosts", func(params json.RawMessage) (any, error) {
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		count, err := blockMgr.LoadFile(p.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"message": "hosts file imported",
			"entries": count,
		}, nil
	})

	// 15. Start all servers in goroutines
	startTime := time.Now()
	_ = startTime // available for uptime calculations

	go func() {
		if err := dnsServer.Start(ctx); err != nil {
			logger.Error("DNS server error", "error", err)
		}
	}()

	go func() {
		if err := dhcpServer.Start(ctx); err != nil {
			logger.Error("DHCP server error", "error", err)
		}
	}()

	go func() {
		if err := ctrlServer.Start(ctx); err != nil {
			logger.Error("control server error", "error", err)
		}
	}()

	if webServer != nil {
		go func() {
			if err := webServer.Start(ctx); err != nil {
				logger.Error("web server error", "error", err)
			}
		}()
	}

	logger.Info("all servers started successfully")

	// 16. Set up signal handling
	sigChan := make(chan os.Signal, 1)
	reloadChan := make(chan os.Signal, 1)

	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	signal.Notify(reloadChan, syscall.SIGHUP)

	// Wait for signals
	for {
		select {
		case sig := <-sigChan:
			logger.Info("received shutdown signal", "signal", sig)
			goto shutdown
		case <-reloadChan:
			logger.Info("reloading config (SIGHUP)")
			newCfg, err := config.Load(configPath)
			if err != nil {
				logger.Error("failed to reload config", "error", err)
				continue
			}
			_ = newCfg
			if err := blockMgr.Reload(); err != nil {
				logger.Error("failed to reload blocklists", "error", err)
			}
			logger.Info("config reloaded successfully")
		}
	}

shutdown:
	// 17. Gracefully shut down everything
	logger.Info("shutting down servers")
	cancel() // Signal all goroutines to stop

	// Stop servers
	if err := dnsServer.Stop(); err != nil {
		logger.Error("DNS server stop error", "error", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := dhcpServer.Stop(shutdownCtx); err != nil {
		logger.Error("DHCP server stop error", "error", err)
	}
	// control server stops itself when ctx is cancelled (in Start)
	if webServer != nil {
		if err := webServer.Stop(); err != nil {
			logger.Error("web server stop error", "error", err)
		}
	}
	dnsCache.Close()

	// 18. Save leases to disk
	leaseFile := cfg.DHCP.LeaseFile
	if leaseFile != "" {
		leaseDir := filepath.Dir(leaseFile)
		if err := os.MkdirAll(leaseDir, 0700); err != nil {
			logger.Error("failed to create lease directory", "error", err)
		}
		allLeases := leasePool.GetAllLeases()
		if len(allLeases) > 0 {
			data, err := json.MarshalIndent(allLeases, "", "  ")
			if err != nil {
				logger.Error("failed to marshal leases", "error", err)
			} else if err := os.WriteFile(leaseFile, data, 0600); err != nil {
				logger.Error("failed to save leases", "error", err)
			} else {
				logger.Info("leases saved to disk", "count", len(allLeases), "file", leaseFile)
			}
		}
	}

	logger.Info("lantern shutdown complete")
	return nil
}

// runReload reloads the config on a running server
func runReload(cmd *cobra.Command, args []string) error {
	client := control.NewClient(socketPath)

	result, err := client.Call("reload", nil)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	fmt.Println(string(result))
	return nil
}

// runStatus shows the server status
func runStatus(cmd *cobra.Command, args []string) error {
	client := control.NewClient(socketPath)

	var status map[string]any
	if err := client.CallResult("status", nil, &status); err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	fmt.Println("lantern server status:")
	for k, v := range status {
		fmt.Printf("  %-20s %v\n", k+":", v)
	}

	return nil
}

// runLeases lists all current DHCP leases
func runLeases(cmd *cobra.Command, args []string) error {
	client := control.NewClient(socketPath)

	var leases []model.Lease
	if err := client.CallResult("leases", nil, &leases); err != nil {
		return fmt.Errorf("failed to get leases: %w", err)
	}

	if len(leases) == 0 {
		fmt.Println("No active leases")
		return nil
	}

	// Print table header
	fmt.Printf("%-20s %-15s %-30s %-10s %s\n", "MAC", "IP", "Name", "Static", "Expires")
	fmt.Printf("%-20s %-15s %-30s %-10s %s\n", "---", "--", "----", "------", "-------")

	// Print leases
	for _, lease := range leases {
		expiresIn := time.Until(lease.ExpiresAt)
		if expiresIn < 0 {
			expiresIn = 0
		}
		name := lease.DNSName
		if name == "" {
			name = lease.Hostname
		}
		if name == "" {
			name = "-"
		}
		staticStr := ""
		if lease.Static {
			staticStr = "yes"
		}
		fmt.Printf("%-20s %-15s %-30s %-10s %v\n",
			lease.MAC,
			lease.IP,
			name,
			staticStr,
			expiresIn.Round(time.Second),
		)
	}

	return nil
}

// runStaticAdd adds a static IP reservation
func runStaticAdd(cmd *cobra.Command, args []string) error {
	client := control.NewClient(socketPath)

	mac := args[0]
	ip := args[1]
	hostname := ""
	if len(args) > 2 {
		hostname = args[2]
	}

	params := map[string]string{
		"mac":      mac,
		"ip":       ip,
		"hostname": hostname,
	}

	_, err := client.Call("static.add", params)
	if err != nil {
		return fmt.Errorf("failed to add static reservation: %w", err)
	}

	fmt.Printf("Static reservation added: %s -> %s", mac, ip)
	if hostname != "" {
		fmt.Printf(" (%s)", hostname)
	}
	fmt.Println()

	return nil
}

// runStaticRemove removes a static IP reservation
func runStaticRemove(cmd *cobra.Command, args []string) error {
	client := control.NewClient(socketPath)

	mac := args[0]

	_, err := client.Call("static.remove", map[string]string{"mac": mac})
	if err != nil {
		return fmt.Errorf("failed to remove static reservation: %w", err)
	}

	fmt.Printf("Static reservation removed: %s\n", mac)
	return nil
}
