#!/bin/bash
# verify-rebrand.sh - Verification script for Dockara to Dockpal rebrand
# This script verifies that the rebrand is complete and correct
#
# Requires: ripgrep (rg), go, git
# Install: sudo apt install ripgrep || sudo snap install ripgrep

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

ERRORS=0

# Check for ripgrep
if ! command -v rg &> /dev/null; then
    echo "Error: ripgrep (rg) is required but not installed."
    echo "Install with: sudo apt install ripgrep"
    exit 1
fi

log_pass() {
    echo -e "${GREEN}✓${NC} $1"
}

log_fail() {
    echo -e "${RED}✗${NC} $1"
    ERRORS=$((ERRORS + 1))
}

log_info() {
    echo -e "${YELLOW}→${NC} $1"
}

echo "=== Dockpal Rebrand Verification ==="
echo ""

# Step 1: go build ./...
log_info "Step 1: Running go build ./..."
if go build ./... 2>/dev/null; then
    log_pass "go build ./... succeeded"
else
    log_fail "go build ./... failed"
fi
echo ""

# Step 2: go vet ./...
log_info "Step 2: Running go vet ./..."
if go vet ./... 2>/dev/null; then
    log_pass "go vet ./... succeeded"
else
    log_fail "go vet ./... failed"
fi
echo ""

# Step 3: git ls-files dockara should return nothing
log_info "Step 3: Checking for dockara files in git..."
DOCKARA_FILES=$(git ls-files dockara 2>/dev/null || true)
if [ -z "$DOCKARA_FILES" ]; then
    log_pass "No dockara files tracked in git"
else
    log_fail "Found dockara files in git:"
    echo "$DOCKARA_FILES" | while read -r f; do echo "  - $f"; done
fi
echo ""

# Step 4: No github.com/dockara/dockara in Go files
log_info "Step 4: Checking for old import path in Go files..."
if ! rg 'github.com/dockara/dockara' --type go -q 2>/dev/null; then
    log_pass "No github.com/dockara/dockara found in Go files"
else
    log_fail "Found github.com/dockara/dockara in Go files:"
    rg 'github.com/dockara/dockara' --type go | head -5 | while read -r l; do echo "  - $l"; done
fi
echo ""

# Step 5: No DOCKARA_ env var prefix in production code
log_info "Step 5: Checking for DOCKARA_ env var prefix..."
# Exclude .kiro, .auto-claude, .playwright-mcp, and scripts directories
if ! rg 'DOCKARA_' --glob '!*.kiro/*' --glob '!*.auto-claude/*' --glob '!*.playwright-mcp/*' --glob '!scripts/*' -q 2>/dev/null; then
    log_pass "No DOCKARA_ env var prefix found in production code"
else
    log_fail "Found DOCKARA_ env var prefix in production code:"
    rg 'DOCKARA_' --glob '!*.kiro/*' --glob '!*.auto-claude/*' --glob '!*.playwright-mcp/*' --glob '!scripts/*' | head -5 | while read -r l; do echo "  - $l"; done
fi
echo ""

# Step 6: No dockara in web/ directory
log_info "Step 6: Checking for dockara in web/ directory..."
if ! rg -i 'dockara' web/ -q 2>/dev/null; then
    log_pass "No dockara found in web/ directory"
else
    log_fail "Found dockara references in web/:"
    rg -i 'dockara' web/ | head -5 | while read -r l; do echo "  - $l"; done
fi
echo ""

# Step 7: Check LEGACY-DOCKARA comments for allowed dockara references
log_info "Step 7: Checking LEGACY-DOCKARA comments in production code..."

# Allowed files for dockara references with LEGACY-DOCKARA comments:
# - installer.sh
# - internal/docker/recovery.go
# - internal/docker/compose.go

# Find all dockara references in production Go files
PROD_GO_ALLOWLIST=("internal/docker/recovery.go" "internal/docker/compose.go")

# Check if dockara references in production Go files have LEGACY-DOCKARA comments
FOUND_UNALLOWED=0
for pattern in $(rg -l 'dockara' --type go 2>/dev/null || true); do
    # Check if file is in allowlist
    ALLOWED=0
    for allowed in "${PROD_GO_ALLOWLIST[@]}"; do
        if [[ "$pattern" == *"$allowed" ]]; then
            ALLOWED=1
            break
        fi
    done
    
    if [ $ALLOWED -eq 0 ]; then
        # File not in allowlist - check if it has any dockara references
        if rg -q 'dockara' "$pattern" 2>/dev/null; then
            log_fail "dockara found in non-allowlisted file: $pattern"
            FOUND_UNALLOWED=1
        fi
    else
        # File is in allowlist - verify LEGACY-DOCKARA comments exist
        if rg -q 'dockara' "$pattern" 2>/dev/null; then
            # Check for LEGACY-DOCKARA comment in the same file
            if ! rg -q 'LEGACY-DOCKARA' "$pattern" 2>/dev/null; then
                log_fail "File $pattern has dockara but no LEGACY-DOCKARA comment"
                FOUND_UNALLOWED=1
            fi
        fi
    fi
done

# Check installer.sh for LEGACY-DOCKARA comments
if [ -f "installer.sh" ]; then
    if rg -qi 'dockara' installer.sh 2>/dev/null; then
        if ! rg -q 'LEGACY-DOCKARA' installer.sh 2>/dev/null; then
            log_fail "installer.sh has dockara but no LEGACY-DOCKARA comment"
            FOUND_UNALLOWED=1
        fi
    fi
fi

if [ $FOUND_UNALLOWED -eq 0 ]; then
    log_pass "All dockara references in production code have LEGACY-DOCKARA comments"
fi
echo ""

# Step 8: README should only have dockara in "Upgrading from Dockara" section
log_info "Step 8: Checking README.md for dockara isolation..."

if [ -f "README.md" ]; then
    # Get all dockara matches in README
    README_DOCKARA_MATCHES=$(rg -ni 'dockara' README.md 2>/dev/null || true)
    
    if [ -z "$README_DOCKARA_MATCHES" ]; then
        log_pass "No dockara found in README.md (OK if fresh install only)"
    else
        # Check if we can find the "Upgrading from Dockara" section
        if rg -q '## Upgrading from Dockara' README.md 2>/dev/null; then
            # Extract section and check if all dockara is within it
            # Get line number of "Upgrading from Dockara" heading
            SECTION_START=$(rg -n '## Upgrading from Dockara' README.md | cut -d: -f1)
            
            # Get line number of next heading AFTER the section (skip all headings before and including the section)
            # First get all heading line numbers after SECTION_START
            NEXT_HEADING=$(rg -n '^## ' README.md | rg -v "## Upgrading from Dockara" | awk -F: -v start="$SECTION_START" '$1 > start {print $1; exit}' || true)
            
            # Check if all dockara references are within the section
            OUTSIDE_SECTION=0
            while IFS= read -r line; do
                LINENUM=$(echo "$line" | cut -d: -f1)
                CONTENT=$(echo "$line" | cut -d: -f2-)
                
                # Check if line is inside the section
                if [ -n "$NEXT_HEADING" ]; then
                    if [ "$LINENUM" -ge "$SECTION_START" ] && [ "$LINENUM" -lt "$NEXT_HEADING" ]; then
                        continue
                    fi
                else
                    # No next heading, all lines after SECTION_START are in the section
                    if [ "$LINENUM" -ge "$SECTION_START" ]; then
                        continue
                    fi
                fi
                
                # This dockara is outside the section
                log_fail "dockara found outside 'Upgrading from Dockara' section (line $LINENUM): $CONTENT"
                OUTSIDE_SECTION=1
            done <<< "$README_DOCKARA_MATCHES"
            
            if [ $OUTSIDE_SECTION -eq 0 ]; then
                log_pass "All dockara references in README.md are within 'Upgrading from Dockara' section"
            fi
        else
            # No "Upgrading from Dockara" section - check if there are any dockara references
            if [ -n "$README_DOCKARA_MATCHES" ]; then
                log_fail "dockara found in README.md but no 'Upgrading from Dockara' section exists"
                echo "$README_DOCKARA_MATCHES" | head -3 | while read -r l; do echo "  - $l"; done
            else
                log_pass "No dockara references in README.md"
            fi
        fi
    fi
else
    log_fail "README.md not found"
fi
echo ""

# Summary
echo "=== Verification Summary ==="
if [ $ERRORS -eq 0 ]; then
    echo -e "${GREEN}All verifications passed!${NC}"
    exit 0
else
    echo -e "${RED}$ERRORS verification(s) failed${NC}"
    exit 1
fi