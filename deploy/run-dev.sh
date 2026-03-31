#!/usr/bin/env bash
# run-dev.sh — Run Lantern as a non-root user for development/testing.
#
# Usage: ./run-dev.sh [CONFIG_FILE]
#
# This script:
#   1. Downloads the latest release binary (or uses a local one)
#   2. Grants it network capabilities via setcap (requires one sudo call)
#   3. Creates a local data directory for cache/leases
#   4. Runs lantern as the current user
#
# The NAT setup (nat-enable.sh) still requires root separately.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
CONFIG="${1:-$SCRIPT_DIR/config.testnet.json}"
DATA_DIR="$REPO_DIR/.lantern-data"
BINARY="$REPO_DIR/lantern"

# --- Colors for output -------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[dev]${NC} $*"; }
warn()  { echo -e "${YELLOW}[dev]${NC} $*"; }
error() { echo -e "${RED}[dev]${NC} $*" >&2; }

# --- Find or build the binary ------------------------------------------------
if [ -f "$BINARY" ]; then
    info "Using existing binary: $BINARY"
elif command -v go &>/dev/null; then
    info "Building lantern from source..."
    (cd "$REPO_DIR" && go build -o lantern ./cmd/lantern)
    info "Built: $BINARY"
else
    error "No lantern binary found and Go is not installed."
    echo "  Either:"
    echo "    - Build it: go build -o lantern ./cmd/lantern"
    echo "    - Download a release: gh release download --pattern 'lantern-linux-*'"
    echo "    - Place the binary at: $BINARY"
    exit 1
fi

# --- Create local data directory ---------------------------------------------
mkdir -p "$DATA_DIR"
info "Data directory: $DATA_DIR"

# --- Rewrite config paths to use local data dir ------------------------------
# Create a temporary config with local paths instead of /var/lib/lantern
TEMP_CONFIG="$DATA_DIR/config.json"
sed \
    -e "s|/var/lib/lantern/|${DATA_DIR}/|g" \
    "$CONFIG" > "$TEMP_CONFIG"
info "Config: $CONFIG → $TEMP_CONFIG (paths rewritten to $DATA_DIR/)"

# --- Grant capabilities (one-time sudo) --------------------------------------
# Check if binary already has the right caps
NEEDS_SETCAP=false
if ! getcap "$BINARY" 2>/dev/null | grep -q 'cap_net_bind_service.*cap_net_raw.*cap_net_broadcast'; then
    NEEDS_SETCAP=true
fi

if [ "$NEEDS_SETCAP" = true ]; then
    warn "Granting network capabilities to binary (requires sudo once)..."
    sudo setcap 'cap_net_bind_service,cap_net_raw,cap_net_broadcast=+ep' "$BINARY"
    info "Capabilities set. Future runs won't need sudo unless you rebuild."
else
    info "Binary already has network capabilities."
fi

# --- Check that the USB interface exists and has an IP -----------------------
USB_IFACE=$(grep -o '"interface":\s*"[^"]*"' "$TEMP_CONFIG" | head -1 | cut -d'"' -f4)
if [ -n "$USB_IFACE" ] && [ "$USB_IFACE" != "lo" ]; then
    if ! ip link show "$USB_IFACE" &>/dev/null; then
        error "Interface $USB_IFACE not found. Available interfaces:"
        ip -br link show
        exit 1
    fi
    if ! ip addr show "$USB_IFACE" | grep -q 'inet '; then
        # Extract gateway IP and subnet mask from config to use as interface address
        GW_IP=$(grep -o '"gateway":\s*"[^"]*"' "$TEMP_CONFIG" | head -1 | cut -d'"' -f4)
        SUBNET_MASK=$(grep -o '"subnet":\s*"[^"]*"' "$TEMP_CONFIG" | head -1 | cut -d'"' -f4 | cut -d'/' -f2)
        if [ -n "$GW_IP" ] && [ -n "$SUBNET_MASK" ]; then
            warn "Interface $USB_IFACE has no IP address. Assigning ${GW_IP}/${SUBNET_MASK}..."
            sudo ip addr add "${GW_IP}/${SUBNET_MASK}" dev "$USB_IFACE"
            sudo ip link set "$USB_IFACE" up
            info "Assigned ${GW_IP}/${SUBNET_MASK} to $USB_IFACE"
        else
            warn "Interface $USB_IFACE has no IP address and couldn't extract gateway/subnet from config."
            warn "Run manually: sudo ip addr add 10.99.0.1/24 dev $USB_IFACE && sudo ip link set $USB_IFACE up"
        fi
    fi
fi

# --- Run it ------------------------------------------------------------------
SOCKET_PATH="$DATA_DIR/lantern.sock"
info "Starting lantern..."
info "Control socket: $SOCKET_PATH"
echo ""
exec "$BINARY" serve -c "$TEMP_CONFIG" --socket "$SOCKET_PATH"
