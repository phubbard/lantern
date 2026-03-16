package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pfh/lantern/pkg/blocker"
	"github.com/pfh/lantern/pkg/cache"
	"github.com/pfh/lantern/pkg/config"
	"github.com/pfh/lantern/pkg/control"
	"github.com/pfh/lantern/pkg/dhcp"
	"github.com/pfh/lantern/pkg/dns"
	"github.com/pfh/lantern/pkg/events"
	"github.com/pfh/lantern/pkg/metrics"
	"github.com/pfh/lantern/pkg/model"
	"github.com/pfh/lantern/pkg/upstream"
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
	rootCmd.PersistentFlags().StringVar(&socketPath, "socket", "/var/run/lantern.sock",
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

	// Import command group
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import configuration from external files",
	}

	importHostsCmd := &cobra.Command{
		Use:   "hosts <file>",
		Short: "Import /etc/hosts file as blocklist entries",
		Args:  cobra.ExactArgs(1),
		RunE:  runImportHosts,
	}
	importCmd.AddCommand(importHostsCmd)

	importBindCmd := &cobra.Command{
		Use:   "bind <file>",
		Short: "Import BIND zone file as static entries",
		Args:  cobra.ExactArgs(1),
		RunE:  runImportBind,
	}
	importCmd.AddCommand(importBindCmd)

	rootCmd.AddCommand(importCmd)

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
	if cfg.LogJSON {
		logHandler = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, nil)
	}
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.Info("lantern starting", "config_path", configPath)

	// 3. Create metrics collector
	metricsCollector := metrics.NewCollector()

	// 4. Create events store
	eventsStore := events.NewStore()

	// 5. Create lease pool from config
	leasePool := model.NewLeasePool(
		cfg.DHCP.Subnet,
		cfg.DHCP.RangeStart,
		cfg.DHCP.RangeEnd,
		cfg.DHCP.Domain,
		time.Duration(cfg.DHCP.DefaultLeaseTTL)*time.Second,
		time.Duration(cfg.DHCP.MaxLeaseTTL)*time.Second,
	)

	// 6. Initialize static hosts from config into the lease pool
	for _, staticHost := range cfg.DHCP.StaticHosts {
		err := leasePool.AddStatic(
			staticHost.MAC,
			staticHost.IP,
			staticHost.Name,
		)
		if err != nil {
			logger.Warn("failed to add static host", "mac", staticHost.MAC, "error", err)
		}
	}

	// 7. Create SQLite cache
	cachePath := filepath.Join(cfg.CachePath, "lantern.db")
	os.MkdirAll(cfg.CachePath, 0700)
	dnsCache, err := cache.NewSQLiteCache(cachePath)
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}
	defer dnsCache.Close()

	// 8. Create upstream resolver
	upstreamResolver := upstream.NewResolver(cfg.DNS.Upstreams, dnsCache)

	// 9. Create blocker, load blocklist files
	blocklistMgr := blocker.NewBlocklistManager()
	for _, blocklistFile := range cfg.Blocklists {
		err := blocklistMgr.LoadFromFile(blocklistFile)
		if err != nil {
			logger.Warn("failed to load blocklist", "file", blocklistFile, "error", err)
		}
	}
	blockMgr := blocker.NewBlocker(blocklistMgr)

	// 10. Create DNS server with upstream and blocker
	dnsServer := dns.NewServer(
		cfg.DNS.ListenAddr,
		leasePool,
		upstreamResolver,
		blockMgr,
		metricsCollector,
	)

	// 11. Create DHCP server with onLease callback
	dhcpServer := dhcp.NewServer(
		cfg.DHCP.ListenAddr,
		leasePool,
		cfg.DHCP.Domain,
		time.Duration(cfg.DHCP.DefaultLeaseTTL)*time.Second,
	)

	// Set up callback for new leases - updates DNS zone
	dhcpServer.OnLease(func(lease *model.Lease) {
		logger.Info("new lease", "mac", lease.MAC, "ip", lease.IP, "name", lease.Name)
		leasePool.AddLease(lease)
		metricsCollector.RecordLeaseGrant()
		eventsStore.Record(events.Event{
			Type:      "lease_granted",
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"mac":  lease.MAC,
				"ip":   lease.IP,
				"name": lease.Name,
			},
		})
	})

	// 12. Load saved leases from disk, populate DNS zone
	leasesPath := filepath.Join(cfg.DataPath, "leases.json")
	os.MkdirAll(cfg.DataPath, 0700)
	if savedLeases, err := leasePool.LoadFromDisk(leasesPath); err == nil && len(savedLeases) > 0 {
		logger.Info("loaded saved leases from disk", "count", len(savedLeases))
		for _, lease := range savedLeases {
			leasePool.AddLease(lease)
		}
	}

	// 13. Create control server, register RPC handlers
	ctrlServer := control.NewServer(socketPath)
	os.Remove(socketPath) // Clean up old socket if it exists

	// Register RPC handlers
	ctrlServer.RegisterStatusHandler(func(ctx context.Context) (map[string]interface{}, error) {
		return map[string]interface{}{
			"uptime":        time.Since(time.Now()),
			"dns_queries":   metricsCollector.GetDNSQueryCount(),
			"blocked_count": metricsCollector.GetBlockedCount(),
			"cache_size":    metricsCollector.GetCacheSize(),
		}, nil
	})

	ctrlServer.RegisterLeasesHandler(func(ctx context.Context) ([]*model.Lease, error) {
		return leasePool.GetAllLeases(), nil
	})

	ctrlServer.RegisterAddStaticHandler(func(ctx context.Context, mac, ip, name string) error {
		return leasePool.AddStatic(mac, ip, name)
	})

	ctrlServer.RegisterRemoveStaticHandler(func(ctx context.Context, mac string) error {
		return leasePool.RemoveStatic(mac)
	})

	ctrlServer.RegisterReloadConfigHandler(func(ctx context.Context) error {
		newCfg, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("failed to load new config: %w", err)
		}
		// Apply new config - reload upstreams, blocklists, etc
		cfg = newCfg
		logger.Info("config reloaded")
		return nil
	})

	ctrlServer.RegisterImportHostsHandler(func(ctx context.Context, filePath string) error {
		return blocklistMgr.LoadFromFile(filePath)
	})

	ctrlServer.RegisterImportBindHandler(func(ctx context.Context, filePath string) error {
		return leasePool.ImportBindZoneFile(filePath)
	})

	// 14. Start all servers in goroutines
	go func() {
		if err := dnsServer.Start(ctx); err != nil && err != context.Canceled {
			logger.Error("DNS server error", "error", err)
		}
	}()

	go func() {
		if err := dhcpServer.Start(ctx); err != nil && err != context.Canceled {
			logger.Error("DHCP server error", "error", err)
		}
	}()

	go func() {
		if err := ctrlServer.Start(ctx); err != nil && err != context.Canceled {
			logger.Error("control server error", "error", err)
		}
	}()

	go func() {
		if err := dnsCache.StartPruner(ctx, time.Duration(cfg.CachePruneInterval)*time.Second); err != nil && err != context.Canceled {
			logger.Error("cache pruner error", "error", err)
		}
	}()

	logger.Info("all servers started successfully")

	// 15. Set up signal handling
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
			logger.Info("reloading config")
			newCfg, err := config.Load(configPath)
			if err != nil {
				logger.Error("failed to reload config", "error", err)
				continue
			}
			// Update config and reload components
			cfg = newCfg
			if err := blocklistMgr.Reload(); err != nil {
				logger.Error("failed to reload blocklists", "error", err)
			}
			logger.Info("config reloaded successfully")
		}
	}

shutdown:
	// 16. Gracefully shut down everything
	logger.Info("shutting down servers")
	cancel() // Signal all goroutines to stop

	// Wait for servers to shut down (with timeout)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Stop servers
	dnsServer.Stop(shutdownCtx)
	dhcpServer.Stop(shutdownCtx)
	ctrlServer.Stop(shutdownCtx)
	dnsCache.Close()

	// 17. Save leases to disk
	leasesPath = filepath.Join(cfg.DataPath, "leases.json")
	if allLeases := leasePool.GetAllLeases(); len(allLeases) > 0 {
		if err := leasePool.SaveToDisk(leasesPath, allLeases); err != nil {
			logger.Error("failed to save leases to disk", "error", err)
		} else {
			logger.Info("leases saved to disk")
		}
	}

	logger.Info("lantern shutdown complete")
	return nil
}

// runReload reloads the config on a running server
func runReload(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	if err := client.ReloadConfig(context.Background()); err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	fmt.Println("Config reloaded successfully")
	return nil
}

// runStatus shows the server status
func runStatus(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	status, err := client.GetStatus(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	fmt.Println("lantern server status:")
	fmt.Printf("  Uptime:        %v\n", status["uptime"])
	fmt.Printf("  DNS Queries:   %d\n", status["dns_queries"])
	fmt.Printf("  Blocked:       %d\n", status["blocked_count"])
	fmt.Printf("  Cache Size:    %d\n", status["cache_size"])

	return nil
}

// runLeases lists all current DHCP leases
func runLeases(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	leases, err := client.GetLeases(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get leases: %w", err)
	}

	if len(leases) == 0 {
		fmt.Println("No leases")
		return nil
	}

	// Print table header
	fmt.Printf("%-20s %-15s %-30s %s\n", "MAC", "IP", "Name", "Expires")
	fmt.Println(string(make([]byte, 80)) + "\n")

	// Print leases
	for _, lease := range leases {
		expiresIn := time.Until(lease.ExpiresAt)
		if expiresIn < 0 {
			expiresIn = 0
		}
		name := lease.Name
		if name == "" {
			name = "-"
		}
		fmt.Printf("%-20s %-15s %-30s %v\n",
			lease.MAC,
			lease.IP,
			name,
			expiresIn.Round(time.Second),
		)
	}

	return nil
}

// runStaticAdd adds a static IP reservation
func runStaticAdd(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	mac := args[0]
	ip := args[1]
	name := ""
	if len(args) > 2 {
		name = args[2]
	}

	if err := client.AddStatic(context.Background(), mac, ip, name); err != nil {
		return fmt.Errorf("failed to add static reservation: %w", err)
	}

	fmt.Printf("Static reservation added: %s -> %s", mac, ip)
	if name != "" {
		fmt.Printf(" (%s)", name)
	}
	fmt.Println()

	return nil
}

// runStaticRemove removes a static IP reservation
func runStaticRemove(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	mac := args[0]

	if err := client.RemoveStatic(context.Background(), mac); err != nil {
		return fmt.Errorf("failed to remove static reservation: %w", err)
	}

	fmt.Printf("Static reservation removed: %s\n", mac)
	return nil
}

// runImportHosts imports a /etc/hosts file as blocklist entries
func runImportHosts(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	filePath := args[0]

	if err := client.ImportHosts(context.Background(), filePath); err != nil {
		return fmt.Errorf("failed to import hosts file: %w", err)
	}

	fmt.Printf("Hosts file imported: %s\n", filePath)
	return nil
}

// runImportBind imports a BIND zone file as static entries
func runImportBind(cmd *cobra.Command, args []string) error {
	client, err := control.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to lantern: %w", err)
	}
	defer client.Close()

	filePath := args[0]

	if err := client.ImportBind(context.Background(), filePath); err != nil {
		return fmt.Errorf("failed to import bind zone file: %w", err)
	}

	fmt.Printf("BIND zone file imported: %s\n", filePath)
	return nil
}
