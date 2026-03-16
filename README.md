# Lantern

[![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://golang.org/dl/)
[![MIT License](https://img.shields.io/badge/License-MIT-green)](LICENSE)
[![CI](https://github.com/phubbard/lantern/actions/workflows/ci.yml/badge.svg)](https://github.com/phubbard/lantern/actions/workflows/ci.yml)

A lightweight, self-hosted DNS and DHCP server for home networks. Combines dynamic host configuration, recursive DNS resolution, ad-blocking, and device fingerprinting into a single stateless binary. Inspired by Pi-hole and designed for minimal hardware (Raspberry Pi, NUC, VM).

## Key Features

- **DNS Server** — Full recursive resolver with UFO caching, upstream fallback (DoH + plain), and local zone override
- **DHCP Server** — DHCPv4 with static IP reservations, custom domain naming, and persistent lease storage
- **Ad Blocking** — Blocklist-based filtering (hosts-file format), returns 0.0.0.0 for blocked domains
- **Device Fingerprinting** — Passive TCP/IP stack analysis (p0f-style), no active scanning
- **Web Dashboard** — Pi-hole-style UI with real-time metrics, leases, DNS log, blocklist status
- **RESTful API** — Full programmatic control via JSON endpoints
- **Server-Sent Events** — Real-time updates without polling or websockets
- **Service Integration** — Drop-in systemd and launchd configurations with proper capabilities
- **Hot Reload** — `lantern reload` or `SIGHUP` to reload config, blocklists, upstreams without restart
- **Metrics** — Query counts, block rate, cache hit/miss, latency percentiles via hdrhistogram

## Quick Start

### From Binary

Pre-built binaries are available for Linux and macOS (amd64, arm64):

```bash
# Check the releases page
wget https://github.com/phubbard/lantern/releases/download/v0.1.0/lantern-linux-amd64
chmod +x lantern-linux-amd64
./lantern-linux-amd64 serve -c /etc/lantern/config.json
```

### From Source

Requires Go 1.22+ and `libpcap-dev` (for device fingerprinting; optional but recommended).

On **Debian/Ubuntu**:
```bash
sudo apt-get install golang-1.22 libpcap-dev build-essential
```

On **macOS**:
```bash
brew install go libpcap
```

Build:
```bash
git clone https://github.com/phubbard/lantern.git
cd lantern
make deps
make build
```

Binary is at `./bin/lantern`.

### Docker

Quick run with default config:
```bash
docker run -d --net host \
  -v /etc/lantern:/etc/lantern \
  -v /var/lib/lantern:/var/lib/lantern \
  --cap-add=NET_BIND_SERVICE \
  --cap-add=NET_RAW \
  phubbard/lantern:latest serve -c /etc/lantern/config.json
```

Or with docker-compose (`docker-compose.yml`):
```yaml
version: '3.8'
services:
  lantern:
    image: phubbard/lantern:latest
    network_mode: host
    cap_add:
      - NET_BIND_SERVICE
      - NET_RAW
    volumes:
      - ./config.json:/etc/lantern/config.json
      - lantern_data:/var/lib/lantern
    restart: unless-stopped

volumes:
  lantern_data:
```

## Configuration

Configuration is a JSON file. See `configs/example.json` for a complete, annotated example.

### Top-Level Keys

| Key | Purpose |
|-----|---------|
| `domain` | Base domain for the zone (e.g., `home.lab`) |
| `interface` | Network interface to bind to (e.g., `eth0`). Required for DHCP only. |
| `dhcp` | DHCP server settings: subnet, range, gateway, default TTL, static hosts |
| `dns` | DNS server settings: listen address, port, name format templates |
| `upstream` | Upstream resolver config: DoH URL, fallback servers, cache database, TTL limits |
| `blocklists` | Array of blocklist files to load (hosts format) |
| `static_hosts` | Static IP-to-hostname mappings (applied before DHCP leases) |
| `fingerprint` | Passive fingerprinting config: packet capture options, signature DB |
| `web` | Web UI settings: listen address, port, TLS cert/key |
| `events` | Event store config: ring buffer size per host, retention |
| `logging` | Logging: level (info/debug/warn/error), JSON output toggle |

### Example Structure

```json
{
  "domain": "home.lab",
  "interface": "eth0",
  "dhcp": {
    "subnet": "192.168.1.0/24",
    "range_start": "192.168.1.100",
    "range_end": "192.168.1.254",
    "gateway": "192.168.1.1",
    "dns_servers": ["192.168.1.1"],
    "default_ttl": "24h",
    "static_ttl": "720h"
  },
  "dns": {
    "listen": "0.0.0.0",
    "port": 53,
    "name_format": {
      "with_hostname": "{hostname}.{domain}",
      "fallback": "dhcp-{octet:03d}.{domain}"
    }
  },
  "upstream": {
    "doh_url": "https://dns.google/dns-query",
    "fallback_servers": ["8.8.8.8", "1.1.1.1"],
    "cache_db": "/var/lib/lantern/cache.db",
    "cache_max_entries": 100000
  },
  "blocklists": [
    {"path": "/etc/lantern/blocklists/adblock.txt", "enabled": true}
  ],
  "web": {
    "listen": "0.0.0.0",
    "port": 8080
  },
  "logging": {
    "level": "info",
    "json": false
  }
}
```

## CLI Reference

All commands use Unix socket control plane at `/var/run/lantern.sock` (configurable with `--socket`).

| Command | Arguments | Description |
|---------|-----------|-------------|
| `lantern serve` | `-c FILE` | Start the server with config file |
| `lantern reload` | — | Reload config, blocklists, and upstreams on running server (SIGHUP) |
| `lantern status` | — | Show server uptime, query count, block count, cache size |
| `lantern leases` | — | List all active DHCP leases with expiry times |
| `lantern static add` | `MAC IP [NAME]` | Add a static IP reservation |
| `lantern static remove` | `MAC` | Remove a static IP reservation |
| `lantern import hosts` | `FILE` | Load a `/etc/hosts` file as blocklist entries |
| `lantern import bind` | `FILE` | Load a BIND zone file as static DNS records |

Examples:
```bash
# Start the server
lantern serve -c /etc/lantern/config.json

# Add a static IP for a printer
lantern static add "aa:bb:cc:dd:ee:ff" "192.168.1.50" "printer"

# Load a hosts-format blocklist
lantern import hosts /etc/lantern/hosts-adblock.txt

# Check status
lantern status
```

## Web Dashboard

The web UI (default: `http://localhost:8080`) provides a Pi-hole-inspired interface with:

### Pages

- **Dashboard** — Overview card with query rate, block rate, cache hit %, top clients by query count, latency percentiles
- **Leases** — Table of active DHCP leases, device fingerprint OS/type, IP, hostname, MAC, time remaining
- **DNS Log** — Real-time DNS queries, searchable by domain/client IP, shows response type (cached/blocked/forwarded) and latency
- **Blocklist** — Enable/disable individual blocklists, show load status and entry counts
- **Metrics** — Charts for query rate over time, block rate, cache effectiveness, per-client query volume

All pages update in real-time via Server-Sent Events (no polling).

## REST API

### Status & Metrics

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/` | Main dashboard HTML |
| `GET` | `/api/status` | Server status (uptime, query count, cache size) |
| `GET` | `/api/metrics` | Detailed metrics snapshot (JSON) |

### DHCP Leases

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/leases` | All active leases as JSON array |
| `GET` | `/api/leases/{mac}` | Single lease details |
| `POST` | `/api/static` | Add static reservation (JSON: `{mac, ip, name}`) |
| `DELETE` | `/api/static/{mac}` | Remove static reservation |

### DNS & Events

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/dns/log` | Recent DNS queries (paginated) |
| `GET` | `/api/events/stream` | Server-Sent Events stream (real-time) |

### Control

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/reload` | Reload config and blocklists |
| `POST` | `/api/import/hosts` | Upload and import hosts file |
| `POST` | `/api/import/bind` | Upload and import BIND zone file |

## DNS Name Resolution

Host names are assigned using this priority:

1. **Static config** — Names from `static_hosts` config section
2. **DHCP hostname** — Client-provided hostname (DHCP option 12/81)
3. **Fallback pattern** — `dhcp-{last-octet:03d}.{domain}` (e.g., `dhcp-050.home.lab` for 192.168.1.50)

### Fingerprint TXT Records

If device fingerprinting detects an OS, an additional TXT record is created:

```
dhcp-050.home.lab. 3600 IN TXT "os=Windows 10;type=desktop;confidence=0.92"
```

This allows DNS clients (or other tools) to query device metadata without needing a web request.

## Ad Blocking

### Blocklist Format

Lantern supports hosts-file format blocklists:

```
# Comments and blank lines ignored
0.0.0.0 ads.example.com
127.0.0.1 tracker.com another-tracker.com
ads2.example.org
```

Supports multiple entries per line and domain-only format (no IP required).

### How It Works

- All blocklists are loaded into memory at startup (and on reload)
- Domain names are normalized: lowercased, trailing dot removed
- On a DNS query match, the server returns `0.0.0.0` (or `::` for AAAA)
- Metrics track block rate per blocklist

Popular blocklist sources:
- [Steven Black's hosts](https://github.com/StevenBlack/hosts)
- [Pi-hole default lists](https://github.com/pi-hole/pi-hole/blob/master/adlists/default.list)
- [Energized](https://energized.pro/)

## Device Fingerprinting

Lantern passively identifies clients using TCP/IP stack analysis (inspired by [p0f](https://lcamtuf.coredump.cx/p0f3/)).

### How It Works

- Listens on the wire for TCP SYN packets (requires `CAP_NET_RAW`)
- Extracts signature: IP TTL, TCP window size, MSS, window scale, option order
- Matches against a built-in database (Windows, macOS, Linux, iOS, Android, IoT, etc.)
- Stores OS, version, device type, and confidence score in the lease fingerprint

### No Active Scanning

Fingerprinting is passive only — no port scans, no probes sent. It only analyzes existing traffic.

### Example

A client with a detected fingerprint appears as:

```json
{
  "mac": "aa:bb:cc:dd:ee:ff",
  "ip": "192.168.1.50",
  "hostname": "my-laptop",
  "fingerprint": {
    "os": "Windows 11",
    "os_version": "22H2",
    "device_type": "desktop",
    "confidence": 0.95,
    "first_seen": "2024-02-27T10:15:00Z",
    "last_seen": "2024-02-27T15:30:00Z"
  }
}
```

## Architecture

### Package Layout

```
pkg/
├── config/       Config parsing, validation, custom Duration type
├── model/        Lease, Event, HostFingerprint, LeasePool
├── dhcp/         DHCPv4 server, lease allocation, option handling
├── dns/          DNS resolver, zone management, query handling
├── upstream/     DoH + fallback resolver chain, caching
├── cache/        SQLite-backed DNS response cache with LRU
├── blocker/      Blocklist loading, domain matching, metrics
├── fingerprint/  TCP SYN packet capture, signature DB, matching
├── metrics/      Query/block counters, latency histograms
├── events/       Ring buffer per host, pub/sub, SSE streaming
├── web/          HTTP server, HTML templates, API handlers
├── control/      Unix socket RPC (status, leases, reload)
└── internal/netutil/  Network helper functions

cmd/lantern/     Main entry point, cobra CLI setup
```

### Data Flow

```
[DHCP Request] ──→ DHCP Server ──→ LeasePool ──→ [DNS Zone Update]
                                         ↓
[DNS Query] ──────→ DNS Server ─→ Local Zone? ──→ [Response]
                           ↓
                        Blocked? ──→ [0.0.0.0]
                           ↓
                        [Upstream Resolver]
                           ├─→ Cache (SQLite)
                           ├─→ DoH Resolver
                           └─→ Fallback (plain DNS)

[TCP SYN] ────────→ Fingerprint Engine ──→ [Signature Match] ──→ [Update Lease]

[All Events] ─────→ Ring Buffers (per host) ──→ SSE Stream → [Web UI]
```

### Key Design Decisions

- **Thread safety**: LeasePool protected by `sync.RWMutex`, fine-grained locks on hot paths
- **No CGo**: Pure Go SQLite (`modernc.org/sqlite`), no libpcap binding, easy cross-compilation
- **Context propagation**: All long-running goroutines accept `context.Context` for graceful shutdown
- **Custom Duration JSON**: Config supports `"24h"`, `"30m"` — no numeric-only seconds
- **Ring buffers**: Bounded event storage per host prevents unbounded memory growth
- **IPv6 ready**: Data model uses `net.IP` (not `net.IP4`), AAAA records supported
- **Stateless design**: Can lose state on restart — leases reloaded from disk, blocklists from files

## Deployment

### systemd Service (Linux)

Create `/etc/systemd/system/lantern.service`:

```ini
[Unit]
Description=Lantern Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lantern
Group=lantern
ExecStart=/usr/local/bin/lantern serve -c /etc/lantern/config.json
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=lantern

# Capabilities for DNS (port 53) and raw packet capture (fingerprinting)
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_RAW

# Optional: enable for production
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/lantern /var/run/lantern.sock

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo useradd -r -s /bin/false lantern || true
sudo mkdir -p /etc/lantern /var/lib/lantern
sudo chown lantern:lantern /var/lib/lantern
sudo systemctl daemon-reload
sudo systemctl enable lantern
sudo systemctl start lantern
sudo systemctl status lantern
sudo journalctl -u lantern -f
```

### launchd Service (macOS)

Create `~/Library/LaunchAgents/com.phubbard.lantern.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.phubbard.lantern</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/lantern</string>
        <string>serve</string>
        <string>-c</string>
        <string>/usr/local/etc/lantern/config.json</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/lantern.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/lantern.error.log</string>
</dict>
</plist>
```

Load and start:
```bash
mkdir -p /usr/local/etc/lantern
# Copy config.json to /usr/local/etc/lantern/
launchctl load ~/Library/LaunchAgents/com.phubbard.lantern.plist
launchctl start com.phubbard.lantern
launchctl list | grep lantern
tail -f /var/log/lantern.log
```

### Docker

See "Quick Start > Docker" above. The official image includes Go runtime and libpcap.

```bash
docker build -t phubbard/lantern:latest .
docker run --rm -it --net host -v $(pwd)/config.json:/etc/lantern/config.json \
  phubbard/lantern:latest serve -c /etc/lantern/config.json
```

## Development

### Make Targets

```bash
make deps         # Download and tidy Go modules
make build        # Compile binary to bin/lantern
make build-linux  # Cross-compile for Linux (amd64, arm64)
make build-darwin # Cross-compile for macOS (amd64, arm64)
make test         # Run all tests with -race flag
make lint         # Run golangci-lint
make generate     # Run templ generate (if installed)
make clean        # Remove bin/
make install      # Build and install to /usr/local/bin
```

### Running Tests

```bash
# All tests
make test

# Specific test
go test -run TestLeasePool ./pkg/model/

# With coverage
go test -cover ./...

# Verbose output, no cache
go test -v -count=1 ./...

# Single package
go test ./pkg/config
```

### Code Style

- Structured logging: `slog.Info()`, `slog.Error()` with contextual attributes
- Error wrapping: `fmt.Errorf("operation failed: %w", err)`
- Exported functions have doc comments
- Mutexes embedded in structs, locked at method entry, deferred unlock
- No global state — dependency injection via function parameters

### Project Dependencies

The project intentionally uses a minimal set of dependencies:

| Dependency | Purpose | Notes |
|-----------|---------|-------|
| `miekg/dns` | DNS protocol | Standard library for Go DNS |
| `insomniacslk/dhcp` | DHCPv4 protocol | Well-maintained, no alternatives |
| `modernc.org/sqlite` | Database (cache) | Pure Go, no CGo, easy cross-compile |
| `google/gopacket` | Packet capture (fingerprinting) | De-facto standard for packet handling |
| `spf13/cobra` | CLI framework | Industry standard, minimal overhead |
| `HdrHistogram/hdrhistogram-go` | Latency metrics | Accurate percentile measurement |

Avoid adding dependencies without discussion. Consider:
- Is there a stdlib alternative?
- Does it add CGo dependencies?
- Is it actively maintained?
- What's the impact on binary size / startup time?

### Future Work

- [ ] IPv6 DHCPv6 support (data model ready, server pending)
- [ ] VLAN support (multiple DHCP pools per interface)
- [ ] mDNS integration for `.local` queries
- [ ] Prometheus `/metrics` endpoint (alongside hdrhistogram)
- [ ] Active port scanning (optional, triggered from web UI)
- [ ] Templ migration (from html/template, type-safe templates)
- [ ] DNSSEC support
- [ ] Multi-upstream load balancing

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Make changes with tests
4. Run `make test` and `make lint` to ensure quality
5. Commit with clear messages
6. Open a pull request

Please ensure:
- All tests pass (`make test`)
- Linting passes (`make lint`)
- Code follows conventions (see Development section)
- Commit messages are descriptive

## License

MIT License — see [LICENSE](LICENSE) file for details.

---

**Questions?** Open an issue on GitHub. **Security concerns?** Please email privately to maintainers.
