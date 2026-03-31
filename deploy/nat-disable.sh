#!/usr/bin/env bash
# nat-disable.sh — Remove NAT routing rules set up by nat-enable.sh
#
# Usage: sudo ./nat-disable.sh [WAN_IFACE] [LAN_IFACE] [LAN_SUBNET]

set -euo pipefail

WAN_IFACE="${1:-$(ip route show default | awk '/default/ {print $5; exit}')}"
LAN_IFACE="${2:-enx5c857e306c10}"
LAN_SUBNET="${3:-10.99.0.0/24}"

echo "=== Lantern Testnet NAT Teardown ==="
echo "  WAN interface:  $WAN_IFACE"
echo "  LAN interface:  $LAN_IFACE"
echo "  LAN subnet:     $LAN_SUBNET"
echo ""

echo "[1/3] Removing iptables rules..."
iptables -t nat -D POSTROUTING -s "$LAN_SUBNET" -o "$WAN_IFACE" -j MASQUERADE 2>/dev/null && echo "  Removed MASQUERADE rule" || echo "  MASQUERADE rule not found (already removed)"
iptables -D FORWARD -i "$LAN_IFACE" -o "$WAN_IFACE" -s "$LAN_SUBNET" -j ACCEPT 2>/dev/null && echo "  Removed FORWARD LAN->WAN rule" || echo "  FORWARD LAN->WAN rule not found"
iptables -D FORWARD -i "$WAN_IFACE" -o "$LAN_IFACE" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null && echo "  Removed FORWARD WAN->LAN rule" || echo "  FORWARD WAN->LAN rule not found"

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
echo "Clients on $LAN_SUBNET can no longer route through this server."
