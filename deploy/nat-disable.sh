#!/usr/bin/env bash
# nat-disable.sh — Remove NAT routing rules set up by nat-enable.sh
#
# Usage: sudo ./nat-disable.sh [WAN_IFACE] [LAN_IFACE]

set -euo pipefail

LAN_IFACE="${2:-enx5c857e306c10}"
NFT_TABLE="lantern-nat"

echo "=== Lantern Testnet NAT Teardown ==="

echo "[1/3] Removing nftables table '$NFT_TABLE'..."
if nft list table ip "$NFT_TABLE" &>/dev/null; then
    nft delete table ip "$NFT_TABLE"
    echo "  Removed."
else
    echo "  Table not found (already removed)."
fi

echo "[2/3] Disabling IP forwarding..."
sysctl -w net.ipv4.ip_forward=0 > /dev/null
rm -f /etc/sysctl.d/99-lantern-nat.conf
echo "  Removed /etc/sysctl.d/99-lantern-nat.conf"

echo "[3/3] Removing IP from LAN interface..."
LAN_IP="10.99.0.1"
if ip addr show "$LAN_IFACE" 2>/dev/null | grep -q "$LAN_IP"; then
    ip addr del "${LAN_IP}/24" dev "$LAN_IFACE"
    echo "  Removed $LAN_IP/24 from $LAN_IFACE"
else
    echo "  $LAN_IFACE doesn't have $LAN_IP (already removed or never set)"
fi

echo ""
echo "=== NAT disabled ==="
