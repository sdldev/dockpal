#!/usr/bin/env bash
#
# Dockpal Installer
#
# Supported install paths:
#   - Fresh install: No existing dockara or dockpal installation
#   - Upgrade in-place: Existing dockpal installation
#   - Upgrade from Dockara: Existing dockara installation (migrates data)
#
# Exit codes:
#   0 - Success
#   1 - Generic failure (root check, arch check, dependency install, etc.)
#   3 - Stop dockara service failed (Req 9.2)
#   4 - Cloudflared migration failed (Req 5.8)
#   5 - Conflict at /opt/dockpal/ (Req 9.9)
#   6 - Binary download failed after 3 retries (Req 8.9)
#   7 - Architecture not supported (Req 8.10)
#   8 - Not running as root (Req 8.11)
#

set -e

# LEGACY-DOCKARA: Configuration for upgrade path detection
REPO="sdldev/dockpal"
VERSION="${DOCKPAL_VERSION:-latest}"
INSTALL_DIR="/usr/local/bin"
BINARY="$INSTALL_DIR/dockpal"
DATA_DIR="/opt/dockpal"

# LEGACY-DOCKARA: Old paths for migration detection
OLD_DATA_DIR="/opt/dockara"
OLD_BINARY="/usr/local/bin/dockara"
OLD_UNIT="dockara.service"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

detect_arch() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64)  echo "amd64" ;;
        aarch64) echo "arm64" ;;
        armv7l)  echo "armv7" ;;
        *)
            log_error "Unsupported architecture: $arch"
            exit 7
            ;;
    esac
}

install_dependencies() {
    log_info "Checking dependencies..."
    # LEGACY-DOCKARA: jq needed for cloudflared token extraction
    local deps=("curl" "lsof" "jq")
    local to_install=()

    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &>/dev/null; then
            to_install+=("$dep")
        fi
    done

    if [ ${#to_install[@]} -gt 0 ]; then
        log_info "Installing: ${to_install[*]}"
        if command -v apt-get &>/dev/null; then
            apt-get update -qq && apt-get install -y -qq "${to_install[@]}"
        elif command -v yum &>/dev/null; then
            yum install -y "${to_install[@]}"
        else
            log_error "Cannot install dependencies. Please install: ${to_install[*]}"
            exit 1
        fi
    fi
}

install_docker() {
    if command -v docker &>/dev/null; then
        log_info "Docker already installed: $(docker --version)"
        return
    fi
    log_info "Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable docker
    systemctl start docker
    log_info "Docker installed successfully"
}

# LEGACY-DOCKARA: Check if dockara is installed (for upgrade path)
is_dockara_install() {
    [[ -d "$OLD_DATA_DIR" ]] || \
    systemctl list-unit-files "$OLD_UNIT" &>/dev/null || \
    [[ -x "$OLD_BINARY" ]]
}

# LEGACY-DOCKARA: Check if dockpal is already installed (for in-place upgrade)
is_dockpal_install() {
    [[ -d "$DATA_DIR" ]] || \
    systemctl list-unit-files dockpal.service &>/dev/null || \
    [[ -x "$BINARY" ]]
}

# LEGACY-DOCKARA: Migrate cloudflared container from dockara-cloudflared to dockpal-cloudflared
migrate_cloudflared() {
    # LEGACY-DOCKARA: Check if old container exists
    if ! docker ps -a --format '{{.Names}}' | grep -q "^dockara-cloudflared$"; then
        log_info "No existing dockara-cloudflared container found, skipping migration"
        return 0
    fi

    log_info "Migrating cloudflared container..."

    # Extract token from existing container
    local token
    token=$(docker inspect dockara-cloudflared --format '{{json .Args}}' 2>/dev/null | \
        jq -r '.[]' 2>/dev/null | grep -oP '(?<=--token\s)\S+' || true)

    if [[ -z "$token" ]]; then
        log_error "Failed to extract cloudflared token from dockara-cloudflared"
        exit 4
    fi

    # Stop and remove old container
    log_info "Stopping dockara-cloudflared container..."
    if ! timeout 30 docker stop dockara-cloudflared &>/dev/null; then
        log_error "Failed to stop dockara-cloudflared container"
        exit 4
    fi

    log_info "Removing dockara-cloudflared container..."
    if ! docker rm dockara-cloudflared &>/dev/null; then
        log_error "Failed to remove dockara-cloudflared container"
        exit 4
    fi

    # Create new container with dockpal name and labels
    log_info "Creating dockpal-cloudflared container..."
    if ! docker run -d \
        --name dockpal-cloudflared \
        --restart unless-stopped \
        --label dockpal.managed=true \
        --label dockpal.tunnel=true \
        cloudflare/cloudflared:latest \
        tunnel --no-autoupdate run --token "$token" 2>/dev/null; then
        log_error "Failed to create dockpal-cloudflared container"
        exit 4
    fi

    log_info "Cloudflared container migrated successfully"
}

# LEGACY-DOCKARA: Detect conflicts when merging /opt/dockara to /opt/dockpal
detect_conflicts() {
    if [[ ! -d "$OLD_DATA_DIR" ]]; then
        return 0
    fi

    if [[ ! -d "$DATA_DIR" ]]; then
        return 0
    fi

    log_info "Checking for conflicts..."

    # Check for type mismatches and content conflicts
    while IFS= read -r -d '' file; do
        local relative_path="${file#$OLD_DATA_DIR/}"
        local target_path="$DATA_DIR/$relative_path"

        if [[ -e "$target_path" ]]; then
            # Type mismatch check
            if [[ -f "$file" && -d "$target_path" ]]; then
                log_error "Conflict: $file is a file but $target_path is a directory"
                return 1
            fi
            if [[ -d "$file" && -f "$target_path" ]]; then
                log_error "Conflict: $file is a directory but $target_path is a file"
                return 1
            fi

            # Content mismatch check for regular files
            if [[ -f "$file" && -f "$target_path" ]]; then
                if ! cmp -s "$file" "$target_path"; then
                    log_error "Conflict: $file and $target_path have different content"
                    return 1
                fi
            fi
        fi
    done < <(find "$OLD_DATA_DIR" -type f -print0 2>/dev/null)

    return 0
}

# LEGACY-DOCKARA: Migrate data from dockara to dockpal
migrate_data() {
    if [[ ! -d "$OLD_DATA_DIR" ]]; then
        log_info "No existing $OLD_DATA_DIR to migrate"
        return 0
    fi

    log_info "Migrating data from $OLD_DATA_DIR to $DATA_DIR..."

    # Check for conflicts first
    if ! detect_conflicts; then
        log_error "Conflict detected during data migration"
        exit 5
    fi

    # Create new data directory structure if it doesn't exist
    mkdir -p "$DATA_DIR"

    if [[ -d "$DATA_DIR" && $(ls -A "$DATA_DIR" 2>/dev/null) ]]; then
        # Merge mode: use rsync to copy only new files
        log_info "Merging data (rsync)..."
        rsync -a --ignore-existing "$OLD_DATA_DIR/" "$DATA_DIR/" 2>/dev/null || true
    else
        # Fresh move: atomic rename
        log_info "Moving data directory..."
        mv "$OLD_DATA_DIR" "$DATA_DIR"
    fi

    # Rename database file if it exists
    if [[ -f "$DATA_DIR/data/dockara.db" ]]; then
        log_info "Renaming database file..."
        mv "$DATA_DIR/data/dockara.db" "$DATA_DIR/data/dockpal.db"
    fi

    # Rename log file if it exists
    if [[ -f "$DATA_DIR/data/dockara.log" ]]; then
        log_info "Renaming log file..."
        mv "$DATA_DIR/data/dockara.log" "$DATA_DIR/data/dockpal.log"
    fi

    # Rename rotated log files if they exist
    if ls "$DATA_DIR/data/dockara.log."* 1>/dev/null 2>&1; then
        log_info "Renaming rotated log files..."
        for logfile in "$DATA_DIR/data/dockara.log."*; do
            if [[ -f "$logfile" ]]; then
                mv "$logfile" "${logfile/dockara.log/dockpal.log}"
            fi
        done
    fi

    # Remove old binary if it exists
    # LEGACY-DOCKARA: Remove old dockara binary
    if [[ -x "$OLD_BINARY" ]]; then
        log_info "Removing old dockara binary..."
        rm -f "$OLD_BINARY"
    fi

    # Disable and remove old unit file
    # LEGACY-DOCKARA: Clean up old systemd unit
    if systemctl list-unit-files "$OLD_UNIT" &>/dev/null; then
        log_info "Disabling old dockara service..."
        systemctl disable "$OLD_UNIT" 2>/dev/null || true

        log_info "Removing old systemd unit..."
        rm -f "/etc/systemd/system/$OLD_UNIT"

        log_info "Reloading systemd daemon..."
        systemctl daemon-reload
    fi

    log_info "Data migration completed"
}

download_binary() {
    local arch="$1"
    local url
    local max_retries=3
    local timeout_seconds=60

    if [ "$VERSION" = "latest" ]; then
        url="https://github.com/$REPO/releases/latest/download/dockpal-linux-$arch"
    else
        url="https://github.com/$REPO/releases/download/$VERSION/dockpal-linux-$arch"
    fi

    log_info "Downloading Dockpal for $arch (URL: $url)..."

    local attempt=1
    while [ $attempt -le $max_retries ]; do
        log_info "Download attempt $attempt of $max_retries..."

        if curl -fsSL --max-time "$timeout_seconds" "$url" -o "$BINARY" 2>/dev/null; then
            chmod +x "$BINARY"
            log_info "Binary installed to $BINARY"
            return 0
        fi

        log_warn "Download attempt $attempt failed"
        attempt=$((attempt + 1))
    done

    log_error "Failed to download binary after $max_retries attempts"
    exit 6
}

setup_directories() {
    log_info "Setting up directories..."
    
    # Create base directory first
    if [ ! -d "$DATA_DIR" ]; then
        mkdir -p "$DATA_DIR"
    fi
    
    # Make base directory writable for users who will run dockpal
    # (systemd runs as root, but direct execution may run as other users)
    chmod 757 "$DATA_DIR"
    
    # Create subdirectories with proper permissions
    mkdir -p "$DATA_DIR/data"
    mkdir -p "$DATA_DIR/logs" 
    mkdir -p "$DATA_DIR/repos"
    mkdir -p "$DATA_DIR/compose"
    mkdir -p "$DATA_DIR/traefik"
    
    # Data and logs need to be writable by the server
    chmod 757 "$DATA_DIR/data"
    chmod 757 "$DATA_DIR/logs"
    chmod 757 "$DATA_DIR/repos"
    chmod 757 "$DATA_DIR/compose"
    chmod 757 "$DATA_DIR/traefik"
    
    # Verify directories were created
    for dir in "$DATA_DIR" "$DATA_DIR/data" "$DATA_DIR/logs" "$DATA_DIR/repos"; do
        if [ ! -d "$dir" ]; then
            log_error "Failed to create directory: $dir"
            exit 1
        fi
        if [ ! -w "$dir" ]; then
            log_error "Directory not writable: $dir"
            exit 1
        fi
    done
    
    log_info "Directories created successfully"
}

setup_systemd() {
    log_info "Setting up systemd service..."
    cat > /etc/systemd/system/dockpal.service << 'SERVICEEOF'
[Unit]
Description=Dockpal — Docker Management Platform
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=/opt/dockpal
ExecStart=/usr/local/bin/dockpal server
Restart=always
RestartSec=5
Environment="PORT=3012"

[Install]
WantedBy=multi-user.target
SERVICEEOF

    systemctl daemon-reload
    systemctl enable dockpal
    systemctl start dockpal
    log_info "Dockpal service started"
}

# Stop existing dockpal service (for in-place upgrade)
stop_dockpal_service() {
    if systemctl is-active --quiet dockpal 2>/dev/null; then
        log_info "Stopping existing Dockpal service..."
        systemctl stop dockpal
    fi
}

# Stop existing dockara service (for upgrade from dockara)
stop_dockara_service() {
    # LEGACY-DOCKARA: Stop dockara service with timeout as per Req 9.1
    if systemctl is-active --quiet "$OLD_UNIT" 2>/dev/null; then
        log_info "Stopping existing Dockara service..."
        if ! timeout 30 systemctl stop "$OLD_UNIT"; then
            log_error "Failed to stop dockara service within 30 seconds"
            exit 3
        fi
    fi
}

# Verify service is running
verify_installation() {
    local max_wait=30
    local count=0

    log_info "Verifying installation..."

    while [ $count -lt $max_wait ]; do
        if systemctl is-active --quiet dockpal 2>/dev/null; then
            log_info "Dockpal service is running"
            return 0
        fi
        sleep 1
        count=$((count + 1))
    done

    log_error "Dockpal service failed to start within $max_wait seconds"
    return 1
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

    local arch
    arch=$(detect_arch)
    log_info "Detected architecture: $arch"

    install_dependencies
    install_docker

    # Detection: determine which path to follow
    local has_dockara=false
    local has_dockpal=false

    if is_dockara_install; then
        has_dockara=true
    fi

    if is_dockpal_install; then
        has_dockpal=true
    fi

    log_info "Detection: dockara_install=$has_dockara, dockpal_install=$has_dockpal"

    # Execute the appropriate install path
    if [[ "$has_dockara" == "true" ]]; then
        # Upgrade from Dockara path
        log_info "Detected existing Dockara installation, running upgrade path..."

        # Stop dockara service
        stop_dockara_service

        # Migrate cloudflared container
        migrate_cloudflared

        # Migrate data
        migrate_data

        # Download new binary (overwrites any existing dockpal binary)
        download_binary "$arch"

        # Setup systemd (writes new unit file, enables and starts)
        setup_systemd

    elif [[ "$has_dockpal" == "true" ]]; then
        # Upgrade in-place path
        log_info "Detected existing Dockpal installation, running in-place upgrade..."

        # Stop existing dockpal service
        stop_dockpal_service

        # Download new binary
        download_binary "$arch"

        # Setup systemd (updates unit file if needed, restarts)
        setup_systemd

    else
        # Fresh install path
        log_info "No existing installation detected, running fresh install..."

        # Setup directories
        setup_directories

        # Download binary
        download_binary "$arch"

        # Setup systemd
        setup_systemd
    fi

    # Verify the installation
    verify_installation

    echo ""
    log_info "Installation complete!"
    log_info "Access Dockpal at: http://$(hostname -I | awk '{print $1}'):3012"
    log_info "Default credentials: admin / admin"
    echo ""
}

main "$@"