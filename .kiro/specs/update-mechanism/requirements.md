# Requirements Document

## Introduction

The dockpal update mechanism downloads new release binaries from GitHub and replaces the running production binary at `/usr/local/bin/dockpal`. Field reports show that updating from v0.3.3 to v0.4.0 fails at the verification stage with the message "Binary verification failed: binary does not have executable permissions" while the progress bar stays at 0%. Investigation of `internal/update/service.go` and related files identified a verification ordering bug (chmod is applied only after install, but verify checks the executable bit before install), missing fsync, missing cleanup on error paths, no rollback when service start fails after the binary has already been replaced, no integrity verification beyond size and mode bits, and no architecture/OS sanity check. The Update_Dialog also does not expose intermediate stages or a retry path when a transient failure occurs.

This feature defines the functional, security, and observability requirements for a corrected update mechanism that covers the backend Go package `internal/update/` and the contract it exposes to the Update_Dialog frontend.

## Glossary

- **Update_Service**: The Go component in `internal/update/` that downloads, verifies, installs, and reports progress for binary updates. Implemented in `service.go`.
- **Version_Service**: The Go component in `internal/update/version.go` that fetches release metadata from the GitHub API and caches it.
- **Update_Scheduler**: The background scheduler in `internal/update/scheduler.go` that periodically calls Version_Service.
- **Update_Dialog**: The frontend "Update Available" dialog that initiates the update flow and displays progress to the user.
- **Production_Binary**: The currently installed dockpal executable located at `/usr/local/bin/dockpal`.
- **Temp_Binary**: The newly downloaded executable located at `/tmp/dockpal-new` before installation completes.
- **Backup_Binary**: A copy of the Production_Binary made immediately before installation, used for rollback. Stored at `/usr/local/bin/dockpal.bak` (path is implementation-defined).
- **Release_Asset**: A binary file attached to a GitHub release, named with the pattern `dockpal-<os>-<arch>` (for example `dockpal-linux-amd64`).
- **Checksum_File**: A release asset containing SHA256 checksums for the binary assets, when present.
- **Update_Stage**: One of `downloading`, `verifying`, `installing`, `restarting`, `complete`, or `error`.
- **Update_Progress**: A structured event emitted by Update_Service that contains an Update_Stage, a human-readable message, a percentage in the range 0..100, and an optional error code.
- **Error_Code**: A stable machine-readable identifier for a failure mode (for example `download_timeout`, `verify_size_too_small`, `install_start_failed`).
- **Transient_Error**: An error that may succeed on retry without user intervention (for example network timeout, HTTP 5xx, GitHub rate limit).
- **Permanent_Error**: An error that will not succeed on retry without user intervention (for example checksum mismatch, sudo not available, architecture mismatch).
- **Sudo_Available**: True when the current user can run `sudo -n true` successfully (passwordless sudo).
- **MinBinarySize**: 1048576 bytes (1 MiB), the minimum acceptable size of a downloaded binary.
- **MaxBinarySize**: 200 MiB, the maximum acceptable size of a downloaded binary (DoS guard).
- **DownloadTimeout**: 5 minutes, the maximum duration of a single download attempt.

## Requirements

### Requirement 1: Correct Executable Permissions Before Verification

**User Story:** As a dockpal user, I want the update verification step to succeed for a freshly downloaded binary, so that I can install legitimate updates without being blocked by a tooling bug.

#### Acceptance Criteria

1. WHEN the Update_Service writes the Temp_Binary to disk, THE Update_Service SHALL set the file mode of the Temp_Binary to `0755` before returning from the download step.
2. WHEN the Update_Service finishes writing bytes to the Temp_Binary, THE Update_Service SHALL flush the file contents to the underlying storage (fsync) and close the file before any verification step reads its mode or size.
3. WHEN the Update_Service verifies the Temp_Binary, THE Update_Service SHALL accept any file whose mode has at least one executable bit set in the user, group, or other class.
4. IF setting the file mode of the Temp_Binary fails, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `temp_chmod_failed`, and a message that names the path and the underlying OS error.

### Requirement 2: Comprehensive Binary Verification

**User Story:** As a dockpal user, I want downloaded binaries to be checked for integrity and compatibility before installation, so that a corrupted or wrong-architecture binary cannot replace my running service.

#### Acceptance Criteria

1. WHEN the Update_Service verifies the Temp_Binary, THE Update_Service SHALL confirm that the file size is greater than or equal to MinBinarySize and less than or equal to MaxBinarySize.
2. WHEN the Update_Service verifies the Temp_Binary, THE Update_Service SHALL confirm that at least one executable bit is set on the file mode.
3. WHERE a Checksum_File is published as a Release_Asset for the same release, THE Update_Service SHALL download the Checksum_File, locate the entry matching the downloaded asset name, and confirm that the SHA256 digest of the Temp_Binary equals the entry value.
4. WHERE no Checksum_File is published for the release, THE Update_Service SHALL record this fact in the Update_Progress message for stage `verifying` and proceed with the remaining verification steps.
5. WHEN the Update_Service verifies the Temp_Binary on Linux, THE Update_Service SHALL read the ELF header of the file and confirm that the ELF machine field matches the host architecture as reported by `runtime.GOARCH`.
6. IF the file size is outside the allowed range, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `verify_size_out_of_range`, and a message that includes the observed size and the allowed range.
7. IF the SHA256 digest does not match the published value, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `verify_checksum_mismatch`, and a message that includes both digests in hexadecimal.
8. IF the ELF header indicates an architecture different from the host, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `verify_arch_mismatch`, and a message that names both architectures.
9. IF the file is not a valid ELF executable, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `verify_not_elf`, and a message that names the path.

### Requirement 3: Atomic Installation With Rollback

**User Story:** As a dockpal user, I want a failed install to leave my system in a working state, so that an interrupted update does not break my running dockpal service.

#### Acceptance Criteria

1. WHEN the Update_Service begins installation, THE Update_Service SHALL copy the current Production_Binary to a Backup_Binary path before any other modification of the Production_Binary.
2. WHEN the Update_Service installs the Temp_Binary, THE Update_Service SHALL replace the Production_Binary using a procedure that is atomic from the perspective of an external observer (for example write a sibling file then `rename`, or `install -m 0755`).
3. WHEN the Production_Binary has been replaced, THE Update_Service SHALL set its file mode to `0755` and its owner to the same owner that the Backup_Binary had before the replacement.
4. IF stopping the dockpal service via `systemctl stop dockpal` fails, THEN THE Update_Service SHALL leave the Temp_Binary, the Production_Binary, and the Backup_Binary unchanged and emit an Update_Progress with stage `error` and error code `install_stop_failed`.
5. IF replacing the Production_Binary fails after the service has been stopped, THEN THE Update_Service SHALL restore the Backup_Binary in place of the Production_Binary, attempt `systemctl start dockpal`, and emit an Update_Progress with stage `error` and error code `install_replace_failed`.
6. IF starting the dockpal service via `systemctl start dockpal` fails after the Production_Binary has been replaced, THEN THE Update_Service SHALL restore the Backup_Binary in place of the Production_Binary, attempt `systemctl start dockpal` again with the restored binary, and emit an Update_Progress with stage `error` and error code `install_start_failed`.
7. WHEN the dockpal service has been confirmed to be active after replacement, THE Update_Service SHALL delete the Backup_Binary and the Temp_Binary.

### Requirement 4: Sudo And Privilege Preconditions

**User Story:** As a dockpal user, I want the update flow to fail fast with a clear message when it cannot obtain the privileges it needs, so that I am not left staring at a stalled progress bar.

#### Acceptance Criteria

1. WHEN the user initiates an update, THE Update_Service SHALL evaluate Sudo_Available before downloading any bytes.
2. IF Sudo_Available is false, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `sudo_unavailable`, a message that explains passwordless sudo is required, and SHALL NOT start a download.
3. WHEN Sudo_Available is true at the start of an update, THE Update_Service SHALL re-evaluate Sudo_Available immediately before the install step and SHALL emit an Update_Progress with stage `error` and error code `sudo_lost` if the result has changed.

### Requirement 5: Robust Download Behavior

**User Story:** As a dockpal user, I want the download step to handle network problems and partial files gracefully, so that I can recover from a flaky connection without manual cleanup.

#### Acceptance Criteria

1. WHEN the user initiates an update, THE Update_Service SHALL apply DownloadTimeout as the maximum duration of the download step.
2. WHEN the Update_Service starts a download, THE Update_Service SHALL stream the response body to the Temp_Binary path with a maximum in-memory buffer of 64 KiB.
3. WHEN the HTTP response declares a `Content-Length` header, THE Update_Service SHALL emit an Update_Progress with stage `downloading` whose `percentage` is computed as bytes-written divided by content-length and clamped to the range 0..100.
4. WHERE the HTTP response does not declare a `Content-Length` header, THE Update_Service SHALL emit Update_Progress events with stage `downloading` and `percentage` set to a value derived from elapsed bytes (for example a stepped indicator) without exceeding 99 until the download completes.
5. IF the HTTP response status code is not 200, THEN THE Update_Service SHALL emit an Update_Progress with stage `error`, error code `download_http_status`, a message that includes the status code, and SHALL delete the Temp_Binary if any partial content was written.
6. IF the read or write of the response body fails before completion, THEN THE Update_Service SHALL delete any partial Temp_Binary and emit an Update_Progress with stage `error` and error code `download_io_failed`.
7. IF the DownloadTimeout elapses before the body has been fully received, THEN THE Update_Service SHALL delete any partial Temp_Binary and emit an Update_Progress with stage `error` and error code `download_timeout`.
8. IF the disk write fails with `ENOSPC` or an equivalent disk-full condition, THEN THE Update_Service SHALL delete any partial Temp_Binary and emit an Update_Progress with stage `error` and error code `download_disk_full`.

### Requirement 6: Download Source Validation

**User Story:** As a security-conscious user, I want updates to come only from the official GitHub repository, so that the update channel cannot be redirected to a malicious host.

#### Acceptance Criteria

1. WHEN the Update_Service receives a download URL, THE Update_Service SHALL accept the URL only if its scheme is `https`.
2. WHEN the Update_Service receives a download URL, THE Update_Service SHALL accept the URL only if its host equals `github.com`, `objects.githubusercontent.com`, or another `*.githubusercontent.com` subdomain.
3. IF a download URL contains a userinfo component (credentials), THEN THE Update_Service SHALL reject the URL with error code `url_credentials_present`.
4. WHEN the Update_Service resolves a download URL, THE Update_Service SHALL reject the URL if any resolved IP address is loopback, private, link-local, or multicast.
5. WHEN the Update_Service selects a Release_Asset, THE Update_Service SHALL select the asset whose name matches the pattern `dockpal-<runtime.GOOS>-<runtime.GOARCH>` (with `arm` mapped to `armv7`).
6. IF no Release_Asset matches the host operating system and architecture, THEN THE Update_Service SHALL emit an Update_Progress with stage `error` and error code `asset_not_found_for_platform` and SHALL NOT fall back to an arbitrary asset.

### Requirement 7: Update Stage Reporting

**User Story:** As a dockpal user, I want the Update_Dialog to show me which step the update is on, so that I can tell the difference between a slow download and a stuck install.

#### Acceptance Criteria

1. WHEN the Update_Service progresses through the update flow, THE Update_Service SHALL emit Update_Progress events using the Update_Stage values in the order: `downloading`, `verifying`, `installing`, `restarting`, `complete`.
2. WHEN the Update_Service emits an Update_Progress event with stage `downloading`, THE Update_Service SHALL set `percentage` between 0 and 99 inclusive while bytes remain to be transferred.
3. WHEN the Update_Service emits an Update_Progress event with stage `verifying`, THE Update_Service SHALL set `percentage` to a value between 0 and 99 inclusive that reflects the verification substep (size, executable bit, checksum, ELF header).
4. WHEN the Update_Service emits an Update_Progress event with stage `installing` or `restarting`, THE Update_Service SHALL set `percentage` to a value greater than the most recent `downloading` percentage and less than 100.
5. WHEN the Update_Service emits an Update_Progress event with stage `complete`, THE Update_Service SHALL set `percentage` to 100.
6. WHEN the Update_Service emits an Update_Progress event with stage `error`, THE Update_Service SHALL include a non-empty Error_Code and a non-empty human-readable message.
7. THE Update_Service SHALL emit at most one Update_Progress event per 100 milliseconds for the same stage during a single update invocation, except for terminal events (`complete`, `error`) which SHALL be emitted immediately.

### Requirement 8: Update Dialog Behavior

**User Story:** As a dockpal user, I want the Update_Dialog to reflect the real state of the update and to let me retry transient failures, so that I can resolve common issues without restarting the application.

#### Acceptance Criteria

1. WHEN the Update_Dialog receives an Update_Progress event, THE Update_Dialog SHALL render the current Update_Stage label and the `percentage` on the progress bar.
2. WHEN the Update_Dialog receives an Update_Progress event with stage `error`, THE Update_Dialog SHALL display the Error_Code, the message, and a "Retry" control if the Error_Code is in the Transient_Error set defined in this document.
3. WHEN the user activates the "Retry" control, THE Update_Dialog SHALL call the same update endpoint with the same target version and SHALL reset its progress display to 0 percent and stage `downloading`.
4. WHERE the Error_Code is a Permanent_Error, THE Update_Dialog SHALL display the Error_Code, the message, and a "Close" control instead of "Retry".
5. THE Update_Dialog SHALL classify the following Error_Codes as Transient_Error: `download_http_status` (when the status is 5xx or 429), `download_io_failed`, `download_timeout`, `download_disk_full`, `install_stop_failed`, `install_start_failed`.
6. THE Update_Dialog SHALL classify the following Error_Codes as Permanent_Error: `verify_size_out_of_range`, `verify_checksum_mismatch`, `verify_arch_mismatch`, `verify_not_elf`, `temp_chmod_failed`, `sudo_unavailable`, `sudo_lost`, `url_credentials_present`, `asset_not_found_for_platform`.
7. WHEN an update transitions to stage `complete`, THE Update_Dialog SHALL display the new version string and a "Close" control.

### Requirement 9: Cleanup On All Exit Paths

**User Story:** As a system operator, I want the update flow to leave no stale files behind, so that disk usage and security surface remain predictable across repeated update attempts.

#### Acceptance Criteria

1. WHEN an update completes successfully, THE Update_Service SHALL delete the Temp_Binary and the Backup_Binary.
2. IF an update fails before the install step, THEN THE Update_Service SHALL delete the Temp_Binary.
3. IF an update fails during the install step before the Production_Binary has been replaced, THEN THE Update_Service SHALL delete the Temp_Binary and SHALL leave the Production_Binary and Backup_Binary in their pre-install state.
4. IF an update fails during the install step after the Production_Binary has been replaced and rollback restored the Backup_Binary, THEN THE Update_Service SHALL delete the Temp_Binary and the Backup_Binary.
5. WHEN the Update_Service starts a new update attempt, THE Update_Service SHALL delete any pre-existing Temp_Binary and any pre-existing Backup_Binary before downloading new content.

### Requirement 10: Concurrency And Idempotence

**User Story:** As a dockpal user, I want repeated clicks on the update button to be safe, so that I cannot accidentally start two parallel updates.

#### Acceptance Criteria

1. WHILE an update is in progress, THE Update_Service SHALL reject a new update request with error code `update_already_running` and SHALL NOT start a second download.
2. WHEN an update reaches stage `complete` or `error`, THE Update_Service SHALL release its in-progress lock so that a subsequent request can start a new attempt.
3. WHEN the Update_Service is asked for the current Update_Progress while no update is in progress, THE Update_Service SHALL return a sentinel state distinguishable from any active stage (for example stage `idle`).

### Requirement 11: Observability

**User Story:** As a system operator, I want detailed logs of every update attempt, so that I can diagnose failures from log files alone.

#### Acceptance Criteria

1. WHEN the Update_Service starts an update attempt, THE Update_Service SHALL log a record with at least the fields `current_version`, `target_version`, `download_url`, and `attempt_id`.
2. WHEN the Update_Service emits any Update_Progress event, THE Update_Service SHALL log a record with at least the fields `attempt_id`, `stage`, `percentage`, and (for stage `error`) `error_code`.
3. WHEN the Update_Service performs a checksum verification, THE Update_Service SHALL log the expected SHA256 and the computed SHA256 in hexadecimal.
4. IF the Update_Service performs a rollback, THEN THE Update_Service SHALL log a record with `attempt_id`, `error_code`, `rollback_outcome`, and the names of the binaries involved.
5. THE Update_Service SHALL NOT log credentials or full file contents at any log level.

### Requirement 12: Compatibility Of The Progress Contract

**User Story:** As a frontend developer, I want the progress event payload to remain backward compatible, so that existing Update_Dialog code does not break.

#### Acceptance Criteria

1. THE Update_Progress payload SHALL include the existing fields `status`, `message`, and `percentage` with their existing types.
2. THE Update_Progress payload SHALL extend the `status` field domain to include `verifying` in addition to the existing values `downloading`, `installing`, `restarting`, `complete`, `error`.
3. THE Update_Progress payload SHALL add an optional field `errorCode` of type string that is populated only when `status` equals `error`.
4. THE Update_Progress payload SHALL add an optional field `stageDetail` of type string that may carry the verification substep name (`size`, `mode`, `checksum`, `arch`).
5. WHEN any optional field is absent, THE Update_Service SHALL omit the field from the JSON payload rather than emitting a null value.
