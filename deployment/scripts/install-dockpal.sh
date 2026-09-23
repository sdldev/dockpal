#!/bin/bash
set -euo pipefail

# Dockpal Installer (Debian/Ubuntu)
REPO="sdldev/dockpal"
TARGET="/usr/local/bin/dockpal"
SERVICE_NAME="dockpal"

echo "📦 Installing Dockpal..."

# Check prerequisites
command -v curl >/dev/null || { echo "curl required"; exit 1; }
command -v tar >/dev/null || { echo "tar required"; exit 1; }

# Determine architecture
ARCH=$(uname -m)
if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" ]]; then
  ARCH="arm64"
else
  echo "Unsupported architecture: $ARCH"
  exit 1
fi

# Download latest binary
echo "Downloading latest release for $ARCH..."
URL="https://github.com/$REPO/releases/latest/download/dockpal-$ARCH"
sha_url="${URL}.sha256"

TMP_BIN=$(mktemp)
if ! curl -fsSL "$URL" -o "$TMP_BIN"; then
  echo "Failed to download binary"
  rm -f "$TMP_BIN"
  exit 1
fi

# Verify checksum if available
if command -v sha256sum >/dev/null && curl -fsSL "$sha_url" -o "${TMP_BIN}.sha256" 2>/dev/null; then
  EXPECTED=$(awk '{print $1}' "${TMP_BIN}.sha256")
  ACTUAL=$(sha256sum "$TMP_BIN" | awk '{print $1}')
  if [[ "$EXPECTED" != "$ACTUAL" ]]; then
    echo "Checksum mismatch"
    rm -f "$TMP_BIN" "${TMP_BIN}.sha256"
    exit 1
  fi
  rm -f "${TMP_BIN}.sha256"
fi

# Install binary
mkdir -p "$(dirname "$TARGET")"
mv "$TMP_BIN" "$TARGET"
chmod +x "$TARGET"

# Create system user
if ! id -u dockpal >/dev/null 2>&1; then
  echo "Creating dockpal user..."
  useradd -r -s /bin/false dockpal
fi

# Create data directories
DATA_DIR="/opt/dockpal/data"
mkdir -p "$DATA_DIR"
chown -R dockpal:dockpal "$DATA_DIR"

# Install systemd service
cat > "/etc/systemd/system/${SERVICE_NAME}.service" << EOF
[Unit]
Description=Dockpal - Self-hosted Docker Management Panel
After=network.target docker.service

[Service]
Type=simple
User=dockpal
Group=dockpal
Environment="DOCKPAL_DATA_DIR=$DATA_DIR"
Environment="PORT=3012"
ExecStart=$TARGET server
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

echo ""
echo "✅ Dockpal installed successfully!"
echo ""
echo "Next steps:"
echo "1. Check status: systemctl status dockpal"
echo "2. View logs: journalctl -u dockpal -f"
echo "3. Get admin password: journalctl -u dockpal | grep admin password"
echo ""
