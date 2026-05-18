# Implementation Plan: Gap Fixes & Security Hardening

## Overview

Implement 14 identified gaps across Dockara's security, functional completeness, and operational reliability subsystems. Tasks are grouped by subsystem to minimize coupling and enable incremental delivery. All implementations use Go with the existing Gin/BBolt/Docker SDK stack.

## Tasks

- [x] 1. Security Hardening
  - [x] 1.1 Implement JWT secret auto-generation and persistence
    - Create `internal/auth/secret.go` with `LoadOrGenerateSecret()` function
    - Implement priority chain: env var → file → generate new 32-byte hex secret
    - Persist generated secret to `/opt/dockara/data/.secret` with 0600 permissions
    - Integrate `LoadOrGenerateSecret()` into `main.go` startup, replacing hardcoded secret
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5_

  - [x] 1.2 Write property test for secret generation format
    - **Property 2: Secret Generation Format**
    - Verify output is always a valid 64-character hexadecimal string
    - **Validates: Requirements 3.4**

  - [x] 1.3 Implement login rate limiter
    - Create `internal/server/ratelimit.go` with `RateLimiter` struct and `Allow()` method
    - Implement sliding window (1-minute) with max 5 requests per IP
    - Create `RateLimitMiddleware` Gin handler returning HTTP 429 with Retry-After header
    - Register middleware on `/api/login` route in `internal/server/routes.go`
    - _Requirements: 4.1, 4.2, 4.3, 4.4_

  - [x] 1.4 Write property tests for rate limiter
    - **Property 3: Rate Limiter Enforcement** — requests 6+ within window are rejected
    - **Property 4: Rate Limiter Window Expiration** — requests allowed after window elapses
    - **Validates: Requirements 4.2, 4.4**

  - [x] 1.5 Implement JWT token versioning
    - Add `TokenVersion int` field to `User` struct in `internal/db/db.go`
    - Add `TokenVersion int` to JWT `Claims` struct in `internal/auth/jwt.go`
    - Update `GenerateJWT` to accept and embed `tokenVersion` in claims
    - Update `ValidateJWT` to compare claims version against stored version
    - Implement `UpdatePasswordWithVersion` in `internal/db/db.go` that increments token_version atomically
    - Update password change handler in `internal/auth/handler.go` to use new function
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5_

  - [x] 1.6 Write property tests for token versioning
    - **Property 5: Token Version Increment** — N password changes → version increases by N
    - **Property 6: Token Version Round-Trip** — JWT with matching version passes, mismatched rejects
    - **Validates: Requirements 5.2, 5.3, 5.4, 5.5**

  - [x] 1.7 Implement WebSocket origin validation
    - Replace permissive `CheckOrigin` in `internal/server/routes.go` with strict `checkOrigin` function
    - Validate Origin header host matches request Host; reject empty/missing/mismatched with 403
    - Apply to all `websocket.Upgrader` instances
    - _Requirements: 2.1, 2.2, 2.3, 2.4_

  - [x] 1.8 Write property test for WebSocket origin validation
    - **Property 1: WebSocket Origin Validation**
    - Verify allow iff Origin host == request Host; reject empty/missing/non-matching
    - **Validates: Requirements 2.1, 2.2, 2.3, 2.4**

  - [x] 1.9 Implement path traversal prevention
    - Add `ValidatePath` function to `internal/docker/fileops.go`
    - Reject paths with null bytes, control characters, non-absolute paths, and ".." traversal
    - Integrate `ValidatePath` call into all file operation handlers
    - _Requirements: 6.1, 6.2, 6.3, 6.4_

  - [x] 1.10 Write property test for path traversal prevention
    - **Property 7: Path Traversal Prevention**
    - Verify rejection of ".." segments, non-absolute paths, null bytes, control chars
    - **Validates: Requirements 6.1, 6.2, 6.3, 6.4**

  - [x] 1.11 Implement input validation package
    - Create `internal/validator/validator.go` with `ValidateContainerName`, `ValidateGitURL`, `ValidateEnvVarName`
    - Container name: `^[a-zA-Z0-9][a-zA-Z0-9_.\-]+$`, max 128 chars
    - Git URL: https:// or git:// scheme, no shell metacharacters
    - Env var name: `^[a-zA-Z_][a-zA-Z0-9_]*$`, max 256 chars
    - Integrate validators into relevant route handlers with HTTP 400 responses
    - _Requirements: 7.1, 7.2, 7.3, 7.4_

  - [x] 1.12 Write property tests for input validation
    - **Property 8: Container Name Validation** — accept iff matches regex and length ≤ 128
    - **Property 9: Git URL Validation** — accept iff https/git scheme and no metacharacters
    - **Property 10: Environment Variable Name Validation** — accept iff matches regex and length ≤ 256
    - **Validates: Requirements 7.1, 7.2, 7.3**

- [x] 2. Checkpoint - Security Hardening
  - Ensure all tests pass, ask the user if questions arise.

- [x] 3. Functional Completeness
  - [x] 3.1 Implement container file write
    - Implement `WriteFile` method on Docker `Client` in `internal/docker/fileops.go`
    - Use base64-encoded content with `sh -c 'echo ... | base64 -d > path'` via Docker exec
    - Call `ValidatePath` before writing; return error on failure
    - Add `/api/containers/:id/files/write` POST endpoint in `internal/server/routes.go`
    - _Requirements: 1.1, 1.2, 1.3, 1.4_

  - [x] 3.2 Write property test for file write command safety
    - **Property 18: File Write Command Safety**
    - Verify base64 encoding prevents shell injection for any content string
    - **Validates: Requirements 1.4**

  - [x] 3.3 Implement full compose YAML parser
    - Rewrite `internal/docker/compose.go` with typed structs (`ComposeFile`, `ComposeService`, `PortBinding`, `VolumeMount`)
    - Implement `ParseComposeFile` using `gopkg.in/yaml.v3`
    - Implement `ParsePort` (short and long syntax), `ParseVolume` (host:container:mode), `ParseEnvironment` (list and map)
    - Implement `ResolveStartOrder` with topological sort for depends_on
    - Integrate parsed structs into container creation flow
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, 8.7_

  - [x] 3.4 Write property tests for compose parsing
    - **Property 11: Compose Port and Volume Parsing** — correct PortBinding/VolumeMount from valid specs
    - **Property 12: Compose Dependency Ordering** — topological order respects all depends_on
    - **Property 13: Invalid YAML Rejection** — non-YAML or missing services returns error
    - **Validates: Requirements 8.2, 8.3, 8.6, 8.7**

  - [x] 3.5 Implement system info endpoint
    - Add `SystemInfo` struct and `getSystemInfo()` function in `internal/server/routes.go`
    - Use `runtime.NumCPU()`, `syscall.Sysinfo`, `syscall.Statfs`, `os.Hostname()` for host metrics
    - Get Docker version via `ServerVersion` API call
    - Register `GET /api/system/info` route with auth middleware
    - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5_

  - [x] 3.6 Implement WebSocket stats streaming
    - Add `handleStatsStream` function in `internal/server/routes.go`
    - Use Docker `ContainerStats` with 2-second ticker, send JSON over WebSocket
    - Handle client disconnect with context cancellation and goroutine cleanup
    - Send error and close connection if container not running
    - Register `GET /api/containers/:id/stats/ws` WebSocket route
    - _Requirements: 10.1, 10.2, 10.3, 10.4_

- [x] 4. Checkpoint - Functional Completeness
  - Ensure all tests pass, ask the user if questions arise.

- [x] 5. Operational Reliability
  - [x] 5.1 Implement container auto-recovery
    - Create `internal/docker/recovery.go` with `HealthMonitor` struct
    - Implement 60-second ticker goroutine filtering containers by label `dockara.auto-recover=true`
    - Restart exited/dead containers with max 3 retries per cycle per container
    - Log restart events and critical failures
    - Integrate `HealthMonitor.Start()` into `main.go` startup
    - _Requirements: 11.1, 11.2, 11.3, 11.4_

  - [x] 5.2 Write property test for auto-recovery retry limit
    - **Property 17: Auto-Recovery Retry Limit**
    - Verify at most 3 restart attempts per container per health check cycle
    - **Validates: Requirements 11.4**

  - [x] 5.3 Implement log rotation
    - Add `LogRotator` struct in `main.go` (or new `internal/logging/rotator.go`)
    - Implement `Write()` with size check triggering rotation at 2MB
    - Implement `rotate()` with numeric suffix shifting and max 5 retained files
    - Set `LogRotator` as output for Go's `log` package in main startup
    - _Requirements: 13.1, 13.2, 13.3, 13.4_

  - [x] 5.4 Write property test for log file retention
    - **Property 15: Log File Retention Invariant**
    - Verify rotated file count never exceeds 5 for any sequence of rotations
    - **Validates: Requirements 13.2, 13.4**

  - [x] 5.5 Implement Traefik integration on deploy
    - Hook compose deploy handler to call `traefik.GenerateConfig` when domain is non-empty
    - Hook service deletion to call `traefik.RemoveDomain` when service has associated domain
    - Pass extracted port from parsed compose to config generation
    - _Requirements: 12.1, 12.2, 12.3_

  - [x] 5.6 Write property test for Traefik config structure
    - **Property 14: Traefik Config Structure**
    - Verify generated config contains `websecure` entrypoint and `letsencrypt` certResolver
    - **Validates: Requirements 12.3**

  - [x] 5.7 Implement Cloudflare Tunnel integration
    - Create `internal/tunnel/cloudflare.go` with `CloudflareTunnel` struct
    - Implement `Deploy()` using Docker SDK to create cloudflared container with token
    - Configure restart policy "unless-stopped" and label `dockara.managed=true`
    - Implement `Remove()` to stop and remove the container
    - Implement `ValidateTunnelToken()` with regex validation
    - Add `/api/tunnel` POST and DELETE endpoints in `internal/server/routes.go`
    - _Requirements: 14.1, 14.2, 14.3, 14.4, 14.5_

  - [x] 5.8 Write property test for tunnel token validation
    - **Property 16: Tunnel Token Validation**
    - Verify rejection of empty strings and strings with chars outside `[a-zA-Z0-9\-_.]`
    - **Validates: Requirements 14.4**

- [x] 6. Final Checkpoint
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- Checkpoints ensure incremental validation per subsystem
- Property tests validate universal correctness properties from the design document
- All implementations use Go with existing Gin/BBolt/Docker SDK stack
- The project uses `gopkg.in/yaml.v3` for compose parsing (ensure dependency is added)

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.9", "1.11"] },
    { "id": 1, "tasks": ["1.2", "1.3", "1.7", "1.10", "1.12"] },
    { "id": 2, "tasks": ["1.4", "1.5", "1.8", "3.1"] },
    { "id": 3, "tasks": ["1.6", "3.2", "3.3", "3.5"] },
    { "id": 4, "tasks": ["3.4", "3.6", "5.1", "5.3"] },
    { "id": 5, "tasks": ["5.2", "5.4", "5.5", "5.7"] },
    { "id": 6, "tasks": ["5.6", "5.8"] }
  ]
}
```
