#!/usr/bin/env bash
#
# Dockpal Updater (Debian/Ubuntu, linux/amd64)
#
# Safely updates an existing Dockpal systemd installation:
#   - Resolves the requested release (latest or DOCKPAL_VERSION)
#   - Downloads and verifies the linux/amd64 binary
#   - Backs up the current binary and templates
#   - Replaces the binary and optionally refreshes templates
#   - Restarts dockpal and rolls back if health checks fail
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/sdldev/dockpal/main/update.sh | sudo bash
#
# Optional environment variables:
#   DOCKPAL_VERSION            Release tag to install (default: latest)
#   DOCKPAL_REPO               GitHub repo (default: sdldev/dockpal)
#   DOCKPAL_BINARY             Binary path (default: /usr/local/bin/dockpal)
#   DOCKPAL_SERVICE            systemd service name (default: dockpal)
#   DOCKPAL_DATA_DIR           Data root (default: /opt/dockpal)
#   DOCKPAL_BACKUP_DIR         Backup directory (default: /opt/dockpal/backups)
#   DOCKPAL_UPDATE_TEMPLATES   Refresh templates from release archive: 1/0 (default: 1)
#   DOCKPAL_FORCE              Reinstall even if already on target version: 1/0 (default: 0)
#
# Exit codes:
#   0 - Success
#   1 - Generic failure
#   2 - Not running as root
#   3 - Unsupported architecture
#   4 - Existing installation not found
#   5 - Download failed
#   6 - Update verification failed and rollback attempted
#   7 - Rollback failed
#   8 - Already up to date
#

set -euo pipefail

REPO="${DOCKPAL_REPO:-sdldev/dockpal}"
REQUESTED_VERSION="${DOCKPAL_VERSION:-latest}"
BINARY="${DOCKPAL_BINARY:-/usr/local/bin/dockpal}"
SERVICE="${DOCKPAL_SERVICE:-dockpal}"
DATA_DIR="${DOCKPAL_DATA_DIR:-/opt/dockpal}"
TEMPLATES_DIR="$DATA_DIR/templates"
BACKUP_DIR="${DOCKPAL_BACKUP_DIR:-$DATA_DIR/backups}"
UPDATE_TEMPLATES="${DOCKPAL_UPDATE_TEMPLATES:-1}"
FORCE="${DOCKPAL_FORCE:-0}"
SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE}.service"
PORT="${PORT:-3012}"

TMP_DIR=""
BACKUP_BINARY=""
BACKUP_TEMPLATES=""
TARGET_TAG=""
DOWNLOAD_URL=""
CHECKSUM_URL=""
CURRENT_VERSION="unknown"
TEMPLATES_UPDATED=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }

cleanup_tmp() {
    if [[ -n "$TMP_DIR" && -d "$TMP_DIR" ]]; then
        rm -rf "$TMP_DIR"
    fi
}
trap cleanup_tmp EXIT

require_root() {
    if [[ "$(id -u)" -ne 0 ]]; then
        log_error "This script must be run as root"
        exit 2
    fi
}

require_amd64() {
    local arch
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)
            log_info "Detected architecture: amd64"
            ;;
        *)
            log_error "Unsupported architecture: $arch (Dockpal only supports linux/amd64)"
            exit 3
            ;;
    esac
}

require_command() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        log_error "Missing required command: $cmd"
        exit 1
    fi
}

install_missing_dependencies() {
    local deps=("curl" "jq" "tar")
    local missing=()

    for dep in "${deps[@]}"; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            missing+=("$dep")
        fi
    done

    if [[ ${#missing[@]} -eq 0 ]]; then
        return 0
    fi

    if ! command -v apt-get >/dev/null 2>&1; then
        log_error "Missing dependencies (${missing[*]}) and apt-get is unavailable"
        exit 1
    fi

    log_info "Installing missing dependencies: ${missing[*]}"
    DEBIAN_FRONTEND=noninteractive apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${missing[@]}"
}

require_existing_install() {
    if [[ ! -x "$BINARY" ]]; then
        log_error "Dockpal binary not found or not executable: $BINARY"
        exit 4
    fi

    if ! systemctl list-unit-files "${SERVICE}.service" >/dev/null 2>&1; then
        log_error "systemd service not found: ${SERVICE}.service"
        exit 4
    fi

    if [[ ! -d "$DATA_DIR" ]]; then
        log_error "Dockpal data directory not found: $DATA_DIR"
        exit 4
    fi
}

normalize_version() {
    local raw="$1"
    raw="${raw#Dockpal }"
    raw="${raw#v}"
    echo "$raw"
}

current_version() {
    local out
    if out=$("$BINARY" version 2>/dev/null); then
        normalize_version "$out"
        return 0
    fi
    echo "unknown"
}

resolve_latest_release() {
    local api_url="https://api.github.com/repos/$REPO/releases/latest"
    local release_json

    log_info "Resolving latest release from $api_url"
    if ! release_json=$(curl -fsSL --connect-timeout 15 --max-time 60 "$api_url"); then
        log_error "Failed to resolve latest release"
        exit 5
    fi

    TARGET_TAG=$(jq -r '.tag_name // empty' <<<"$release_json")
    DOWNLOAD_URL=$(jq -r '.assets[]? | select(.name == "dockpal-linux-amd64") | .browser_download_url' <<<"$release_json" | head -n 1)
    CHECKSUM_URL=$(jq -r '.assets[]? | select(.name == "dockpal-linux-amd64.sha256") | .browser_download_url' <<<"$release_json" | head -n 1)

    if [[ -z "$TARGET_TAG" || -z "$DOWNLOAD_URL" ]]; then
        log_error "Latest release does not contain dockpal-linux-amd64 asset"
        exit 5
    fi
}

resolve_explicit_release() {
    TARGET_TAG="$REQUESTED_VERSION"
    DOWNLOAD_URL="https://github.com/$REPO/releases/download/$TARGET_TAG/dockpal-linux-amd64"
    CHECKSUM_URL="https://github.com/$REPO/releases/download/$TARGET_TAG/dockpal-linux-amd64.sha256"
}

resolve_release() {
    if [[ "$REQUESTED_VERSION" == "latest" ]]; then
        resolve_latest_release
    else
        resolve_explicit_release
    fi

    log_info "Target release: $TARGET_TAG"
    log_info "Binary URL: $DOWNLOAD_URL"
}

download_binary() {
    local tmp_binary="$TMP_DIR/dockpal"
    local max_retries=3
    local attempt=1

    while [[ $attempt -le $max_retries ]]; do
        log_info "Downloading binary (attempt $attempt of $max_retries)..."
        if curl -fSL --connect-timeout 15 --max-time 120 --retry 1 "$DOWNLOAD_URL" -o "$tmp_binary"; then
            chmod +x "$tmp_binary"
            return 0
        fi
        rm -f "$tmp_binary"
        attempt=$((attempt + 1))
    done

    log_error "Failed to download binary after $max_retries attempts"
    exit 5
}

verify_binary() {
    local tmp_binary="$TMP_DIR/dockpal"
    local size

    size=$(stat -c '%s' "$tmp_binary")
    if [[ "$size" -lt 5242880 || "$size" -gt 209715200 ]]; then
        log_error "Downloaded binary size $size is outside expected range"
        exit 5
    fi

    if command -v file >/dev/null 2>&1; then
        if ! file "$tmp_binary" | grep -q 'ELF 64-bit'; then
            log_error "Downloaded file is not an ELF 64-bit binary"
            exit 5
        fi
    fi

    if [[ -n "$CHECKSUM_URL" ]]; then
        log_info "Checking for checksum asset..."
        if curl -fsSL --connect-timeout 15 --max-time 60 "$CHECKSUM_URL" -o "$TMP_DIR/dockpal.sha256"; then
            local expected actual
            expected=$(awk '{print $1}' "$TMP_DIR/dockpal.sha256" | head -n 1)
            actual=$(sha256sum "$tmp_binary" | awk '{print $1}')
            if [[ -n "$expected" && "$expected" != "$actual" ]]; then
                log_error "Checksum mismatch: expected $expected, got $actual"
                exit 5
            fi
            log_info "Checksum verified"
        else
            log_warn "Checksum asset unavailable, continuing with ELF and version checks"
        fi
    fi

    if ! "$tmp_binary" version >/dev/null 2>&1; then
        log_error "Downloaded binary failed version smoke check"
        exit 5
    fi
}

backup_current_binary() {
    mkdir -p "$BACKUP_DIR"
    chmod 750 "$BACKUP_DIR" || true

    local timestamp
    timestamp=$(date +%Y%m%d%H%M%S)
    BACKUP_BINARY="$BACKUP_DIR/dockpal-${CURRENT_VERSION}-${timestamp}"
    cp "$BINARY" "$BACKUP_BINARY"
    chmod 755 "$BACKUP_BINARY"
    log_info "Current binary backed up to $BACKUP_BINARY"

    if [[ -f "$SYSTEMD_UNIT_PATH" ]]; then
        cp "$SYSTEMD_UNIT_PATH" "$BACKUP_DIR/${SERVICE}.service-${timestamp}" || true
    fi
}

backup_templates() {
    if [[ "$UPDATE_TEMPLATES" != "1" || ! -d "$TEMPLATES_DIR" ]]; then
        return 0
    fi

    local timestamp
    timestamp=$(date +%Y%m%d%H%M%S)
    BACKUP_TEMPLATES="$BACKUP_DIR/templates-${timestamp}"
    cp -a "$TEMPLATES_DIR" "$BACKUP_TEMPLATES"
    log_info "Templates backed up to $BACKUP_TEMPLATES"
}

download_templates() {
    if [[ "$UPDATE_TEMPLATES" != "1" ]]; then
        log_info "Template update disabled"
        return 0
    fi

    local ref="$TARGET_TAG"
    local archive_url="https://github.com/$REPO/archive/refs/tags/$ref.tar.gz"
    local tmp_tar="$TMP_DIR/source.tar.gz"
    local tmp_extract="$TMP_DIR/source"
    local extracted_templates

    log_info "Downloading templates for $ref..."
    if ! curl -fsSL --connect-timeout 15 --max-time 60 "$archive_url" -o "$tmp_tar"; then
        log_warn "Failed to download templates archive; keeping existing templates"
        return 0
    fi

    mkdir -p "$tmp_extract"
    if ! tar -xzf "$tmp_tar" -C "$tmp_extract"; then
        log_warn "Failed to extract templates archive; keeping existing templates"
        return 0
    fi

    extracted_templates=$(find "$tmp_extract" -maxdepth 3 -type d -name templates | head -n 1 || true)
    if [[ -z "$extracted_templates" ]]; then
        log_warn "templates/ not found in release archive; keeping existing templates"
        return 0
    fi

    rm -rf "$TMP_DIR/templates.new"
    mkdir -p "$TMP_DIR/templates.new"
    cp -a "$extracted_templates/." "$TMP_DIR/templates.new/"
}

install_templates() {
    if [[ "$UPDATE_TEMPLATES" != "1" || ! -d "$TMP_DIR/templates.new" ]]; then
        return 0
    fi

    rm -rf "$TEMPLATES_DIR.new"
    mkdir -p "$TEMPLATES_DIR.new"
    cp -a "$TMP_DIR/templates.new/." "$TEMPLATES_DIR.new/"
    chown -R dockpal:dockpal "$TEMPLATES_DIR.new" 2>/dev/null || true
    chmod 750 "$TEMPLATES_DIR.new" || true
    find "$TEMPLATES_DIR.new" -type f -exec chmod 640 {} + 2>/dev/null || true

    rm -rf "$TEMPLATES_DIR"
    mv "$TEMPLATES_DIR.new" "$TEMPLATES_DIR"
    TEMPLATES_UPDATED=1
    log_info "Templates updated at $TEMPLATES_DIR"
}

stop_service() {
    log_info "Stopping ${SERVICE}.service..."
    systemctl stop "$SERVICE"
}

start_service() {
    log_info "Starting ${SERVICE}.service..."
    systemctl start "$SERVICE"
}

replace_binary() {
    local tmp_binary="$TMP_DIR/dockpal"
    local staged="${BINARY}.new"

    cp "$tmp_binary" "$staged"
    chmod 755 "$staged"
    chown root:root "$staged" 2>/dev/null || true
    mv -f "$staged" "$BINARY"
    log_info "Binary replaced at $BINARY"
}

verify_service() {
    local max_wait=30
    local count=0

    log_info "Verifying ${SERVICE}.service..."
    while [[ $count -lt $max_wait ]]; do
        if systemctl is-active --quiet "$SERVICE" 2>/dev/null; then
            if ss -tln 2>/dev/null | grep -q ":${PORT} "; then
                if curl -fsS --connect-timeout 2 --max-time 5 "http://127.0.0.1:${PORT}/" >/dev/null 2>&1; then
                    log_info "Service is active and responding on :$PORT"
                    return 0
                fi
            fi
        fi
        sleep 1
        count=$((count + 1))
    done

    log_error "Service failed health check"
    return 1
}

rollback() {
    log_warn "Rolling back update..."
    systemctl stop "$SERVICE" >/dev/null 2>&1 || true

    if [[ -n "$BACKUP_BINARY" && -f "$BACKUP_BINARY" ]]; then
        cp "$BACKUP_BINARY" "$BINARY"
        chmod 755 "$BINARY"
        chown root:root "$BINARY" 2>/dev/null || true
    else
        log_error "No binary backup available for rollback"
        return 1
    fi

    if [[ "$TEMPLATES_UPDATED" == "1" && -n "$BACKUP_TEMPLATES" && -d "$BACKUP_TEMPLATES" ]]; then
        rm -rf "$TEMPLATES_DIR"
        cp -a "$BACKUP_TEMPLATES" "$TEMPLATES_DIR"
        chown -R dockpal:dockpal "$TEMPLATES_DIR" 2>/dev/null || true
    fi

    systemctl start "$SERVICE" || return 1
    verify_service || return 1
    log_warn "Rollback completed"
    return 0
}

print_success() {
    echo ""
    log_info "Dockpal update complete"
    log_info "Previous version: $CURRENT_VERSION"
    log_info "Current release: $TARGET_TAG"
    log_info "Binary backup: $BACKUP_BINARY"
    if [[ -n "$BACKUP_TEMPLATES" ]]; then
        log_info "Templates backup: $BACKUP_TEMPLATES"
    fi
    log_info "Logs: journalctl -u $SERVICE -n 100 --no-pager"
    echo ""
}

main() {
    echo ""
    echo "  🐳  Dockpal Updater"
    echo "  ==================="
    echo ""

    require_root
    require_amd64
    install_missing_dependencies
    require_command sha256sum
    require_existing_install

    CURRENT_VERSION=$(current_version)
    log_info "Current version: $CURRENT_VERSION"

    resolve_release

    local normalized_target
    normalized_target=$(normalize_version "$TARGET_TAG")
    if [[ "$CURRENT_VERSION" == "$normalized_target" && "$FORCE" != "1" ]]; then
        log_info "Dockpal is already up to date ($TARGET_TAG)"
        exit 8
    fi

    TMP_DIR=$(mktemp -d)

    download_binary
    verify_binary
    download_templates
    backup_current_binary
    backup_templates

    stop_service
    replace_binary
    install_templates
    systemctl daemon-reload
    start_service

    if ! verify_service; then
        if rollback; then
            log_error "Update failed; rollback succeeded"
            exit 6
        fi
        log_error "Update failed and rollback failed"
        log_error "Check logs with: journalctl -u $SERVICE -n 100 --no-pager"
        exit 7
    fi

    print_success
}

main "$@"
