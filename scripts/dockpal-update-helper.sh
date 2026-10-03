#!/usr/bin/env bash
#
# Dockpal update helper — runs as root via the dockpal-updater.path unit.
#
# The unprivileged panel (user `dockpal`, systemd-hardened) cannot replace its
# own binary, so it requests an update by writing a small JSON trigger file
# under its one writable tree (/opt/dockpal/update-request.json). This helper,
# activated by systemd's PathExists watcher, consumes that trigger, runs
# update.sh for the requested version, and records the outcome in
# /opt/dockpal/update-result.json so the panel can report success/failure
# after it restarts.
#
# This script is intentionally thin: update.sh remains the single source of
# truth for download/verify/backup/swap/health-check/rollback logic.
#
# Environment (all optional, mirror update.sh):
#   DOCKPAL_DATA_DIR   Trigger/result dir when the panel runs with the default
#                      data layout (default: /opt/dockpal). NOTE: when the
#                      panel sets DOCKPAL_UPDATE_TRIGGER_DIR, that same value
#                      must be exported here so both sides agree on where
#                      update-request.json lives.
#   DOCKPAL_UPDATE_SH  Path to update.sh (default: /opt/dockpal/update.sh)
#
set -euo pipefail

DATA_DIR="${DOCKPAL_DATA_DIR:-/opt/dockpal}"
UPDATE_SH="${DOCKPAL_UPDATE_SH:-$DATA_DIR/update.sh}"
RESULT_FILE="$DATA_DIR/update-result.json"

# The panel writes the trigger either to DOCKPAL_UPDATE_TRIGGER_DIR (when the
# systemd units watch a dedicated dir) or to its data dir root. Honor the
# explicit variable first, then the historical /opt/dockpal default, then the
# plain data-dir location — so any panel version finds its updater.
PANEL_TRIGGER_DIR="${DOCKPAL_UPDATE_TRIGGER_DIR:-}"
TRIGGER_FILE=""
for candidate in \
    "$PANEL_TRIGGER_DIR/update-request.json" \
    "/opt/dockpal/update-request.json" \
    "$DATA_DIR/data/update-request.json"; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
        TRIGGER_FILE="$candidate"
        break
    fi
done

log() { echo "[dockpal-update-helper] $*" >&2; }

write_result() {
    local status="$1" target="$2" exit_code="$3" message="$4"
    # Write to a temp file then rename so the panel never reads partial JSON.
    local tmp="$RESULT_FILE.tmp"
    jq -n \
        --arg status "$status" \
        --arg target "$target" \
        --argjson exit_code "$exit_code" \
        --arg message "$message" \
        --argjson finished_at "$(date +%s)" \
        '{status:$status, target:$target, exit_code:$exit_code, message:$message, finished_at:$finished_at}' \
        > "$tmp"
    chmod 644 "$tmp"
    mv -f "$tmp" "$RESULT_FILE"
    # Mirror the result next to the trigger file when it lives elsewhere, so
    # the panel's manager (which reads from its own trigger dir) sees it too.
    if [[ -n "$TRIGGER_FILE" && "$(dirname "$TRIGGER_FILE")" != "$(dirname "$RESULT_FILE")" ]]; then
        cp -f "$RESULT_FILE" "$(dirname "$TRIGGER_FILE")/update-result.json" 2>/dev/null || true
    fi
}

if [[ -z "$TRIGGER_FILE" ]]; then
    log "no trigger file found (checked trigger dir, /opt/dockpal and $DATA_DIR/data); nothing to do"
    exit 0
fi

# Read the requested target before removing the trigger.
TARGET="$(jq -r '.version // empty' "$TRIGGER_FILE" 2>/dev/null || true)"

# Remove the trigger FIRST so the .path unit does not immediately re-fire
# (PathExists triggers whenever the file exists) and so a crash mid-update
# does not loop the update on every boot.
rm -f "$TRIGGER_FILE"

if [[ -z "$TARGET" ]]; then
    log "trigger file missing 'version'"
    write_result "failed" "" 1 "trigger file missing version"
    exit 1
fi

if [[ ! -x "$UPDATE_SH" ]]; then
    log "update.sh not found or not executable at $UPDATE_SH"
    write_result "failed" "$TARGET" 1 "update.sh not available at $UPDATE_SH"
    exit 1
fi

log "starting update to $TARGET via $UPDATE_SH"

# Run update.sh. It performs its own backup, verification, health check and
# rollback, and restarts the panel service itself. We must not let a non-zero
# exit abort before writing the result, hence the temporary set +e.
set +e
DOCKPAL_VERSION="$TARGET" DOCKPAL_RESULT_FILE="$RESULT_FILE" "$UPDATE_SH"
rc=$?
set -e

if [[ $rc -eq 0 || $rc -eq 8 ]]; then
    # 0 = updated; 8 = already on target (idempotent).
    log "update to $TARGET completed (exit $rc)"
    # update.sh writes its own result file when DOCKPAL_RESULT_FILE is set;
    # only write here if it did not (older update.sh).
    [[ -f "$RESULT_FILE" ]] || write_result "done" "$TARGET" "$rc" "updated to $TARGET"
else
    log "update to $TARGET failed (exit $rc); update.sh rolled back if possible"
    [[ -f "$RESULT_FILE" ]] || write_result "failed" "$TARGET" "$rc" "update failed (exit $rc)"
fi

exit 0
