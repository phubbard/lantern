#!/usr/bin/env bash
# install.sh — Install or upgrade Lantern on an Ubuntu/Debian server.
# Usage: ./install.sh [path-to-binary]
#   If no binary path given, downloads the latest release from GitHub.
set -euo pipefail

REPO="phubbard/lantern"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/lantern"
DATA_DIR="/var/lib/lantern"
RUN_DIR="/var/run/lantern"

# Detect architecture
ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m)
case "$ARCH" in
    amd64|x86_64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "==> Lantern installer (arch: $ARCH)"

# Get binary
if [ -n "${1:-}" ]; then
    BINARY="$1"
    echo "==> Using provided binary: $BINARY"
else
    echo "==> Downloading latest release from GitHub..."
    LATEST=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$LATEST" ]; then
        echo "ERROR: Could not determine latest release. Pass binary path as argument."
        exit 1
    fi
    echo "    Latest version: $LATEST"
    BINARY="/tmp/lantern-linux-${ARCH}"
    curl -sL "https://github.com/${REPO}/releases/download/${LATEST}/lantern-linux-${ARCH}" -o "$BINARY"
    chmod +x "$BINARY"
fi

# Verify it runs
if ! "$BINARY" --help >/dev/null 2>&1; then
    echo "ERROR: Binary doesn't execute. Wrong architecture?"
    exit 1
fi

# Create directories
echo "==> Creating directories..."
mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$DATA_DIR/blocklists" "$RUN_DIR"

# Install binary
echo "==> Installing binary to $INSTALL_DIR/lantern"
was_running=false
if systemctl is-active --quiet lantern 2>/dev/null; then
    was_running=true
    echo "    Stopping running instance..."
    systemctl stop lantern
fi

cp "$BINARY" "$INSTALL_DIR/lantern"
chmod 755 "$INSTALL_DIR/lantern"

# Install systemd unit
echo "==> Installing systemd service..."
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
if [ -f "$SCRIPT_DIR/lantern.service" ]; then
    cp "$SCRIPT_DIR/lantern.service" /etc/systemd/system/lantern.service
    systemctl daemon-reload
fi

# Install default config if none exists
if [ ! -f "$CONFIG_DIR/config.json" ]; then
    echo "==> No config found, installing template..."
    if [ -f "$SCRIPT_DIR/config.production.json" ]; then
        cp "$SCRIPT_DIR/config.production.json" "$CONFIG_DIR/config.json"
        echo "    IMPORTANT: Edit $CONFIG_DIR/config.json before starting!"
    else
        echo "    WARNING: No config template found. Create $CONFIG_DIR/config.json manually."
    fi
else
    echo "==> Existing config preserved at $CONFIG_DIR/config.json"
fi

# Enable service
systemctl enable lantern

if $was_running; then
    echo "==> Restarting lantern..."
    systemctl start lantern
    sleep 2
    systemctl status lantern --no-pager
fi

echo "==> Done! Version: $(lantern --help 2>&1 | head -1 || echo 'installed')"
echo ""
echo "Next steps:"
echo "  1. Edit /etc/lantern/config.json"
echo "  2. systemctl start lantern"
echo "  3. systemctl status lantern"
echo "  4. journalctl -u lantern -f"
