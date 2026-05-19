# Implementation Plan: Multi-Instance Server

## Overview

This plan implements Phase 1B of the DockPal multi-instance architecture, extending the Server to manage Docker containers across multiple remote hosts from a single dashboard. The implementation follows a strict ordering that maintains a working build at each stage: database layer first, then agent types, then manager, then new routes, then backward-compatible migration, and finally frontend changes.

## Tasks

- [x] 1. Database layer — Instance model and CRUD methods
  - [x] 1.1 Add Instance struct and bucket to internal/db/db.go
    - Add the Instance struct with all fields (ID, Name, Host, Port, Mode, AgentTokenHash, AgentTokenEncrypted, AgentVersion, Status, DockerVersion, OS, CPUCores, TotalMemory, LastSeen, CreatedAt)
    - Add `bucketInstances = []byte("instances")` to the bucket list
    - Create the bucket in the `New()` function alongside existing buckets
    - Add InstanceID field to Service, Domain, and RegistryCredential structs (empty string = local/global)
    - _Requirements: 1.1, 1.2, 1.4, 1.5, 1.6_

  - [x] 1.2 Implement Instance CRUD methods in internal/db/db.go
    - Implement SaveInstance, GetInstance, ListInstances, DeleteInstance
    - Implement UpdateInstanceStatus, UpdateInstanceLastSeen, UpdateInstanceInfo
    - Implement EnsureLocalInstance (creates ID "local", Name "This Server", Mode "local", Status "online" if not exists)
    - DeleteInstance must reject deletion of ID "local" with an error
    - GetInstance and DeleteInstance must return "not found" error for non-existent IDs
    - _Requirements: 1.3, 1.7, 1.8, 1.9_

  - [x] 1.3 Implement instance-scoped query methods in internal/db/db.go
    - Implement ListServicesByInstance: for "local" return services with empty InstanceID; for others match InstanceID
    - Implement ListDomainsByInstance: same scoping logic as services
    - Implement FindRegistryCredentialByDomainAndInstance: case-insensitive domain match, instance-specific first then global fallback
    - _Requirements: 1.10, 1.11, 9.1, 9.2, 9.3, 9.5_

  - [x] 1.4 Write property tests for Instance persistence (Property 1)
    - **Property 1: Instance persistence round-trip**
    - Create `internal/db/db_instance_prop_test.go`
    - Generate random valid Instance structs, save and retrieve, assert field equality
    - Use pgregory.net/rapid for generation
    - **Validates: Requirements 1.1, 1.7**

  - [x] 1.5 Write property tests for instance-scoped service filtering (Property 2)
    - **Property 2: Instance-scoped service filtering**
    - Generate sets of Service records with varying InstanceIDs
    - Assert ListServicesByInstance returns exactly matching services
    - Assert "local" query returns services with empty InstanceID
    - **Validates: Requirements 1.4, 1.10, 1.11**

  - [x] 1.6 Write property tests for credential scoping lookup (Property 3)
    - **Property 3: Credential scoping lookup order**
    - Generate instance-specific and global credentials for same domain
    - Assert instance-specific takes priority over global
    - Assert case-insensitive domain matching
    - **Validates: Requirements 9.1, 9.2, 9.3, 9.5**

  - [x] 1.7 Write property test for local instance deletion protection (Property 12)
    - **Property 12: Local instance deletion protection**
    - Assert DeleteInstance("local") always returns error
    - Assert local instance record remains unchanged after attempted deletion
    - **Validates: Requirements 4.9, 1.9**

- [x] 2. Checkpoint — Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

- [x] 3. Agent types — AgentClient interface and LocalClient
  - [x] 3.1 Create internal/agent/types.go with AgentClient interface
    - Define HostInfo and HostStats structs
    - Define AgentClient interface with all methods (ListContainers, InspectContainer, StartContainer, StopContainer, RestartContainer, RemoveContainer, EditContainer, GetContainerStats, ContainerLogs, DeployCompose, DeployComposeStreamed, ListImages, PullImage, PullImageWithAuth, RemoveImage, GetHostInfo, GetHostStats, Ping, Close)
    - Define AgentRequest and AgentResponse structs for edge communication
    - _Requirements: 2.1_

  - [x] 3.2 Create internal/agent/local.go implementing LocalClient
    - Implement NewLocalClient wrapping existing docker.Client
    - Delegate all container/image/deploy methods directly to docker.Client
    - Move getSystemInfo, getMemoryInfo, getCgroupMemoryUsage, getCPUPercent, getHostname from routes.go into GetHostInfo and GetHostStats implementations
    - Implement Ping delegating to docker.Client.Ping
    - Close() is a no-op (lifecycle managed by main.go)
    - _Requirements: 2.2, 2.5, 2.6, 13.1, 13.2, 13.3, 13.4_

  - [x] 3.3 Create internal/agent/direct.go implementing DirectClient
    - Implement NewDirectClient with instance ID, host, port, auth token
    - Configure HTTP client with 30s timeout and TLS config accepting self-signed certs
    - Implement all AgentClient methods as HTTP requests to agent REST API
    - Include Bearer token in Authorization header for all requests
    - Implement DeployComposeStreamed with WebSocket relay from agent
    - _Requirements: 2.3, 2.7, 2.8, 2.9_

  - [x] 3.4 Create internal/agent/edge.go implementing EdgeClient
    - Implement NewEdgeClient with instance ID and Manager back-reference
    - Implement all AgentClient methods by sending AgentRequest through Manager.SendEdgeRequest
    - Each request uses UUID v4 for request_id
    - DeployComposeStreamed handles streaming responses (Stream: true) until completion
    - _Requirements: 2.4, 2.10_

- [x] 4. Agent Manager — Connection management
  - [x] 4.1 Create internal/agent/manager.go
    - Implement Manager struct with db, cryptoKey, sync.RWMutex, local *LocalClient, edge map[string]*EdgeConnection
    - Implement NewManager(database, localDocker, jwtSecret)
    - Implement GetClient routing: "local" → LocalClient, direct → new DirectClient with decrypted token, edge → EdgeClient if connected else error
    - Implement RegisterEdgeConnection, UnregisterEdgeConnection
    - Implement SendEdgeRequest with 60s timeout and request_id correlation
    - Implement edgeReadLoop for routing responses to pending channels
    - Implement Close for graceful shutdown
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9, 3.10, 3.11_

  - [x] 4.2 Write property tests for GetClient routing (Property 4)
    - **Property 4: GetClient routing by mode**
    - Create `internal/agent/manager_prop_test.go`
    - Assert LocalClient returned for "local", DirectClient for direct mode, EdgeClient for edge with connection
    - Assert offline error for edge without connection, not-found for non-existent IDs
    - **Validates: Requirements 2.11, 3.2, 3.3, 3.4, 3.5, 3.6**

  - [x] 4.3 Write property tests for edge request/response multiplexing (Property 7)
    - **Property 7: Edge request/response multiplexing**
    - Simulate N concurrent requests with unique request_ids
    - Assert each caller receives exactly its own response matched by request_id
    - **Validates: Requirements 6.4, 6.7, 3.9**

- [x] 5. Checkpoint — Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

- [x] 6. Instance CRUD routes
  - [x] 6.1 Create internal/server/instance_routes.go with RegisterInstanceRoutes
    - Implement handleCreateInstance: validate name (1–100 chars), host, port (1–65535), mode ("direct"/"edge"); generate 32-byte random token; store bcrypt hash + AES-256-GCM encrypted; return instance + install command
    - Implement handleListInstances: return all instances with id, name, host, port, mode, status, last_seen
    - Implement handleGetInstance: return full instance details or 404
    - Implement handleUpdateInstance: update name/host/port with validation, return updated record or 404
    - Implement handleDeleteInstance: reject "local" with 403, remove record + disconnect agent, or 404
    - Implement handleTestInstance: 10s timeout connectivity test, return result
    - Implement handleRotateToken: generate new token, update hash + encrypted, return new install command
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9, 4.10, 4.11_

  - [x] 6.2 Implement install command generation helpers
    - Generate direct-mode Docker run command: image sdldev/dockpal-agent:latest, DOCKPAL_MODE=direct, DOCKPAL_TOKEN, port mapping, docker.sock volume
    - Generate edge-mode Docker run command: image sdldev/dockpal-agent:latest, DOCKPAL_MODE=edge, DOCKPAL_SERVER=wss://{host}:3012/api/agent/connect, DOCKPAL_TOKEN, docker.sock volume, no port mapping
    - _Requirements: 5.1, 5.2_

  - [x] 6.3 Write property tests for install command generation (Property 5)
    - **Property 5: Install command generation correctness**
    - Create `internal/server/instance_routes_prop_test.go`
    - Assert direct mode includes port mapping, correct env vars, volume mount
    - Assert edge mode includes DOCKPAL_SERVER, no port mapping
    - **Validates: Requirements 5.1, 5.2**

  - [x] 6.4 Write property tests for agent token verification (Property 6)
    - **Property 6: Agent token verification**
    - Generate random 32-byte tokens, store bcrypt hash, verify original matches
    - Assert modified tokens do not match
    - **Validates: Requirements 5.3, 5.4**

  - [x] 6.5 Write property tests for instance input validation (Property 8)
    - **Property 8: Instance input validation**
    - Assert invalid mode/port/name rejected with 400
    - Assert valid inputs accepted with 200/201
    - **Validates: Requirements 4.11**

  - [x] 6.6 Write property tests for token rotation (Property 13)
    - **Property 13: Token rotation produces distinct credentials**
    - Assert new hash differs from previous hash after rotation
    - Assert new encrypted token decrypts to different plaintext
    - **Validates: Requirements 4.8**

- [x] 7. Agent WebSocket endpoint and enrollment
  - [x] 7.1 Create internal/server/agent_ws.go
    - Implement HandleAgentConnect: WebSocket upgrade, 10s auth timeout, token verification via bcrypt against all instances
    - On successful auth: RegisterEdgeConnection, update status to "online", update LastSeen, request host info
    - On auth failure: close with code 4001
    - On timeout: close connection
    - Implement verifyAgentToken helper scanning instances for bcrypt match
    - Handle edge agent disconnect: mark offline, unregister connection
    - _Requirements: 5.3, 5.4, 5.5, 5.6, 5.7, 5.8, 5.9, 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 6.8, 6.9_

- [x] 8. Checkpoint — Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

- [x] 9. Instance-scoped routes
  - [x] 9.1 Create internal/server/middleware.go InstanceMiddleware
    - Resolve instance_id from URL param
    - Call agentMgr.GetClient(instanceID)
    - Return 404 if not found, 503 if offline
    - Set "instance_id" and "agent_client" in Gin context
    - _Requirements: 7.1_

  - [x] 9.2 Create internal/server/instance_scoped_routes.go with RegisterInstanceScopedRoutes
    - Implement instance-scoped container routes: GET /containers, GET /containers/:id, POST start/stop/restart, DELETE, PUT, GET stats, GET logs
    - Each handler extracts AgentClient from context and delegates
    - _Requirements: 7.2_

  - [x] 9.3 Add instance-scoped deploy routes
    - Implement POST /deploy/stream with deploy relay for remote instances
    - Implement POST /deploy/compose delegating through AgentClient
    - Implement POST /deploy/git: git clone on server, send compose YAML to agent
    - Resolve registry credentials per-instance using FindRegistryCredentialByDomainAndInstance
    - _Requirements: 7.3, 7.10, 9.3, 9.4_

  - [x] 9.4 Add instance-scoped image, host, service, domain, and registry routes
    - Image routes: GET /images, POST /images/pull, DELETE /images/:id
    - Host routes: GET /host/info, GET /host/stats, GET /system/info (merge HostInfo + HostStats into SystemInfo format)
    - Service routes: GET /services (instance-scoped), DELETE /services/:id
    - Domain routes: GET /domains, POST /domains, DELETE /domains/:id
    - Registry routes: GET /registries (return both instance-specific and global with scope indicator), POST, GET/:id, PUT/:id, DELETE/:id, POST/:id/test
    - _Requirements: 7.4, 7.5, 7.6, 7.7, 7.8, 7.9, 9.6_

  - [x] 9.5 Write property tests for multi-registry credential resolution (Property 9)
    - **Property 9: Multi-registry credential resolution**
    - Create `internal/agent/credential_prop_test.go`
    - Generate compose YAML with N distinct registry domains
    - Assert resolveRegistryAuths produces correct map with instance-then-global fallback
    - **Validates: Requirements 9.4**

  - [x] 9.6 Write property tests for SystemInfo merge (Property 14)
    - **Property 14: SystemInfo merge completeness**
    - Create `internal/server/system_info_prop_test.go`
    - Assert merged response contains all fields from HostInfo and HostStats with matching values
    - **Validates: Requirements 7.9, 13.5**

- [x] 10. Deploy stream relay for remote instances
  - [x] 10.1 Implement deploy relay logic in internal/server/instance_scoped_routes.go
    - Create DeployRelay struct tracking server_session_id → agent_session_id mapping
    - For direct mode: open WebSocket to agent deploy stream, forward events to browser session
    - For edge mode: send deploy request via edge channel, forward streamed AgentResponse chunks
    - Handle timeouts: 60s for first event, error DeployEvent on timeout
    - Handle disconnects: agent drop → error event to browser; browser drop → stop reading from agent
    - Cleanup relay mapping 30s after terminal state
    - _Requirements: 14.1, 14.2, 14.3, 14.4, 14.5, 14.6, 14.7, 14.8, 14.9_

  - [x] 10.2 Write property tests for deploy event relay order (Property 10)
    - **Property 10: Deploy event relay preserves order**
    - Create `internal/server/deploy_relay_prop_test.go`
    - Generate sequences of DeployEvent messages, assert relay preserves order and field values
    - **Validates: Requirements 14.1**

- [x] 11. Checkpoint — Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

- [x] 12. Backward-compatible route migration
  - [x] 12.1 Update RegisterRoutes signature and wire Agent_Manager
    - Add agentMgr *agent.Manager parameter to RegisterRoutes
    - Register instance routes: call RegisterInstanceRoutes(protected, database, agentMgr, jwtSecret)
    - Register agent WebSocket: r.GET("/api/agent/connect", HandleAgentConnect(database, agentMgr))
    - Register instance-scoped group: instances := protected.Group("/instances/:instance_id") with InstanceMiddleware
    - Call RegisterInstanceScopedRoutes on the instances group
    - _Requirements: 8.3_

  - [x] 12.2 Migrate existing routes to delegate through LocalClient
    - Update container routes (GET /containers, GET /containers/:id, POST start/stop/restart, DELETE, PUT, GET stats, GET logs) to use agentMgr.GetClient("local")
    - Update deploy routes to use LocalClient
    - Update image routes to use LocalClient
    - Update system/info route to use LocalClient.GetHostInfo() + GetHostStats()
    - Remove direct calls to getSystemInfo, getMemoryInfo, getCgroupMemoryUsage, getCPUPercent, getHostname from routes.go
    - Retain dockerClient parameter for backward compatibility but delegate through agent layer
    - _Requirements: 8.1, 8.2, 8.4, 8.5, 13.5, 13.6_

  - [x] 12.3 Write property tests for backward-compatible route equivalence (Property 11)
    - **Property 11: Backward-compatible route equivalence**
    - Create `internal/server/backward_compat_prop_test.go`
    - Assert existing routes produce identical JSON structure and status codes as instance-scoped routes with "local"
    - **Validates: Requirements 8.1, 8.4**

- [x] 13. Main initialization changes
  - [x] 13.1 Update main.go to initialize Agent_Manager
    - Create agent.NewManager(database, dockerClient, jwtSecret) after Docker client init
    - Call database.EnsureLocalInstance() — fatal on failure
    - Pass agentMgr to RegisterRoutes
    - Call agentMgr.Close() on shutdown before dockerClient.Close() and database.Close()
    - _Requirements: 15.1, 15.2, 15.3, 15.4, 15.5, 15.6_

- [x] 14. Checkpoint — Ensure all tests pass and build succeeds
  - Ensure all tests pass, ask the user if questions arise.

- [x] 15. Frontend — Instance selector and state management
  - [x] 15.1 Update web/assets/modules/state.js with instance state
    - Add instances array and selectedInstance (from localStorage, default "local")
    - Add instanceApi(method, path, body) helper that prepends /api/instances/{selectedInstance}
    - Add selectInstance(id) method: save to localStorage, reset page state, reload dashboard
    - Add isLocalInstance computed property
    - Add loadInstances() method fetching GET /api/instances
    - _Requirements: 10.4, 10.5, 12.1_

  - [x] 15.2 Add instance selector to web/partials/sidebar.html
    - Add native HTML select element between logo and nav items
    - Show status indicator (green/red/yellow circle) + instance name for each option
    - "This Server" always first with gear icon
    - Bind to selectedInstance state, trigger selectInstance on change
    - _Requirements: 10.1, 10.2, 10.3, 10.7_

  - [x] 15.3 Add "Instances" nav item to sidebar under Settings group
    - Link to instances management page
    - _Requirements: 10.6_

  - [x] 15.4 Update frontend pages to use instanceApi
    - Update dashboard.js to use instanceApi for containers and system info
    - Update containers.js for all container operations
    - Update services.js for service listing/deletion
    - Update images.js for image operations
    - Update deploy functionality (compose, git, template) to use instanceApi
    - Update registry.js for credential CRUD
    - _Requirements: 12.2, 12.3, 12.4, 12.5, 12.6, 12.7_

  - [x] 15.5 Implement local-only feature visibility
    - Hide Traefik domain management, Cloudflare tunnel, and auto-recovery when remote instance selected
    - Show them when Local_Instance is selected
    - Use isLocalInstance computed property for conditional rendering
    - _Requirements: 12.9, 12.10_

  - [x] 15.6 Handle WebSocket URLs and instance errors in frontend
    - Construct WebSocket URLs with instance-scoped paths and JWT token query param
    - Use wss: when page loaded over HTTPS, ws: otherwise
    - Display inline error message when instanceApi returns offline/unreachable error
    - _Requirements: 12.8, 12.11_

- [x] 16. Frontend — Instance management page
  - [x] 16.1 Create instance management page
    - Create web/pages/instances.html or add instances section to existing page structure
    - Display table with columns: name, host, mode, status (with badge), last seen (relative time)
    - Show empty state message when no remote instances exist
    - _Requirements: 11.1, 11.7, 11.8_

  - [x] 16.2 Implement Add Instance dialog
    - Fields: name (1–64 chars), host (direct only), port (1–65535, default 9273, direct only), mode selector
    - Hide host/port when edge mode selected
    - Disable submit until valid (name non-empty, host non-empty for direct, valid port)
    - On success: display install command with copy-to-clipboard button
    - _Requirements: 11.2, 11.3, 11.4, 11.9_

  - [x] 16.3 Implement Test Connection and Remove actions
    - Test Connection: call POST /api/instances/:id/test, show success/error toast
    - Remove: confirmation dialog using showConfirm, call DELETE /api/instances/:id
    - Disable Test/Remove for offline instances
    - Hide Remove for Local_Instance
    - _Requirements: 11.5, 11.6, 11.7_

- [x] 17. Final checkpoint — Ensure all tests pass and build succeeds
  - Run `go build -o dockpal .` to verify clean build
  - Run `go test ./...` to verify all tests pass
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- Checkpoints ensure incremental validation
- Property tests validate universal correctness properties using pgregory.net/rapid
- Unit tests validate specific examples and edge cases
- The implementation order ensures a working build at each stage with no breaking changes until the migration step
- Frontend changes are last because they depend on all backend routes being available

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2", "1.3"] },
    { "id": 2, "tasks": ["1.4", "1.5", "1.6", "1.7", "3.1"] },
    { "id": 3, "tasks": ["3.2", "3.3", "3.4"] },
    { "id": 4, "tasks": ["4.1"] },
    { "id": 5, "tasks": ["4.2", "4.3", "6.1", "6.2"] },
    { "id": 6, "tasks": ["6.3", "6.4", "6.5", "6.6", "7.1"] },
    { "id": 7, "tasks": ["9.1"] },
    { "id": 8, "tasks": ["9.2", "9.3", "9.4"] },
    { "id": 9, "tasks": ["9.5", "9.6", "10.1"] },
    { "id": 10, "tasks": ["10.2"] },
    { "id": 11, "tasks": ["12.1"] },
    { "id": 12, "tasks": ["12.2", "13.1"] },
    { "id": 13, "tasks": ["12.3"] },
    { "id": 14, "tasks": ["15.1"] },
    { "id": 15, "tasks": ["15.2", "15.3", "15.4"] },
    { "id": 16, "tasks": ["15.5", "15.6", "16.1"] },
    { "id": 17, "tasks": ["16.2", "16.3"] }
  ]
}
```
