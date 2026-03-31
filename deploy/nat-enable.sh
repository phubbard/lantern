#!/usr/bin/env bash
# nat-enable.sh — Enable NAT routing so clients on the Lantern test network
# can reach the internet through this server's primary interface.
#
# Usage: sudo ./nat-enable.sh [WAN_IFACE] [LAN_IFACE] [LAN_SUBNET]
#
# Defaults auto-detect the primary default-route interface for WAN.
# LAN_IFACE and LAN_SUBNET default to the testnet config values.

set -euo pipefail

# --- Arguments / defaults ---------------------------------------------------

# WAN: interface with the default route (typically eth0, ens18, etc.)
WAN_IFACE="${1:-$(ip route show default | awk '/default/ {print $5; exit}')}"

# LAN: the USB ethernet interface running Lantern's testnet
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
# Make persistent across reboots
if ! grep -q '^net.ipv4.ip_forward=1' /etc/sysctl.d/99-lantern-nat.conf 2>/dev/null; then
    echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-lantern-nat.conf
    echo "  Written to /etc/sysctl.d/99-lantern-nat.conf"
fi

# --- Configure iptables NAT -------------------------------------------------
echo "[2/4] Adding iptables MASQUERADE rule..."
# Remove existing rule first (idempotent)
iptables -t nat -D POSTROUTING -s "$LAN_SUBNET" -o "$WAN_IFACE" -j MASQUERADE 2>/dev/null || true
iptables -t nat -A POSTROUTING -s "$LAN_SUBNET" -o "$WAN_IFACE" -j MASQUERADE

echo "[3/4] Adding iptables FORWARD rules..."
# Allow forwarding from LAN to WAN
iptables -D FORWARD -i "$LAN_IFACE" -o "$WAN_IFACE" -s "$LAN_SUBNET" -j ACCEPT 2>/dev/null || true
iptables -A FORWARD -i "$LAN_IFACE" -o "$WAN_IFACE" -s "$LAN_SUBNET" -j ACCEPT

# Allow established/related return traffic
iptables -D FORWARD -i "$WAN_IFACE" -o "$LAN_IFACE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
iptables -A FORWARD -i "$WAN_IFACE" -o "$LAN_IFACE" -m state --state RELATED,ESTABLISHED -j ACCEPT

# --- Assign IP to LAN interface if not already set --------------------------
echo "[4/4] Checking LAN interface IP..."
LAN_IP="10.99.0.1"
if ! ip addr show "$LAN_IFACE" | grep -q "$LAN_IP"; then
    ip addr add "${LAN_IP}/24" dev "$LAN_IFACE"
    ip link set "$LAN_IFACE" up
    echo "  Assigned $LAN_IP/24 to $LAN_IFACE"
else
    echo "  $LAN_IFACE already has $LAN_IP"
fi

echo ""
echo "=== NAT enabled ==="
echo "Clients on $LAN_SUBNET via $LAN_IFACE can now reach the internet."
echo "To verify: connect a device, get a DHCP lease from Lantern, then ping 8.8.8.8"
echo "To disable: sudo ./nat-disable.sh $WAN_IFACE $LAN_IFACE $LAN_SUBNET"
