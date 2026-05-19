# Multi-Instance Architecture

> DockPal extension for managing Docker across multiple remote hosts from a single dashboard.

---

## Overview

DockPal currently manages a single local Docker daemon. This document describes the architecture for extending it to manage multiple remote hosts (instances), each running a lightweight **DockPal Agent** that proxies Docker API calls and reports host health.

### Goals

- Manage containers on any number of remote VPS from a single DockPal dashboard
- Support hosts with public IP (directly reachable) **and** hosts behind NAT/firewall (no inbound access)
- Share credentials (e.g., one GitHub PAT) across all instances, with per-instance overrides
- Maintain backward compatibility — existing single-host usage works unchanged
- Keep the agent minimal: small binary, low memory, no UI, no database

### Non-Goals (for now)

- Multi-user / PaaS (future phase)
- Billing or quota enforcement
- Container orchestration across instances (swarm-like scheduling)

---

## Architecture

```
                         DockPal Server
                    (Dashboard + API + Agent Manager)
                               │
              ┌────────────────┼────────────────┐
              │                │                │
     ┌────────▼──────┐  ┌────▼───────┐  ┌─────▼───────┐
     │  Agent (VPS1)  │  │ Agent (VPS2)│  │ Agent (VPS3) │
     │  Direct mode   │  │ Edge mode   │  │ Direct mode  │
     │  Docker API    │  │ Docker API  │  │ Docker API   │
     │  Host stats    │  │ Host stats  │  │ Host stats   │
     └───────────────┘  └─────────────┘  └──────────────┘
```

### Components

| Component | Role | Repo |
|-----------|------|------|
| **DockPal Server** | Dashboard, API, agent connection manager, credential store | `dockpal` |
| **DockPal Agent** | Lightweight Docker proxy + host reporter, runs on each managed host | `dockpal-agent` (separate repo) |

---

## Connection Modes

Agents connect to the Server using one of two modes, depending on network topology.

### Direct Mode

For hosts with a publicly reachable address (or any address the Server can connect to).

```
┌──────────────┐       HTTP/HTTPS        ┌──────────────┐
│  Server       │ ─────────────────────► │  Agent        │
│               │ ◄───────────────────── │  :9273        │
└──────────────┘                        └──────────────┘
```

- Agent listens on a TCP port (default `9273`)
- Server initiates requests to `http://<host>:9273/agent/...`
- Straightforward HTTP request/response
- Suitable for: VPS with public IP, hosts on the same private network as the Server

### Edge Mode

For hosts behind NAT or firewall — no inbound access from the Server.

```
┌──────────────┐      WebSocket (WSS)     ┌──────────────┐
│  Server       │ ◄───────────────────── │  Agent        │
│  :3012        │ ──────────────────────►│  (outbound)   │
└──────────────┘                        └──────────────┘
```

- Agent initiates an outbound WebSocket connection to the Server
- The connection stays open (keep-alive with heartbeat)
- Server sends requests through the established WebSocket; Agent responds over the same connection
- Auto-reconnect on disconnect
- Suitable for: hosts behind NAT, private networks, containers without published ports

### How Edge Mode Request/Response Works

Since the WebSocket is long-lived and shared, requests are multiplexed using a request ID:

```
1. Server creates a unique request_id
2. Server sends JSON over the WebSocket:
   {
     "request_id": "req-abc123",
     "method": "GET",
     "path": "/docker/containers",
     "body": null
   }
3. Agent receives, executes against local Docker, responds:
   {
     "request_id": "req-abc123",
     "status": 200,
     "body": [{...}, {...}]
   }
4. Server matches response to the pending request via request_id
```

Streaming endpoints (logs, stats) use the same channel with chunked responses:

```
Server:  { "request_id": "req-def456", "method": "GET", "path": "/docker/containers/abc/logs" }
Agent:   { "request_id": "req-def456", "stream": true, "chunk": 1, "data": "log line 1\n" }
Agent:   { "request_id": "req-def456", "stream": true, "chunk": 2, "data": "log line 2\n" }
Agent:   { "request_id": "req-def456", "stream": false, "status": 200 }  // stream end
```

---

## DockPal Agent

The agent is a minimal binary deployed on each managed host. It has no UI, no database, and no update mechanism of its own.

### Responsibilities

| Function | Description |
|----------|-------------|
| Docker proxy | Forward a subset of Docker API calls to the local daemon |
| Host info | Report OS, CPU cores, total memory, Docker version |
| Host stats | Stream real-time CPU, memory, disk, network usage |
| Enrollment | Authenticate with the Server on first connection |
| Heartbeat | Periodic ping to maintain connection state (edge mode) |

### What the Agent Does NOT Do

- No web UI or dashboard
- No persistent database
- No template system
- No Traefik or Cloudflare Tunnel configuration
- No self-update (Server handles agent updates by redeploying the container)
- No credential storage (credentials are delivered on-demand from the Server)

### Agent API

All endpoints require `Authorization: Bearer <agent_token>`.

```
# Enrollment
POST /agent/enroll              → Handshake, verify token, register instance

# Docker proxy
GET    /agent/docker/containers           → List containers
GET    /agent/docker/containers/:id       → Inspect container
POST   /agent/docker/containers/:id/start → Start container
POST   /agent/docker/containers/:id/stop  → Stop container
POST   /agent/docker/containers/:id/restart → Restart container
DELETE /agent/docker/containers/:id       → Remove container
PUT    /agent/docker/containers/:id       → Edit container (in-place + recreate)
GET    /agent/docker/containers/:id/stats → Container stats (stream)
GET    /agent/docker/containers/:id/logs  → Container logs (stream)
POST   /agent/docker/deploy               → Deploy from compose YAML
GET    /agent/docker/images               → List images

# Host info
GET    /agent/host/info     → Static info (OS, CPU, RAM, Docker version)
GET    /agent/host/stats    → Real-time stats (stream)

# Health
GET    /agent/ping          → Liveness check
```

### Agent Configuration

The agent reads config from environment variables (ideal for Docker deployment):

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DOCKPAL_MODE` | yes | — | `direct` or `edge` |
| `DOCKPAL_TOKEN` | yes | — | Enrollment token from Server |
| `DOCKPAL_DIRECT_LISTEN` | no | `:9273` | Listen address (direct mode) |
| `DOCKPAL_DIRECT_TLS` | no | `true` | Enable TLS (direct mode) |
| `DOCKPAL_EDGE_SERVER` | edge only | — | Server URL, e.g. `wss://dockpal.example.com:3012` |
| `DOCKPAL_EDGE_RECONNECT` | no | `5s` | Reconnect interval on disconnect |
| `DOCKPAL_EDGE_HEARTBEAT` | no | `30s` | Heartbeat ping interval |
| `DOCKPAL_DOCKER_SOCKET` | no | `/var/run/docker.sock` | Docker daemon socket path |

### Agent Project Structure (separate repo)

```
dockpal-agent/
├── main.go                  # Entry point, CLI, mode selection
├── internal/
│   ├── server/              # HTTP server (direct mode)
│   ├── edge/                # WebSocket client (edge mode)
│   ├── docker/              # Docker SDK proxy handlers
│   ├── host/                # System stats collector
│   ├── enroll/              # Enrollment handshake logic
│   └── config/              # Config parsing from env vars
├── Dockerfile
├── go.mod
└── README.md
```

Target: binary < 10 MB, idle RAM < 20 MB.

---

## Enrollment Flow

The process of adding a new managed host to DockPal.

```
┌──────────┐                    ┌──────────┐                ┌──────────┐
│  Browser  │                    │  Server   │                │   Agent   │
└────┬─────┘                    └────┬─────┘                └────┬─────┘
     │                               │                           │
     │ 1. POST /api/instances        │                           │
     │    { name, host, mode }       │                           │
     ├──────────────────────────────►│                           │
     │                               │                           │
     │ 2. Instance created,          │                           │
     │    agent_token generated,     │                           │
     │    install command returned   │                           │
     │◄──────────────────────────────┤                           │
     │                               │                           │
     │ 3. User copies install        │                           │
     │    command, runs on target    │                           │
     │    host                       │                           │
     │                               │                           │
     │                               │    4. docker run agent    │
     │                               │    ◄──────────────────────┤
     │                               │                           │
     │                               │    5. Agent connects      │
     │                               │    (Direct: HTTP to       │
     │                               │     :9273 / Edge: WSS     │
     │                               │     to Server)            │
     │                               │    ◄──────────────────────┤
     │                               │                           │
     │                               │    6. POST /agent/enroll  │
     │                               │    { token: "agt-xxx" }   │
     │                               │    ◄──────────────────────┤
     │                               │                           │
     │                               │    7. Server verifies     │
     │                               │    token, collects host   │
     │                               │    info, marks instance   │
     │                               │    as "online"            │
     │                               │    ──────────────────────►│
     │                               │                           │
     │ 8. Instance appears online    │                           │
     │    in dashboard               │                           │
     │◄──────────────────────────────┤                           │
     │                               │                           │
```

### Install Commands

The Server generates the appropriate command based on the chosen mode.

**Direct mode** (host with reachable address):

```bash
docker run -d \
  --name dockpal-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 9273:9273 \
  -e DOCKPAL_MODE=direct \
  -e DOCKPAL_TOKEN=agt-abc123xyz \
  sdldev/dockpal-agent:latest
```

**Edge mode** (host behind NAT, no inbound access needed):

```bash
docker run -d \
  --name dockpal-agent \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e DOCKPAL_MODE=edge \
  -e DOCKPAL_SERVER=wss://dockpal.example.com:3012 \
  -e DOCKPAL_TOKEN=agt-abc123xyz \
  sdldev/dockpal-agent:latest
```

Note: Edge mode does **not** publish any port (`-p` flag absent). The agent makes an outbound connection only.

---

## Credential Sharing

### Scoping Model

Credentials (GitHub PAT, registry tokens, SSH keys) follow a two-tier scoping model:

```
┌─────────────────────────────────────────────────────┐
│  Credential Store (Server-side, AES-256-GCM)        │
│                                                      │
│  Scope: GLOBAL (shared across all instances)         │
│  ├── github.com    → ghp_xxxx                        │
│  └── ghcr.io       → ghp_yyyy                        │
│                                                      │
│  Scope: INSTANCE:my-vps-1 (specific to one instance) │
│  ├── registry.internal.example.com → token_zzz       │
│  └── gitlab.company.com            → glpat_xxx       │
│                                                      │
│  Scope: INSTANCE:my-vps-2                            │
│  └── (no instance-specific — uses global only)       │
└─────────────────────────────────────────────────────┘
```

### Lookup Order

When deploying or pulling an image on a specific instance:

1. Check instance-specific credentials for the matching registry domain
2. If not found, fall back to global credentials
3. If neither exists, proceed without authentication (public images only)

### Credential Delivery

Credentials are never stored on the agent. Delivery flow:

```
1. User triggers deploy on instance X requiring registry auth
2. Server looks up credential (instance-specific → global fallback)
3. Server decrypts credential
4. Server sends credential to Agent in the deploy request (over TLS/WSS)
5. Agent uses credential for docker login / pull, keeps in memory only
6. After deploy completes, Agent discards credential from memory
```

### Multi-Registry Credential Delivery

A single compose file may reference images from multiple registries. The Server resolves **all** matching credentials and sends them as a map:

```jsonc
// Deploy request body sent to Agent
{
  "name": "my-app",
  "compose": "...",
  "registry_auths": {
    "ghcr.io": "base64(json({username, password}))",
    "registry.internal.com": "base64(json({username, password}))"
  }
}
```

The Agent builds a `getAuthHeader(imageRef)` function from this map, matching each image's domain to the corresponding credential. Images without a matching credential are pulled without auth (public fallback).

---

## Data Model Changes

### New Bucket: `instances`

```jsonc
// Key: instance ID
{
  "id":              "inst-abc123",
  "name":            "my-vps-1",
  "host":            "203.0.113.50",       // empty for edge mode
  "port":            9273,                 // agent listen port (direct mode)
  "mode":            "direct",             // "direct" | "edge"
  "agent_token_hash": "$2a$10$...",        // bcrypt hash of agent token (for verification)
  "status":          "online",             // "online" | "offline" | "enrolling"
  "docker_version":  "27.1.1",
  "os":              "Ubuntu 24.04",
  "cpu_cores":       4,
  "total_memory":    8589934592,
  "last_seen":       1716123456,
  "created_at":      1716000000
}
```

### Modified: `services`

Add `instance_id` field:

```jsonc
{
  "id":           "svc-xyz789",
  "instance_id":  "inst-abc123",     // ← NEW: which instance owns this service
  "name":         "my-app",
  "type":         "compose",
  "domain":       "app.example.com",
  "compose":      "...",
  "created_at":   1716000000
}
```

### Modified: `domains`

Add `instance_id` field:

```jsonc
{
  "id":           "dom-def456",
  "instance_id":  "inst-abc123",     // ← NEW
  "domain":       "app.example.com",
  "service":      "svc-xyz789",
  "port":         8080
}
```

### Modified: `registries`

Add `instance_id` field (empty string = global):

```jsonc
{
  "id":              "reg-ghi012",
  "instance_id":     "",              // ← NEW: empty = global scope
  "registry":        "github.com",
  "username":        "sdldev",
  "encrypted_token": "...",
  "created_at":      1716000000,
  "updated_at":      1716000000,
  "last_validated_at": 1716000000
}
```

**Scoping rules:**
- `instance_id == ""` (empty string) → **Global scope** — credential available to all instances
- `instance_id == "inst-abc123"` → **Instance-specific** — only available to that instance
- Existing records (pre-migration) have no `instance_id` field → treated as global (empty string)
- The value `"local"` is NOT used for scoping — local instance uses global credentials like any other instance

### Local Instance (Backward Compatibility)

A special instance with ID `"local"` represents the Docker daemon on the same host as the Server. This ensures existing routes continue to work:

```
GET /api/containers  ≡  GET /api/instances/local/containers
```

The "local" instance is auto-created on startup and does not require an agent — the Server connects to the Docker socket directly as it does today.

---

## Server API Changes

### Instance Management

```
POST   /api/instances                    → Create instance (generates agent token + install command)
GET    /api/instances                    → List all instances with status
GET    /api/instances/:id               → Instance detail + health summary
PUT    /api/instances/:id               → Update instance config (name, host, port)
DELETE /api/instances/:id               → Remove instance (disconnects agent)
POST   /api/instances/:id/test          → Test connectivity to agent
POST   /api/instances/:id/rotate-token  → Rotate agent token
```

### Instance-Scoped Operations

All existing container/deploy operations are available per-instance:

```
GET    /api/instances/:id/containers                → List containers on instance
GET    /api/instances/:id/containers/:cid            → Inspect container
POST   /api/instances/:id/containers/:cid/start     → Start container
POST   /api/instances/:id/containers/:cid/stop      → Stop container
POST   /api/instances/:id/containers/:cid/restart   → Restart container
DELETE /api/instances/:id/containers/:cid            → Remove container
PUT    /api/instances/:id/containers/:cid            → Edit container
GET    /api/instances/:id/containers/:cid/stats      → Container stats
GET    /api/instances/:id/containers/:cid/logs       → Container logs (WebSocket)

POST   /api/instances/:id/deploy/stream              → Deploy compose (streamed)
POST   /api/instances/:id/deploy/compose             → Deploy compose
POST   /api/instances/:id/deploy/git                 → Deploy from git repo

GET    /api/instances/:id/host/info                   → Host info (OS, CPU, RAM, Docker version)
GET    /api/instances/:id/host/stats                  → Real-time host stats

GET    /api/instances/:id/registries                  → List instance + global registries
POST   /api/instances/:id/registries                  → Create instance-scoped registry
```

### Agent Connection Endpoint (Edge Mode)

```
GET    /api/agent/connect     → WebSocket upgrade for edge-mode agents
```

This is the endpoint that edge-mode agents connect to. The Server authenticates via the agent token sent in the initial handshake message.

### Backward-Compatible Routes

Existing routes continue to work, proxying to the `"local"` instance:

```
GET  /api/containers          →  GET  /api/instances/local/containers
POST /api/deploy/stream       →  POST /api/instances/local/deploy/stream
GET  /api/registries          →  GET  /api/instances/local/registries
...etc
```

No breaking changes for existing single-host deployments.

---

## Security Model

### Transport Security

| Path | Protocol | Authentication |
|------|----------|----------------|
| Server ↔ Agent (direct) | HTTPS (TLS 1.3) | Agent token in `Authorization` header |
| Server ↔ Agent (edge) | WSS (WebSocket Secure) | Agent token in handshake message |
| Browser ↔ Server | HTTPS (via reverse proxy) | JWT (existing) |

### Agent Token

- Generated by the Server during instance creation (cryptographically random, 32 bytes)
- Stored as bcrypt hash in the Server database (like a password — only hash stored)
- Stored in plaintext only in the agent's environment variable or config
- Can be rotated from the dashboard (`POST /api/instances/:id/rotate-token`)
- After rotation, the agent container must be restarted with the new token

### Credential Delivery Security

- Credentials are stored encrypted (AES-256-GCM) on the Server — already implemented
- When the Server sends a credential to an agent for a deploy, it decrypts server-side and sends over the TLS/WSS channel
- The agent holds the credential in memory only for the duration of the operation
- The agent never writes credentials to disk

### Instance Isolation

- Each instance can only be managed through its own agent connection
- An agent cannot access data from other instances
- The Server validates that operations on `/api/instances/:id/...` are routed only to the correct agent

---

## UI Changes

### Instance Selector

A dropdown in the sidebar to switch between managed instances:

```
┌─────────────────────────────────────────────┐
│  🐳 Dockpal                                 │
│                                             │
│  ▼ Instance: my-vps-1  ──────────────┐     │
│    ├── 🟢 my-vps-1 (203.0.113.50)    │     │
│    ├── 🟢 my-vps-2 (10.0.0.5)       │     │
│    ├── 🔴 staging (offline)          │     │
│    └── ⚙️ Local (this server)        │     │
│                                       │     │
│  ─────────────────────────────────────┘     │
│                                             │
│  📊 Dashboard                               │
│  📦 Containers                              │
│  🚀 Deploy                                  │
│  🌐 Domains                                 │
│  🔑 Registry                                │
│  ⚙️ Settings                                │
│                                             │
│  ─────────────                              │
│  + Add Instance                             │
└─────────────────────────────────────────────┘
```

### Multi-Instance Dashboard

Overview cards for all instances at a glance:

```
┌──────────────────────────────────────────────────────────────┐
│  Dashboard                                                   │
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │ my-vps-1     │  │ my-vps-2     │  │ Local        │         │
│  │ 🟢 Online    │  │ 🟢 Online    │  │ 🟢 Online    │         │
│  │ 4 containers │  │ 12 containers│  │ 3 containers │         │
│  │ CPU: 23%     │  │ CPU: 67%     │  │ CPU: 12%     │         │
│  │ RAM: 2.1/8GB │  │ RAM: 6.4/16GB│  │ RAM: 1.8/4GB │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                              │
│  [View All]  [Add Instance +]                               │
└──────────────────────────────────────────────────────────────┘
```

### Add Instance Dialog

```
┌──────────────────────────────────────────┐
│  Add New Instance                         │
│                                           │
│  Name:     [ my-vps-1                  ]  │
│  Host:     [ 203.0.113.50             ]  │  ← required for direct mode
│  Port:     [ 9273                     ]  │
│  Mode:     ○ Direct  ● Edge             │
│                                           │
│  ── Installation Command ──              │
│  Direct:                                  │
│  $ docker run -d --name dockpal-agent \  │
│    -v /var/run/docker.sock:/var/run/...  │
│    -p 9273:9273 \                         │
│    -e DOCKPAL_MODE=direct \               │
│    -e DOCKPAL_TOKEN=agt-abc123xyz \       │
│    sdldev/dockpal-agent:latest            │
│                                           │
│  Edge:                                    │
│  $ docker run -d --name dockpal-agent \  │
│    -v /var/run/docker.sock:/var/run/...  │
│    -e DOCKPAL_MODE=edge \                 │
│    -e DOCKPAL_SERVER=wss://... \         │
│    -e DOCKPAL_TOKEN=agt-abc123xyz \      │
│    sdldev/dockpal-agent:latest            │
│                                           │
│  [Copy Command]  [Test Connection]        │
│                                           │
│  [Cancel]              [Add Instance]     │
└──────────────────────────────────────────┘
```

When mode is set to "Edge", the Host and Port fields are hidden (not needed — the agent connects outbound).

---

## Server Internal: Agent Connection Manager

```go
// internal/agent/manager.go

// AgentManager maintains connections to all registered agents.
// It abstracts away the difference between direct and edge mode,
// providing a uniform interface for the route handlers.

type AgentManager struct {
    db       *db.DB
    direct   map[string]*DirectConnection   // instance_id → HTTP client
    edge     map[string]*EdgeConnection     // instance_id → WebSocket
    local    *docker.Client                 // local Docker socket
    mu       sync.RWMutex
}

// AgentClient is the uniform interface for communicating with any instance.
type AgentClient interface {
    ListContainers(ctx context.Context, all bool) ([]types.Container, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string) error
    RestartContainer(ctx context.Context, id string) error
    RemoveContainer(ctx context.Context, id string, force bool) error
    InspectContainer(ctx context.Context, id string) (types.ContainerJSON, error)
    GetContainerStats(ctx context.Context, id string) (io.ReadCloser, error)
    ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error)
    DeployCompose(ctx context.Context, name, compose string, authHeader func(string) string) error
    GetHostInfo(ctx context.Context) (*HostInfo, error)
    GetHostStats(ctx context.Context) (<-chan HostStats, error)
}

// GetClient returns the appropriate client for an instance.
// - "local" → direct Docker socket client
// - direct mode → HTTP client to agent
// - edge mode → multiplexed WebSocket client
func (m *AgentManager) GetClient(instanceID string) (AgentClient, error)
```

### DirectConnection

```go
type DirectConnection struct {
    InstanceID string
    BaseURL    string         // https://203.0.113.50:9273
    Client     *http.Client   // TLS client with self-signed cert acceptance
}
```

Implements `AgentClient` by making HTTP requests to the agent's REST API.

### EdgeConnection

```go
type EdgeConnection struct {
    InstanceID   string
    Conn         *websocket.Conn
    mu           sync.Mutex
    pending      map[string]chan *AgentResponse   // request_id → response channel
    nextReqID    atomic.Uint64
}
```

Implements `AgentClient` by sending JSON requests over the WebSocket and waiting for responses matched by `request_id`.

---

## Implementation Phases

### Phase 1A: Agent Binary (separate repo)

Create the `dockpal-agent` repository and implement:

- [ ] Config parsing from environment variables
- [ ] Direct mode: HTTP server on `:9273`
- [ ] Docker proxy handlers (list, start, stop, restart, remove, inspect, stats, logs)
- [ ] Host info endpoint (OS, CPU, RAM, Docker version)
- [ ] Host stats endpoint (real-time CPU, memory, disk, network)
- [ ] Enrollment handshake (`POST /agent/enroll`)
- [ ] Agent token authentication middleware
- [ ] Health check endpoint (`GET /agent/ping`)
- [ ] Dockerfile + CI build for linux/amd64 and linux/arm64

### Phase 1B: Server Foundation (this repo)

Extend DockPal Server to support instances:

- [ ] `instances` bucket in BBolt
- [ ] `instance_id` field on `services`, `domains`, `registries`
- [ ] `AgentManager` with `AgentClient` interface
- [ ] `LocalClient` (wraps existing `docker.Client`)
- [ ] Instance CRUD routes (`/api/instances`)
- [ ] Enrollment API (generate token, return install command)
- [ ] Proxy existing routes through instance-aware handler
- [ ] Backward-compatible route mapping (local instance)
- [ ] Instance selector UI component
- [ ] Add Instance dialog with install command generation

### Phase 1C: Edge Mode

Add WebSocket-based connectivity for agents behind NAT:

- [ ] Agent: edge mode (WebSocket client, auto-reconnect, heartbeat)
- [ ] Server: `/api/agent/connect` WebSocket endpoint
- [ ] Server: request/response multiplexing over WebSocket
- [ ] Server: edge agent registration and tracking
- [ ] Server: detect offline agents (heartbeat timeout)
- [ ] Instance status indicators (online/offline/enrolling)

### Phase 2: Feature Parity

Bring all existing features to remote instances:

- [ ] Deploy compose to remote instance
- [ ] Deploy from git to remote instance
- [ ] Container edit (in-place + recreate) on remote instance
- [ ] Container logs streaming via agent WebSocket
- [ ] Container stats streaming via agent WebSocket
- [ ] Host stats per instance in dashboard
- [ ] Credential scoping (global vs instance-specific)
- [ ] Traefik config generation per instance
- [ ] Cloudflare tunnel management per instance
- [ ] Auto-recovery health monitor per instance

### Phase 3: Polish & Advanced Features

- [ ] Instance health monitoring with alerts
- [ ] Bulk deploy (same compose to multiple instances)
- [ ] Agent token rotation
- [ ] Agent auto-update (Server triggers agent container recreation)
- [ ] Instance grouping / tagging
- [ ] Dashboard with aggregate metrics across all instances
- [ ] Instance resource comparison view

---

## Open Questions

These decisions should be made before implementation begins:

1. **TLS for direct mode agents**: Should the agent auto-generate a self-signed cert, or should the Server maintain a CA and issue certs? Self-signed is simpler but requires the Server to trust any cert. CA-issued is more secure but adds complexity.

2. **Agent token storage**: The token is stored as a bcrypt hash on the Server (like a password). This means the Server cannot send the token to the agent — the agent already knows its token. Is this sufficient, or do we need a more sophisticated key exchange?

3. **Edge mode WebSocket path**: Currently proposed as `GET /api/agent/connect`. Should this be a separate port to avoid interference with the main API? Or is path-based separation sufficient?

4. **Offline instance behavior**: When an instance goes offline, should its containers/services still appear in the UI (cached from last known state)? Or should they be hidden with a warning?

5. **Local instance agent**: Should the "local" instance also run through an agent (for consistency), or continue using the Docker socket directly? Direct socket is simpler and has zero overhead, but an agent would make the architecture uniform.

---

## Resolved Design Decisions

> The following open questions from above have been resolved during detailed planning. See `PLAN-MULTI-INSTANCE-SERVER.md` (G1-G10) and `PLAN-DOCKPAL-AGENT.md` (G1-G6) for full details.

1. **TLS for direct mode agents** — Auto-generated self-signed cert. Server uses `InsecureSkipVerify` for MVP. Phase 2: CA fingerprint pinning during enrollment.

2. **Agent token storage** — Dual storage: `AgentTokenHash` (bcrypt, for verification) + `AgentTokenEncrypted` (AES-256-GCM, for sending to agent via DirectClient). Mirrors existing `RegistryCredential.EncryptedToken` pattern.

3. **Edge mode WebSocket path** — Path-based separation (`GET /api/agent/connect`) is sufficient. No separate port needed. The edge WS is authenticated and multiplexed alongside the main API.

4. **Offline instance behavior** — Show cached data with offline warning badge. Last-known container/service list remains visible but actions are disabled.

5. **Local instance agent** — No agent for local instance. `LocalClient` implements `AgentClient` interface using direct Docker socket access. Zero overhead, simpler deployment. The `AgentClient` abstraction makes this transparent to the rest of the codebase.

### Additional Resolved Decisions

- **SystemInfo shape** — Server provides combined `/system/info` endpoint that merges Agent's HostInfo + HostStats into the existing `SystemInfo` format. Frontend only changes API path, not data shape.
- **Deploy stream relay** — Server proxies events from Agent WebSocket to browser WebSocket. Direct mode: Server opens WS to Agent. Edge mode: Server uses edge WS channel with `Method: "WS"`.
- **Git deploy for remote** — Git clone happens on Server side. Compose YAML sent to Agent. Agent doesn't need git.
- **Traefik/Tunnel/Recovery** — Server-local only for MVP. Not available for remote instances. Phase 2 adds Agent-side health monitor and proxy config.
- **Naming consistency** — `AgentRequest`/`AgentResponse` used on both Server and Agent sides (not `AgentMessage`).
- **Compose stop/remove** — Added to Agent API: `POST /compose/stop` and `POST /compose/remove`.
- **Version negotiation** — Agent reports version during enrollment and in heartbeats. Server stores in `Instance.AgentVersion`. Capability map for future use.
- **Registry auth delivery** — Server resolves credential, sends `registry_auths` map (domain → auth header) in deploy request body. Agent builds `getAuthHeader` function from the map, matching each image's domain. Supports multiple registries per compose file. Discards after use.

### Additional Resolved Decisions (from codebase analysis)

- **`RegisterRoutes` transition** — During Phase 1B, `RegisterRoutes` gains `agentMgr *agent.Manager` parameter. The existing `dockerClient` parameter is retained for backward compatibility and used internally by `LocalClient`. It will be removed in Phase 2 when all routes go through `AgentManager`.
- **Compose file storage path** — Server stores compose files at `/opt/dockpal/compose/`. Agent stores at `/opt/dockpal-agent/compose/`. Different paths prevent conflicts if Server and Agent run on the same host (e.g., during development/testing).
- **Frontend instance switching** — When user switches instance via the selector, the app resets to the container list page (`currentPage = 'containers'`). Any open container detail, edit mode, or deploy session is discarded. This prevents stale data from the previous instance being displayed.
- **Deploy session relay mapping** — Server maintains a `deployRelays map[string]string` (server_session_id → agent_session_id) to correlate browser-facing sessions with agent-facing sessions. Cleaned up after 30 seconds (same as current session cleanup).
- **Edge mode timeout** — Server sets a 60-second timeout per request sent to edge agents. If no response within 60s, the request fails with "agent timeout" error. Browser-facing WebSocket has its own keep-alive (existing behavior).
- **System info access after refactor** — After moving `getMemoryInfo()`/`getCPUPercent()` to `internal/agent/local.go`, route handlers access system info via `agentMgr.GetClient(instanceID).GetHostInfo()` / `GetHostStats()`. No direct function calls from routes.
