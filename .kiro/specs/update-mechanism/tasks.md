# Implementation Plan

## Overview

This task list implements the update-mechanism spec: fixing the binary verification ordering bug, adding comprehensive verification (size, mode, SHA256, ELF arch), atomic install with rollback, single-flight concurrency, structured progress events, and cleanup on all exit paths. The implementation targets `internal/update/` and the HTTP handler in `internal/server/routes.go`.

## Tasks

- [x] 1. Foundations and Data Model
  - [x] 1.1 Add new status and stage detail constants to `internal/update/service.go`: `StatusIdle = "idle"`, `StatusVerifying = "verifying"`, `StageDetailSize = "size"`, `StageDetailMode = "mode"`, `StageDetailChecksum = "checksum"`, `StageDetailArch = "arch"`, `BackupBinaryPath = "/usr/local/bin/dockpal.bak"`, `StagingBinarySuffix = ".new"`, `MaxBinarySize int64 = 200 * (1 << 20)`, `ProgressThrottle = 100 * time.Millisecond`, `DownloadBufferSize = 64 * 1024`, ELF magic (`elfMagic uint32 = 0x464C457F`), and e_machine values (`EM_386 = 3`, `EM_ARM = 40`, `EM_X86_64 = 62`, `EM_AARCH64 = 183`). Requirements: R12.2, R12.4, R2.1, R7.7, R5.2, R3.1, R2.5
  - [x] 1.2 Create `internal/update/errors.go` with all error code string constants grouped by category: Sudo (`ErrSudoUnavailable`, `ErrSudoLost`), URL (`ErrURLCredentialsPresent`, `ErrURLSchemeNotHTTPS`, `ErrURLHostNotAllowed`, `ErrURLResolvesPrivateIP`, `ErrAssetNotFoundForOSArch`), Download (`ErrDownloadHTTPStatus`, `ErrDownloadIOFailed`, `ErrDownloadTimeout`, `ErrDownloadDiskFull`), Verify (`ErrTempChmodFailed`, `ErrVerifySizeOOR`, `ErrVerifyChecksum`, `ErrVerifyArchMismatch`, `ErrVerifyNotELF`), Install (`ErrInstallStopFailed`, `ErrInstallReplaceFailed`, `ErrInstallChownFailed`, `ErrInstallStartFailed`), Concurrency (`ErrUpdateAlreadyRunning`). Requirements: R1.4, R2.6-R2.9, R3.4-R3.7, R4.2-R4.3, R5.5-R5.8, R6.3, R6.6, R10.1
  - [x] 1.3 Add `UpdateState` type (`int32`) with constants `UpdateIdle` and `UpdateRunning`, and add fields `mu sync.Mutex`, `state UpdateState`, `lastProgress *UpdateProgress` to the `UpdateService` struct. Requirements: R10.1, R10.2, R10.3
  - [x] 1.4 Extend `UpdateProgress` struct with `ErrorCode string` (json tag `errorCode,omitempty`) and `StageDetail string` (json tag `stageDetail,omitempty`). Requirements: R12.3, R12.4, R12.5
  - [x] 1.5 Add `ProgressEmitter` type definition: `type ProgressEmitter func(UpdateProgress)`. Requirements: R7.1
- [x] 2. ELF Architecture Verifier
  - [x] 2.1 Create `internal/update/verify_elf.go` implementing `verifyELFArch(path string) error`. Read first 20 bytes, parse ELF magic at offset 0, class at offset 4, endianness at offset 5, e_machine (uint16) at offset 18. Use decision table: amd64 maps to (ELFCLASS64, EM_X86_64), arm64 maps to (ELFCLASS64, EM_AARCH64), arm maps to (ELFCLASS32, EM_ARM), 386 maps to (ELFCLASS32, EM_386). Return error with code `verify_not_elf` if magic does not match, `verify_arch_mismatch` if class or e_machine does not match host `runtime.GOARCH`. Requirements: R2.5, R2.8, R2.9
  - [x] 2.2 (PBT) Create `internal/update/verify_elf_prop_test.go` with Property 7 test using `pgregory.net/rapid`. Generate random 20-byte ELF header prefixes (valid magic plus random class and e_machine combinations) and verify accept or reject matches the GOARCH-to-ELF table. Also generate non-ELF prefixes and verify `verify_not_elf` is returned. Requirements: R2.5, R2.8, R2.9. Property: P7. Depends on: 2.1, 1.2
- [x] 3. Checksum File Parser
  - [x] 3.1 Create `internal/update/checksum.go` implementing `parseChecksumFile(content []byte, assetName string) (string, error)`. Parse GNU coreutils sha256sum format (64-hex followed by two spaces then filename). Split on newlines, ignore empty and hash-prefixed comment lines. For each line split on whitespace, require first field is 64 hex chars, second field equals assetName (after trimming leading star or dot-slash). Return lowercase hex digest on match, `errAssetNotInChecksumFile` when no match, `errMalformedChecksumLine` on parse failure. Requirements: R2.3
  - [x] 3.2 (PBT) Create `internal/update/checksum_prop_test.go` with Property 3 round-trip test using `testing/quick`. Generate random sets of (hex-digest, asset-name) pairs with unique names, format them as sha256sum content, then assert `parseChecksumFile` returns the correct digest for each name. Requirements: R2.3. Property: P3. Depends on: 3.1
- [x] 4. URL Validator Refinement
  - [x] 4.1 Refactor `ValidateDownloadURL` in `service.go` to return typed errors that map to specific error codes (`url_scheme_not_https`, `url_host_not_allowed`, `url_credentials_present`, `url_resolves_private_ip`). Extract DNS resolution into a `resolver` interface parameter for testability. Keep the host allowlist: `github.com`, `*.github.com`, `objects.githubusercontent.com`, `*.githubusercontent.com`. Requirements: R6.1, R6.2, R6.3, R6.4
  - [x] 4.2 (PBT) Create `internal/update/url_validator_prop_test.go` with Property 1 test using `pgregory.net/rapid`. Generate URLs with random schemes, hosts (from allowlist and non-allowlist), userinfo presence, and stub resolver returning public or private IPs. Assert accept iff all four conditions hold; assert correct error code on rejection. Requirements: R6.1, R6.2, R6.3, R6.4. Property: P1. Depends on: 4.1, 1.2
- [x] 5. Asset Selection Hardening
  - [x] 5.1 Refactor `(*GitHubRelease).GetBrowserDownloadURL()` in `version.go` to a new method `GetAssetForPlatform(goos, goarch string) (string, error)` that does NOT fall back to `Assets[0]`. Returns the matching asset URL or an error wrapping `asset_not_found_for_platform`. Update `GetBrowserDownloadURL()` to call `GetAssetForPlatform(runtime.GOOS, runtime.GOARCH)` and return empty string on error (preserving backward compat for VersionService callers). Requirements: R6.5, R6.6
  - [x] 5.2 (PBT) Add Property 2 test in `internal/update/service_prop_test.go` using `testing/quick`. Generate random asset lists and (GOOS, GOARCH) pairs. Assert: when a matching asset exists it is returned; when none match error contains `asset_not_found_for_platform` and no fallback occurs. Requirements: R6.5, R6.6. Property: P2. Depends on: 5.1, 1.2
- [x] 6. Backend Interfaces (Test Seams)
  - [x] 6.1 Create `internal/update/backends.go` defining interfaces: `fsBackend` (Stat, Rename, Remove, Chmod, Chown, WriteFile, ReadFile, MkdirAll, Create, Sync), `serviceController` (Stop and Start methods accepting ctx returning error), `sudoChecker` (Check returning bool and error), `resolver` (LookupIP accepting host string returning slice of net.IP and error). Provide default implementations: `osFS`, `systemctlController`, `sudoCheckerImpl`, `netResolver`. Requirements: R3, R4, R10 testability
  - [x] 6.2 Create `internal/update/fakefs_test.go` with test doubles: `memFS` (in-memory filesystem tracking file existence, content, mode, uid/gid), `scriptedController` (returns pre-configured success or error per call index), `scriptedSudoChecker` (returns pre-configured sequence of bool results), `stubResolver` (returns configured IPs per host). Requirements: P8, P11, P12 testability
- [x] 7. UpdateService Struct Refactor
  - [x] 7.1 Add backend fields to `UpdateService` struct: `fs fsBackend`, `svc serviceController`, `sudo sudoChecker`, `dns resolver`. Add `backupPath string` field (default `BackupBinaryPath`). Create `NewUpdateServiceWithBackends(currentVersion, binPath, tempPath, backupPath string, fs fsBackend, svc serviceController, sudo sudoChecker, dns resolver) *UpdateService` constructor for tests. Update existing `NewUpdateService` and `NewUpdateServiceWithPaths` to use default backends internally (no breaking change to public API or main.go). Requirements: R3.1, R10 testability. Depends on: 6.1, 1.3
- [x] 8. Download Step Implementation
  - [x] 8.1 Implement private method `(*UpdateService).downloadToTemp(ctx context.Context, url string, emit ProgressEmitter) error`. Stream response body to tempPath using 64 KiB buffer. Emit throttled downloading progress (max one event per 100ms). After write completes: Chmod(tempPath, 0755), Sync(), Close(). On any error (HTTP non-200, IO, timeout, disk full): delete partial temp file and return error with appropriate code. Map context.DeadlineExceeded to download_timeout, syscall.ENOSPC to download_disk_full, HTTP non-200 to download_http_status, other IO to download_io_failed. Requirements: R1.1, R1.2, R1.4, R5.1, R5.2, R5.3, R5.4, R5.5, R5.6, R5.7, R5.8, R7.7. Depends on: 7.1, 1.4, 1.5
  - [x] 8.2 (PBT) Add Property 9 test (percentage clamping) in `service_prop_test.go` using `testing/quick`. For any (written int64, contentLength int64) with written >= 0 and contentLength >= 0, the percentage helper returns a value in [0, 100]. Requirements: R5.3, R5.4, R7.2. Property: P9. Depends on: 8.1
  - [x] 8.3 (PBT) Add Property 14 test (temp file mode after download) in `service_prop_test.go` using `pgregory.net/rapid`. Spin up httptest.Server serving random-sized payloads (at least 1 MiB), call downloadToTemp, assert resulting file mode has executable bits set (mode and 0111 not equal 0). Requirements: R1.1. Property: P14. Depends on: 8.1
  - [x] 8.4 Add unit tests in `internal/update/service_test.go`: Test_DownloadFsync_Example (verify SHA256 of temp file matches served content, R1.2), Test_TempChmodFailed_Example (inject chmod failure via fsBackend, assert error code temp_chmod_failed, R1.4), Test_DownloadTimeout_Example (stalling httptest server, assert download_timeout, R5.1), Test_ProgressThrottle_Example (fast stream, count events at most ceil(duration/100ms)+2, R7.7). Requirements: R1.2, R1.4, R5.1, R7.7. Depends on: 8.1
- [x] 9. Verify Step Implementation
  - [x] 9.1 Implement private method `(*UpdateService).verifyTempBinary(ctx context.Context, expectedAssetName string, checksumURL string, emit ProgressEmitter) error`. Execute 4 substeps in order, emitting verifying progress with stageDetail for each: (1) size check MinBinarySize to MaxBinarySize, (2) mode check mode and 0111 not equal 0, (3) checksum download checksumURL if non-empty then call parseChecksumFile and compute SHA256 of temp file and compare or if checksumURL is empty log no checksum published and skip, (4) ELF arch via verifyELFArch. Return first error encountered with appropriate error code. Requirements: R2.1, R2.2, R2.3, R2.4, R2.5, R2.6, R2.7, R2.8, R2.9, R12.4. Depends on: 7.1, 2.1, 3.1, 1.4
  - [x] 9.2 (PBT) Add Property 4 (size bounds), Property 5 (executable bit), and Property 6 (checksum match) tests in `service_prop_test.go`. P4: for any size s, verify passes iff MinBinarySize <= s <= MaxBinarySize. P5: for any mode m, verify passes iff m and 0111 not equal 0. P6: for any file content and published checksum, verify passes iff sha256(content) equals checksum. Requirements: R2.1, R2.6, R1.3, R2.2, R2.3, R2.7. Properties: P4, P5, P6. Depends on: 9.1
  - [x] 9.3 Add unit test `Test_NoChecksumPublished_Example` in `service_test.go`: serve no checksum asset (empty checksumURL), assert run proceeds past verify step and emits a verifying event with message containing no checksum published. Requirements: R2.4. Depends on: 9.1
- [x] 10. Install Step With Rollback
  - [x] 10.1 Implement private method `(*UpdateService).installAtomic(ctx context.Context, emit ProgressEmitter) error`. Sequence: (1) re-check sudo via s.sudo.Check() and emit sudo_lost on failure, (2) capture uid/gid from s.fs.Stat(s.binPath), (3) copy prod binary to backupPath preserving ownership, (4) s.svc.Stop(ctx) and on failure emit install_stop_failed and return with no rollback per R3.4, (5) write temp to sibling binPath plus .new then s.fs.Rename(sibling, binPath) and on failure call rollback() and emit install_replace_failed, (6) s.fs.Chmod(binPath, 0755) plus s.fs.Chown(binPath, uid, gid) and on failure call rollback() and emit install_chown_failed. Requirements: R3.1, R3.2, R3.3, R3.4, R3.5, R4.3. Depends on: 7.1, 6.1
  - [x] 10.2 Implement private methods: restartService(ctx, emit) error which emits restarting then calls s.svc.Start(ctx) and on failure calls rollback() plus retry start plus emit install_start_failed per R3.6. rollback(reason string) which calls s.fs.Rename(backupPath, binPath) and logs rollback_outcome per R11.4. cleanup(success bool) which on success deletes temp plus backup per R9.1 and on failure deletes per cleanup table R9.2-R9.4. Requirements: R3.6, R3.7, R9.1, R9.2, R9.3, R9.4, R11.4. Depends on: 7.1, 6.1
  - [x] 10.3 (PBT) Add Property 11 test (cleanup and rollback invariants) in `service_prop_test.go` using `pgregory.net/rapid`. Inject errors at each of the 16 exit classes from the cleanup table. After RunUpdate returns, assert filesystem state matches the table: prod binary content, tempPath existence, backupPath existence. Requirements: R3.1-R3.7, R5.5-R5.8, R9.1-R9.5. Property: P11. Depends on: 10.1, 10.2, 6.2
- [x] 11. Orchestrator and Concurrency
  - [x] 11.1 Implement public method `(*UpdateService).RunUpdate(ctx context.Context, downloadURL string, emit ProgressEmitter) error`. Acquire lock (return ErrUpdateAlreadyRunning if held). Call in order: cleanupStaleArtifacts(), check sudo (R4.1), downloadToTemp, verifyTempBinary, installAtomic, restartService, cleanup(true), emit complete at 100%. On any error: cleanup(false), emit error event, release lock. Requirements: R7.1, R10.1, R10.2, R4.1, R4.2. Depends on: 8.1, 9.1, 10.1, 10.2
  - [x] 11.2 Implement public method `(*UpdateService).Status() UpdateProgress`. Return Status idle when state equals UpdateIdle, otherwise return lastProgress. Requirements: R10.3. Depends on: 1.3, 1.4
  - [x] 11.3 Implement private method `(*UpdateService).cleanupStaleArtifacts()` which deletes pre-existing tempPath and backupPath at start of each run. Requirements: R9.5. Depends on: 7.1
  - [x] 11.4 (PBT) Add Property 10 test (stage ordering, monotonic progress, error envelope) in `service_prop_test.go` using `pgregory.net/rapid`. Inject errors at random steps, collect emitted events, assert: status sequence is a prefix of downloading verifying installing restarting complete plus at most one error; percentages are monotonically non-decreasing; error events have non-empty ErrorCode and Message; verifying events have valid StageDetail. Requirements: R7.1, R7.3, R7.4, R7.5, R7.6, R12.3, R12.4. Property: P10. Depends on: 11.1
  - [x] 11.5 (PBT) Add Property 8 test (sudo gating) in `service_prop_test.go` using `pgregory.net/rapid`. When first sudo check returns false: assert sudo_unavailable emitted and no HTTP request made. When first returns true and second returns false: assert sudo_lost emitted and prod binary untouched. Requirements: R4.1, R4.2, R4.3. Property: P8. Depends on: 11.1
  - [x] 11.6 (PBT) Add Property 12 test (single-flight concurrency) in `service_prop_test.go` using `pgregory.net/rapid`. Launch N concurrent RunUpdate calls, assert exactly one executes the pipeline and N-1 return update_already_running without touching filesystem. Requirements: R10.1, R10.2. Property: P12. Depends on: 11.1
  - [x] 11.7 Add unit tests: Test_StatusIdle_Example (fresh service returns idle, R10.3), Test_StatusDomainExtended_Example (marshal verifying and idle events, assert JSON shape, R12.2). Requirements: R10.3, R12.2. Depends on: 11.1, 11.2
- [x] 12. JSON Backward Compatibility Test
  - [x] 12.1 (PBT) Add Property 13 test in `service_prop_test.go` using `testing/quick`. For any UpdateProgress where ErrorCode equals empty string and StageDetail equals empty string, marshal to JSON and assert keys are exactly status, message, percentage (no errorCode or stageDetail keys, no null values). Round-trip marshal then unmarshal yields equal value. Requirements: R12.1, R12.5. Property: P13. Depends on: 1.4
- [x] 13. Logging and Observability
  - [x] 13.1 Add structured logging throughout RunUpdate: at start log current_version, target_version, download_url, attempt_id (R11.1). On each emit log attempt_id, stage, percentage, and for error log error_code (R11.2). On checksum verify log expected_sha256 and actual_sha256 (R11.3). On rollback log attempt_id, error_code, rollback_outcome, binary names (R11.4). Never log credentials or file contents (R11.5). Use standard log package consistent with existing repo patterns. Requirements: R11.1, R11.2, R11.3, R11.4, R11.5. Depends on: 11.1
  - [x] 13.2 Add unit test Test_LogFields_Example in service_test.go: capture log output during a scripted run (using a custom log writer), assert presence of required fields (attempt_id, stage, percentage, error_code on error, expected_sha256 and actual_sha256 on checksum) and absence of credential-like strings. Requirements: R11.1, R11.2, R11.3, R11.4, R11.5. Depends on: 13.1
- [x] 14. HTTP Handler Adapter
  - [x] 14.1 Refactor HandleUpdate in `internal/server/routes.go` (around line 1633): remove direct calls to CheckSudoAccess, DownloadUpdate, VerifyBinary, InstallBinary. Replace with a single call to updateService.RunUpdate(c.Request.Context(), req.DownloadURL, emit) where emit writes SSE frames (data: json newline newline plus flush). Preserve existing auth and admin check, request body binding, and SSE response headers. Requirements: R7, R10, R12. Depends on: 11.1
  - [x] 14.2 Add unit test for refactored handler using httptest.NewRecorder: verify Content-Type is text/event-stream, response contains data frames with JSON, and status is 200. Requirements: R7, R12. Depends on: 14.1
- [x] 15. Public API Cleanup
  - [x] 15.1 Search repo (grep) for external callers of DownloadUpdate, VerifyBinary, InstallBinary, GetProgressStatus. If only called from the now-refactored handler (Task 14.1), remove these public methods. If called elsewhere, mark with Deprecated comment and have them delegate to internal methods. Depends on: 14.1
  - [x] 15.2 Fix any broken tests resulting from removed or deprecated public methods. Depends on: 15.1
- [x] 16. Integration Smoke Tests (optional)
  - [x] 16.1 Create `internal/update/integration_test.go` with Test_SmokeDownloadAndVerify_Integration: spin up httptest.Server serving a valid ELF binary fixture (matching current GOOS/GOARCH, stored in internal/update/testdata/) plus a sha256sum file. Run download plus verify end-to-end (skip install). Assert: file exists at tempPath, mode includes 0755, size in range, checksum matches, ELF arch matches. Requirements: R1.1, R2.1, R2.3, R2.5. Depends on: 8.1, 9.1
  - [x] 16.2 Add Test_SmokeAssetNotFound_Integration: serve a release JSON with asset names that do not match current GOOS/GOARCH, assert error contains asset_not_found_for_platform. Requirements: R6.5, R6.6. Depends on: 5.1
- [ ] 17. Manual Verification (Human Task)
  - [ ] 17.1 (manual) End-to-end verification on a staging machine with systemd plus sudo: trigger update from current version to target version. Verify: (a) Update_Dialog shows stages downloading then verifying then installing then restarting then complete with progress reaching 100%, (b) systemctl status dockpal shows new binary running, (c) dockpal --version shows target version, (d) no dockpal.bak, dockpal.new, or dockpal-new files remain in /usr/local/bin/ or /tmp/.

## Task Dependency Graph

```json
{
  "waves": [
    {
      "id": "wave-1",
      "name": "Foundations",
      "tasks": ["1"],
      "description": "Constants, error codes, data model extensions, type definitions"
    },
    {
      "id": "wave-2",
      "name": "Helpers and Interfaces",
      "tasks": ["2", "3", "4", "5", "6"],
      "description": "ELF verifier, checksum parser, URL validator, asset selection, backend interfaces - all parallelizable"
    },
    {
      "id": "wave-3",
      "name": "Service Refactor",
      "tasks": ["7"],
      "description": "Inject backends into UpdateService struct"
    },
    {
      "id": "wave-4",
      "name": "Pipeline Steps",
      "tasks": ["8", "9", "10"],
      "description": "Download, verify, and install implementations - parallelizable"
    },
    {
      "id": "wave-5",
      "name": "Orchestrator",
      "tasks": ["11", "12"],
      "description": "RunUpdate orchestrator, concurrency, JSON compat test"
    },
    {
      "id": "wave-6",
      "name": "Integration Layer",
      "tasks": ["13", "14"],
      "description": "Logging and HTTP handler adapter"
    },
    {
      "id": "wave-7",
      "name": "Cleanup and Verification",
      "tasks": ["15", "16", "17"],
      "description": "API cleanup, integration smoke tests, manual verification"
    }
  ]
}
```

## Notes

- All PBT tests use `pgregory.net/rapid` (already in go.mod) or `testing/quick`. No gopter dependency.
- Property tests run at least 100 iterations each.
- Each test file carries a comment for traceability: `// Feature: update-mechanism, Property N: <description>`.
- Task 17 is a human-only task requiring a staging machine with systemd and sudo access.
- The release workflow (.github/workflows/release.yml) is NOT modified in this spec.
