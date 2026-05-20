#!/usr/bin/env bash
#
# Dockpal Installer (Debian/Ubuntu, linux/amd64)
#
# Performs a clean install of Dockpal:
#   - Installs required dependencies (curl, lsof, jq, tar)
#   - Installs Docker Engine if missing
#   - Downloads the latest dockpal-linux-amd64 binary
#   - Provisions /opt/dockpal data layout and templates
#   - Installs and starts the dockpal.service systemd unit
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh | sudo bash
#
# Optional environment variables:
#   DOCKPAL_VERSION   Release tag to install (default: latest)
#
# Exit codes:
#   0 - Success
#   1 - Generic failure (root check, dependency install, etc.)
#   6 - Binary download failed after 3 retries
#   7 - Architecture not supported (only x86_64/amd64 is supported)
#   8 - Not running as root
#

set -euo pipefail

REPO="sdldev/dockpal"
VERSION="${DOCKPAL_VERSION:-latest}"
INSTALL_DIR="/usr/local/bin"
BINARY="$INSTALL_DIR/dockpal"
DATA_DIR="/opt/dockpal"
TEMPLATES_DIR="$DATA_DIR/templates"
SYSTEMD_UNIT_PATH="/etc/systemd/system/dockpal.service"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }

require_amd64() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)
            log_info "Detected architecture: amd64"
            ;;
        *)
            log_error "Unsupported architecture: $arch (Dockpal only supports linux/amd64)"
            exit 7
            ;;
    esac
}

require_debian_family() {
    if ! command -v apt-get &>/dev/null; then
        log_error "Unsupported distribution: this installer requires apt-get (Debian/Ubuntu)"
        exit 1
    fi
}

install_dependencies() {
    log_info "Checking dependencies..."
    local deps=("curl" "lsof" "jq" "tar")
    local to_install=()

    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &>/dev/null; then
            to_install+=("$dep")
        fi
    done

    if [ ${#to_install[@]} -gt 0 ]; then
        log_info "Installing: ${to_install[*]}"
        DEBIAN_FRONTEND=noninteractive apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${to_install[@]}"
    fi
}

install_docker() {
    if command -v docker &>/dev/null; then
        log_info "Docker already installed: $(docker --version)"
        if ! systemctl is-active --quiet docker 2>/dev/null; then
            log_info "Starting Docker daemon..."
            systemctl enable docker || true
            systemctl start docker || true
        fi
        return
    fi
    log_info "Installing Docker via get.docker.com..."
    local tmp_script
    tmp_script=$(mktemp)
    if ! curl -fsSL --max-time 60 https://get.docker.com -o "$tmp_script"; then
        rm -f "$tmp_script"
        log_error "Failed to download Docker install script. Check network connectivity."
        exit 1
    fi
    sh "$tmp_script"
    rm -f "$tmp_script"
    systemctl enable docker
    systemctl start docker
    log_info "Docker installed successfully"
}

download_binary() {
    local url
    local max_retries=3
    local timeout_seconds=120

    if [ "$VERSION" = "latest" ]; then
        url="https://github.com/$REPO/releases/latest/download/dockpal-linux-amd64"
    else
        url="https://github.com/$REPO/releases/download/$VERSION/dockpal-linux-amd64"
    fi

    log_info "Downloading Dockpal (URL: $url)..."

    mkdir -p "$INSTALL_DIR"
    local tmp_binary="${BINARY}.download"
    rm -f "$tmp_binary"

    local attempt=1
    while [ $attempt -le $max_retries ]; do
        log_info "Download attempt $attempt of $max_retries..."

        if curl -fSL --max-time "$timeout_seconds" --connect-timeout 15 --retry 1 \
                "$url" -o "$tmp_binary"; then
            install_downloaded_binary "$tmp_binary"
            return 0
        fi
        log_warn "curl failed (attempt $attempt)"

        if command -v wget >/dev/null 2>&1; then
            log_info "Trying wget as fallback..."
            if wget -q --timeout="$timeout_seconds" -O "$tmp_binary" "$url"; then
                install_downloaded_binary "$tmp_binary"
                return 0
            fi
            log_warn "wget also failed"
        fi

        rm -f "$tmp_binary" 2>/dev/null || true
        attempt=$((attempt + 1))
    done

    rm -f "$tmp_binary" 2>/dev/null || true
    log_error "Failed to download binary after $max_retries attempts"
    log_error "Check network connectivity and that $url is reachable"
    log_error "Manual test: curl -fSL '$url' -o /tmp/dockpal-test"
    exit 6
}

install_downloaded_binary() {
    local tmp_binary="$1"
    chmod +x "$tmp_binary"
    mv -f "$tmp_binary" "$BINARY"
    log_info "Binary installed to $BINARY"
}

# Download templates archive from GitHub source. Templates are required for the
# /templates page; the binary falls back to /opt/dockpal/templates when the
# CWD-local templates directory is not present (which is the case under systemd).
install_templates() {
    if [[ -d "$TEMPLATES_DIR" ]] && [[ -n "$(ls -A "$TEMPLATES_DIR" 2>/dev/null || true)" ]]; then
        log_info "Templates already present at $TEMPLATES_DIR, skipping"
        return 0
    fi

    log_info "Downloading templates..."
    local ref
    if [ "$VERSION" = "latest" ]; then
        ref="main"
    else
        ref="$VERSION"
    fi
    local archive_url="https://github.com/$REPO/archive/refs/heads/$ref.tar.gz"
    local tag_url="https://github.com/$REPO/archive/refs/tags/$ref.tar.gz"

    local tmp_tar
    tmp_tar=$(mktemp --suffix=.tar.gz)
    local tmp_extract
    tmp_extract=$(mktemp -d)

    if ! curl -fsSL --max-time 60 --connect-timeout 15 "$archive_url" -o "$tmp_tar" 2>/dev/null; then
        if ! curl -fsSL --max-time 60 --connect-timeout 15 "$tag_url" -o "$tmp_tar" 2>/dev/null; then
            rm -f "$tmp_tar"
            rm -rf "$tmp_extract"
            log_warn "Failed to download templates archive (the templates page will be unavailable)"
            return 0
        fi
    fi

    if ! tar -xzf "$tmp_tar" -C "$tmp_extract"; then
        rm -f "$tmp_tar"
        rm -rf "$tmp_extract"
        log_warn "Failed to extract templates archive"
        return 0
    fi

    local extracted_templates
    extracted_templates=$(find "$tmp_extract" -maxdepth 3 -type d -name templates | head -n 1 || true)

    if [[ -z "$extracted_templates" ]]; then
        rm -f "$tmp_tar"
        rm -rf "$tmp_extract"
        log_warn "templates/ not found in archive"
        return 0
    fi

    mkdir -p "$TEMPLATES_DIR"
    cp -a "$extracted_templates/." "$TEMPLATES_DIR/"
    chmod 755 "$TEMPLATES_DIR"
    find "$TEMPLATES_DIR" -type f -exec chmod 644 {} +

    rm -f "$tmp_tar"
    rm -rf "$tmp_extract"
    log_info "Templates installed to $TEMPLATES_DIR"
}

setup_directories() {
    log_info "Setting up directories and user/group..."

    # Create dockpal group and user if they do not exist
    if ! getent group dockpal >/dev/null; then
        groupadd -r dockpal
    fi
    if ! getent passwd dockpal >/dev/null; then
        useradd -r -g dockpal -d "$DATA_DIR" -s /sbin/nologin -c "Dockpal system user" dockpal
    fi

    # Ensure dockpal is in docker group to access docker.sock
    usermod -aG docker dockpal || true

    mkdir -p "$DATA_DIR"
    mkdir -p "$DATA_DIR/data"
    mkdir -p "$DATA_DIR/logs"
    mkdir -p "$DATA_DIR/repos"
    mkdir -p "$DATA_DIR/compose"
    mkdir -p "$DATA_DIR/traefik"
    mkdir -p "$TEMPLATES_DIR"

    chown -R dockpal:dockpal "$DATA_DIR"
    chmod 750 "$DATA_DIR"
    chmod 750 "$DATA_DIR/data"
    chmod 750 "$DATA_DIR/logs"
    chmod 750 "$DATA_DIR/repos"
    chmod 750 "$DATA_DIR/compose"
    chmod 750 "$DATA_DIR/traefik"
    chmod 750 "$TEMPLATES_DIR"

    for dir in "$DATA_DIR" "$DATA_DIR/data" "$DATA_DIR/logs" "$DATA_DIR/repos" \
               "$DATA_DIR/compose" "$DATA_DIR/traefik" "$TEMPLATES_DIR"; do
        if [ ! -d "$dir" ]; then
            log_error "Failed to create directory: $dir"
            exit 1
        fi
        if [ ! -w "$dir" ] && [ "$(id -u)" -eq 0 ]; then
            # Since chowned to dockpal, root still has access, but let chown finish first
            true
        fi
    done

    log_info "Directories created and permissions configured successfully"
}

setup_systemd() {
    log_info "Setting up systemd service..."
    cat > "$SYSTEMD_UNIT_PATH" << 'SERVICEEOF'
[Unit]
Description=Dockpal — Docker Management Platform
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=dockpal
Group=docker
WorkingDirectory=/opt/dockpal
ExecStart=/usr/local/bin/dockpal server
Restart=always
RestartSec=5
Environment="PORT=3012"
# Hardening Directives
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/dockpal
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
SERVICEEOF

    chmod 644 "$SYSTEMD_UNIT_PATH"
    systemctl daemon-reload
    systemctl enable dockpal >/dev/null 2>&1 || true
    systemctl restart dockpal
    log_info "Dockpal service started"
}

verify_installation() {
    local max_wait=30
    local count=0

    log_info "Verifying installation..."

    while [ $count -lt $max_wait ]; do
        if systemctl is-active --quiet dockpal 2>/dev/null; then
            # Service active; also check the port is actually bound
            if ss -tlnp 2>/dev/null | grep -q ":3012 "; then
                log_info "Dockpal service is running and listening on :3012"
                return 0
            fi
        fi
        sleep 1
        count=$((count + 1))
    done

    if ! systemctl is-active --quiet dockpal 2>/dev/null; then
        log_error "Dockpal service failed to start within $max_wait seconds"
    else
        log_error "Dockpal service is active but not listening on port 3012 within $max_wait seconds"
    fi
    log_error "Check logs with: journalctl -u dockpal -n 100 --no-pager"
    return 1
}

open_firewall() {
    # Open port 3012 in any active host firewall so the UI is reachable from outside.
    # Failures are non-fatal: the service is already running, the user can open it manually.
    if command -v ufw >/dev/null 2>&1; then
        if ufw status 2>/dev/null | grep -qi "Status: active"; then
            log_info "UFW is active, allowing port 3012/tcp..."
            ufw allow 3012/tcp >/dev/null 2>&1 || log_warn "Failed to add UFW rule (run manually: ufw allow 3012/tcp)"
        fi
    fi

    if command -v firewall-cmd >/dev/null 2>&1; then
        if firewall-cmd --state >/dev/null 2>&1; then
            log_info "firewalld is active, allowing port 3012/tcp..."
            firewall-cmd --permanent --add-port=3012/tcp >/dev/null 2>&1 || true
            firewall-cmd --reload >/dev/null 2>&1 || true
        fi
    fi
}

primary_ip() {
    local ip
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    if [[ -z "$ip" ]]; then
        ip=$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n 1)
    fi
    if [[ -z "$ip" ]]; then
        ip="<server-ip>"
    fi
    echo "$ip"
}

main() {
    if [ "$(id -u)" -ne 0 ]; then
        log_error "This script must be run as root"
        exit 8
    fi

    echo ""
    echo "  🐳  Dockpal Installer"
    echo "  ===================="
    echo ""

    require_amd64
    require_debian_family

    install_dependencies
    install_docker

    setup_directories
    install_templates
    download_binary
    setup_systemd

    verify_installation
    open_firewall

    local ip
    ip=$(primary_ip)

    echo ""
    log_info "Installation complete!"
    log_info "Access Dockpal at: http://${ip}:3012"
    log_info "Default credentials: admin / admin"
    echo ""
}

main "$@"
