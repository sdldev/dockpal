# Requirements Document

## Introduction

This specification covers 14 identified gaps in the Dockara platform spanning security hardening, functional completeness, and operational reliability. Dockara is a Go-based single-binary Docker management platform with an embedded web UI. These fixes address critical security vulnerabilities (file write no-op, hardcoded secrets, missing rate limiting, path traversal), functional gaps (compose parsing, system info, WebSocket stats streaming), and operational improvements (auto-recovery, log rotation, Cloudflare Tunnel integration).

## Glossary

- **Dockara**: The Go-based Docker management platform (single binary with embedded web UI)
- **FileOps_Module**: The internal/docker/fileops.go module responsible for file operations inside containers
- **WebSocket_Gateway**: The gorilla/websocket upgrade handler managing real-time connections
- **Auth_Module**: The internal/auth package handling JWT generation, validation, and login
- **Rate_Limiter**: A middleware component that restricts request frequency per IP address
- **Token_Version**: An integer field stored per user in BBolt, incremented on password change, embedded in JWT claims
- **Compose_Parser**: The module responsible for parsing docker-compose YAML into typed Go structs
- **System_Info_Endpoint**: The /api/system/info route returning host and Docker metrics
- **Stats_Stream**: A WebSocket endpoint delivering real-time container resource statistics
- **Health_Monitor**: A background goroutine performing periodic container health checks and auto-restart
- **Log_Rotator**: A component managing application log file size and retention
- **Traefik_Integrator**: The internal/traefik package generating dynamic reverse proxy configuration
- **Cloudflare_Tunnel_Module**: A module managing cloudflared container deployment for tunnel connectivity
- **Input_Validator**: A middleware or utility layer that sanitizes and validates user-supplied input fields
- **Secret_Manager**: The component responsible for generating, persisting, and loading the JWT signing secret

## Requirements

### Requirement 1: Container File Write Implementation

**User Story:** As an administrator, I want to write file content into containers through the web UI, so that I can edit configuration files without exec-ing into containers manually.

#### Acceptance Criteria

1. WHEN the FileOps_Module receives a write request with container ID, file path, and content, THE FileOps_Module SHALL write the provided content to the specified path inside the target container using Docker exec.
2. WHEN the FileOps_Module completes a file write operation, THE FileOps_Module SHALL return a success status to the caller.
3. IF the FileOps_Module fails to write the file due to a container error, THEN THE FileOps_Module SHALL return an error message describing the failure reason.
4. THE FileOps_Module SHALL use a shell command with heredoc or printf-based approach to write content into the container filesystem via exec.

### Requirement 2: WebSocket Origin Validation

**User Story:** As a security-conscious operator, I want WebSocket connections to validate the Origin header, so that cross-site WebSocket hijacking attacks are prevented.

#### Acceptance Criteria

1. WHEN the WebSocket_Gateway receives an upgrade request, THE WebSocket_Gateway SHALL extract the Origin header and compare the host portion against the server's configured host.
2. WHEN the Origin header host matches the server host, THE WebSocket_Gateway SHALL allow the WebSocket upgrade to proceed.
3. IF the Origin header host does not match the server host, THEN THE WebSocket_Gateway SHALL reject the upgrade request with HTTP 403 status.
4. IF the Origin header is empty or missing, THEN THE WebSocket_Gateway SHALL reject the upgrade request with HTTP 403 status.

### Requirement 3: JWT Secret Auto-Generation and Persistence

**User Story:** As a platform operator, I want the JWT secret to be automatically generated and persisted on first launch, so that each installation has a unique signing key without manual configuration.

#### Acceptance Criteria

1. WHEN the Secret_Manager starts and the JWT_SECRET environment variable is set, THE Secret_Manager SHALL use the environment variable value as the JWT signing secret.
2. WHEN the Secret_Manager starts and no JWT_SECRET environment variable is set, THE Secret_Manager SHALL check for a secret file at /opt/dockara/data/.secret.
3. WHEN the secret file exists at /opt/dockara/data/.secret, THE Secret_Manager SHALL read and use the file contents as the JWT signing secret.
4. WHEN no JWT_SECRET environment variable is set and no secret file exists, THE Secret_Manager SHALL generate a cryptographically random 32-byte secret, encode it as hex, and persist it to /opt/dockara/data/.secret with file permissions 0600.
5. THE Secret_Manager SHALL create the secret file with 0600 permissions to restrict read access to the owner only.

### Requirement 4: Login Rate Limiting

**User Story:** As a security-conscious operator, I want login attempts to be rate-limited, so that brute-force password attacks are mitigated.

#### Acceptance Criteria

1. THE Rate_Limiter SHALL track login request counts per client IP address on the /api/login endpoint.
2. WHILE a client IP has made 5 or more requests to /api/login within the current 1-minute window, THE Rate_Limiter SHALL reject subsequent requests from that IP with HTTP 429 status.
3. WHEN a rate-limited request is rejected, THE Rate_Limiter SHALL include a Retry-After header indicating the number of seconds until the next allowed request.
4. THE Rate_Limiter SHALL use an in-memory store with automatic expiration of rate limit entries after the window elapses.

### Requirement 5: JWT Token Versioning

**User Story:** As an administrator, I want existing tokens to be invalidated when I change my password, so that compromised tokens cannot be used after a password reset.

#### Acceptance Criteria

1. THE Auth_Module SHALL store a token_version integer field for each user record in the BBolt database.
2. WHEN a user password is changed, THE Auth_Module SHALL increment the token_version field for that user by 1.
3. WHEN the Auth_Module generates a JWT token, THE Auth_Module SHALL include the current token_version value in the JWT claims.
4. WHEN the Auth_Module validates a JWT token, THE Auth_Module SHALL compare the token_version claim against the current token_version stored in the database for that user.
5. IF the token_version in the JWT does not match the stored token_version, THEN THE Auth_Module SHALL reject the token as invalid.

### Requirement 6: Path Traversal Prevention

**User Story:** As a security-conscious operator, I want file operation paths to be validated against traversal attacks, so that container file access is restricted to allowed directories.

#### Acceptance Criteria

1. WHEN the FileOps_Module receives a file path, THE FileOps_Module SHALL resolve the path to its canonical absolute form using filepath.Clean.
2. WHEN the FileOps_Module receives a file path, THE FileOps_Module SHALL verify the resolved path starts with the root prefix "/".
3. IF the resolved file path contains ".." segments that would escape the root directory, THEN THE FileOps_Module SHALL reject the request with an error indicating path traversal is not allowed.
4. THE FileOps_Module SHALL reject paths containing null bytes or other control characters.

### Requirement 7: Input Sanitization and Validation

**User Story:** As a security-conscious operator, I want user-supplied inputs to be validated, so that injection attacks through container names, git URLs, and environment variable names are prevented.

#### Acceptance Criteria

1. WHEN the Input_Validator receives a container name, THE Input_Validator SHALL verify the name matches the pattern `^[a-zA-Z0-9][a-zA-Z0-9_.-]+$` with a maximum length of 128 characters.
2. WHEN the Input_Validator receives a git repository URL, THE Input_Validator SHALL verify the URL uses the https:// or git:// scheme and contains no shell metacharacters.
3. WHEN the Input_Validator receives an environment variable name, THE Input_Validator SHALL verify the name matches the pattern `^[a-zA-Z_][a-zA-Z0-9_]*$` with a maximum length of 256 characters.
4. IF the Input_Validator detects an invalid input value, THEN THE Input_Validator SHALL reject the request with HTTP 400 status and an error message identifying the invalid field.

### Requirement 8: Full Compose YAML Parsing

**User Story:** As a developer, I want the compose parser to fully understand docker-compose YAML structure, so that ports, volumes, environment variables, networks, and depends_on are correctly applied when deploying services.

#### Acceptance Criteria

1. THE Compose_Parser SHALL parse docker-compose YAML using gopkg.in/yaml.v3 into typed Go structs representing services, ports, volumes, environment, networks, and depends_on fields.
2. WHEN the Compose_Parser encounters a service with port mappings, THE Compose_Parser SHALL parse both short syntax (host:container) and long syntax into PortBinding structs.
3. WHEN the Compose_Parser encounters a service with volumes, THE Compose_Parser SHALL parse both short syntax (host:container) and long syntax into VolumeMount structs.
4. WHEN the Compose_Parser encounters a service with environment variables, THE Compose_Parser SHALL parse both list format (KEY=VALUE) and map format into string slices.
5. WHEN the Compose_Parser encounters a service with networks, THE Compose_Parser SHALL parse the network names and pass them to Docker container creation.
6. WHEN the Compose_Parser encounters a service with depends_on, THE Compose_Parser SHALL start dependent services before the services that depend on them.
7. IF the Compose_Parser receives invalid YAML, THEN THE Compose_Parser SHALL return a descriptive error identifying the parsing failure.

### Requirement 9: Extended System Information

**User Story:** As an administrator, I want the system info endpoint to report hardware and Docker metrics, so that I can monitor server resource utilization from the dashboard.

#### Acceptance Criteria

1. WHEN the System_Info_Endpoint receives a request, THE System_Info_Endpoint SHALL return the number of CPU cores available on the host.
2. WHEN the System_Info_Endpoint receives a request, THE System_Info_Endpoint SHALL return total RAM and used RAM in bytes.
3. WHEN the System_Info_Endpoint receives a request, THE System_Info_Endpoint SHALL return total disk space and used disk space in bytes for the root filesystem.
4. WHEN the System_Info_Endpoint receives a request, THE System_Info_Endpoint SHALL return the Docker daemon version string.
5. WHEN the System_Info_Endpoint receives a request, THE System_Info_Endpoint SHALL return the hostname and operating system identifier.

### Requirement 10: WebSocket Stats Streaming

**User Story:** As an administrator, I want real-time container resource stats streamed over WebSocket, so that the dashboard updates live without polling.

#### Acceptance Criteria

1. WHEN a client connects to the Stats_Stream WebSocket endpoint with a container ID, THE Stats_Stream SHALL begin sending container resource statistics as JSON messages.
2. WHILE the Stats_Stream WebSocket connection is open, THE Stats_Stream SHALL send updated stats (CPU percent, memory usage, memory limit, network RX/TX) at an interval of 2 seconds.
3. WHEN the client disconnects from the Stats_Stream, THE Stats_Stream SHALL stop the stats collection goroutine and clean up resources.
4. IF the specified container is not running, THEN THE Stats_Stream SHALL send an error message and close the WebSocket connection.

### Requirement 11: Container Auto-Recovery

**User Story:** As an operator, I want containers marked for auto-recovery to be automatically restarted when they exit unexpectedly, so that services remain available without manual intervention.

#### Acceptance Criteria

1. THE Health_Monitor SHALL run a periodic health check every 60 seconds as a background goroutine.
2. WHEN the Health_Monitor detects a container with the label `dockara.auto-recover=true` in an exited or dead state, THE Health_Monitor SHALL attempt to restart that container.
3. WHEN the Health_Monitor successfully restarts a container, THE Health_Monitor SHALL log the restart event with the container name and timestamp.
4. IF the Health_Monitor fails to restart a container after 3 consecutive attempts, THEN THE Health_Monitor SHALL log a critical error and stop retrying that container until the next health check cycle.

### Requirement 12: Traefik Integration on Deploy

**User Story:** As a developer, I want Traefik configuration to be automatically generated when I deploy a service with a domain, so that the reverse proxy routes traffic without manual config editing.

#### Acceptance Criteria

1. WHEN a compose deployment request includes a non-empty domain field, THE Traefik_Integrator SHALL call GenerateConfig with the domain, service name, and port to create a routing rule.
2. WHEN a service with an associated domain is removed, THE Traefik_Integrator SHALL call RemoveDomain to delete the corresponding routing rule.
3. THE Traefik_Integrator SHALL generate Traefik dynamic configuration with HTTPS entrypoint and Let's Encrypt certificate resolver.

### Requirement 13: Application Log Rotation

**User Story:** As an operator, I want application logs to be automatically rotated, so that disk space is not exhausted by unbounded log growth.

#### Acceptance Criteria

1. THE Log_Rotator SHALL rotate the active log file when the file size reaches 2 megabytes.
2. THE Log_Rotator SHALL retain a maximum of 5 rotated log files.
3. WHEN the Log_Rotator rotates a log file, THE Log_Rotator SHALL rename the current file with a numeric suffix and create a new empty active log file.
4. WHEN the number of rotated files exceeds 5, THE Log_Rotator SHALL delete the oldest rotated file.

### Requirement 14: Cloudflare Tunnel Integration

**User Story:** As an operator, I want to expose services through Cloudflare Tunnel by providing a tunnel token, so that services are publicly accessible without opening firewall ports.

#### Acceptance Criteria

1. WHEN the Cloudflare_Tunnel_Module receives a tunnel token, THE Cloudflare_Tunnel_Module SHALL deploy a cloudflared container with the provided token as a run argument.
2. THE Cloudflare_Tunnel_Module SHALL configure the cloudflared container with restart policy "unless-stopped" and the label `dockara.managed=true`.
3. WHEN the Cloudflare_Tunnel_Module receives a request to remove the tunnel, THE Cloudflare_Tunnel_Module SHALL stop and remove the cloudflared container.
4. IF the provided tunnel token is empty or malformed, THEN THE Cloudflare_Tunnel_Module SHALL reject the request with HTTP 400 status and an error message.
5. THE Cloudflare_Tunnel_Module SHALL use the official `cloudflare/cloudflared:latest` Docker image for the tunnel container.
