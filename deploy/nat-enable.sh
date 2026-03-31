#!/usr/bin/env bash
# nat-enable.sh — Enable NAT routing so clients on the Lantern test network
# can reach the internet through this server's primary interface.
#
# Usage: sudo ./nat-enable.sh [WAN_IFACE] [LAN_IFACE] [LAN_SUBNET]
#
# Defaults auto-detect the primary default-route interface for WAN.
# Uses nftables (nft) which is standard on modern Debian/Ubuntu.

set -euo pipefail

# --- Arguments / defaults ---------------------------------------------------

WAN_IFACE="${1:-$(ip route show default | awk '/default/ {print $5; exit}')}"
LAN_IFACE="${2:-}"
LAN_SUBNET="${3:-10.99.0.0/24}"

if [ -z "$WAN_IFACE" ]; then
    echo "ERROR: Could not detect WAN interface. Pass it as the first argument."
    exit 1
fi

if [ -z "$LAN_IFACE" ]; then
    echo "ERROR: LAN interface required. Pass the USB ethernet interface name."
    echo "       Available interfaces:"
    ip -br link show | grep -v "^lo "
    echo ""
    echo "Usage: sudo $0 [WAN_IFACE] LAN_IFACE [LAN_SUBNET]"
    exit 1
fi

echo "=== Lantern Testnet NAT Setup ==="
echo "  WAN interface:  $WAN_IFACE"
echo "  LAN interface:  $LAN_IFACE"
echo "  LAN subnet:     $LAN_SUBNET"
echo ""

# --- Enable IP forwarding ---------------------------------------------------
echo "[1/4] Enabling IP forwarding..."
sysctl -w net.ipv4.ip_forward=1 > /dev/null
if ! grep -q '^net.ipv4.ip_forward=1' /etc/sysctl.d/99-lantern-nat.conf 2>/dev/null; then
    echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-lantern-nat.conf
    echo "  Written to /etc/sysctl.d/99-lantern-nat.conf"
fi

# --- Configure nftables NAT -------------------------------------------------
NFT_TABLE="lantern-nat"

echo "[2/4] Creating nftables table and NAT masquerade..."
# Remove old table if it exists (idempotent)
nft delete table ip "$NFT_TABLE" 2>/dev/null || true

nft -f - <<NFTEOF
table ip $NFT_TABLE {
    chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        ip saddr $LAN_SUBNET oifname "$WAN_IFACE" masquerade
    }

    chain forward {
        type filter hook forward priority filter; policy accept;
        iifname "$LAN_IFACE" oifname "$WAN_IFACE" ip saddr $LAN_SUBNET accept
        iifname "$WAN_IFACE" oifname "$LAN_IFACE" ct state established,related accept
    }
}
NFTEOF
echo "  Created nft table '$NFT_TABLE'"

# --- Assign IP to LAN interface if not already set --------------------------
echo "[3/4] Checking LAN interface IP..."
LAN_IP="10.99.0.1"
if ! ip addr show "$LAN_IFACE" | grep -q "$LAN_IP"; then
    ip addr add "${LAN_IP}/24" dev "$LAN_IFACE"
    ip link set "$LAN_IFACE" up
    echo "  Assigned $LAN_IP/24 to $LAN_IFACE"
else
    echo "  $LAN_IFACE already has $LAN_IP"
fi

echo "[4/4] Verifying..."
nft list table ip "$NFT_TABLE" | head -3
echo "  ..."

echo ""
echo "=== NAT enabled ==="
echo "Clients on $LAN_SUBNET via $LAN_IFACE can now reach the internet."
echo "To verify: connect a device, get a DHCP lease from Lantern, then ping 8.8.8.8"
echo "To disable: sudo ./nat-disable.sh $WAN_IFACE $LAN_IFACE"
