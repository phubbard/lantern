#!/usr/bin/env bash
# swap-to-lantern.sh — Activate Lantern as the network's DNS server.
#
# What this does:
#   1. Verifies Lantern is installed and config is valid
#   2. Starts the Lantern service
#   3. Runs a health check
#   4. Prints the UDM configuration change needed
#
# What you do manually on the UDM:
#   Settings → Networks → LAN → DHCP → DNS Server 1 → set to this server's IP
#
# Rollback: run swap-to-unifi.sh (or just revert the UDM DNS setting)
set -euo pipefail

CONFIG="/etc/lantern/config.json"

echo "=== Swap to Lantern DNS ==="
echo ""

# Check binary
if ! command -v lantern &>/dev/null; then
    echo "ERROR: lantern binary not found. Run install.sh first."
    exit 1
fi

# Check config
if [ ! -f "$CONFIG" ]; then
    echo "ERROR: Config not found at $CONFIG"
    exit 1
fi

# Validate config loads
if ! lantern serve -c "$CONFIG" --help >/dev/null 2>&1; then
    echo "WARNING: Could not validate config (non-fatal, continuing)"
fi

# Detect our IP on the primary interface
IFACE=$(jq -r '.interface // "eth0"' "$CONFIG" 2>/dev/null || echo "eth0")
OUR_IP=$(ip -4 addr show "$IFACE" 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1)
if [ -z "$OUR_IP" ]; then
    echo "WARNING: Could not detect IP on $IFACE"
    OUR_IP="<this-server-ip>"
fi

# Start Lantern
echo "1. Starting Lantern service..."
systemctl start lantern
sleep 3

# Health check
echo "2. Running health check..."
if curl -sf "http://localhost:8080/health" >/dev/null 2>&1; then
    echo "   ✓ Web UI healthy"
else
    echo "   ⚠ Web UI not responding (may be disabled in config)"
fi

# Test DNS resolution
if command -v dig &>/dev/null; then
    DNS_PORT=$(jq -r '.dns.listen // ":53"' "$CONFIG" 2>/dev/null | sed 's/.*://')
    if dig @127.0.0.1 -p "${DNS_PORT:-53}" example.com +short +time=2 >/dev/null 2>&1; then
        echo "   ✓ DNS resolution working"
    else
        echo "   ✗ DNS resolution FAILED — do NOT proceed with UDM change"
        echo "     Check: journalctl -u lantern -n 50"
        exit 1
    fi
else
    echo "   (install dnsutils for DNS health check)"
fi

echo ""
echo "3. Lantern is running. Now configure your UDM:"
echo ""
echo "   UniFi Controller → Settings → Networks → Default (LAN)"
echo "   → DHCP → DHCP Name Server: Manual"
echo "   → DNS Server 1: $OUR_IP"
echo ""
echo "   Clients will pick up the new DNS server on their next DHCP renewal."
echo "   To force: disconnect/reconnect WiFi, or run 'sudo dhclient -r && sudo dhclient' on Linux."
echo ""
echo "   Monitor: journalctl -u lantern -f"
echo "   Dashboard: http://${OUR_IP}:8080"
echo ""
echo "   To revert: run swap-to-unifi.sh"
