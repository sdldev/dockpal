# Multi-Instance Server-Side Implementation Plan

> Detailed plan for Phase 1B: extending the DockPal Server codebase to support multiple instances.
> The agent binary (Phase 1A) is a separate repo and not covered here.

---

## Summary of Changes

| Layer | Files Changed | New Files | Description |
|-------|--------------|-----------|-------------|
| Database | `internal/db/db.go` | — | Add `instances` bucket, add `instance_id` to Service/Domain/Registry |
| Docker | `internal/docker/container.go` | — | No change (Client stays as-is for local) |
| Agent Manager | — | `internal/agent/manager.go` | New package: connection pool, AgentClient interface |
| Agent Manager | — | `internal/agent/local.go` | LocalClient wrapping existing docker.Client |
| Agent Manager | — | `internal/agent/direct.go` | DirectConnection: HTTP client to remote agent |
| Agent Manager | — | `internal/agent/edge.go` | EdgeConnection: WebSocket multiplexed client |
| Agent Manager | — | `internal/agent/types.go` | Shared types: HostInfo, HostStats, AgentRequest, AgentResponse |
| Server | `internal/server/routes.go` | — | Add instance routes, modify existing routes to be instance-aware |
| Server | `internal/server/middleware.go` | — | Add instance resolution middleware |
| Server | — | `internal/server/instance_routes.go` | Instance CRUD + enrollment routes |
| Server | — | `internal/server/agent_ws.go` | WebSocket endpoint for edge-mode agents |
| Main | `main.go` | — | Initialize AgentManager, pass to routes |
| Frontend | `web/assets/modules/state.js` | — | Add instance state, selectedInstance |
| Frontend | `web/assets/modules/instances.js` | — | New module: instance list, add, select |
| Frontend | `web/partials/sidebar.html` | — | Add instance selector dropdown |
| Frontend | `web/pages/instances.html` | — | New page: instance list + add dialog |
| Frontend | `web/index.html` | — | Include new module + page |
| Frontend | `web/assets/modules/dashboard.js` | — | Instance-aware dashboard |
| Frontend | `web/assets/modules/containers.js` | — | Instance-aware API calls |
| Frontend | `web/assets/modules/auth.js` | — | Instance-aware API helper |
| Frontend | `web/assets/app.js` | — | Include instances module |

---

## Step 1: Database — Instance Model & Schema Migration

### File: `internal/db/db.go`

**Add Instance struct:**

```go
type Instance struct {
    ID                  string `json:"id"`
    Name                string `json:"name"`
    Host                string `json:"host,omitempty"`               // empty for edge mode
    Port                int    `json:"port,omitempty"`                // agent port (direct mode)
    Mode                string `json:"mode"`                          // "direct" | "edge" | "local"
    AgentTokenHash      string `json:"agent_token_hash"`              // bcrypt hash (for verification)
    AgentTokenEncrypted []byte `json:"agent_token_encrypted,omitempty"` // AES-256-GCM encrypted plaintext (for sending to agent)
    AgentVersion        string `json:"agent_version,omitempty"`        // agent binary version (reported during enrollment)
    Status              string `json:"status"`                        // "online" | "offline" | "enrolling"
    DockerVersion       string `json:"docker_version,omitempty"`
    OS                  string `json:"os,omitempty"`
    CPUCores            int    `json:"cpu_cores,omitempty"`
    TotalMemory         uint64 `json:"total_memory,omitempty"`
    LastSeen            int64  `json:"last_seen,omitempty"`
    CreatedAt           int64  `json:"created_at"`
}
```

**Add `instance_id` field to existing structs:**

- `Service.InstanceID string` — which instance owns this service
- `Domain.InstanceID string` — which instance owns this domain
- `RegistryCredential.InstanceID string` — empty = global scope

**Add new bucket:**

```go
bucketInstances = []byte("instances")
```

**Add CRUD methods:**

```go
func (d *DB) SaveInstance(inst Instance) error
func (d *DB) GetInstance(id string) (*Instance, error)
func (d *DB) ListInstances() ([]Instance, error)
func (d *DB) DeleteInstance(id string) error
func (d *DB) UpdateInstanceStatus(id, status string) error
func (d *DB) UpdateInstanceLastSeen(id string, lastSeen int64) error
func (d *DB) UpdateInstanceInfo(id string, dockerVersion, os string, cpuCores int, totalMemory uint64) error
```

**Add instance-scoped queries:**

```go
func (d *DB) ListServicesByInstance(instanceID string) ([]Service, error)
func (d *DB) ListDomainsByInstance(instanceID string) ([]Domain, error)
func (d *DB) FindRegistryCredentialByDomainAndInstance(domain, instanceID string) (*RegistryCredential, error)
```

**Migration:** In `New()`, add `bucketInstances` to the bucket creation loop. Existing data requires no migration — empty fields are handled by code.

**Backward compatibility and scoping rules:**
- `Service.InstanceID` / `Domain.InstanceID`: empty string → belongs to `"local"` instance. Code treats empty as "local" for backward compat.
- `RegistryCredential.InstanceID`: empty string → **global scope** (available to ALL instances, not just local). This is intentional — existing registry credentials become globally shared, which is the most useful default.
- When listing services/domains for an instance, filter by `instance_id == instanceID || instance_id == ""` (for local only, empty = local).
- When looking up registry credentials: first check `instance_id == instanceID`, then fall back to `instance_id == ""` (global).

---

## Step 2: Agent Manager — Abstraction Layer

### New package: `internal/agent/`

#### File: `internal/agent/types.go`

Shared types used by all client implementations:

```go
package agent

type HostInfo struct {
    Hostname      string `json:"hostname"`
    OS            string `json:"os"`
    CPUCores      int    `json:"cpu_cores"`
    TotalMemory   uint64 `json:"total_memory"`
    DockerVersion string `json:"docker_version"`
}

type HostStats struct {
    CPUPercent float64 `json:"cpu_percent"`
    UsedRAM    uint64  `json:"used_ram"`
    TotalRAM   uint64  `json:"total_ram"`
    UsedDisk   uint64  `json:"used_disk"`
    TotalDisk  uint64  `json:"total_disk"`
}

// AgentClient is the uniform interface for Docker operations on any instance.
type AgentClient interface {
    ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error)
    InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string) error
    RestartContainer(ctx context.Context, id string) error
    RemoveContainer(ctx context.Context, id string, force bool) error
    EditContainer(ctx context.Context, id string, req docker.ContainerEditRequest) (*docker.ContainerDetail, error)
    GetContainerStats(ctx context.Context, id string) (*docker.ContainerStats, error)
    ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error)
    DeployCompose(ctx context.Context, name, composeYAML string, getAuthHeader docker.AuthHeaderFunc) error
    DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, getAuthHeader docker.AuthHeaderFunc) error
    ListImages(ctx context.Context) ([]docker.ImageInfo, error)
    PullImage(ctx context.Context, image string) error
    PullImageWithAuth(ctx context.Context, image, registryAuth string) error
    RemoveImage(ctx context.Context, id string) error
    GetHostInfo(ctx context.Context) (*HostInfo, error)
    GetHostStats(ctx context.Context) (*HostStats, error)
    Ping(ctx context.Context) error
    Close() error
}
```

#### File: `internal/agent/local.go`

Wraps the existing `docker.Client` to implement `AgentClient`. Zero overhead — just type adaptation.

```go
type LocalClient struct {
    client *docker.Client
}

func NewLocalClient(client *docker.Client) *LocalClient

// Each method delegates directly to docker.Client
func (l *LocalClient) ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error) {
    return l.client.ListContainers(ctx, all)
}
// ... etc for all AgentClient methods
```

GetHostInfo/GetHostStats reuse the existing `getSystemInfo()` / `getCPUPercent()` / `getMemoryInfo()` functions from `routes.go` (move them to a shared location or duplicate the logic).

#### File: `internal/agent/direct.go`

HTTP client that talks to a remote agent's REST API.

```go
type DirectClient struct {
    instanceID string
    baseURL    string          // https://203.0.113.50:9273
    httpClient *http.Client    // TLS client (accept self-signed for now)
    authToken  string          // agent token for Authorization header
}

func NewDirectClient(instanceID, host string, port int, authToken string) *DirectClient

// Each method makes HTTP request to agent:
func (d *DirectClient) ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error) {
    // GET {baseURL}/agent/docker/containers?all={all}
    // Authorization: Bearer {authToken}
    // Decode JSON response
}
// ... etc
```

For streaming endpoints (logs, stats), use WebSocket to `ws://{baseURL}/agent/docker/containers/{id}/logs`.

#### File: `internal/agent/edge.go`

Multiplexed WebSocket client that talks through the Server's edge connection.

```go
type EdgeClient struct {
    instanceID string
    manager    *Manager        // back-reference to send via manager's WS
}

func NewEdgeClient(instanceID string, manager *Manager) *EdgeClient

// Each method sends a request through the Manager's edge connection:
func (e *EdgeClient) ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error) {
    req := &AgentRequest{
        RequestID: generateRequestID(),
        Method:    "GET",
        Path:      "/docker/containers",
        Query:     map[string]string{"all": strconv.FormatBool(all)},
    }
    resp, err := e.manager.SendEdgeRequest(e.instanceID, req)
    // Decode response body
}
// ... etc
```

#### File: `internal/agent/manager.go`

Central connection manager. Holds references to all instance clients.

```go
type Manager struct {
    db       *db.DB
    mu       sync.RWMutex
    local    *LocalClient
    direct   map[string]*DirectClient    // instance_id → client
    edge     map[string]*EdgeConnection   // instance_id → active WebSocket
    edgeMu   sync.Mutex
}

type EdgeConnection struct {
    instanceID string
    conn       *websocket.Conn
    pending    map[string]chan *AgentResponse  // request_id → response channel
    mu         sync.Mutex
}

func NewManager(database *db.DB, localDocker *docker.Client) *Manager

// GetClient returns the AgentClient for a given instance.
// "local" → LocalClient
// direct mode instance → DirectClient (creates if not cached)
// edge mode instance → EdgeClient
func (m *Manager) GetClient(instanceID string) (AgentClient, error)

// RegisterEdgeConnection stores a WebSocket from an edge-mode agent.
func (m *Manager) RegisterEdgeConnection(instanceID string, conn *websocket.Conn)

// UnregisterEdgeConnection removes an edge connection.
func (m *Manager) UnregisterEdgeConnection(instanceID string)

// SendEdgeRequest sends a request through an edge connection and waits for response.
func (m *Manager) SendEdgeRequest(instanceID string, req *AgentRequest) (*AgentResponse, error)

// IsInstanceOnline checks if an instance is reachable.
func (m *Manager) IsInstanceOnline(instanceID string) bool

// Close shuts down all connections.
func (m *Manager) Close()
```

---

## Step 3: Server — Instance Routes & Middleware

### File: `internal/server/instance_routes.go` (new)

Instance CRUD and enrollment endpoints:

```
POST   /api/instances                    → Create instance
GET    /api/instances                    → List all instances
GET    /api/instances/:id               → Get instance detail
PUT    /api/instances/:id               → Update instance (name, host, port)
DELETE /api/instances/:id               → Remove instance
POST   /api/instances/:id/test          → Test connectivity to agent
POST   /api/instances/:id/rotate-token  → Rotate agent token
```

**Create instance flow:**

1. Validate input (name, host for direct mode, mode)
2. Generate agent token (32 bytes, crypto/rand)
3. Hash token with bcrypt (store hash only)
4. Save Instance to DB with status "enrolling"
5. Generate install command based on mode
6. Return instance + install command

**Install command generation:**

```go
func generateInstallCommand(mode, host string, port int, token string, serverURL string) string
```

### File: `internal/server/agent_ws.go` (new)

WebSocket endpoint for edge-mode agents to connect:

```
GET /api/agent/connect   → WebSocket upgrade for edge agents
```

**Flow:**
1. Agent connects via WebSocket
2. Agent sends enrollment message: `{ "token": "agt-xxx" }`
3. Server verifies token against stored hash
4. Server registers connection in AgentManager
5. Server updates instance status to "online"
6. Server enters request/response loop

**Heartbeat:** Server expects ping from agent every 30s. If no ping for 60s, mark instance offline.

### File: `internal/server/middleware.go`

Add instance resolution middleware:

```go
// InstanceMiddleware extracts the instance_id from the URL parameter
// or defaults to "local" for backward-compatible routes.
// Sets the appropriate AgentClient in the Gin context.
func InstanceMiddleware(agentMgr *agent.Manager) gin.HandlerFunc {
    return func(c *gin.Context) {
        instanceID := c.Param("instance_id")
        if instanceID == "" {
            instanceID = "local"
        }
        client, err := agentMgr.GetClient(instanceID)
        if err != nil {
            c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
            c.Abort()
            return
        }
        c.Set("instance_id", instanceID)
        c.Set("agent_client", client)
        c.Next()
    }
}
```

### File: `internal/server/routes.go`

**Major refactor approach:**

The current routes use `dockerClient` directly. We need to make them instance-aware.

**Strategy: Add new instance-scoped route group, keep existing routes as aliases.**

```go
func RegisterRoutes(r *gin.Engine, dockerClient *docker.Client, jwtSecret string, database *db.DB,
    versionService *update.VersionService, updateService *update.UpdateService,
    agentMgr *agent.Manager) {  // ← NEW parameter

    // ... existing setup ...

    // NEW: Instance management routes
    RegisterInstanceRoutes(protected, database, agentMgr)

    // NEW: Instance-scoped operations
    instances := protected.Group("/instances/:instance_id")
    instances.Use(InstanceMiddleware(agentMgr))
    RegisterInstanceScopedRoutes(instances, database, agentMgr)

    // EXISTING routes: now proxy to "local" instance
    // Each existing route handler gets the agent_client from context
    // For "local" instance, this is the LocalClient wrapping dockerClient
}
```

**Instance-scoped routes (new group):**

```go
func RegisterInstanceScopedRoutes(g *gin.RouterGroup, database *db.DB, agentMgr *agent.Manager) {
    // Containers
    g.GET("/containers", handleListContainers)
    g.GET("/containers/:id", handleInspectContainer)
    g.POST("/containers/:id/start", handleStartContainer)
    g.POST("/containers/:id/stop", handleStopContainer)
    g.POST("/containers/:id/restart", handleRestartContainer)
    g.DELETE("/containers/:id", handleRemoveContainer)
    g.PUT("/containers/:id", handleEditContainer)
    g.GET("/containers/:id/stats", handleGetContainerStats)
    g.GET("/containers/:id/logs", handleContainerLogs)
    g.GET("/containers/:id/stats/ws", handleStatsStreamWS)

    // Deploy
    g.POST("/deploy/stream", handleDeployStream)
    g.POST("/deploy/compose", handleDeployCompose)
    g.POST("/deploy/git", handleDeployGit)

    // Images
    g.GET("/images", handleListImages)
    g.POST("/images/pull", handlePullImage)
    g.DELETE("/images/:id", handleRemoveImage)

    // Files
    g.GET("/files", handleListFiles)
    g.GET("/files/read", handleReadFile)
    g.POST("/files/write", handleWriteFile)
    g.POST("/files/upload", handleUploadFile)
    g.GET("/files/download", handleDownloadFile)
    g.DELETE("/files", handleDeleteFile)
    g.POST("/containers/:id/files/write", handleContainerFileWrite)

    // Host
    g.GET("/host/info", handleHostInfo)
    g.GET("/host/stats", handleHostStats)

    // Services (instance-scoped)
    g.GET("/services", handleListServices)
    g.DELETE("/services/:id", handleDeleteService)

    // Domains (instance-scoped)
    g.GET("/domains", handleListDomains)
    g.POST("/domains", handleCreateDomain)
    g.DELETE("/domains/:id", handleDeleteDomain)

    // Registries (instance + global)
    g.GET("/registries", handleListRegistries)
    g.POST("/registries", handleCreateRegistry)
    g.GET("/registries/:id", handleGetRegistry)
    g.PUT("/registries/:id", handleUpdateRegistry)
    g.DELETE("/registries/:id", handleDeleteRegistry)
    g.POST("/registries/:id/test", handleTestRegistry)

    // Templates (same for all instances — no scoping needed)
    g.GET("/templates", handleListTemplates)
    g.GET("/templates/:id", handleGetTemplate)
    g.POST("/templates/:id/deploy", handleDeployTemplate)
    g.POST("/templates/:id/deploy/stream", handleDeployTemplateStream)

    // GitHub repos (uses global or instance credential)
    g.GET("/github/repos", handleGithubRepos)

    // System
    g.GET("/system/info", handleSystemInfo)

    // Tunnel
    g.POST("/tunnel", handleCreateTunnel)
    g.DELETE("/tunnel", handleDeleteTunnel)
}
```

**Refactoring existing route handlers:**

Current handlers reference `dockerClient` directly. Refactor to get client from context:

```go
// BEFORE:
protected.GET("/containers", func(c *gin.Context) {
    containers, err := dockerClient.ListContainers(c.Request.Context(), true)
    // ...
})

// AFTER (instance-scoped handler):
func handleListContainers(c *gin.Context) {
    client := c.MustGet("agent_client").(agent.AgentClient)
    containers, err := client.ListContainers(c.Request.Context(), true)
    // ...
}
```

**Backward-compatible routes:**

Existing routes (`/api/containers`, `/api/deploy/stream`, etc.) continue to work by internally routing to the "local" instance:

```go
// Existing routes now just proxy to local instance
protected.GET("/containers", func(c *gin.Context) {
    client, _ := agentMgr.GetClient("local")
    containers, err := client.ListContainers(c.Request.Context(), true)
    // ... same response format
})
```

This means **zero breaking changes** for existing single-host users.

---

## Step 4: Main — Wire Up AgentManager

### File: `main.go`

Changes:

1. Import `agent` package
2. Create `AgentManager` after Docker client initialization
3. Auto-create "local" instance in DB on startup
4. Pass `AgentManager` to `RegisterRoutes`

```go
// After dockerClient creation:
agentMgr := agent.NewManager(database, dockerClient)

// Ensure "local" instance exists in DB
database.EnsureLocalInstance()

// Pass to routes
server.RegisterRoutes(srv.Router(), dockerClient, jwtSecret, database, versionService, updateService, agentMgr)

// On shutdown:
agentMgr.Close()
```

Add `EnsureLocalInstance()` to db.go:

```go
func (d *DB) EnsureLocalInstance() error {
    _, err := d.GetInstance("local")
    if err != nil {
        return d.SaveInstance(Instance{
            ID:        "local",
            Name:      "This Server",
            Mode:      "local",
            Status:    "online",
            CreatedAt: time.Now().Unix(),
        })
    }
    return nil
}
```

---

## Step 5: Frontend — Instance State & Selector

### File: `web/assets/modules/state.js`

Add instance-related state:

```js
// Instance state
instances: [],
selectedInstance: 'local',
instanceLoading: false,
addInstanceForm: {
    name: '',
    host: '',
    port: 9273,
    mode: 'direct'   // 'direct' | 'edge'
},
addInstanceResult: null,   // { install_command, instance }
```

Add "Instances" nav item:

```js
navItems: [
    { id: 'dashboard', label: 'Dashboard', icon: '...' },
    { type: 'group', label: 'Infrastructure' },
    { id: 'instances', label: 'Instances', icon: '...' },  // ← NEW
    { id: 'containers', label: 'Containers', icon: '...' },
    // ...
]
```

### File: `web/assets/modules/instances.js` (new)

```js
window.Dockpal = window.Dockpal || {};

Dockpal.instances = {
    async loadInstances() {
        const resp = await this.api('GET', '/api/instances');
        if (resp && resp.ok) {
            this.instances = await resp.json();
        }
    },

    async selectInstance(id) {
        this.selectedInstance = id;
        // Reset to safe page — container detail, edit mode, and deploy sessions
        // are instance-specific and become invalid when switching.
        if (this.currentPage === 'container-detail') {
            this.currentPage = 'containers';
        }
        this.containerEditMode = false;
        this.containerEditSaving = false;
        this.selectedContainer = null;
        this.containerStats = null;
        this.logs = [];
        this.destroyChart();
        // Reload current page data for the new instance
        await this.loadPageData(this.currentPage);
    },

    async addInstance() {
        this.instanceLoading = true;
        const form = this.addInstanceForm;
        const body = {
            name: form.name,
            host: form.mode === 'direct' ? form.host : '',
            port: form.mode === 'direct' ? form.port : 0,
            mode: form.mode,
        };
        const resp = await this.api('POST', '/api/instances', body);
        if (resp && resp.ok) {
            const result = await resp.json();
            this.addInstanceResult = result;
            this.toast('Instance added', 'success');
            await this.loadInstances();
        } else {
            const data = await resp?.json?.().catch(() => ({}));
            this.toast(data.error || 'Failed to add instance', 'error');
        }
        this.instanceLoading = false;
    },

    async removeInstance(id) {
        this.showConfirm({
            title: 'Remove Instance',
            message: 'This will remove the instance from the dashboard. The agent on the remote host will continue running.',
            confirmText: 'Remove',
            onConfirm: async () => {
                const resp = await this.api('DELETE', '/api/instances/' + id);
                if (resp && resp.ok) {
                    this.toast('Instance removed', 'success');
                    if (this.selectedInstance === id) {
                        this.selectedInstance = 'local';
                    }
                    await this.loadInstances();
                }
            }
        });
    },

    async testInstance(id) {
        const resp = await this.api('POST', '/api/instances/' + id + '/test');
        if (resp && resp.ok) {
            const result = await resp.json();
            this.toast(result.status === 'ok' ? 'Connection successful' : 'Connection failed: ' + result.error,
                       result.status === 'ok' ? 'success' : 'error');
        }
    },

    copyInstallCommand(cmd) {
        this.copyToClipboard(cmd);
        this.toast('Command copied to clipboard', 'success');
    },

    instanceApiPath(path) {
        // Helper to build instance-scoped API paths
        return '/api/instances/' + this.selectedInstance + path;
    },
};
```

### File: `web/assets/modules/auth.js`

Modify `api()` to support instance-scoped paths:

```js
// Add helper method
instanceApi(method, path, body) {
    const instancePath = '/api/instances/' + this.selectedInstance + path;
    return this.api(method, instancePath, body);
},
```

### File: `web/assets/modules/dashboard.js`

Modify to use instance-scoped API calls:

```js
async loadDashboard() {
    const resp = await this.instanceApi('GET', '/containers');
    if (!resp) return;
    this.containers = await resp.json();

    if (this.sysResourceHistory.labels.length === 0) {
        const sysResp = await this.instanceApi('GET', '/host/info');
        if (sysResp) this.systemInfo = await sysResp.json();
        // ... chart setup ...
    }
    this.startSysResourcePolling();
},

async fetchSystemInfo() {
    const sysResp = await this.instanceApi('GET', '/host/stats');
    if (sysResp) {
        const stats = await sysResp.json();
        // Update systemInfo with stats
        // ... chart update ...
    }
},
```

### File: `web/assets/modules/containers.js`

Modify all API calls to use `instanceApi()`:

```js
async selectContainer(c) {
    const resp = await this.instanceApi('GET', '/containers/' + c.id);
    // ...
},

async containerAction(id, action) {
    const resp = await this.instanceApi('POST', '/containers/' + id + '/' + action);
    // ...
},

async submitContainerEdit() {
    const resp = await this.instanceApi('PUT', '/containers/' + (c.name || c.id), body);
    // ...
},

startLogStream(id) {
    const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(wsProto + '//' + location.host +
        '/api/instances/' + this.selectedInstance + '/containers/' + id + '/logs?token=' + this.token);
    // ...
},
```

### File: `web/partials/sidebar.html`

Add instance selector between logo and nav items:

```html
<!-- Instance Selector -->
<div class="px-3 py-2 border-b border-zinc-800">
    <div class="relative">
        <select x-model="selectedInstance" @change="selectInstance(selectedInstance)"
            class="w-full bg-zinc-800 border border-zinc-700 rounded-sm px-3 py-1.5 text-sm text-white appearance-none cursor-pointer focus:outline-none focus:border-zinc-600">
            <option value="local">⚙️ This Server</option>
            <template x-for="inst in instances.filter(i => i.id !== 'local')" :key="inst.id">
                <option :value="inst.id" x-text="(inst.status === 'online' ? '🟢 ' : '🔴 ') + inst.name"></option>
            </template>
        </select>
        <div class="pointer-events-none absolute inset-y-0 right-0 flex items-center px-2 text-zinc-500">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
        </div>
    </div>
</div>
```

### File: `web/pages/instances.html` (new)

Instance management page with:
- List of all instances with status, host, mode
- Add instance dialog with mode selection and install command display
- Test connection button
- Remove instance button

### File: `web/index.html`

Add new page include and script:

```html
<!-- In the page content div: -->
<!--#include "pages/instances.html"-->

<!-- In the scripts section: -->
<script src="/assets/modules/instances.js"></script>
```

### File: `web/assets/app.js`

Add instances module to the merge list:

```js
const modules = [D.ui, D.auth, D.charts, D.computed, D.dashboard,
                 D.containers, D.templates, D.services, D.images,
                 D.domains, D.files, D.updateBanner, D.registry,
                 D.instances];  // ← NEW
```

---

## Step 6: Credential Scoping

### File: `internal/registry/registry.go`

Modify `GetAuthHeader` and `GetTokenForDomain` to accept an optional `instanceID`:

```go
// GetAuthHeaderForInstance returns auth header with instance-specific → global fallback.
func (m *Manager) GetAuthHeaderForInstance(imageRef string, instanceID string) (string, error) {
    domain := ExtractDomain(imageRef)
    if domain == "" {
        return "", nil
    }

    // 1. Try instance-specific credential
    cred, err := m.db.FindRegistryCredentialByDomainAndInstance(domain, instanceID)
    if err == nil && cred != nil {
        return m.buildAuthHeader(cred)
    }

    // 2. Try alias fallback for instance-specific
    if alias, ok := registryAliases[domain]; ok {
        cred, err = m.db.FindRegistryCredentialByDomainAndInstance(alias, instanceID)
        if err == nil && cred != nil {
            return m.buildAuthHeader(cred)
        }
    }

    // 3. Fall back to global credential (instance_id = "")
    cred, err = m.db.FindRegistryCredentialByDomain(domain)
    if err == nil || cred != nil {
        if cred != nil {
            return m.buildAuthHeader(cred)
        }
    }

    // 4. Try alias for global
    if alias, ok := registryAliases[domain]; ok {
        cred, err = m.db.FindRegistryCredentialByDomain(alias)
        if err == nil && cred != nil {
            return m.buildAuthHeader(cred)
        }
    }

    return "", nil
}
```

The existing `GetAuthHeader` method stays as-is (uses global scope) for backward compatibility.

---

## Step 7: Move System Info Functions

Several functions in `routes.go` read host system info directly from `/proc`, `/sys/fs/cgroup`, etc. These only work for the local instance. For remote instances, we need to get this info from the agent.

### Move from `routes.go` to `internal/agent/local.go`:

- `getSystemInfo()`
- `getMemoryInfo()`
- `getCgroupMemoryUsage()`
- `getCPUPercent()`
- `getHostname()`
- `SystemInfo` struct

These become methods on `LocalClient`:

```go
func (l *LocalClient) GetHostInfo(ctx context.Context) (*HostInfo, error) {
    // Uses the moved functions
}

func (l *LocalClient) GetHostStats(ctx context.Context) (*HostStats, error) {
    // Uses the moved functions
}
```

For `DirectClient`, these call the agent's `/agent/host/info` and `/agent/host/stats` endpoints.

**Route handler access pattern after move:**

```go
// BEFORE (current code in routes.go):
protected.GET("/system/info", func(c *gin.Context) {
    info := getSystemInfo(dockerClient)  // direct function call
    c.JSON(http.StatusOK, info)
})

// AFTER (instance-aware):
func handleSystemInfo(c *gin.Context) {
    client := c.MustGet("agent_client").(agent.AgentClient)
    info, err := client.GetHostInfo(c.Request.Context())
    stats, err2 := client.GetHostStats(c.Request.Context())
    // Merge into SystemInfo format for frontend compatibility
    c.JSON(http.StatusOK, SystemInfo{
        Hostname:      info.Hostname,
        OS:            info.OS,
        CPUCores:      info.CPUCores,
        CPUPercent:    stats.CPUPercent,
        TotalRAM:      stats.TotalRAM,
        UsedRAM:       stats.UsedRAM,
        TotalDisk:     stats.TotalDisk,
        UsedDisk:      stats.UsedDisk,
        DockerVersion: info.DockerVersion,
    })
}
```

The `SystemInfo` struct stays in `internal/server/` (it's an API response type). The underlying data collection moves to `internal/agent/local.go`.

---

## Implementation Order

The steps should be implemented in this order to maintain a working build at each stage:

### 1. Database layer (no breaking changes)
- Add `Instance` struct and `instances` bucket to `db.go`
- Add `instance_id` fields to `Service`, `Domain`, `RegistryCredential`
- Add instance CRUD methods
- Add `EnsureLocalInstance()`
- Add instance-scoped query methods
- **Test:** Run existing tests, ensure nothing breaks

### 2. Agent types + LocalClient (no route changes)
- Create `internal/agent/types.go` with `AgentClient` interface
- Create `internal/agent/local.go` wrapping `docker.Client`
- Move system info functions from `routes.go` to `local.go`
- **Test:** Unit test LocalClient against docker.Client

### 3. AgentManager (no route changes yet)
- Create `internal/agent/manager.go`
- Create `internal/agent/direct.go` (stub — can't test without agent)
- Create `internal/agent/edge.go` (stub — can't test without agent)
- Wire up in `main.go`: create Manager, pass to routes (unused for now)
- **Test:** Build succeeds, existing functionality unchanged

### 4. Instance routes (new routes, no existing route changes)
- Create `internal/server/instance_routes.go`
- Create `internal/server/agent_ws.go` (stub for now)
- Add instance CRUD endpoints
- Add enrollment endpoint (generate token + install command)
- **Test:** Can create/list/delete instances via API

### 5. Instance-scoped routes (new route group)
- Create `internal/server/instance_routes.go` scoped handlers
- Add `InstanceMiddleware`
- Register `/api/instances/:instance_id/*` routes
- For "local" instance, handlers work via LocalClient
- **Test:** `/api/instances/local/containers` returns same data as `/api/containers`

### 6. Backward-compatible route migration
- Modify existing route handlers to proxy through AgentManager
- All `/api/containers`, `/api/deploy/*`, etc. now go through LocalClient
- **Test:** All existing functionality works unchanged

### 7. Frontend — Instance state & selector
- Add instance state to `state.js`
- Create `instances.js` module
- Add instance selector to `sidebar.html`
- Create `instances.html` page
- Modify `dashboard.js` to use `instanceApi()`
- **Test:** Can switch instances in UI, local instance works as before

### 8. Frontend — Instance-aware all pages
- Modify `containers.js` to use `instanceApi()`
- Modify `services.js`, `images.js`, `domains.js`, `files.js`, `registry.js`, `templates.js`
- Modify `auth.js` to add `instanceApi()` helper
- **Test:** All pages work with local instance, instance selector visible

### 9. Credential scoping
- Add `instance_id` support to registry manager
- Modify deploy routes to pass instance ID for credential lookup
- **Test:** Global credentials work for all instances

### 10. Direct mode connection
- Implement `DirectClient` HTTP calls
- Implement agent connectivity test
- **Test:** Can connect to a running agent (requires agent binary from Phase 1A)

### 11. Edge mode connection
- Implement `agent_ws.go` WebSocket endpoint
- Implement `EdgeConnection` in manager
- Implement request/response multiplexing
- **Test:** Edge agent can connect and respond to requests

---

## Risk Areas & Mitigations

| Risk | Mitigation |
|------|-----------|
| BBolt schema migration — existing data has no `instance_id` | Empty `instance_id` treated as "local" — no migration needed |
| `routes.go` is 1564 lines — refactoring is risky | Add new handlers in separate file, keep existing routes as thin wrappers |
| WebSocket for edge mode is complex | Implement direct mode first (simpler), edge mode later |
| Existing tests may break | Each step maintains build; run tests after each step |
| Frontend API path changes | Use `instanceApi()` helper; existing `api()` still works for non-scoped routes |
| System info functions moved from routes.go | Move to agent/local.go, import from there — no logic change |

---

## Resolved Gaps & Design Decisions

> These issues were identified during review and are now resolved. Each decision is documented here to prevent ambiguity during implementation.

### G1: Agent Token Storage — Plaintext Needed for DirectClient

**Problem:** The Instance struct stored `AgentTokenHash` (bcrypt), but `DirectClient` needs the plaintext token to send in `Authorization: Bearer <token>`. Bcrypt is one-way — plaintext cannot be recovered.

**Decision:** Store the token in two forms:
- `AgentTokenHash` (bcrypt) — for verifying tokens during enrollment (edge mode agent sends its token, Server verifies against hash)
- `AgentTokenEncrypted` (AES-256-GCM, encrypted with the same key as registry credentials) — for retrieving plaintext when DirectClient needs to send it

This mirrors the existing pattern: `RegistryCredential.EncryptedToken` stores encrypted plaintext for the same reason (need to send it to Docker, but don't want plaintext in DB).

**Token lifecycle:**
1. Server generates random 32-byte token
2. Server bcrypt-hashes it → `AgentTokenHash`
3. Server AES-encrypts the plaintext → `AgentTokenEncrypted`
4. Both stored in Instance record
5. DirectClient decrypts `AgentTokenEncrypted` on demand
6. Enrollment verification compares against `AgentTokenHash`

### G2: SystemInfo vs HostInfo/HostStats — Frontend Compatibility

**Problem:** The current frontend uses a single `systemInfo` object from `GET /api/system/info`:
```js
systemInfo.cpu_percent, systemInfo.total_ram, systemInfo.used_ram, etc.
```
The Agent exposes two separate endpoints: `/agent/host/info` (static) and `/agent/host/stats` (dynamic). The Server plan's instance-scoped routes mirror this split: `/host/info` and `/host/stats`.

**Decision:** The Server provides a **combined** `/system/info` endpoint for instance-scoped routes that merges HostInfo + HostStats into the existing `SystemInfo` format. This way the frontend doesn't need to change its data shape — only the API path changes.

```go
// Instance-scoped handler
func handleSystemInfo(c *gin.Context) {
    client := c.MustGet("agent_client").(agent.AgentClient)
    info, err := client.GetHostInfo(c.Request.Context())
    stats, err2 := client.GetHostStats(c.Request.Context())

    // Merge into SystemInfo format (same JSON shape as current)
    c.JSON(http.StatusOK, SystemInfo{
        Hostname:      info.Hostname,
        OS:            info.OS,
        CPUCores:      info.CPUCores,
        CPUPercent:    stats.CPUPercent,
        TotalRAM:      stats.TotalRAM,
        UsedRAM:       stats.UsedRAM,
        TotalDisk:     stats.TotalDisk,
        UsedDisk:      stats.UsedDisk,
        DockerVersion: info.DockerVersion,
    })
}
```

For `LocalClient`, `GetHostInfo` and `GetHostStats` use the same `/proc`/cgroup functions as today, just split into two methods.

### G3: Deploy Stream — 3-Party WebSocket Relay

**Problem:** Deploy streaming involves three parties: Browser ↔ Server ↔ Agent. The current flow is:
1. Browser: `POST /api/deploy/stream` → gets `deploy_id`
2. Browser: `GET /api/deploy/stream/{deploy_id}?token=xxx` → WebSocket for events

For remote instances, the Agent also has its own deploy stream:
1. Server: `POST /agent/docker/deploy/stream` → gets agent's `deploy_id`
2. Server: `GET /agent/docker/deploy/stream/{deploy_id}?token=xxx` → WebSocket for events

**Decision:** The Server owns the browser-facing session. The Server proxies events from the Agent to the browser.

**Flow for direct mode:**
1. Browser → `POST /api/instances/{id}/deploy/stream` → Server creates its own `DeploySession`
2. Server → `POST /agent/docker/deploy/stream` to Agent → gets agent `deploy_id`
3. Server opens WebSocket to Agent: `GET /agent/docker/deploy/stream/{agent_deploy_id}?token=xxx`
4. Server reads events from Agent WebSocket, writes them to its own `DeploySession.Events` channel
5. Browser → `GET /api/instances/{id}/deploy/stream/{server_deploy_id}?token=xxx` → reads from Server's session

**Flow for edge mode:**
1. Same as above, but step 2-3 uses the edge WebSocket channel
2. Server sends `AgentRequest` with `Method: "POST"`, `Path: "/docker/deploy/stream"` via edge WS
3. Agent responds with `deploy_id`
4. Server sends `AgentRequest` with `Method: "WS"`, `Path: "/docker/deploy/stream/{deploy_id}"` via edge WS
5. Agent streams `AgentResponse` messages with `Stream: true` back
6. Server forwards each chunk to its `DeploySession.Events` channel

**For LocalClient:** No relay needed — the existing `DeployComposeStreamed` method writes directly to the session.

**Data structure for relay tracking:**

```go
// In the deploy handler or AgentManager
type DeployRelay struct {
    ServerSessionID string
    AgentSessionID  string
    InstanceID      string
    CreatedAt       time.Time
}

// Server maintains active relays (cleaned up after 30s like existing sessions)
deployRelays map[string]*DeployRelay  // server_session_id → relay info
```

**Timeout handling:**
- Server sets a 60-second timeout per request sent to edge agents
- If Agent doesn't respond within 60s, the relay is terminated and browser receives an error event
- Direct mode uses HTTP client timeout (same 60s)
- Browser-facing WebSocket has its own keep-alive (existing behavior, unaffected)

### G4: Git Deploy for Remote Instances

**Problem:** The Server currently has `POST /api/deploy/git` which clones a git repo, reads the compose YAML, and deploys it. The Agent doesn't have a git endpoint (it doesn't store credentials or have git installed).

**Decision:** Git clone happens on the **Server side**. The Server:
1. Clones the repo (using stored GitHub credentials)
2. Reads the compose YAML from the cloned files
3. Sends the compose YAML to the Agent's `POST /agent/docker/deploy/stream` (or `/deploy/compose`)

The instance-scoped `/deploy/git` handler does the git work on the Server, then delegates the actual Docker deploy to the Agent. This means:
- Git credentials stay on the Server (no need to send to Agent)
- Agent doesn't need git installed
- Same pattern works for both direct and edge mode

### G5: Traefik, Cloudflare Tunnel, Auto-Recovery for Remote Instances

**Problem:** These features currently run on the Server's local Docker daemon. For remote instances, they need to run on the remote host, but the Agent doesn't have endpoints for them.

**Decision for Phase 1B/1C:** These features are **Server-local only** for the MVP. They are NOT available for remote instances in the initial release.

- **Traefik config:** Only generated for the local instance. Remote instances must manage their own reverse proxy.
- **Cloudflare Tunnel:** Only deployable on the local instance. Remote instances must set up their own tunnel.
- **Auto-recovery:** Only monitors local containers. Remote instance recovery is a Phase 2 feature (requires Agent-side health monitor).

**Phase 2 plan:** Add health monitor to the Agent (runs independently, restarts crashed containers). Add Traefik config generation endpoint to the Agent. Add tunnel deploy endpoint to the Agent.

**UI implication:** The Domains, Tunnel, and auto-recover features should be hidden or disabled when a remote instance is selected. The sidebar nav items or page sections can check `selectedInstance === 'local'`.

### G6: Naming Consistency — AgentRequest vs AgentMessage

**Problem:** The Server plan uses `AgentRequest`/`AgentResponse` in `manager.go`. The Agent plan uses `AgentMessage`/`AgentResponse` in `edge/protocol.go`.

**Decision:** Unified naming: **`AgentRequest`** and **`AgentResponse`** on both sides. The Agent's `edge/protocol.go` will use `AgentRequest` (not `AgentMessage`). This avoids confusion during implementation.

### G7: Missing Agent Endpoints — StopCompose, RemoveCompose

**Problem:** The Server's service deletion calls `dockerClient.RemoveCompose()`. The Agent plan doesn't include `StopCompose` or `RemoveCompose` endpoints.

**Decision:** Add to Agent API:
- `POST /agent/docker/compose/stop` — Stop all containers for a project
- `POST /agent/docker/compose/remove` — Remove all containers + compose files for a project

These are needed when the user deletes a service from the dashboard.

### G8: Frontend Pages Not Covered

**Problem:** The Server plan mentions modifying `containers.js`, `dashboard.js`, etc. but doesn't mention:
- `deploy.html` page — has compose/git deploy forms
- `template-config.html` page — has template deploy with streaming
- `services.js` module — service list and deletion

**Decision:** All pages that make API calls to Docker/deploy endpoints must be updated to use `instanceApi()`. The complete list:
- `dashboard.js` — containers list, system info
- `containers.js` — all container operations
- `services.js` — service list, delete (calls RemoveCompose)
- `templates.js` + template-config page — template deploy
- `images.js` — image list, pull, remove
- `domains.js` — domain CRUD (local-only for now, but data scoped by instance)
- `files.js` — file operations
- `registry.js` — registry CRUD (instance + global)
- `deploy.html` page logic (currently inline in services.js) — compose/git deploy

### G9: Agent Version / Capability Negotiation

**Problem:** If the Server adds new API endpoints, older agents won't support them. There's no mechanism to detect what an agent supports.

**Decision:** The Agent reports its version during enrollment (already in `Instance.AgentVersion`). The Server can maintain a capability map: `{ "0.1.0": ["containers", "deploy", "images", "files", "host"] }`. If a feature requires a newer agent, the Server returns a clear error: `"this feature requires agent v0.2.0+, current agent is v0.1.0"`.

For MVP, all agents are expected to be the same version as the Server. Version checking is a safety net, not a primary feature.

### G10: Credential Delivery to Agent — Multi-Registry Auth in Deploy Requests

**Problem:** The Server plan's deploy handlers need to pass registry credentials to the Agent. The current `DeployCompose` method takes a `getAuthHeader AuthHeaderFunc` that is called **per image** (supporting multiple registries in one compose file). For remote instances, the Server needs to resolve all credentials and send them in the deploy request body.

**Decision:** The Server resolves **all** matching credentials for the compose file's images, then sends them as a map:

```go
// Instance-scoped deploy handler
func handleDeployCompose(c *gin.Context) {
    client := c.MustGet("agent_client").(agent.AgentClient)
    instanceID := c.MustGet("instance_id").(string)

    // Parse compose to extract all image references
    cf, _ := docker.ParseComposeFile(req.Compose)

    // Resolve credentials for all unique registry domains
    registryAuths := make(map[string]string) // domain → base64 auth header
    for _, svc := range cf.Services {
        domain := registry.ExtractDomain(svc.Image)
        if domain == "" || registryAuths[domain] != "" {
            continue // skip Docker Hub or already resolved
        }
        auth, _ := registryManager.GetAuthHeaderForInstance(svc.Image, instanceID)
        if auth != "" {
            registryAuths[domain] = auth
        }
    }

    err := client.DeployCompose(c.Request.Context(), req.Name, req.Compose, registryAuths)
}
```

The `AgentClient.DeployCompose` signature uses a map to support multiple registries:

```go
type AgentClient interface {
    // ...
    DeployCompose(ctx context.Context, name, composeYAML string, registryAuths map[string]string) error
    DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, registryAuths map[string]string) error
    // ...
}
```

**Implementation per client type:**

- `LocalClient` builds a `getAuthHeader` function from the map: `func(imageRef) { return registryAuths[extractDomain(imageRef)] }`
- `DirectClient` sends `registry_auths` as JSON map in the HTTP request body to the Agent
- `EdgeClient` sends `registry_auths` in the `AgentRequest.Body`

**Agent side:** The Agent receives `registry_auths` map and builds its own `getAuthHeader` function:

```go
getAuthHeader := func(imageRef string) (string, error) {
    domain := extractDomain(imageRef)
    if auth, ok := req.RegistryAuths[domain]; ok {
        return auth, nil
    }
    return "", nil // no auth for this domain
}
```
