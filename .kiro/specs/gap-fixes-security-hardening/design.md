# Design Document: Gap Fixes & Security Hardening

## Overview

This design addresses 14 identified gaps across Dockara's security, functional completeness, and operational reliability. Changes are organized by subsystem to minimize coupling and enable incremental delivery.

**Technology Stack:** Go 1.25, Gin framework, BBolt DB, Docker SDK (moby/moby/client), gorilla/websocket, gopkg.in/yaml.v3

---

## Architecture

The design is organized into three independent subsystems to minimize coupling and enable incremental delivery:

1. **Subsystem 1 – Security Hardening**: JWT secret management, rate limiting, token versioning, WebSocket origin validation, path traversal prevention, and input validation. These components form a security layer that wraps existing handlers and middleware.
2. **Subsystem 2 – Functional Completeness**: Container file write, full YAML-based compose parser, system info endpoint, and WebSocket stats streaming. These extend existing Docker and server packages with new capabilities.
3. **Subsystem 3 – Operational Reliability**: Container auto-recovery, log rotation, Traefik integration on deploy, and Cloudflare tunnel management. These are background services and lifecycle hooks that run alongside the main server.

Each subsystem can be developed and tested independently. Security hardening is applied first as it gates all other functionality.

---

## Components and Interfaces

| Package / Module | New File(s) | Responsibility |
|-----------------|-------------|----------------|
| `internal/auth` | `secret.go` | JWT secret resolution (env → file → generate) |
| `internal/server` | `ratelimit.go` | Sliding-window rate limiter middleware |
| `internal/auth` | updated `jwt.go` | Token versioning in claims + validation |
| `internal/docker` | updated `fileops.go` | Path validation + container file write |
| `internal/validator` | `validator.go` | Input validation (container names, git URLs, env vars) |
| `internal/docker` | updated `compose.go` | Full YAML struct-based compose parser |
| `internal/server` | updated `routes.go` | System info endpoint, WebSocket stats, origin check |
| `internal/docker` | `recovery.go` | Health monitor with auto-restart logic |
| `internal/tunnel` | `cloudflare.go` | Cloudflare tunnel container lifecycle |
| `main.go` | updated | Log rotation setup |

**Key Interfaces:**

- `RateLimiter.Allow(ip string) (bool, time.Duration)` – checks if an IP is within limits
- `SecretManager: LoadOrGenerateSecret() (string, error)` – resolves JWT signing secret
- `HealthMonitor.Start() / Stop()` – background container recovery lifecycle
- `CloudflareTunnel.Deploy(ctx, token) / Remove(ctx)` – tunnel container management
- `ValidatePath(path string) (string, error)` – path safety check
- `ParseComposeFile(yaml string) (*ComposeFile, error)` – full compose parsing

---

## Subsystem 1: Security Hardening

### 1.1 JWT Secret Management (`internal/auth/secret.go`)

**Architecture:** A `SecretManager` component with a priority-based secret resolution chain:

1. Check `JWT_SECRET` environment variable
2. Check `/opt/dockara/data/.secret` file
3. Generate 32-byte cryptographic random secret, hex-encode, persist to file

```go
package auth

import (
    "crypto/rand"
    "encoding/hex"
    "fmt"
    "os"
)

const secretFilePath = "/opt/dockara/data/.secret"

// LoadOrGenerateSecret resolves the JWT signing secret using a priority chain:
// env var > file > generate new.
func LoadOrGenerateSecret() (string, error) {
    // Priority 1: Environment variable
    if secret := os.Getenv("JWT_SECRET"); secret != "" {
        return secret, nil
    }

    // Priority 2: Existing secret file
    if data, err := os.ReadFile(secretFilePath); err == nil && len(data) > 0 {
        return string(data), nil
    }

    // Priority 3: Generate and persist
    secret, err := generateSecret()
    if err != nil {
        return "", fmt.Errorf("failed to generate secret: %w", err)
    }

    if err := os.MkdirAll("/opt/dockara/data", 0755); err != nil {
        return "", fmt.Errorf("failed to create data dir: %w", err)
    }

    if err := os.WriteFile(secretFilePath, []byte(secret), 0600); err != nil {
        return "", fmt.Errorf("failed to persist secret: %w", err)
    }

    return secret, nil
}

func generateSecret() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return hex.EncodeToString(b), nil
}
```

### 1.2 Rate Limiter (`internal/server/ratelimit.go`)

**Architecture:** In-memory sliding window rate limiter using a mutex-protected map. Each entry tracks request timestamps within a 1-minute window. Entries are lazily cleaned on access.

```go
package server

import (
    "net/http"
    "sync"
    "time"

    "github.com/gin-gonic/gin"
)

const (
    rateLimitWindow   = 1 * time.Minute
    rateLimitMax      = 5
)

type rateLimitEntry struct {
    timestamps []time.Time
}

type RateLimiter struct {
    mu      sync.Mutex
    entries map[string]*rateLimitEntry
}

func NewRateLimiter() *RateLimiter {
    return &RateLimiter{
        entries: make(map[string]*rateLimitEntry),
    }
}

// Allow checks if the given IP is within rate limits.
// Returns (allowed bool, retryAfter time.Duration).
func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    now := time.Now()
    entry, exists := rl.entries[ip]
    if !exists {
        entry = &rateLimitEntry{}
        rl.entries[ip] = entry
    }

    // Prune timestamps outside window
    cutoff := now.Add(-rateLimitWindow)
    valid := entry.timestamps[:0]
    for _, t := range entry.timestamps {
        if t.After(cutoff) {
            valid = append(valid, t)
        }
    }
    entry.timestamps = valid

    if len(entry.timestamps) >= rateLimitMax {
        oldest := entry.timestamps[0]
        retryAfter := oldest.Add(rateLimitWindow).Sub(now)
        return false, retryAfter
    }

    entry.timestamps = append(entry.timestamps, now)
    return true, 0
}

// RateLimitMiddleware applies rate limiting to a route.
func RateLimitMiddleware(rl *RateLimiter) gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        allowed, retryAfter := rl.Allow(ip)
        if !allowed {
            c.Header("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
            c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
            c.Abort()
            return
        }
        c.Next()
    }
}
```

### 1.3 Token Versioning (`internal/auth/`, `internal/db/`)

**Data Model Change:** Add `TokenVersion int` field to the `User` struct in BBolt.

```go
// Updated User struct in internal/db/db.go
type User struct {
    ID           string `json:"id"`
    Username     string `json:"username"`
    PasswordHash string `json:"password_hash"`
    TokenVersion int    `json:"token_version"`
    CreatedAt    int64  `json:"created_at"`
}
```

**JWT Claims Update:**

```go
// Updated Claims in internal/auth/jwt.go
type Claims struct {
    UserID       string `json:"user_id"`
    Username     string `json:"username"`
    TokenVersion int    `json:"token_version"`
    jwt.RegisteredClaims
}
```

**Validation Flow:**
1. `GenerateJWT` accepts `tokenVersion int` and embeds it in claims
2. `ValidateJWT` now requires a `db.DB` reference to look up current `token_version`
3. If `claims.TokenVersion != storedUser.TokenVersion`, reject token
4. `UpdatePassword` increments `TokenVersion` atomically

```go
func (d *DB) UpdatePasswordWithVersion(username, hash string) error {
    return d.db.Update(func(tx *bbolt.Tx) error {
        b := tx.Bucket(bucketUsers)
        data := b.Get([]byte(username))
        if data == nil {
            return fmt.Errorf("user not found")
        }
        var user User
        if err := json.Unmarshal(data, &user); err != nil {
            return err
        }
        user.PasswordHash = hash
        user.TokenVersion++
        updated, err := json.Marshal(user)
        if err != nil {
            return err
        }
        return b.Put([]byte(username), updated)
    })
}
```

### 1.4 WebSocket Origin Validation (`internal/server/routes.go`)

**Architecture:** Replace the permissive `CheckOrigin` function with a strict validator that compares the Origin header's host against the request's Host header.

```go
// checkOrigin validates WebSocket upgrade requests by comparing
// Origin header host against the request Host.
func checkOrigin(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    if origin == "" {
        return false
    }

    u, err := url.Parse(origin)
    if err != nil || u.Host == "" {
        return false
    }

    return u.Host == r.Host
}

var upgrader = websocket.Upgrader{
    CheckOrigin: checkOrigin,
}
```

### 1.5 Path Traversal Prevention (`internal/docker/fileops.go`)

**Architecture:** A `ValidatePath` function that canonicalizes and validates paths before any file operation.

```go
package docker

import (
    "fmt"
    "path/filepath"
    "strings"
)

// ValidatePath ensures the path is safe for container file operations.
// Returns the cleaned path or an error.
func ValidatePath(path string) (string, error) {
    // Reject null bytes and control characters
    for _, c := range path {
        if c == 0 || (c < 32 && c != '\t') {
            return "", fmt.Errorf("path contains invalid control character")
        }
    }

    cleaned := filepath.Clean(path)

    // Must be absolute
    if !strings.HasPrefix(cleaned, "/") {
        return "", fmt.Errorf("path must be absolute")
    }

    // After cleaning, no ".." should remain that escapes root
    // filepath.Clean resolves ".." but if the result still has ".." it means traversal
    if strings.Contains(cleaned, "..") {
        return "", fmt.Errorf("path traversal not allowed")
    }

    return cleaned, nil
}
```

### 1.6 Input Validation (`internal/validator/validator.go`)

**Architecture:** A standalone `validator` package with pure validation functions.

```go
package validator

import (
    "fmt"
    "regexp"
    "strings"
)

var (
    containerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]+$`)
    envVarNameRegex    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
    shellMetachars     = []string{";", "&", "|", "$", "`", "(", ")", "{", "}", "<", ">", "!", "\\", "'", "\"", "\n"}
)

func ValidateContainerName(name string) error {
    if len(name) == 0 || len(name) > 128 {
        return fmt.Errorf("container name must be 1-128 characters")
    }
    if !containerNameRegex.MatchString(name) {
        return fmt.Errorf("container name contains invalid characters")
    }
    return nil
}

func ValidateGitURL(url string) error {
    if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "git://") {
        return fmt.Errorf("git URL must use https:// or git:// scheme")
    }
    for _, meta := range shellMetachars {
        if strings.Contains(url, meta) {
            return fmt.Errorf("git URL contains invalid characters")
        }
    }
    return nil
}

func ValidateEnvVarName(name string) error {
    if len(name) == 0 || len(name) > 256 {
        return fmt.Errorf("env var name must be 1-256 characters")
    }
    if !envVarNameRegex.MatchString(name) {
        return fmt.Errorf("env var name contains invalid characters")
    }
    return nil
}
```

---

## Subsystem 2: Functional Completeness

### 2.1 Container File Write (`internal/docker/fileops.go`)

**Architecture:** Use `docker exec` with `sh -c 'printf "%s" "content" > path'` approach. Content is base64-encoded to avoid shell escaping issues.

```go
func (c *Client) WriteFile(ctx context.Context, containerID, path, content string) error {
    cleanPath, err := ValidatePath(path)
    if err != nil {
        return err
    }

    // Base64 encode content to avoid shell escaping issues
    encoded := base64.StdEncoding.EncodeToString([]byte(content))
    cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > '%s'", encoded, cleanPath)}

    output, err := execCommand(ctx, c.cli, containerID, cmd)
    if err != nil {
        return fmt.Errorf("file write failed: %w (output: %s)", err, output)
    }
    return nil
}
```

### 2.2 Full Compose Parser (`internal/docker/compose.go`)

**Architecture:** Replace the line-based parser with a proper YAML struct-based parser using `gopkg.in/yaml.v3`.

```go
package docker

import (
    "fmt"
    "strconv"
    "strings"

    "gopkg.in/yaml.v3"
)

type ComposeFile struct {
    Version  string                    `yaml:"version,omitempty"`
    Services map[string]ComposeService `yaml:"services"`
}

type ComposeService struct {
    Image       string            `yaml:"image"`
    Ports       []string          `yaml:"ports,omitempty"`
    Volumes     []string          `yaml:"volumes,omitempty"`
    Environment interface{}       `yaml:"environment,omitempty"`
    Networks    []string          `yaml:"networks,omitempty"`
    DependsOn   interface{}       `yaml:"depends_on,omitempty"`
    Restart     string            `yaml:"restart,omitempty"`
    Labels      map[string]string `yaml:"labels,omitempty"`
    Command     interface{}       `yaml:"command,omitempty"`
}

type PortBinding struct {
    HostPort      int
    ContainerPort int
    Protocol      string
}

type VolumeMount struct {
    HostPath      string
    ContainerPath string
    ReadOnly      bool
}

func ParseComposeFile(yamlContent string) (*ComposeFile, error) {
    var cf ComposeFile
    if err := yaml.Unmarshal([]byte(yamlContent), &cf); err != nil {
        return nil, fmt.Errorf("invalid compose YAML: %w", err)
    }
    if cf.Services == nil || len(cf.Services) == 0 {
        return nil, fmt.Errorf("no services defined in compose file")
    }
    return &cf, nil
}

func ParsePort(spec string) (PortBinding, error) {
    // Handles "8080:80", "8080:80/tcp", "80"
    pb := PortBinding{Protocol: "tcp"}
    
    // Check for protocol suffix
    if idx := strings.Index(spec, "/"); idx != -1 {
        pb.Protocol = spec[idx+1:]
        spec = spec[:idx]
    }

    parts := strings.Split(spec, ":")
    switch len(parts) {
    case 1:
        port, err := strconv.Atoi(parts[0])
        if err != nil {
            return pb, fmt.Errorf("invalid port: %s", spec)
        }
        pb.ContainerPort = port
        pb.HostPort = port
    case 2:
        host, err := strconv.Atoi(parts[0])
        if err != nil {
            return pb, fmt.Errorf("invalid host port: %s", parts[0])
        }
        container, err := strconv.Atoi(parts[1])
        if err != nil {
            return pb, fmt.Errorf("invalid container port: %s", parts[1])
        }
        pb.HostPort = host
        pb.ContainerPort = container
    default:
        return pb, fmt.Errorf("invalid port format: %s", spec)
    }
    return pb, nil
}

func ParseVolume(spec string) (VolumeMount, error) {
    vm := VolumeMount{}
    parts := strings.Split(spec, ":")
    switch len(parts) {
    case 1:
        vm.ContainerPath = parts[0]
    case 2:
        vm.HostPath = parts[0]
        vm.ContainerPath = parts[1]
    case 3:
        vm.HostPath = parts[0]
        vm.ContainerPath = parts[1]
        vm.ReadOnly = parts[2] == "ro"
    default:
        return vm, fmt.Errorf("invalid volume format: %s", spec)
    }
    return vm, nil
}

func ParseEnvironment(env interface{}) []string {
    switch v := env.(type) {
    case []interface{}:
        result := make([]string, 0, len(v))
        for _, item := range v {
            if s, ok := item.(string); ok {
                result = append(result, s)
            }
        }
        return result
    case map[string]interface{}:
        result := make([]string, 0, len(v))
        for key, val := range v {
            result = append(result, fmt.Sprintf("%s=%v", key, val))
        }
        return result
    default:
        return nil
    }
}

// ResolveStartOrder performs topological sort on services based on depends_on.
func ResolveStartOrder(cf *ComposeFile) ([]string, error) {
    deps := make(map[string][]string)
    for name, svc := range cf.Services {
        deps[name] = parseDependsOn(svc.DependsOn)
    }
    return topologicalSort(deps)
}

func parseDependsOn(dep interface{}) []string {
    switch v := dep.(type) {
    case []interface{}:
        result := make([]string, 0, len(v))
        for _, d := range v {
            if s, ok := d.(string); ok {
                result = append(result, s)
            }
        }
        return result
    case map[string]interface{}:
        result := make([]string, 0, len(v))
        for name := range v {
            result = append(result, name)
        }
        return result
    default:
        return nil
    }
}

func topologicalSort(deps map[string][]string) ([]string, error) {
    visited := make(map[string]bool)
    inStack := make(map[string]bool)
    var order []string

    var visit func(string) error
    visit = func(node string) error {
        if inStack[node] {
            return fmt.Errorf("circular dependency detected at %s", node)
        }
        if visited[node] {
            return nil
        }
        inStack[node] = true
        for _, dep := range deps[node] {
            if err := visit(dep); err != nil {
                return err
            }
        }
        inStack[node] = false
        visited[node] = true
        order = append(order, node)
        return nil
    }

    for name := range deps {
        if err := visit(name); err != nil {
            return nil, err
        }
    }
    return order, nil
}
```

### 2.3 System Info Endpoint (`internal/server/routes.go`)

**Architecture:** Use Go `runtime` and `os` packages for host metrics, Docker SDK `ServerVersion` for daemon info.

```go
package server

import (
    "os"
    "runtime"
    "syscall"
)

type SystemInfo struct {
    Hostname      string `json:"hostname"`
    OS            string `json:"os"`
    CPUCores      int    `json:"cpu_cores"`
    TotalRAM      uint64 `json:"total_ram"`
    UsedRAM       uint64 `json:"used_ram"`
    TotalDisk     uint64 `json:"total_disk"`
    UsedDisk      uint64 `json:"used_disk"`
    DockerVersion string `json:"docker_version"`
}

func getSystemInfo(dockerClient *docker.Client) SystemInfo {
    hostname, _ := os.Hostname()

    var sysinfo syscall.Sysinfo_t
    syscall.Sysinfo(&sysinfo)

    var stat syscall.Statfs_t
    syscall.Statfs("/", &stat)

    totalDisk := stat.Blocks * uint64(stat.Bsize)
    freeDisk := stat.Bfree * uint64(stat.Bsize)

    dockerVersion := ""
    if ver, err := dockerClient.ServerVersion(context.Background()); err == nil {
        dockerVersion = ver
    }

    return SystemInfo{
        Hostname:      hostname,
        OS:            runtime.GOOS,
        CPUCores:      runtime.NumCPU(),
        TotalRAM:      sysinfo.Totalram,
        UsedRAM:       sysinfo.Totalram - sysinfo.Freeram,
        TotalDisk:     totalDisk,
        UsedDisk:      totalDisk - freeDisk,
        DockerVersion: dockerVersion,
    }
}
```

### 2.4 WebSocket Stats Streaming (`internal/server/routes.go`)

**Architecture:** A dedicated WebSocket endpoint that starts a stats collection goroutine per connection. Uses Docker `ContainerStats` with `Stream: false` on a 2-second ticker. Goroutine exits on client disconnect or context cancellation.

```go
// GET /api/containers/:id/stats/ws
func handleStatsStream(c *gin.Context, dockerClient *docker.Client) {
    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        return
    }
    defer conn.Close()

    containerID := c.Param("id")
    ctx, cancel := context.WithCancel(c.Request.Context())
    defer cancel()

    // Monitor for client disconnect
    go func() {
        for {
            if _, _, err := conn.ReadMessage(); err != nil {
                cancel()
                return
            }
        }
    }()

    ticker := time.NewTicker(2 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            stats, err := dockerClient.GetContainerStats(ctx, containerID)
            if err != nil {
                conn.WriteJSON(gin.H{"error": err.Error()})
                return
            }
            if err := conn.WriteJSON(stats); err != nil {
                return
            }
        }
    }
}
```

---

## Subsystem 3: Operational Reliability

### 3.1 Container Auto-Recovery (`internal/docker/recovery.go`)

**Architecture:** A background goroutine with a `time.Ticker` (60s interval). Filters containers by label `dockara.auto-recover=true` and state `exited` or `dead`. Tracks consecutive failures per container with a max of 3 retries per cycle.

```go
package docker

import (
    "context"
    "log"
    "sync"
    "time"
)

type HealthMonitor struct {
    client      *Client
    ticker      *time.Ticker
    stop        chan struct{}
    failures    map[string]int // containerID -> consecutive failure count
    failuresMu  sync.Mutex
}

func NewHealthMonitor(client *Client) *HealthMonitor {
    return &HealthMonitor{
        client:   client,
        ticker:   time.NewTicker(60 * time.Second),
        stop:     make(chan struct{}),
        failures: make(map[string]int),
    }
}

func (hm *HealthMonitor) Start() {
    go hm.run()
}

func (hm *HealthMonitor) Stop() {
    close(hm.stop)
    hm.ticker.Stop()
}

func (hm *HealthMonitor) run() {
    for {
        select {
        case <-hm.stop:
            return
        case <-hm.ticker.C:
            hm.check()
        }
    }
}

func (hm *HealthMonitor) check() {
    ctx := context.Background()
    containers, err := hm.client.ListContainersWithLabel(ctx, "dockara.auto-recover=true")
    if err != nil {
        log.Printf("[recovery] failed to list containers: %v", err)
        return
    }

    // Reset failure counts for new cycle
    hm.failuresMu.Lock()
    hm.failures = make(map[string]int)
    hm.failuresMu.Unlock()

    for _, ctr := range containers {
        if ctr.State != "exited" && ctr.State != "dead" {
            continue
        }

        hm.failuresMu.Lock()
        attempts := hm.failures[ctr.ID]
        hm.failuresMu.Unlock()

        if attempts >= 3 {
            log.Printf("[recovery] CRITICAL: container %s failed 3 restart attempts, skipping", ctr.Name)
            continue
        }

        if err := hm.client.StartContainer(ctx, ctr.ID); err != nil {
            hm.failuresMu.Lock()
            hm.failures[ctr.ID]++
            hm.failuresMu.Unlock()
            log.Printf("[recovery] failed to restart %s (attempt %d): %v", ctr.Name, attempts+1, err)
        } else {
            log.Printf("[recovery] restarted container %s at %s", ctr.Name, time.Now().Format(time.RFC3339))
        }
    }
}
```

### 3.2 Log Rotation (`main.go`)

**Architecture:** Use `lumberjack` (or equivalent custom implementation) for log rotation. Configure as the output writer for Go's `log` package.

```go
package main

import (
    "log"
    "os"
    "fmt"
    "sort"
    "path/filepath"
    "io"
)

const (
    logFilePath   = "/opt/dockara/data/dockara.log"
    maxLogSize    = 2 * 1024 * 1024 // 2MB
    maxLogFiles   = 5
)

type LogRotator struct {
    file     *os.File
    path     string
    maxSize  int64
    maxFiles int
    size     int64
}

func NewLogRotator(path string) (*LogRotator, error) {
    lr := &LogRotator{
        path:     path,
        maxSize:  int64(maxLogSize),
        maxFiles: maxLogFiles,
    }

    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        return nil, err
    }

    f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }

    info, _ := f.Stat()
    lr.file = f
    lr.size = info.Size()
    return lr, nil
}

func (lr *LogRotator) Write(p []byte) (int, error) {
    if lr.size+int64(len(p)) > lr.maxSize {
        if err := lr.rotate(); err != nil {
            return 0, err
        }
    }
    n, err := lr.file.Write(p)
    lr.size += int64(n)
    return n, err
}

func (lr *LogRotator) rotate() error {
    lr.file.Close()

    // Shift existing files
    for i := lr.maxFiles; i >= 1; i-- {
        src := fmt.Sprintf("%s.%d", lr.path, i-1)
        dst := fmt.Sprintf("%s.%d", lr.path, i)
        if i == 1 {
            src = lr.path
        }
        os.Rename(src, dst)
    }

    // Delete oldest if exceeds max
    oldest := fmt.Sprintf("%s.%d", lr.path, lr.maxFiles+1)
    os.Remove(oldest)

    // Create new file
    f, err := os.OpenFile(lr.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
    if err != nil {
        return err
    }
    lr.file = f
    lr.size = 0
    return nil
}

func (lr *LogRotator) Close() error {
    return lr.file.Close()
}
```

### 3.3 Traefik Integration on Deploy (`internal/traefik/config.go`)

**Architecture:** Hook into the compose deploy endpoint. When a deployment includes a non-empty `domain` field, call `GenerateConfig`. When a service with a domain is removed, call `RemoveDomain`. The existing `GenerateConfig` already produces proper Traefik dynamic config with `websecure` entrypoint and `letsencrypt` cert resolver — no structural changes needed, only the integration hook in routes.

```go
// In deploy/compose handler (routes.go), after successful deployment:
if req.Domain != "" {
    port := extractFirstPort(req.Compose) // Extract from parsed compose
    if err := traefik.GenerateConfig(req.Domain, req.Name, port); err != nil {
        log.Printf("Warning: failed to generate traefik config: %v", err)
    }
}

// In service deletion handler:
if svc.Domain != "" {
    traefik.RemoveDomain(svc.Name)
}
```

### 3.4 Cloudflare Tunnel (`internal/tunnel/cloudflare.go`)

**Architecture:** A module that deploys and manages a `cloudflare/cloudflared:latest` container using the Docker SDK.

```go
package tunnel

import (
    "context"
    "fmt"
    "regexp"

    "github.com/moby/moby/api/types/container"
    "github.com/moby/moby/client"
)

const (
    cloudflaredImage     = "cloudflare/cloudflared:latest"
    cloudflaredContainer = "dockara-cloudflared"
)

var tokenRegex = regexp.MustCompile(`^[a-zA-Z0-9\-_.]+$`)

type CloudflareTunnel struct {
    docker *client.Client
}

func NewCloudflareTunnel(docker *client.Client) *CloudflareTunnel {
    return &CloudflareTunnel{docker: docker}
}

func ValidateTunnelToken(token string) error {
    if token == "" {
        return fmt.Errorf("tunnel token is required")
    }
    if !tokenRegex.MatchString(token) {
        return fmt.Errorf("tunnel token contains invalid characters")
    }
    return nil
}

func (ct *CloudflareTunnel) Deploy(ctx context.Context, token string) error {
    if err := ValidateTunnelToken(token); err != nil {
        return err
    }

    createOpts := client.ContainerCreateOptions{
        Name:  cloudflaredContainer,
        Image: cloudflaredImage,
        Config: &container.Config{
            Image: cloudflaredImage,
            Cmd:   []string{"tunnel", "--no-autoupdate", "run", "--token", token},
            Labels: map[string]string{
                "dockara.managed": "true",
            },
        },
        HostConfig: &container.HostConfig{
            RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
        },
    }

    result, err := ct.docker.ContainerCreate(ctx, createOpts)
    if err != nil {
        return fmt.Errorf("failed to create cloudflared container: %w", err)
    }

    if _, err := ct.docker.ContainerStart(ctx, result.ID, client.ContainerStartOptions{}); err != nil {
        return fmt.Errorf("failed to start cloudflared container: %w", err)
    }

    return nil
}

func (ct *CloudflareTunnel) Remove(ctx context.Context) error {
    timeout := 10
    ct.docker.ContainerStop(ctx, cloudflaredContainer, client.ContainerStopOptions{Timeout: &timeout})
    _, err := ct.docker.ContainerRemove(ctx, cloudflaredContainer, client.ContainerRemoveOptions{Force: true})
    return err
}
```

---

## Data Models

### Updated User (BBolt)

| Field | Type | Description |
|-------|------|-------------|
| id | string | Unique user ID |
| username | string | Login username |
| password_hash | string | bcrypt hash |
| token_version | int | Incremented on password change |
| created_at | int64 | Unix timestamp |

### ComposeFile Structs

| Struct | Fields | Description |
|--------|--------|-------------|
| ComposeFile | Version, Services | Top-level compose file |
| ComposeService | Image, Ports, Volumes, Environment, Networks, DependsOn, Restart, Labels, Command | Service definition |
| PortBinding | HostPort, ContainerPort, Protocol | Parsed port mapping |
| VolumeMount | HostPath, ContainerPath, ReadOnly | Parsed volume mount |

### SystemInfo Response

| Field | Type | Description |
|-------|------|-------------|
| hostname | string | Host machine name |
| os | string | Operating system |
| cpu_cores | int | Available CPU cores |
| total_ram | uint64 | Total RAM bytes |
| used_ram | uint64 | Used RAM bytes |
| total_disk | uint64 | Total disk bytes |
| used_disk | uint64 | Used disk bytes |
| docker_version | string | Docker daemon version |

---

## Error Handling

| Component | Error Type | Response |
|-----------|-----------|----------|
| Rate Limiter | Rate exceeded | HTTP 429 + Retry-After header |
| Path Validator | Traversal attempt | HTTP 400 + "path traversal not allowed" |
| Input Validator | Invalid input | HTTP 400 + field identification |
| Origin Check | Origin mismatch | HTTP 403 (WebSocket upgrade rejected) |
| Token Version | Version mismatch | HTTP 401 + "invalid or expired token" |
| Compose Parser | Invalid YAML | HTTP 400 + parse error description |
| Tunnel Token | Empty/malformed | HTTP 400 + "invalid tunnel token" |
| File Write | Container error | HTTP 500 + error description |

---

## Testing Strategy

**Property-Based Tests (Go `testing/quick` or `rapid`):**
- All 18 correctness properties below are validated via property-based tests with minimum 100 iterations each
- Pure functions (validators, parsers, rate limiter logic) are tested with generated inputs covering edge cases
- Round-trip properties verify serialization/parsing consistency

**Unit Tests:**
- Specific examples for error handling paths (invalid YAML, malformed tokens)
- Integration points between components (middleware chaining, handler → validator)
- Edge cases: empty inputs, boundary lengths, concurrent access to rate limiter

**Integration Tests:**
- WebSocket upgrade flow with valid/invalid origins
- End-to-end compose deploy with Traefik config generation
- Health monitor restart cycle with mocked Docker client

---

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system — essentially, a formal statement about what the system should do. Properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

### Property 1: WebSocket Origin Validation

*For any* HTTP request with an Origin header, the WebSocket origin checker SHALL allow the upgrade if and only if the Origin header's host portion exactly matches the request's Host header; requests with empty, missing, or non-matching Origin headers SHALL be rejected.

**Validates: Requirements 2.1, 2.2, 2.3, 2.4**

### Property 2: Secret Generation Format

*For any* invocation of the secret generation function, the output SHALL be a valid 64-character hexadecimal string (representing 32 random bytes).

**Validates: Requirements 3.4**

### Property 3: Rate Limiter Enforcement

*For any* IP address and any sequence of N requests where N > 5 within a 1-minute window, the rate limiter SHALL reject requests 6 through N.

**Validates: Requirements 4.2**

### Property 4: Rate Limiter Window Expiration

*For any* IP address that has been rate-limited, after the full 1-minute window has elapsed since the first tracked request, the rate limiter SHALL allow new requests from that IP.

**Validates: Requirements 4.4**

### Property 5: Token Version Increment

*For any* user and any number N of password changes, the user's token_version SHALL equal the initial token_version plus N.

**Validates: Requirements 5.2**

### Property 6: Token Version Round-Trip

*For any* user with token_version V, a JWT generated with version V SHALL pass validation, and a JWT generated with any version W ≠ V SHALL be rejected during validation when the stored version is V.

**Validates: Requirements 5.3, 5.4, 5.5**

### Property 7: Path Traversal Prevention

*For any* input path string, the path validator SHALL reject it if the cleaned path contains ".." segments, does not start with "/", or contains null bytes or control characters; otherwise it SHALL return the filepath.Clean result.

**Validates: Requirements 6.1, 6.2, 6.3, 6.4**

### Property 8: Container Name Validation

*For any* string, the container name validator SHALL accept it if and only if it matches `^[a-zA-Z0-9][a-zA-Z0-9_.\-]+$` and has length ≤ 128.

**Validates: Requirements 7.1**

### Property 9: Git URL Validation

*For any* string, the git URL validator SHALL accept it if and only if it starts with "https://" or "git://" and contains no shell metacharacters.

**Validates: Requirements 7.2**

### Property 10: Environment Variable Name Validation

*For any* string, the env var name validator SHALL accept it if and only if it matches `^[a-zA-Z_][a-zA-Z0-9_]*$` and has length ≤ 256.

**Validates: Requirements 7.3**

### Property 11: Compose Port and Volume Parsing

*For any* valid port mapping string in short syntax "host:container" or "container" format, ParsePort SHALL produce a PortBinding with correct host and container port values. *For any* valid volume string in "host:container" or "host:container:mode" format, ParseVolume SHALL produce a VolumeMount with correct paths and read-only flag.

**Validates: Requirements 8.2, 8.3**

### Property 12: Compose Dependency Ordering

*For any* valid compose file with a DAG of depends_on relationships, ResolveStartOrder SHALL return an ordering where every service appears after all of its dependencies.

**Validates: Requirements 8.6**

### Property 13: Invalid YAML Rejection

*For any* input string that is not valid YAML or does not contain a "services" key, ParseComposeFile SHALL return a non-nil error.

**Validates: Requirements 8.7**

### Property 14: Traefik Config Structure

*For any* non-empty domain string, service name, and valid port number, GenerateConfig SHALL produce a YAML file containing a router with `websecure` entrypoint and `letsencrypt` certResolver for that domain.

**Validates: Requirements 12.3**

### Property 15: Log File Retention Invariant

*For any* sequence of log rotation events, the total number of rotated log files on disk SHALL never exceed 5.

**Validates: Requirements 13.2, 13.4**

### Property 16: Tunnel Token Validation

*For any* empty string or string containing characters outside `[a-zA-Z0-9\-_.]`, ValidateTunnelToken SHALL return a non-nil error.

**Validates: Requirements 14.4**

### Property 17: Auto-Recovery Retry Limit

*For any* container that fails to restart, the Health Monitor SHALL attempt at most 3 restarts per health check cycle before stopping retries for that container.

**Validates: Requirements 11.4**

### Property 18: File Write Command Safety

*For any* file content string and valid path, the constructed shell command for file writing SHALL not allow shell injection — the content must be transported via base64 encoding to avoid interpretation by the shell.

**Validates: Requirements 1.4**
