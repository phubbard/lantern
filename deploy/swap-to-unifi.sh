#!/usr/bin/env bash
# swap-to-unifi.sh — Revert from Lantern back to UniFi/Pi-hole DNS.
#
# What this does:
#   1. Stops the Lantern service
#   2. Prints the UDM revert instructions
#
# This is the fast rollback script. Run it if anything goes wrong.
set -euo pipefail

echo "=== Revert to UniFi DNS ==="
echo ""

# Stop Lantern
echo "1. Stopping Lantern service..."
if systemctl is-active --quiet lantern 2>/dev/null; then
    systemctl stop lantern
    echo "   ✓ Lantern stopped"
else
    echo "   (Lantern was not running)"
fi

echo ""
echo "2. Revert your UDM DNS settings:"
echo ""
echo "   UniFi Controller → Settings → Networks → Default (LAN)"
echo "   → DHCP → DHCP Name Server: Auto (or set back to Pi-hole IP)"
echo ""
echo "   Clients will revert on their next DHCP renewal."
echo ""
echo "   Note: Lantern service is stopped but still installed."
echo "   To fully remove: systemctl disable lantern && rm /usr/local/bin/lantern"
