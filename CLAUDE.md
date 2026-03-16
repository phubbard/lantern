# CLAUDE.md - AI Development Guide for Lantern

This guide is for AI coding assistants (Claude, Copilot, etc.) working on the Lantern codebase. It captures conventions, architecture, testing strategies, and gotchas to make contributions efficient and aligned with project goals.

## Project Overview

**Lantern** is a single-binary, self-hosted DNS+DHCP server for home networks.

- **Language**: Go 1.22+
- **Module**: `github.com/phubbard/lantern`
- **Target platforms**: Linux, macOS
- **Design goals**: Lightweight (minimal deps, pure Go), stateless (state reloads from disk), production-ready (graceful shutdown, comprehensive logging)
- **Typical deployment**: Raspberry Pi, NUC, Docker, or small Linux VM as home gateway

## Build & Test Commands

```bash
# Dependencies
make deps           # go mod tidy && go mod download

# Building
make build          # Build bin/lantern for current OS
make build-linux    # Cross-compile amd64 + arm64 for Linux
make build-darwin   # Cross-compile amd64 + arm64 for macOS

# Testing & Quality
make test           # go test -race ./... (all tests with race detector)
make lint           # golangci-lint run ./...
make clean          # rm -rf bin/

# Installation
make install        # Build and install to /usr/local/bin
make generate       # templ generate (optional, if templ installed)
```

### Running Specific Tests

```bash
# Single test
go test -run TestLeasePoolAllocate ./pkg/model/

# All tests in a package, verbose, no cache
go test -v -count=1 ./pkg/dhcp

# With coverage
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Race detector only (no tests)
go test -race ./pkg/model/
```

## Project Structure

```
lantern/
├── cmd/lantern/              Main entry point
│   └── main.go              Cobra CLI setup, server initialization, signal handling
│
├── pkg/                      Core packages (importable)
│   ├── config/              Configuration parsing, validation
│   │   └── config.go        Config struct, JSON unmarshal, custom Duration type
│   │
│   ├── model/               Data structures
│   │   └── model.go         Lease, LeasePool, HostFingerprint, Event types
│   │
│   ├── dhcp/                DHCPv4 server
│   │   └── server.go        DHCP packet handling, lease allocation, option parsing
│   │
│   ├── dns/                 DNS resolver & zone
│   │   ├── server.go        DNS packet handler, query routing
│   │   ├── zone.go          Local zone (A, AAAA, PTR, TXT records)
│   │   └── handler.go       Request/response logic
│   │
│   ├── upstream/            Upstream resolver chain
│   │   └── resolver.go      DoH + fallback (plain DNS) with retry logic
│   │
│   ├── cache/               SQLite DNS cache
│   │   └── cache.go         LRU eviction, WAL mode, prepared statements
│   │
│   ├── blocker/             Ad-blocking engine
│   │   └── blocker.go       Hosts-file parsing, domain matching, metrics
│   │
│   ├── fingerprint/         Device fingerprinting
│   │   └── fingerprint.go   TCP SYN capture, signature DB, OS matching
│   │
│   ├── metrics/             Metrics collection
│   │   └── metrics.go       Query/block counters, latency histograms (hdrhistogram)
│   │
│   ├── events/              Event store & streaming
│   │   ├── events.go        Ring buffer per host, pub/sub
│   │   └── stream.go        SSE formatting, goroutine management
│   │
│   ├── web/                 HTTP server & UI
│   │   ├── server.go        HTTP handlers, route setup
│   │   ├── handler.go       API endpoint logic
│   │   ├── templates/       HTML templates (html/template, planned templ migration)
│   │   └── static/          CSS, JS, assets
│   │
│   ├── control/             Unix socket RPC
│   │   └── control.go       Server/client, handler registration, JSON encoding
│   │
│   └── internal/netutil/    Internal helpers
│       └── netutil.go       IP parsing, validation, reverse DNS utilities
│
├── configs/                 Configuration examples
│   └── example.json         Annotated example config
│
├── Makefile                 Build automation
├── go.mod                   Module definition
├── go.sum                   Dependency checksums
├── README.md                User-facing documentation
├── CLAUDE.md                This file
└── LICENSE                  MIT license
```

## Key Architectural Decisions

### 1. Network Types: Always `net.IP`, Never `net.IP4`

**Decision**: Use `net.IP` for all internal storage, even when IPv4-only today.

**Rationale**:
- Future-proofs for IPv6 DHCPv6 support (already in data model)
- `net.IP` can represent both IPv4 and IPv6
- No performance penalty
- Makes it obvious in code that we're IP-version-agnostic

**Example**:
```go
// Good: works for both IPv4 and IPv6
func (lp *LeasePool) AllocateIP(ctx context.Context) (net.IP, error)

// Bad: assumes IPv4 forever
func (lp *LeasePool) AllocateIP(ctx context.Context) (net.IPv4, error)
```

### 2. LeasePool: Central Mutable State

**Decision**: `LeasePool` is the single source of truth for all active leases and static assignments.

**Structure**:
```go
type LeasePool struct {
    mu         sync.RWMutex        // Protects all below
    leases     map[string]*Lease   // Keyed by MAC address
    static     map[string]*Lease   // Static reservations (also keyed by MAC)
    // ... other fields
}
```

**Thread safety**:
- All public methods lock at entry, defer unlock
- Read-heavy operations use `RLock()`
- Writes use full `Lock()`
- No nested locks (deadlock prevention)

**Persistence**:
- Leases saved to disk (JSON) on shutdown via `SaveToDisk()`
- Reloaded at startup via `LoadFromDisk()`
- No in-memory-only state

### 3. DNS Zone Updated by Callback from DHCP

**Decision**: DHCP server has an `OnLease()` callback that updates the DNS zone.

**Flow**:
```go
// In main.go runServe():
dhcpServer.OnLease(func(lease *model.Lease) {
    dnsZone.UpdateFromLease(lease)  // Updates A, AAAA, PTR, TXT records
    metricsCollector.RecordLeaseGrant()
    eventsStore.Record(...)
})
```

**Why**:
- DNS zone is always in sync with DHCP leases
- No race condition between lease grant and DNS record creation
- Single point of entry for metrics/events recording

### 4. Upstream Resolver Chain: Cache → DoH → Fallback

**Decision**: Three-tier resolution strategy.

**Order**:
1. **SQLite cache**: Check if response is cached and not expired
2. **DoH (DNS over HTTPS)**: Primary upstream, privacy-preserving
3. **Fallback (plain DNS)**: If DoH fails, try plain DNS servers

**Retry logic**:
- Fallback only on timeout/error
- No retry on NXDOMAIN or other DNS responses
- Configurable timeouts per tier

**Why**:
- Cache eliminates repeated upstream queries
- DoH provides privacy (ISP can't see queries)
- Fallback ensures connectivity if DoH endpoint is down

### 5. Events: Ring Buffers with Pub/Sub

**Decision**: Per-host ring buffers of bounded size, with SSE pub/sub.

**Structure**:
```go
type Store struct {
    mu      sync.RWMutex
    buffers map[string]*RingBuffer  // One buffer per MAC address
    subs    []Subscriber             // SSE subscribers
}

type RingBuffer struct {
    mu       sync.RWMutex
    events   []Event
    capacity int
    index    int  // Next write position (circular)
}
```

**Why**:
- Bounded memory: each host gets max N events, old ones are discarded
- Prevents DoS (malicious client generating infinite events)
- Pub/sub avoids goroutine per subscriber (efficient SSE)
- RingBuffer is thread-safe independently

### 6. Config Hot-Reload via SIGHUP (No fsnotify)

**Decision**: Server listens for SIGHUP; user must explicitly reload.

**Why no fsnotify**:
- Adds a dependency
- File watch behavior differs across platforms
- Explicit is better than implicit (Unix philosophy)
- Admin has control: `kill -HUP <pid>` or `lantern reload`

**Reload scope**:
- Reload config file from disk
- Reload all blocklists
- Reload upstream servers
- Update DNS zone (re-apply static hosts)
- Does NOT restart DHCP/DNS listeners (no disruption)

### 7. Pure Go SQLite (modernc.org/sqlite)

**Decision**: Use `modernc.org/sqlite` (pure Go), NOT `mattn/go-sqlite3` (CGo wrapper).

**Why**:
- No CGo → easy cross-compilation (make build-linux works on macOS)
- No libsqlite3-dev dependency on target system
- Binary is self-contained
- Performance is acceptable for home use (<10k cached queries)

**Tradeoff**: Slightly slower than native SQLite, but acceptable for this use case.

### 8. Web Templates: html/template → templ (Planned Migration)

**Current**: Using Go's `html/template` package.

**Planned**: Migrate to [templ](https://templ.guide) for type-safe, compiled templates.

**Until then**:
- Templates in `pkg/web/templates/`
- No template preprocessing or codegen
- Be careful with HTML escaping (use `template.HTML` sparingly)

### 9. Context Propagation for Graceful Shutdown

**Decision**: All long-running operations accept `context.Context`.

**Pattern**:
```go
func (s *Server) Start(ctx context.Context) error {
    go func() {
        for {
            select {
            case <-ctx.Done():
                return  // Graceful exit
            case packet := <-s.packetChan:
                s.handlePacket(packet)
            }
        }
    }()
}

func (s *Server) Stop(ctx context.Context) error {
    s.cancel()  // Signal all goroutines
    // Wait for graceful shutdown or timeout
    select {
    case <-s.done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

**Why**:
- Clean shutdown without goroutine leaks
- Resources released properly (sockets, file handles)
- Testable (can inject test context)

## Coding Conventions

### Structured Logging (log/slog)

**Always use slog, never fmt.Println or log.Printf**:

```go
// Good
logger.Info("lease granted", "mac", lease.MAC, "ip", lease.IP, "ttl_seconds", lease.TTL.Seconds())

// Bad
fmt.Printf("Lease granted: %s -> %s\n", lease.MAC, lease.IP)
log.Printf("Error: %v", err)

// Error logging
logger.Error("failed to open cache", "path", dbPath, "error", err)

// Debug (only if slog.Debug level is enabled)
logger.Debug("packet received", "src_ip", packet.SrcIP, "size_bytes", len(packet))
```

**Using context-attached logger**:
```go
logger := logger.With("component", "dhcp.server", "interface", s.cfg.Interface)
logger.InfoContext(ctx, "listening", "address", s.cfg.ListenAddr)
```

### Error Handling: Wrap with Context

**Always wrap errors with context**:

```go
// Good
if err := parseIP(ip); err != nil {
    return fmt.Errorf("invalid IP in config: %w", err)
}

// Bad (loses context)
return err

// Bad (no err wrapping)
return fmt.Errorf("parse failed")
```

**Pattern**:
```go
if err != nil {
    return fmt.Errorf("operation description: %w", err)
}
```

### Public Functions Have Doc Comments

**Every exported (capitalized) function/type/const must have a comment**:

```go
// NewLeasePool creates a new lease pool for the given subnet.
// The pool manages dynamic and static leases, with expiry-based eviction.
func NewLeasePool(subnet string, ...) (*LeasePool, error) {
    // ...
}

// Lease represents a DHCP lease or static IP assignment.
type Lease struct {
    MAC       net.HardwareAddr
    IP        net.IP
    // ... more fields
}
```

### Mutex Usage: Embed and Lock at Entry

```go
type Blocker struct {
    mu      sync.RWMutex              // Embedded, no pointerization
    domains map[string]bool           // Protected by mu
    lists   []BlocklistInfo           // Protected by mu
}

// Good: lock at entry, defer unlock
func (b *Blocker) IsBlocked(domain string) bool {
    b.mu.RLock()
    defer b.mu.RUnlock()
    return b.domains[strings.ToLower(domain)]
}

// Bad: unlock manually (can forget)
func (b *Blocker) IsBlocked(domain string) bool {
    b.mu.RLock()
    defer b.mu.RUnlock()
    // ... but if you forget the defer, you leak the lock
    return b.domains[domain]
}
```

### Context Propagation

**Accept context as first parameter**:

```go
func (r *Resolver) Resolve(ctx context.Context, name string, qtype uint16) (*dns.Msg, error) {
    // Use ctx.Done() to detect cancellation
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
    }

    // Pass ctx to downstream calls
    return r.queryUpstream(ctx, name, qtype)
}
```

## Dependencies (Do Not Add Without Discussion)

The project uses minimal, well-maintained dependencies:

| Dependency | Version | Purpose | Why This One | Notes |
|-----------|---------|---------|-------------|-------|
| `miekg/dns` | 1.1.58+ | DNS protocol | Standard Go DNS library | Good API, widely used |
| `insomniacslk/dhcp` | 0.0.0-20240419+ | DHCPv4 protocol | Only complete DHCPv4 lib | Good option handling |
| `modernc.org/sqlite` | 1.29.5+ | Database for cache | Pure Go, no CGo | Cross-compile friendly |
| `google/gopacket` | 1.1.19+ | Packet capture | De-facto standard | Used for TCP SYN fingerprinting |
| `spf13/cobra` | 1.8.0+ | CLI framework | Industry standard | Minimal overhead |
| `HdrHistogram/hdrhistogram-go` | 1.1.2+ | Latency percentiles | Accurate measurement | Used for p50/p95/p99 latency |

**Before adding a new dependency, consider**:
1. Is there a stdlib alternative? (e.g., encoding/json, net, crypto/tls)
2. Does it add CGo? (breaks cross-compilation)
3. Is it actively maintained? (check GitHub stars, last commit)
4. What's the binary size impact? (use `go mod why <dep>`)
5. Is it fundamental to the project, or a nice-to-have?

**Examples of rejected dependencies**:
- `fsnotify` — Config reload is explicit (SIGHUP), not automatic
- `logrus` — stdlib `log/slog` is sufficient
- `gorm` — Raw `database/sql` is simpler for this use case

## Common Patterns

### Interface-Based Testing

**Define interfaces for components that need mocking**:

```go
// In pkg/dns/server.go
type Blocker interface {
    IsBlocked(domain string) bool
}

type Resolver interface {
    Resolve(ctx context.Context, name string, qtype uint16) (*dns.Msg, error)
}

// In tests
type MockBlocker struct {
    blockedDomains map[string]bool
}

func (m *MockBlocker) IsBlocked(domain string) bool {
    return m.blockedDomains[domain]
}

// Use in test
server := dns.NewServer(..., mockBlocker, ...)
```

### Thread-Safe Lazy Initialization

```go
type Resolver struct {
    mu         sync.Mutex
    httpClient *http.Client
    once       sync.Once
}

func (r *Resolver) getHTTPClient() *http.Client {
    r.once.Do(func() {
        r.httpClient = &http.Client{
            Timeout: 5 * time.Second,
            // ...
        }
    })
    return r.httpClient
}
```

### Graceful Shutdown with Context

```go
type Server struct {
    ctx    context.Context
    cancel context.CancelFunc
    done   chan struct{}
}

func (s *Server) Start(ctx context.Context) error {
    s.ctx, s.cancel = context.WithCancel(ctx)
    go func() {
        defer close(s.done)
        s.serve()
    }()
}

func (s *Server) Stop(ctx context.Context) error {
    s.cancel()  // Signal goroutine
    select {
    case <-s.done:
        return nil
    case <-ctx.Done():
        return fmt.Errorf("shutdown timeout: %w", ctx.Err())
    }
}
```

### Configuration with Custom JSON Types

```go
// Custom Duration that unmarshals "24h", "30m", etc.
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

// In config file
{
  "dhcp": {
    "default_ttl": "24h",  // Parsed by custom UnmarshalJSON
    "max_ttl": "720h"
  }
}
```

## Things To Watch Out For

### 1. DNS Names Must End with a Dot (FQDN)

**The miekg/dns library requires FQDNs**:

```go
// Good: ends with dot
rr := &dns.A{
    Hdr: dns.RR_Header{
        Name:   "example.com.",  // Must end with dot
        Rrtype: dns.TypeA,
        Class:  dns.ClassINET,
        Ttl:    3600,
    },
    A: net.IPv4(192, 168, 1, 1),
}

// Bad: missing trailing dot
rr := &dns.A{
    Hdr: dns.RR_Header{
        Name:   "example.com",   // ERROR: miekg/dns will reject
    },
    A: net.IPv4(192, 168, 1, 1),
}
```

**Fix in Zone.UpdateFromLease()**:
```go
fqdn := lease.DNSName
if fqdn[len(fqdn)-1] != '.' {
    fqdn = fqdn + "."
}
// Now fqdn is safe to use in miekg/dns
```

### 2. DHCP Requires Raw Socket Access

**DHCP uses UDP 67 (bootps), requires elevated privileges**:

```bash
# Run with CAP_NET_BIND_SERVICE
sudo setcap cap_net_bind_service=ep ./bin/lantern

# Or run as root (not recommended)
sudo ./bin/lantern serve -c config.json
```

**In tests**: Mock the packet connection, don't use real DHCP sockets.

```go
// Good: mock the underlying UDP connection
type MockPacketConn struct {}

// Bad: try to bind UDP 67 in tests (fails without root)
server.Start(ctx)  // ← Will fail on non-root
```

### 3. Blocker Normalizes Domain Names

**Always lowercase and strip trailing dots**:

```go
func (b *Blocker) IsBlocked(domain string) bool {
    b.mu.RLock()
    defer b.mu.RUnlock()

    // Normalize: lowercase + remove trailing dot
    normalized := strings.ToLower(domain)
    if normalized[len(normalized)-1] == '.' {
        normalized = normalized[:len(normalized)-1]
    }

    return b.domains[normalized]
}
```

**Why**: `"example.com"`, `"Example.COM"`, and `"example.com."` should all match the same blocklist entry.

### 4. LeasePool.AllocateIP Scans Linearly

**Current algorithm: iterate from RangeStart to RangeEnd**:

```go
func (lp *LeasePool) AllocateIP() (net.IP, error) {
    for ip := lp.rangeStart; ip.Equal(lp.rangeEnd) || ip.Less(lp.rangeEnd); next(&ip) {
        if !lp.isUsed(ip) {
            return ip, nil
        }
    }
    return nil, fmt.Errorf("no IPs available")
}
```

**Limitation**: O(n) for each allocation. Fine for home networks (<500 hosts), not for large deployments.

**Future improvement**: Bitmap or freelist for O(1) allocation.

### 5. Config Duration Type Uses Custom JSON Marshal

**Config durations must use the Duration type, not time.Duration**:

```go
// Good: uses custom Duration type
type DHCPConfig struct {
    DefaultTTL Duration `json:"default_ttl"`  // Custom type
}

// Bad: uses time.Duration (would not unmarshal "24h" format)
type DHCPConfig struct {
    DefaultTTL time.Duration `json:"default_ttl"`
}
```

**Config file**:
```json
{
  "dhcp": {
    "default_ttl": "24h",      // Parsed by Duration.UnmarshalJSON
    "max_ttl": "720h"
  }
}
```

### 6. gopacket/pcap Requires libpcap-dev

**Fingerprinting uses gopacket for raw packet capture**:

```bash
# Build requires libpcap-dev
sudo apt-get install libpcap-dev  # Debian/Ubuntu
brew install libpcap               # macOS
```

**Without it**: Compilation fails.

**In Docker**: Include libpcap-dev in build stage.

### 7. TCP SYN Fingerprinting is Passive Only

**The fingerprint package listens on the wire, does NOT send probes**:

```go
// Good: listen for incoming SYN packets
pcapHandle.SetBPFFilter("tcp[tcpflags] & tcp-syn != 0")

// Bad: send probes to detect OS (NOT in scope)
net.Dial("tcp", "192.168.1.50:80")  // No, this is active scanning
```

**Privacy**: Fingerprinting is completely passive. No packets are sent to clients.

## Testing Strategy

Lantern uses three tiers of tests: unit, protocol, and integration.

### Tier 1: Unit Tests

**White-box testing of individual functions/packages**.

Location: `*_test.go` in the same package.

Examples:
- `pkg/model/model_test.go` — Lease struct, LeasePool methods
- `pkg/blocker/blocker_test.go` — Domain matching, list loading
- `pkg/cache/cache_test.go` — SQLite operations, eviction

**Run**:
```bash
go test ./pkg/model
go test -v ./pkg/blocker
go test -race ./pkg/cache
```

### Tier 2: Protocol Tests

**Test protocol-level behavior (DNS, DHCP) without full server**.

Examples:
- `pkg/dns/handler_test.go` — Query parsing, response generation
- `pkg/dhcp/handler_test.go` — DHCP option parsing, reply logic

**Pattern**:
```go
func TestDHCPDiscover(t *testing.T) {
    pool := model.NewLeasePool(...)
    handler := dhcp.NewHandler(pool, ...)

    discover := dhcpv4.NewDiscovery(...)
    reply, err := handler.ServeDHCP(discover)

    assert.NoError(t, err)
    assert.NotNil(t, reply.YourClientAddr)
}
```

### Tier 3: Integration Tests

**Full server tests with mocked I/O (sockets, files)**.

Examples:
- `cmd/lantern/integration_test.go` — End-to-end server startup/shutdown
- `pkg/web/server_test.go` — HTTP API endpoints

**Pattern**:
```go
func TestServerStartup(t *testing.T) {
    cfg := &config.Config{
        DHCP: config.DHCPConfig{Subnet: "192.168.1.0/24"},
        DNS:  config.DNSConfig{Listen: "127.0.0.1"},
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    server := New(cfg)
    go server.Start(ctx)
    defer server.Stop(ctx)

    // Assert server is listening
}
```

### Test Categories

Run by category:

```bash
# All unit tests
go test ./pkg/...

# All protocol tests
go test ./pkg/dns/...
go test ./pkg/dhcp/...

# Integration tests
go test ./cmd/lantern/...

# With coverage
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# With race detector
go test -race ./...
```

## Future Work / TODOs

### Short-term (next release)

- [ ] Prometheus `/metrics` endpoint (in addition to hdrhistogram)
- [ ] Migrate templates from `html/template` to [templ](https://templ.guide) (type-safe, compiled)
- [ ] Add more TCP SYN fingerprints (iOS 17, Android 14, etc.)
- [ ] Improve error messages in web UI (show reason for block/cache miss)

### Medium-term

- [ ] **IPv6 DHCPv6 support** — Data model is ready, server pending
- [ ] **VLAN support** — Multiple DHCP pools per interface (tagged VLANs)
- [ ] **mDNS integration** — Resolve `.local` queries (Bonjour/Avahi)
- [ ] **Active port scanning** — Optional, triggered from web UI (scan a host's open ports)

### Long-term

- [ ] **DNSSEC support** — Validate DNSSEC responses from upstream
- [ ] **Multi-upstream load balancing** — Round-robin or least-latency selection
- [ ] **Custom DNS extensions** — Lua scripting for complex rules
- [ ] **Backup/restore** — Export and import full config + leases + blocklists
- [ ] **HA mode** — Two instances with shared state (etcd/Raft)

## Glossary

| Term | Definition |
|------|-----------|
| **FQDN** | Fully Qualified Domain Name (ends with dot, e.g., `example.com.`) |
| **A record** | IPv4 address record in DNS |
| **AAAA record** | IPv6 address record in DNS |
| **PTR record** | Reverse DNS record (IP → hostname) |
| **TXT record** | Text record (used for fingerprint metadata) |
| **DoH** | DNS over HTTPS (privacy-preserving DNS) |
| **DHCP** | Dynamic Host Configuration Protocol (assigns IPs) |
| **TTL** | Time To Live (cache expiry, lease duration) |
| **SIGHUP** | Signal 1 (used for graceful reload) |
| **SIGTERM** | Signal 15 (used for graceful shutdown) |
| **CAP_NET_BIND_SERVICE** | Linux capability to bind ports <1024 |
| **CAP_NET_RAW** | Linux capability for raw socket access (packet capture) |
| **RingBuffer** | Circular buffer (oldest events overwritten when full) |
| **Pub/Sub** | Publish/Subscribe pattern (SSE subscribers receive all events) |

---

**Last updated**: February 2026. Use this guide as your reference when contributing to Lantern.
