# Implementation Plan: Auto-Update Notification

## Overview

Implement the auto-update notification feature enabling Dockpal users to receive notifications when a new version is available and perform updates directly from the UI. Tasks are grouped by subsystem to minimize coupling and enable incremental delivery. All backend implementations use Go with the existing Gin/BBolt/Docker SDK stack.

## Tasks

- [x] 1. Version Check API (Backend)
  - [x] 1.1 Create version service package
    - Create `internal/update/version.go` with `VersionService` struct and interface
    - Implement `VersionInfo` and `CachedVersion` structs matching design
    - Implement `GetVersionInfo()` to fetch from GitHub API
    - Implement `GetCachedVersion()` to read from cache file
    - _Requirements: 1.1, 1.2, 1.3, 1.4_

  - [x] 1.2 Implement version comparison logic
    - Add `compareVersions(current, latest string) (bool, error)` function
    - Support semver format vX.Y.Z with major/minor/patch comparison
    - Return error for invalid version strings
    - _Requirements: 1.1, 1.2_

  - [x] 1.3 Implement version cache management
    - Create `internal/update/cache.go` with cache read/write functions
    - Cache file path: `<DATA_DIR>/version-cache.json`
    - Implement 1-hour TTL validation
    - Implement cache write with atomic file operations
    - _Requirements: 1.3, 1.4, 2.2_

  - [x] 1.4 Add version API endpoint
    - Add `GET /api/system/version` route in `internal/server/routes.go`
    - Implement `HandleGetVersion` handler
    - Return JSON: currentVersion, latestVersion, updateAvailable, releaseNotes, downloadUrl
    - Apply auth middleware
    - _Requirements: 1.1_

  - [x]* 1.5 Write property tests for version service
    - **Property 10: Version Comparison Uses Semver**
    - Verify comparison correctly identifies greater version per semver rules
    - **Validates: Requirements 1.1, 1.2**

  - [x]* 1.6 Write property tests for cache validity
    - **Property 2: Cache Is Used on Network Failure**
    - Verify cached data used when GitHub API fails and cache < 1 hour old
    - **Validates: Requirements 1.3, 1.4**

- [x] 2. Background Version Checker
  - [x] 2.1 Implement background scheduler
    - Create `internal/update/scheduler.go` with `VersionCheckScheduler` struct
    - Implement `Start(ctx context.Context, interval time.Duration)` method
    - Implement `Stop()` method with graceful shutdown
    - Run as goroutine on server startup
    - _Requirements: 2.1_

  - [x] 2.2 Integrate scheduler into server startup
    - Call `scheduler.Start()` in `main.go` after server initialization
    - Pass 6-hour interval (360 minutes)
    - Handle graceful shutdown on SIGTERM/SIGINT
    - _Requirements: 2.1_

  - [x]* 2.3 Write property test for background checker interval
    - **Property 3: Background Checker Runs Every 6 Hours**
    - Verify 6-hour interval with ±1 minute tolerance
    - **Validates: Requirements 2.1**

  - [x]* 2.4 Write property test for cache file format
    - **Property 4: Cache File Format Persists**
    - Verify cache file contains all required fields after successful check
    - **Validates: Requirements 2.2_

- [x] 3. Checkpoint - Version API & Background Checker
  - Ensure all tests pass, ask the user if questions arise.

- [x] 4. Update Execution (Backend)
  - [x] 4.1 Create update service package
    - Create `internal/update/service.go` with `UpdateService` struct and interface
    - Implement `UpdateProgress` struct with Status, Message, Percentage
    - _Requirements: 4.2_

  - [x] 4.2 Implement binary download
    - Add `DownloadUpdate(ctx context.Context, url string) (string, error)` method
    - Download to `/tmp/dockpal-new`
    - Implement 5-minute timeout
    - Return path to downloaded file
    - _Requirements: 4.2_

  - [x] 4.3 Implement binary verification
    - Add `VerifyBinary(path string) error` method
    - Check file size > 1MB (minimum valid binary size)
    - Check executable bit is set
    - Return error if verification fails
    - _Requirements: 5.3_

  - [x] 4.4 Implement binary installation
    - Add `InstallBinary(ctx context.Context, binaryPath string) error` method
    - Stop dockpal service via `systemctl stop dockpal`
    - Move new binary to `/usr/local/bin/dockpal`
    - Set executable permissions (0755)
    - Restart service via `systemctl start dockpal`
    - Clean up temp files on failure
    - _Requirements: 4.2_

  - [x] 4.5 Implement sudo access check
    - Add `CheckSudoAccess() (bool, error)` method
    - Use `sudo -n true` to check passwordless sudo
    - Return false if user lacks sudo privileges
    - _Requirements: 5.2_

  - [x] 4.6 Add update API endpoint
    - Add `POST /api/system/update` route in `internal/server/routes.go`
    - Require admin authentication
    - Accept `downloadUrl` in request body
    - Implement streaming progress response
    - _Requirements: 4.1, 4.4, 5.1_

  - [x]* 4.7 Write property tests for binary verification
    - **Property 9: Binary Verification Prevents Invalid Binaries**
    - Verify rejection of files < 1MB or without executable bit
    - **Validates: Requirements 5.3**

  - [x]* 4.8 Write property tests for update security
    - **Property 8: Update Requires Sudo**
    - Verify update fails without sudo privileges
    - **Validates: Requirements 5.2**

- [x] 5. UI Notification Component
  - [x] 5.1 Create update banner component
    - Create `web/js/components/UpdateBanner.js` (or similar location)
    - Implement `UpdateBannerState` interface
    - Implement `show()`, `hide()`, `updateProgress()` methods
    - Use state management consistent with existing UI
    - _Requirements: 3.1, 3.2_

  - [x] 5.2 Implement version check on page load
    - Add fetch to `GET /api/system/version` on app initialization
    - Store version info in component state
    - Pass to banner component
    - _Requirements: 3.1_

  - [x] 5.3 Implement banner actions
    - "Update Now" button: Call `POST /api/system/update` with downloadUrl
    - "Dismiss" button: Save to localStorage with key `update_dismissed`
    - Show loading state during update
    - _Requirements: 3.3, 3.4_

  - [x] 5.4 Implement dismiss persistence
    - Check localStorage on mount for `update_dismissed` flag
    - If dismissed, do not show banner
    - Clear flag on page refresh (session-only)
    - _Requirements: 3.3_

  - [x] 5.5 Implement update progress display
    - Show progress states: downloading → installing → restarting → complete
    - Display percentage and status message
    - Show error message on failure with reason
    - Reload page on success
    - _Requirements: 4.1_

  - [x]* 5.6 Write property tests for UI banner
    - **Property 5: UI Banner Shows on Update Available**
    - Verify banner visible when updateAvailable is true
    - **Validates: Requirements 3.1, 3.2**

  - [x]* 5.7 Write property tests for dismiss persistence
    - **Property 6: Dismiss Persists for Session**
    - Verify banner hidden after dismiss until page refresh
    - **Validates: Requirements 3.3**

- [x] 6. Security Integration
  - [x] 6.1 Verify admin-only access for update endpoint
    - Ensure `POST /api/system/update` requires valid JWT token
    - Verify user has admin role before allowing update
    - Return 401/403 appropriately
    - _Requirements: 5.1_

  - [x] 6.2 Add security property test
    - **Property 7: Update Requires Authentication**
    - Verify 401 returned for unauthenticated requests
    - **Validates: Requirements 5.1**

- [x] 7. Checkpoint - Full Integration
  - Ensure all tests pass, ask the user if questions arise.

- [x] 8. Final Integration & Testing
  - [x] 8.1 Integrate version check endpoint with scheduler
    - Ensure scheduler writes to cache that version endpoint reads
    - Test full flow: startup → background check → API returns cached data
    - _Requirements: 2.1, 2.2_

  - [x] 8.2 End-to-end update flow testing
    - Test: UI banner appears → user clicks Update Now → binary downloads → installs → restarts
    - Verify service comes back up with new version
    - _Requirements: 4.3, 4.5_

  - [x] 8.3 Error scenario testing
    - Test: GitHub API failure → returns cached data with null
    - Test: No sudo → returns appropriate error
    - Test: Network timeout → shows error in UI
    - _Requirements: 1.3, 4.4, 5.2_

- [x] 9. Final Checkpoint
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- Checkpoints ensure incremental validation per subsystem
- Property tests validate universal correctness properties from the design document
- Backend implementations use Go with existing Gin/BBolt stack
- UI implementations follow existing JavaScript/component patterns
- Cache file location: `<DATA_DIR>/version-cache.json` (typically /opt/dockpal/data/)
- Binary paths: `/usr/local/bin/dockpal` (production), `/tmp/dockpal-new` (download)

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.2", "1.3"] },
    { "id": 1, "tasks": ["1.4", "1.5", "1.6"] },
    { "id": 2, "tasks": ["2.1", "2.2", "2.3", "2.4"] },
    { "id": 3, "tasks": ["4.1", "4.2", "4.3"] },
    { "id": 4, "tasks": ["4.4", "4.5", "4.6", "4.7", "4.8"] },
    { "id": 5, "tasks": ["5.1", "5.2", "5.3", "5.4", "5.5", "5.6", "5.7"] },
    { "id": 6, "tasks": ["6.1", "6.2"] },
    { "id": 7, "tasks": ["8.1", "8.2", "8.3"] }
  ]
}
```

## Property Summary

| Property # | Title | Validates |
|------------|-------|-----------|
| 1 | Version Check Returns Valid Data | Req 1.1, 1.2 |
| 2 | Cache Is Used on Network Failure | Req 1.3, 1.4 |
| 3 | Background Checker Runs Every 6 Hours | Req 2.1 |
| 4 | Cache File Format Persists | Req 2.2 |
| 5 | UI Banner Shows on Update Available | Req 3.1, 3.2 |
| 6 | Dismiss Persists for Session | Req 3.3 |
| 7 | Update Requires Authentication | Req 5.1 |
| 8 | Update Requires Sudo | Req 5.2 |
| 9 | Binary Verification Prevents Invalid Binaries | Req 5.3 |
| 10 | Version Comparison Uses Semver | Req 1.1, 1.2 |