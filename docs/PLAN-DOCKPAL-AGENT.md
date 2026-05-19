# DockPal Agent — Implementation Plan

> Detailed plan for the DockPal Agent binary.
> This is a separate Go project (`github.com/sdldev/dockpal-agent`) that runs on each managed host.

---

## 1. Overview

The agent is a **minimal Docker proxy + host reporter**. It has no UI, no database, no templates, no Traefik config, no tunnel management, no self-update. It does exactly one thing: expose the Docker daemon and host info to the DockPal Server over a secure channel.

### Design Constraints

| Constraint | Target |
|------------|--------|
| Binary size | < 10 MB |
| Idle RAM | < 20 MB |
| Dependencies | Go 1.25, Docker SDK, Gin, Gorilla WebSocket |
| Deployment | Docker container (primary), systemd binary (secondary) |
| Network | Direct mode: listen on port; Edge mode: outbound WebSocket only |

---

## 2. Project Structure

```
dockpal-agent/
├── main.go                      # Entry point, CLI, mode selection
├── internal/
│   ├── config/
│   │   └── config.go            # Config parsing from env vars
│   ├── auth/
│   │   └── middleware.go        # Token authentication middleware
│   ├── docker/
│   │   ├── client.go            # Docker SDK client wrapper
│   │   ├── containers.go        # Container proxy handlers
│   │   ├── compose.go           # Compose parser + deploy
│   │   ├── images.go            # Image proxy handlers
│   │   ├── files.go             # File operations (exec-based)
│   │   └── types.go             # Shared Docker types (mirrors Server)
│   ├── host/
│   │   ├── info.go              # Static host info (OS, CPU, RAM, Docker version)
│   │   └── stats.go             # Real-time host stats (CPU, RAM, disk)
│   ├── server/
│   │   ├── server.go            # HTTP server setup (direct mode)
│   │   └── routes.go            # Route registration
│   ├── edge/
│   │   ├── client.go            # WebSocket client (edge mode)
│   │   └── protocol.go          # Request/response message types
│   └── enroll/
│       └── handshake.go         # Enrollment handshake logic
├── Dockerfile
├── go.mod
├── go.sum
└── README.md
```

---

## 3. Configuration

### File: `internal/config/config.go`

All config comes from environment variables (ideal for Docker deployment).

```go
package config

import (
    "fmt"
    "os"
    "strconv"
    "time"
)

type Config struct {
    Mode           string        // "direct" or "edge"
    Token          string        // Enrollment token from Server
    DirectListen   string        // e.g. ":9273"
    DirectTLS      bool          // Enable TLS for direct mode
    TLSCertDir     string        // Directory for TLS certs (auto-generated if empty)
    EdgeServerURL  string        // e.g. "wss://dockpal.example.com:3012"
    EdgeReconnect  time.Duration // Reconnect interval
    EdgeHeartbeat  time.Duration // Heartbeat ping interval
    DockerSocket   string        // e.g. "/var/run/docker.sock"
}

func Load() (*Config, error) {
    mode := os.Getenv("DOCKPAL_MODE")
    if mode != "direct" && mode != "edge" {
        return nil, fmt.Errorf("DOCKPAL_MODE must be 'direct' or 'edge', got %q", mode)
    }

    token := os.Getenv("DOCKPAL_TOKEN")
    if token == "" {
        return nil, fmt.Errorf("DOCKPAL_TOKEN is required")
    }

    cfg := &Config{
        Mode:         mode,
        Token:        token,
        DirectListen: getEnv("DOCKPAL_DIRECT_LISTEN", ":9273"),
        DirectTLS:    getEnvBool("DOCKPAL_DIRECT_TLS", true),
        TLSCertDir:   getEnv("DOCKPAL_TLS_CERT_DIR", ""),
        EdgeServerURL: os.Getenv("DOCKPAL_EDGE_SERVER"),
        EdgeReconnect: getEnvDuration("DOCKPAL_EDGE_RECONNECT", 5*time.Second),
        EdgeHeartbeat: getEnvDuration("DOCKPAL_EDGE_HEARTBEAT", 30*time.Second),
        DockerSocket:  getEnv("DOCKPAL_DOCKER_SOCKET", "/var/run/docker.sock"),
    }

    if mode == "edge" && cfg.EdgeServerURL == "" {
        return nil, fmt.Errorf("DOCKPAL_EDGE_SERVER is required for edge mode")
    }

    return cfg, nil
}

// Helper functions: getEnv, getEnvBool, getEnvDuration
```

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DOCKPAL_MODE` | yes | — | `direct` or `edge` |
| `DOCKPAL_TOKEN` | yes | — | Enrollment token from Server |
| `DOCKPAL_DIRECT_LISTEN` | no | `:9273` | Listen address (direct mode) |
| `DOCKPAL_DIRECT_TLS` | no | `true` | Enable TLS (direct mode) |
| `DOCKPAL_TLS_CERT_DIR` | no | auto | Directory for TLS certs |
| `DOCKPAL_EDGE_SERVER` | edge only | — | Server URL, e.g. `wss://dockpal.example.com:3012` |
| `DOCKPAL_EDGE_RECONNECT` | no | `5s` | Reconnect interval on disconnect |
| `DOCKPAL_EDGE_HEARTBEAT` | no | `30s` | Heartbeat ping interval |
| `DOCKPAL_DOCKER_SOCKET` | no | `/var/run/docker.sock` | Docker daemon socket path |

---

## 4. Entry Point

### File: `main.go`

```go
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/sdldev/dockpal-agent/internal/config"
    "github.com/sdldev/dockpal-agent/internal/docker"
    "github.com/sdldev/dockpal-agent/internal/edge"
    "github.com/sdldev/dockpal-agent/internal/server"
)

const version = "0.1.0"

func main() {
    if len(os.Args) >= 2 {
        switch os.Args[1] {
        case "version":
            fmt.Printf("dockpal-agent v%s\n", version)
            return
        case "help":
            fmt.Println("Dockpal Agent — Lightweight Docker proxy for remote management")
            fmt.Println()
            fmt.Println("Configuration via environment variables. See README.")
            return
        }
    }

    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("Config error: %v", err)
    }

    dockerClient, err := docker.NewClient(cfg.DockerSocket)
    if err != nil {
        log.Fatalf("Docker client error: %v", err)
    }
    defer dockerClient.Close()

    if err := dockerClient.Ping(); err != nil {
        log.Fatalf("Docker daemon unreachable: %v", err)
    }

    log.Printf("Dockpal Agent v%s starting in %s mode", version, cfg.Mode)

    switch cfg.Mode {
    case "direct":
        runDirect(cfg, dockerClient)
    case "edge":
        runEdge(cfg, dockerClient)
    }
}

func runDirect(cfg *config.Config, dockerClient *docker.Client) {
    srv := server.New(cfg, dockerClient)
    if err := srv.Run(); err != nil {
        log.Fatalf("Server error: %v", err)
    }
}

func runEdge(cfg *config.Config, dockerClient *docker.Client) {
    client := edge.NewClient(cfg, dockerClient)
    if err := client.Run(); err != nil {
        log.Fatalf("Edge client error: %v", err)
    }
}
```

---

## 5. Docker Proxy Layer

The agent mirrors a subset of the DockPal Server's Docker operations. It uses the same Docker SDK and the same data types so the Server can decode the JSON responses without translation.

### File: `internal/docker/types.go`

Mirror the types from DockPal Server's `internal/docker/` package. These must be **identical** in structure and JSON tags so the Server can unmarshal agent responses directly.

```go
package docker

// These types must match the Server's internal/docker types exactly.

type ContainerInfo struct {
    ID      string                  `json:"id"`
    Name    string                  `json:"name"`
    Image   string                  `json:"image"`
    Status  string                  `json:"status"`
    State   string                  `json:"state"`
    Ports   []container.PortSummary `json:"ports"`
    Created int64                   `json:"created"`
}

type ContainerDetail struct {
    ContainerInfo
    Platform      string                 `json:"platform"`
    Env           []string               `json:"env"`
    Mounts        []container.MountPoint `json:"mounts"`
    NetworkMode   string                 `json:"network_mode"`
    RestartPolicy string                 `json:"restart_policy"`
    Networks      map[string]string      `json:"networks"`
    MemoryLimit   int64                  `json:"memory_limit"`
    NanoCPUs      int64                  `json:"nano_cpus"`
}

type ContainerStats struct {
    CPUPercent    float64 `json:"cpu_percent"`
    MemoryUsage   uint64  `json:"memory_usage"`
    MemoryLimit   uint64  `json:"memory_limit"`
    MemoryPercent float64 `json:"memory_percent"`
    NetworkRx     uint64  `json:"network_rx"`
    NetworkTx     uint64  `json:"network_tx"`
}

type ContainerEditRequest struct {
    Name          *string    `json:"name,omitempty"`
    RestartPolicy *string    `json:"restart_policy,omitempty"`
    MemoryLimit   *int64     `json:"memory_limit,omitempty"`
    CPULimit      *float64   `json:"cpu_limit,omitempty"`
    Image         *string    `json:"image,omitempty"`
    Env           *[]string  `json:"env,omitempty"`
    Ports         *[]PortMapping  `json:"ports,omitempty"`
    Volumes       *[]VolumeMapping `json:"volumes,omitempty"`
}

type PortMapping struct {
    HostPort      int    `json:"host_port"`
    ContainerPort int    `json:"container_port"`
    Protocol      string `json:"protocol"`
}

type VolumeMapping struct {
    HostPath      string `json:"host_path"`
    ContainerPath string `json:"container_path"`
    ReadOnly      bool   `json:"read_only"`
}

type ImageInfo struct {
    ID      string `json:"id"`
    Repo    string `json:"repo"`
    Tag     string `json:"tag"`
    Size    string `json:"size"`
    Created string `json:"created"`
}

type FileInfo struct {
    Name  string `json:"name"`
    Size  string `json:"size"`
    IsDir bool   `json:"is_dir"`
}

// DeployEvent mirrors the Server's DeployEvent for streaming deploys
type DeployEvent struct {
    Step    string `json:"step"`
    Message string `json:"message"`
    Status  string `json:"status"`
    Time    string `json:"time"`
}

// AuthHeaderFunc — same signature as Server
type AuthHeaderFunc func(imageRef string) (string, error)
```

### File: `internal/docker/client.go`

```go
package docker

import (
    "context"
    "fmt"

    "github.com/moby/moby/client"
)

type Client struct {
    cli *client.Client
}

func NewClient(socketPath string) (*Client, error) {
    // Set DOCKER_HOST if custom socket
    if socketPath != "" && socketPath != "/var/run/docker.sock" {
        os.Setenv("DOCKER_HOST", "unix://"+socketPath)
    }

    cli, err := client.New(client.FromEnv)
    if err != nil {
        return nil, fmt.Errorf("failed to create docker client: %w", err)
    }
    return &Client{cli: cli}, nil
}

func (c *Client) Close() error {
    return c.cli.Close()
}

func (c *Client) Ping(ctx context.Context) error {
    _, err := c.cli.Ping(ctx, client.PingOptions{})
    return err
}

// RawClient returns the underlying Docker client (needed by some operations)
func (c *Client) RawClient() *client.Client {
    return c.cli
}
```

### File: `internal/docker/containers.go`

Container operations. These are the same Docker SDK calls as in the DockPal Server, but exposed as HTTP handlers for the agent API.

The logic is **identical** to the Server's `internal/docker/container.go` — same Docker SDK calls, same data transformations, same JSON output. This is intentional: the Server must be able to decode agent responses without any translation layer.

Key methods on `Client`:

```go
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerInfo, error)
func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerDetail, error)
func (c *Client) StartContainer(ctx context.Context, id string) error
func (c *Client) StopContainer(ctx context.Context, id string) error
func (c *Client) RestartContainer(ctx context.Context, id string) error
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error
func (c *Client) EditContainer(ctx context.Context, id string, req ContainerEditRequest) (*ContainerDetail, error)
func (c *Client) GetContainerStats(ctx context.Context, id string) (*ContainerStats, error)
func (c *Client) ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error)
func (c *Client) ServerVersion(ctx context.Context) (string, error)
```

**Implementation note:** Copy the method bodies from the Server's `container.go`. The Docker SDK calls are the same. Only the `Client` struct definition differs slightly (same `cli` field).

### File: `internal/docker/compose.go`

Compose deploy. Same logic as Server's `compose.go` and `deploy_stream.go`.

Key methods:

```go
// Compose parsing (same as Server)
func ParseComposeFile(yamlContent string) (*ComposeFile, error)
func ParsePort(spec string) (PortBinding, error)
func ParseVolume(spec string) (VolumeMount, error)
func ParseEnvironment(env interface{}) []string
func ResolveStartOrder(cf *ComposeFile) ([]string, error)

// Deploy operations
func (c *Client) DeployCompose(ctx context.Context, projectName, composeYAML string, getAuthHeader AuthHeaderFunc) error
func (c *Client) DeployComposeStreamed(ctx context.Context, projectName, composeYAML string, session *DeploySession, getAuthHeader AuthHeaderFunc) error
func (c *Client) StopCompose(ctx context.Context, projectName string) error
func (c *Client) RemoveCompose(ctx context.Context, projectName string) error
```

**Compose file storage:** Agent writes compose files to `/opt/dockpal-agent/compose/{projectName}/docker-compose.yml`. This uses a **different base path** than the Server (`/opt/dockpal/compose/`) to prevent conflicts if both Server and Agent run on the same host (e.g., during development or when the local instance also has an agent for testing).

**DeploySession + DeployManager:** Same implementation as Server's `deploy_stream.go` — the agent needs its own session tracking for streamed deploys.

**Auth header handling:** The agent receives auth credentials from the Server in the deploy request body as a **map** (domain → auth header). This supports compose files with images from multiple registries. The agent does NOT store credentials. The `getAuthHeader` function is constructed from the request's `registry_auths` field:

```go
// In the deploy handler:
var req struct {
    Name          string            `json:"name"`
    Compose       string            `json:"compose"`
    RegistryAuths map[string]string `json:"registry_auths,omitempty"` // domain → base64 auth header
}

// Build AuthHeaderFunc from the map
getAuthHeader := func(imageRef string) (string, error) {
    domain := extractImageDomain(imageRef)
    if domain == "" {
        return "", nil
    }
    if auth, ok := req.RegistryAuths[strings.ToLower(domain)]; ok {
        return auth, nil
    }
    return "", nil // no auth for this domain — pull as public
}
```

This allows a single deploy to authenticate against multiple registries simultaneously (e.g., `ghcr.io` for the app image and `registry.internal.com` for a sidecar).

### File: `internal/docker/images.go`

Image operations. Same as Server.

```go
func (c *Client) ListImages(ctx context.Context) ([]ImageInfo, error)
func (c *Client) PullImage(ctx context.Context, image string) error
func (c *Client) PullImageWithAuth(ctx context.Context, image, registryAuth string) error
func (c *Client) RemoveImage(ctx context.Context, id string) error
```

### File: `internal/docker/files.go`

File operations inside containers (exec-based). Same as Server's `fileops.go`.

```go
func (c *Client) ListFiles(ctx context.Context, containerID, path string) ([]FileInfo, error)
func (c *Client) ReadFile(ctx context.Context, containerID, path string) (string, error)
func (c *Client) WriteFile(ctx context.Context, containerID, path, content string) error
func (c *Client) DeleteFile(ctx context.Context, containerID, path string) error
```

---

## 6. Host Info & Stats

### File: `internal/host/info.go`

Collects static host information. Same logic as Server's `getSystemInfo()`.

```go
package host

type HostInfo struct {
    Hostname      string `json:"hostname"`
    OS            string `json:"os"`
    CPUCores      int    `json:"cpu_cores"`
    TotalMemory   uint64 `json:"total_memory"`
    DockerVersion string `json:"docker_version"`
}

func GetHostInfo(dockerVersion string) *HostInfo {
    hostname, _ := os.Hostname()
    totalRAM, _ := getMemoryTotal()  // same cgroup/procfs logic as Server

    return &HostInfo{
        Hostname:      hostname,
        OS:            runtime.GOOS,
        CPUCores:      runtime.NumCPU(),
        TotalMemory:   totalRAM,
        DockerVersion: dockerVersion,
    }
}
```

### File: `internal/host/stats.go`

Collects real-time host stats. Same logic as Server's `getCPUPercent()`, `getMemoryInfo()`.

```go
package host

type HostStats struct {
    CPUPercent float64 `json:"cpu_percent"`
    TotalRAM   uint64  `json:"total_ram"`
    UsedRAM    uint64  `json:"used_ram"`
    TotalDisk  uint64  `json:"total_disk"`
    UsedDisk   uint64  `json:"used_disk"`
}

func GetHostStats() *HostStats {
    totalRAM, usedRAM := getMemoryInfo()
    totalDisk, usedDisk := getDiskInfo()
    cpuPercent := getCPUPercent()

    return &HostStats{
        CPUPercent: cpuPercent,
        TotalRAM:   totalRAM,
        UsedRAM:    usedRAM,
        TotalDisk:  totalDisk,
        UsedDisk:   usedDisk,
    }
}
```

**Memory info:** Same cgroup v2 → cgroup v1 → /proc/meminfo → syscall fallback chain as the Server.

**CPU info:** Same /proc/stat dual-read with 200ms interval.

**Disk info:** Same `syscall.Statfs` call.

---

## 7. Authentication

### File: `internal/auth/middleware.go`

Token-based authentication for agent API endpoints.

```go
package auth

import (
    "net/http"
    "strings"
    "github.com/gin-gonic/gin"
)

func TokenMiddleware(token string) gin.HandlerFunc {
    return func(c *gin.Context) {
        // Check Authorization header
        authHeader := c.GetHeader("Authorization")
        if authHeader == "" {
            // For WebSocket, check query param
            if c.Query("token") == token {
                c.Next()
                return
            }
            c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization"})
            c.Abort()
            return
        }

        parts := strings.SplitN(authHeader, " ", 2)
        if len(parts) != 2 || parts[1] != token {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
            c.Abort()
            return
        }

        c.Next()
    }
}
```

**Note:** The token comparison is a simple string equality check. The Server stores a bcrypt hash, but the agent stores and compares the plaintext token. This is acceptable because:
- The token is a random 32-byte value, not a user-chosen password
- The agent is on the remote host — if compromised, the token is already exposed
- Bcrypt comparison would require the agent to store the plaintext anyway (since it can't hash the incoming request token and compare to another hash without knowing the original)

---

## 8. Direct Mode — HTTP Server

### File: `internal/server/server.go`

```go
package server

type Server struct {
    cfg     *config.Config
    docker  *docker.Client
    router  *gin.Engine
    httpSrv *http.Server
}

func New(cfg *config.Config, dockerClient *docker.Client) *Server {
    gin.SetMode(gin.ReleaseMode)
    router := gin.New()
    router.Use(gin.Recovery())

    srv := &Server{
        cfg:    cfg,
        docker: dockerClient,
        router: router,
    }

    srv.registerRoutes()
    return srv
}

func (s *Server) Run() error {
    // TLS setup
    var listener net.Listener
    var err error

    if s.cfg.DirectTLS {
        cert, key, err := tls.EnsureCerts(s.cfg.TLSCertDir)
        // ... auto-generate self-signed cert if needed
        listener, err = tls.Listen("tcp", s.cfg.DirectListen, &tls.Config{
            Certificates: []tls.Certificate{cert},
            MinVersion:   tls.VersionTLS12,
        })
    } else {
        listener, err = net.Listen("tcp", s.cfg.DirectListen)
    }

    s.httpSrv = &http.Server{
        Handler: s.router,
    }

    log.Printf("Agent listening on %s (TLS: %v)", s.cfg.DirectListen, s.cfg.DirectTLS)
    return s.httpSrv.Serve(listener)
}
```

### File: `internal/server/routes.go`

All agent API endpoints. Most endpoints require `Authorization: Bearer <token>`. The `/agent/ping` endpoint is **unauthenticated** for Docker HEALTHCHECK compatibility.

```go
func (s *Server) registerRoutes() {
    // Unauthenticated health check (for Docker HEALTHCHECK)
    s.router.GET("/agent/ping", s.handlePing)

    // Authenticated agent routes
    agent := s.router.Group("/agent")
    agent.Use(auth.TokenMiddleware(s.cfg.Token))

    // Enrollment
    agent.POST("/enroll", s.handleEnroll)

    // Docker proxy
    docker := agent.Group("/docker")

    // Containers
    docker.GET("/containers", s.handleListContainers)
    docker.GET("/containers/:id", s.handleInspectContainer)
    docker.POST("/containers/:id/start", s.handleStartContainer)
    docker.POST("/containers/:id/stop", s.handleStopContainer)
    docker.POST("/containers/:id/restart", s.handleRestartContainer)
    docker.DELETE("/containers/:id", s.handleRemoveContainer)
    docker.PUT("/containers/:id", s.handleEditContainer)
    docker.GET("/containers/:id/stats", s.handleGetContainerStats)
    docker.GET("/containers/:id/logs", s.handleContainerLogs)        // WebSocket
    docker.GET("/containers/:id/stats/ws", s.handleStatsStream)     // WebSocket

    // Deploy
    docker.POST("/deploy/compose", s.handleDeployCompose)
    docker.POST("/deploy/stream", s.handleDeployStream)
    docker.POST("/compose/stop", s.handleStopCompose)
    docker.POST("/compose/remove", s.handleRemoveCompose)

    // Images
    docker.GET("/images", s.handleListImages)
    docker.POST("/images/pull", s.handlePullImage)
    docker.DELETE("/images/:id", s.handleRemoveImage)

    // Files
    docker.GET("/files", s.handleListFiles)
    docker.GET("/files/read", s.handleReadFile)
    docker.POST("/files/write", s.handleWriteFile)
    docker.POST("/files/upload", s.handleUploadFile)
    docker.GET("/files/download", s.handleDownloadFile)
    docker.DELETE("/files", s.handleDeleteFile)
    docker.POST("/containers/:id/files/write", s.handleContainerFileWrite)

    // Host
    agent.GET("/host/info", s.handleHostInfo)
    agent.GET("/host/stats", s.handleHostStats)
}
```

### Handler Implementations

Each handler is a thin wrapper that:
1. Parses request parameters
2. Calls the corresponding `docker.Client` method
3. Returns the JSON response

Example:

```go
func (s *Server) handleListContainers(c *gin.Context) {
    all := c.Query("all") == "true"
    containers, err := s.docker.ListContainers(c.Request.Context(), all)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
        return
    }
    c.JSON(http.StatusOK, containers)
}

func (s *Server) handleStartContainer(c *gin.Context) {
    if err := s.docker.StartContainer(c.Request.Context(), c.Param("id")); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
        return
    }
    c.JSON(http.StatusOK, gin.H{"status": "started"})
}
```

**WebSocket handlers** (logs, stats stream):

```go
func (s *Server) handleContainerLogs(c *gin.Context) {
    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        return
    }
    defer conn.Close()

    reader, err := s.docker.ContainerLogs(c.Request.Context(), c.Param("id"), "100")
    if err != nil {
        conn.WriteMessage(websocket.TextMessage, []byte("Error: failed to retrieve logs"))
        return
    }
    defer reader.Close()

    buf := make([]byte, 4096)
    for {
        n, err := reader.Read(buf)
        if n > 0 {
            conn.WriteMessage(websocket.TextMessage, buf[:n])
        }
        if err != nil {
            break
        }
    }
}
```

### TLS Certificate Handling

For direct mode, the agent needs a TLS certificate. Two options:

1. **Auto-generate self-signed cert** on first start. The Server's DirectClient will need to accept self-signed certs (using `InsecureSkipVerify` or a custom TLS config that trusts the agent's CA).

2. **Custom cert** provided by the user via volume mount.

Implementation in `server.go`:

```go
func ensureCerts(certDir string) (tls.Certificate, error) {
    if certDir == "" {
        certDir = "/etc/dockpal/agent/certs"
    }

    certFile := filepath.Join(certDir, "agent.crt")
    keyFile := filepath.Join(certDir, "agent.key")

    // If certs exist, load them
    if _, err := os.Stat(certFile); err == nil {
        return tls.LoadX509KeyPair(certFile, keyFile)
    }

    // Auto-generate self-signed cert
    if err := os.MkdirAll(certDir, 0700); err != nil {
        return tls.Certificate{}, err
    }

    // Generate using crypto/tls or a library
    // ... (ECDSA P-256, valid for 365 days, SAN: agent IP/hostname)

    return tls.LoadX509KeyPair(certFile, keyFile)
}
```

---

## 9. Edge Mode — WebSocket Client

### File: `internal/edge/protocol.go`

Message types for the edge mode WebSocket protocol.

```go
package edge

// AgentRequest is a request sent from Server to Agent over the WebSocket.
type AgentRequest struct {
    RequestID string          `json:"request_id"`
    Method    string          `json:"method"`     // "GET", "POST", "PUT", "DELETE", "WS"
    Path      string          `json:"path"`       // e.g. "/docker/containers"
    Query     map[string]string `json:"query,omitempty"`
    Body      json.RawMessage `json:"body,omitempty"`
}

// AgentResponse is a message sent from Agent to Server over the WebSocket.
type AgentResponse struct {
    RequestID string          `json:"request_id"`
    Status    int             `json:"status"`
    Body      json.RawMessage `json:"body,omitempty"`
    Stream    bool            `json:"stream,omitempty"`  // true for streaming responses
    Chunk     int             `json:"chunk,omitempty"`   // chunk number for streams
    Done      bool            `json:"done,omitempty"`    // true = stream end
}

// EnrollMessage is the first message sent by the agent after connecting.
type EnrollMessage struct {
    Token string `json:"token"`
}

// HeartbeatMessage is sent periodically by the agent.
type HeartbeatMessage struct {
    Type    string `json:"type"`    // "heartbeat"
    Version string `json:"version"`
}
```

### File: `internal/edge/client.go`

The edge mode client. Connects to the Server via WebSocket and processes incoming requests.

```go
package edge

type Client struct {
    cfg    *config.Config
    docker *docker.Client
    conn   *websocket.Conn
    mu     sync.Mutex
}

func NewClient(cfg *config.Config, dockerClient *docker.Client) *Client {
    return &Client{
        cfg:    cfg,
        docker: dockerClient,
    }
}

func (c *Client) Run() error {
    for {
        err := c.connectAndServe()
        if err != nil {
            log.Printf("Edge connection error: %v, reconnecting in %s...", err, c.cfg.EdgeReconnect)
        }
        time.Sleep(c.cfg.EdgeReconnect)
    }
}

func (c *Client) connectAndServe() error {
    // Build WebSocket URL
    wsURL := c.cfg.EdgeServerURL + "/api/agent/connect"

    // Connect (with token in query param for initial auth)
    conn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+c.cfg.Token, nil)
    if err != nil {
        return fmt.Errorf("dial: %w", err)
    }
    c.conn = conn
    defer conn.Close()

    log.Printf("Edge: connected to %s", wsURL)

    // Send enrollment message
    enroll := EnrollMessage{Token: c.cfg.Token}
    if err := conn.WriteJSON(enroll); err != nil {
        return fmt.Errorf("enroll: %w", err)
    }

    // Start heartbeat goroutine
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go c.heartbeat(ctx)

    // Read loop: process incoming requests
    for {
        _, message, err := conn.ReadMessage()
        if err != nil {
            return fmt.Errorf("read: %w", err)
        }

        var msg AgentRequest
        if err := json.Unmarshal(message, &msg); err != nil {
            log.Printf("Edge: invalid message: %v", err)
            continue
        }

        // Handle the request
        go c.handleRequest(msg)
    }
}

func (c *Client) heartbeat(ctx context.Context) {
    ticker := time.NewTicker(c.cfg.EdgeHeartbeat)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            c.mu.Lock()
            err := c.conn.WriteJSON(HeartbeatMessage{
                Type:    "heartbeat",
                Version: version,
            })
            c.mu.Unlock()
            if err != nil {
                log.Printf("Edge: heartbeat failed: %v", err)
                return
            }
        }
    }
}

func (c *Client) handleRequest(msg AgentRequest) {
    // Route the request to the appropriate handler
    // Same logic as the direct mode HTTP handlers, but:
    // - Input comes from the WebSocket message
    // - Output goes back as an AgentResponse on the WebSocket

    var resp AgentResponse
    resp.RequestID = msg.RequestID

    // Route based on msg.Path
    switch {
    case msg.Path == "/docker/containers" && msg.Method == "GET":
        all := msg.Query["all"] == "true"
        containers, err := c.docker.ListContainers(context.Background(), all)
        if err != nil {
            resp.Status = 500
            resp.Body, _ = json.Marshal(gin.H{"error": "internal error"})
        } else {
            resp.Status = 200
            resp.Body, _ = json.Marshal(containers)
        }

    case strings.HasPrefix(msg.Path, "/docker/containers/") && msg.Method == "POST":
        // ... start, stop, restart, etc.
        // Parse container ID from path

    case msg.Path == "/host/info" && msg.Method == "GET":
        info := host.GetHostInfo(c.docker.ServerVersion(context.Background()))
        resp.Status = 200
        resp.Body, _ = json.Marshal(info)

    // ... more routes
    }

    c.mu.Lock()
    c.conn.WriteJSON(resp)
    c.mu.Unlock()
}
```

**Streaming endpoints** (logs, stats) in edge mode:

For WebSocket-based streaming (container logs, stats), the agent sends multiple `AgentResponse` messages with `Stream: true` and incrementing `Chunk` numbers. The final message has `Done: true`.

```go
func (c *Client) handleContainerLogs(msg AgentRequest) {
    containerID := extractPathParam(msg.Path, 3) // /docker/containers/{id}/logs

    reader, err := c.docker.ContainerLogs(context.Background(), containerID, "100")
    if err != nil {
        c.sendResponse(msg.RequestID, 500, gin.H{"error": "internal error"})
        return
    }
    defer reader.Close()

    buf := make([]byte, 4096)
    chunk := 0
    for {
        n, err := reader.Read(buf)
        if n > 0 {
            c.sendStreamChunk(msg.RequestID, chunk, buf[:n])
            chunk++
        }
        if err != nil {
            break
        }
    }
    c.sendStreamEnd(msg.RequestID, 200)
}
```

---

## 10. Enrollment Handshake

### File: `internal/enroll/handshake.go`

The enrollment flow is slightly different for direct vs edge mode.

**Direct mode enrollment:**

1. Agent starts, listens on port
2. Server sends `POST /agent/enroll` with `{ "token": "agt-xxx" }`
3. Agent verifies token matches its `DOCKPAL_TOKEN`
4. Agent responds with host info
5. Server marks instance as "online"

```go
type EnrollResponse struct {
    Status        string   `json:"status"`        // "ok"
    Hostname      string   `json:"hostname"`
    OS            string   `json:"os"`
    CPUCores      int      `json:"cpu_cores"`
    TotalMemory   uint64   `json:"total_memory"`
    DockerVersion string   `json:"docker_version"`
    Version       string   `json:"version"`       // Agent binary version (e.g. "0.1.0")
}

func HandleEnroll(c *gin.Context, cfg *config.Config, dockerClient *docker.Client) {
    var req struct {
        Token string `json:"token"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
        return
    }

    // Verify token matches
    if req.Token != cfg.Token {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
        return
    }

    // Return host info
    ver, _ := dockerClient.ServerVersion(c.Request.Context())
    info := host.GetHostInfo(ver)

    c.JSON(http.StatusOK, EnrollResponse{
        Status:        "ok",
        Hostname:      info.Hostname,
        OS:            info.OS,
        CPUCores:      info.CPUCores,
        TotalMemory:   info.TotalMemory,
        DockerVersion: info.DockerVersion,
        Version:       config.Version, // set at build time via ldflags
    })
}
```

**Edge mode enrollment:**

Already handled in `edge/client.go` — the first message after WebSocket connection is the enrollment message with the token. The Server verifies the token and marks the instance online.

---

## 11. Dockerfile

```dockerfile
FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dockpal-agent .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /dockpal-agent /usr/local/bin/dockpal-agent

EXPOSE 9273
ENTRYPOINT ["dockpal-agent"]
```

**Multi-arch build:**

```bash
# Build for both amd64 and arm64
docker buildx build --platform linux/amd64,linux/arm64 -t sdldev/dockpal-agent:latest .
```

---

## 12. go.mod

```
module github.com/sdldev/dockpal-agent

go 1.25.0

require (
    github.com/gin-gonic/gin v1.12.0
    github.com/gorilla/websocket v1.5.3
    github.com/moby/moby/api v1.54.2
    github.com/moby/moby/client v0.4.1
    gopkg.in/yaml.v3 v3.0.1
)
```

The agent uses the **same major dependencies** as the DockPal Server (Gin, Docker SDK, WebSocket, YAML) to ensure type compatibility and reduce maintenance burden.

---

## 13. Implementation Order

### Phase 1: Core (direct mode only)

| Step | Files | Description |
|------|-------|-------------|
| 1.1 | `go.mod`, `main.go`, `internal/config/config.go` | Project skeleton, config parsing |
| 1.2 | `internal/docker/client.go`, `internal/docker/types.go` | Docker client wrapper + shared types |
| 1.3 | `internal/docker/containers.go` | Container operations (copy from Server) |
| 1.4 | `internal/docker/compose.go` | Compose deploy (copy from Server) |
| 1.5 | `internal/docker/images.go` | Image operations (copy from Server) |
| 1.6 | `internal/docker/files.go` | File operations (copy from Server) |
| 1.7 | `internal/host/info.go`, `internal/host/stats.go` | Host info + stats (copy from Server) |
| 1.8 | `internal/auth/middleware.go` | Token auth middleware |
| 1.9 | `internal/enroll/handshake.go` | Enrollment handler |
| 1.10 | `internal/server/server.go`, `internal/server/routes.go` | HTTP server + route registration |
| 1.11 | `Dockerfile` | Docker image build |
| 1.12 | `README.md` | Documentation |

**Test:** Deploy agent container on a VPS, connect from DockPal Server via direct mode, verify all operations work.

### Phase 2: Edge mode

| Step | Files | Description |
|------|-------|-------------|
| 2.1 | `internal/edge/protocol.go` | Message types |
| 2.2 | `internal/edge/client.go` | WebSocket client + request handler |
| 2.3 | `main.go` update | Add edge mode startup path |

**Test:** Deploy agent on a host behind NAT, verify edge mode connection and all operations work.

### Phase 3: Polish

| Step | Files | Description |
|------|-------|-------------|
| 3.1 | TLS cert auto-generation | Self-signed cert for direct mode |
| 3.2 | Graceful shutdown | Signal handling, connection cleanup |
| 3.3 | Health check endpoint | `GET /agent/ping` (unauthenticated for Docker HEALTHCHECK) |
| 3.4 | Structured logging | Consistent log format |
| 3.5 | CI/CD | GitHub Actions for multi-arch build + push |

---

## 14. API Reference — Complete

### Unauthenticated

| Method | Path | Description |
|--------|------|-------------|
| GET | `/agent/ping` | Liveness check (for Docker HEALTHCHECK) |

### Authenticated (Bearer token)

| Method | Path | Description | Request Body | Response |
|--------|------|-------------|-------------|----------|
| POST | `/agent/enroll` | Enrollment handshake | `{ "token": "agt-xxx" }` | `{ "status": "ok", "hostname": "...", "version": "0.1.0", ... }` |
| GET | `/agent/host/info` | Static host info | — | `HostInfo` |
| GET | `/agent/host/stats` | Real-time host stats | — | `HostStats` |
| GET | `/agent/docker/containers` | List containers | — | `[]ContainerInfo` |
| GET | `/agent/docker/containers/:id` | Inspect container | — | `ContainerDetail` |
| POST | `/agent/docker/containers/:id/start` | Start container | — | `{ "status": "started" }` |
| POST | `/agent/docker/containers/:id/stop` | Stop container | — | `{ "status": "stopped" }` |
| POST | `/agent/docker/containers/:id/restart` | Restart container | — | `{ "status": "restarted" }` |
| DELETE | `/agent/docker/containers/:id` | Remove container | — | `{ "status": "removed" }` |
| PUT | `/agent/docker/containers/:id` | Edit container | `ContainerEditRequest` | `{ "status": "updated", "container": ... }` |
| GET | `/agent/docker/containers/:id/stats` | Container stats | — | `ContainerStats` |
| GET | `/agent/docker/containers/:id/logs` | Container logs (WS) | — | WebSocket stream |
| GET | `/agent/docker/containers/:id/stats/ws` | Stats stream (WS) | — | WebSocket stream |
| POST | `/agent/docker/deploy/compose` | Deploy compose | `{ "name", "compose", "registry_auths": {"domain": "auth"} }` | `{ "status": "deployed" }` |
| POST | `/agent/docker/deploy/stream` | Deploy compose (streamed) | `{ "name", "compose", "registry_auths": {"domain": "auth"} }` | `{ "deploy_id": "..." }` |
| POST | `/agent/docker/compose/stop` | Stop compose project | `{ "name" }` | `{ "status": "stopped" }` |
| POST | `/agent/docker/compose/remove` | Remove compose project | `{ "name" }` | `{ "status": "removed" }` |
| GET | `/agent/docker/images` | List images | — | `[]ImageInfo` |
| POST | `/agent/docker/images/pull` | Pull image | `{ "image", "registry_auth" }` | `{ "status": "pulled" }` |
| DELETE | `/agent/docker/images/:id` | Remove image | — | `{ "status": "removed" }` |
| GET | `/agent/docker/files` | List files | query: `container`, `path` | `[]FileInfo` |
| GET | `/agent/docker/files/read` | Read file | query: `container`, `path` | text content |
| POST | `/agent/docker/files/write` | Write file | `{ "container", "path", "content" }` | `{ "status": "written" }` |
| POST | `/agent/docker/files/upload` | Upload file | multipart: `file`, `container`, `path` | `{ "status": "uploaded" }` |
| GET | `/agent/docker/files/download` | Download file | query: `container`, `path` | binary attachment |
| DELETE | `/agent/docker/files` | Delete file | query: `container`, `path` | `{ "status": "deleted" }` |
| POST | `/agent/docker/containers/:id/files/write` | Write container file | `{ "path", "content" }` | `{ "status": "written" }` |

### Deploy Stream WebSocket

After `POST /agent/docker/deploy/stream` returns a `deploy_id`, the Server connects via WebSocket:

```
GET /agent/docker/deploy/stream/{deploy_id}?token=agt-xxx
```

The agent streams `DeployEvent` messages over this WebSocket until the deploy completes.

---

## 15. Security Considerations

### Agent Token

- Generated by the Server as 32 random bytes, hex-encoded
- Stored in agent's environment variable (`DOCKPAL_TOKEN`)
- Sent on every request as `Authorization: Bearer <token>`
- For WebSocket connections, sent as query parameter `?token=<token>` (WebSocket cannot set custom headers during upgrade)
- The Server stores a bcrypt hash of the token; the agent stores plaintext
- Token can be rotated from the Server dashboard — requires restarting the agent container with the new token

### TLS (Direct Mode)

- Auto-generates self-signed certificate on first start
- Certificate stored in `/etc/dockpal/agent/certs/`
- ECDSA P-256 key, 365-day validity
- The Server's DirectClient must accept self-signed certs (configurable: `InsecureSkipVerify` for now, CA pinning later)

### Credential Delivery

- The agent **never stores** registry credentials
- Credentials are sent from the Server in the deploy request body (`registry_auth` field)
- The agent uses the credential for the deploy operation only
- After the operation, the credential is discarded from memory
- The credential travels over TLS (direct mode) or WSS (edge mode)

### Input Validation

- Container IDs validated with regex (same as Server's `ValidateContainerID`)
- File paths validated with `ValidatePath` (same as Server)
- Compose YAML parsed before execution (same parser as Server)
- Deploy names validated (same as Server's `ValidateContainerName`)

---

## 16. Docker HEALTHCHECK

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:9273/agent/ping || exit 1
```

The `/agent/ping` endpoint is **unauthenticated** so Docker can check it without a token. It returns `{ "status": "ok" }` if the Docker daemon is reachable.

---

## 17. README Content

The README should cover:

1. What is DockPal Agent
2. Quick start (docker run commands for both modes)
3. Environment variables reference
4. API reference (link to this plan or a separate API doc)
5. Building from source
6. Security notes
7. Troubleshooting (common issues: Docker socket not mounted, token mismatch, TLS errors)

---

## 18. Shared Code Strategy

The agent copies (not imports) Docker operation code from the DockPal Server. This is intentional:

**Why not shared Go module?**
- The Server and Agent are in separate repos
- A shared module would create tight coupling between release cycles
- The agent only needs a subset of the Server's Docker code
- Copying is simple and the Docker SDK API is stable

**What gets copied:**
- `internal/docker/containers.go` — container CRUD + edit
- `internal/docker/compose.go` — compose deploy
- `internal/docker/deploy_stream.go` — streamed deploy
- `internal/docker/images.go` — image operations
- `internal/docker/fileops.go` — file operations
- Host info/stats functions from Server's `routes.go`

**What does NOT get copied:**
- Auth/JWT (agent uses simple token auth)
- Database (agent has no DB)
- Templates (agent doesn't have templates)
- Traefik (agent doesn't manage proxy config)
- Tunnel (agent doesn't manage Cloudflare)
- Registry (agent doesn't store credentials)
- Git deploy (agent receives compose YAML from Server, not git URLs)
- Update service (agent is updated by redeploying the container)
- Validator (agent trusts the Server's validation)

**Maintenance:** When the Server adds new Docker operations, the agent needs to be updated to support them. This should be done as part of the Server's release process.

---

## 19. Resolved Gaps & Design Decisions

> These issues were identified during cross-review with the Server plan. Each decision is documented here to prevent ambiguity during implementation.

### G1: Naming — AgentRequest (not AgentMessage)

**Decision:** The edge protocol type is `AgentRequest`, not `AgentMessage`. This aligns with the Server plan's `AgentRequest`/`AgentResponse` naming and avoids confusion. The `edge/protocol.go` file uses `AgentRequest` consistently.

### G2: Missing Compose Stop/Remove Endpoints

**Decision:** Added two endpoints that the Server needs for service deletion:
- `POST /agent/docker/compose/stop` — Stop all containers for a project
- `POST /agent/docker/compose/remove` — Remove all containers + compose files for a project

These are registered in `routes.go` and listed in the API reference. The handler implementations call `docker.Client.StopCompose()` and `docker.Client.RemoveCompose()` (already defined in `compose.go`).

### G3: Deploy Stream Relay for Edge Mode

**Decision:** For edge mode, the Server proxies deploy events through the edge WebSocket:
1. Server sends `AgentRequest` with `Method: "POST"`, `Path: "/docker/deploy/stream"` via edge WS
2. Agent responds with `deploy_id`
3. Server sends `AgentRequest` with `Method: "WS"`, `Path: "/docker/deploy/stream/{deploy_id}"` via edge WS
4. Agent streams `AgentResponse` messages with `Stream: true` back
5. Server forwards each chunk to its browser-facing `DeploySession`

The edge client's `handleRequest` method already supports the `Method: "WS"` pattern for container logs. The deploy stream uses the same mechanism — no new protocol features needed.

### G4: Agent Version Reporting

**Decision:** The Agent reports its version in two places:
1. During enrollment: `POST /agent/enroll` response includes `"version": "0.1.0"`
2. In heartbeat messages: `HeartbeatMessage.Version` field (already defined in `protocol.go`)

The Server stores the version in `Instance.AgentVersion` and can use it for capability checks. For MVP, version checking is a safety net — all agents are expected to match the Server version.

### G5: Credential Delivery — Multi-Registry Auth in Deploy Requests

**Decision:** The Agent receives registry credentials from the Server in the deploy request body as a `registry_auths` map (`map[string]string`, domain → base64 auth header). The Agent:
- Does NOT store credentials
- Builds a `getAuthHeader(imageRef)` function from the map, matching each image's domain to the corresponding credential
- Supports multiple registries per compose file (e.g., `ghcr.io` + `registry.internal.com`)
- Discards all credentials from memory after the operation completes

This is documented in the compose.go section above. Consistent with the Server plan's G10 decision.

### G6: TLS Certificate for Direct Mode — Server Trust

**Decision:** The Agent auto-generates a self-signed ECDSA P-256 certificate on first start. The Server's `DirectClient` must accept this certificate. For MVP, `InsecureSkipVerify: true` is used. For Phase 2, the Server can pin the Agent's CA fingerprint (displayed during enrollment) for verification without a real CA.
