# Requirements Document

## Introduction

Phase 1B of the DockPal multi-instance architecture extends the DockPal Server to manage Docker containers across multiple remote hosts from a single dashboard. This phase implements the server-side foundation: database schema changes, an agent manager abstraction layer, instance CRUD routes, enrollment flow, credential scoping, and frontend instance switching. The DockPal Agent binary (Phase 1A) is a separate repository and not covered here.

## Glossary

- **Server**: The DockPal Server application (Go binary with embedded frontend) that provides the dashboard, API, and agent connection management
- **Agent**: A lightweight binary running on a remote host that proxies Docker API calls and reports host health (separate repo, not implemented here)
- **Instance**: A managed Docker host registered in the Server, identified by a unique ID. Can be local (this server), direct (reachable via HTTP), or edge (connects via outbound WebSocket)
- **Local_Instance**: The special instance with ID "local" representing the Docker daemon on the same host as the Server, requiring no agent
- **Direct_Mode**: Connection mode where the Server initiates HTTP requests to a publicly reachable agent
- **Edge_Mode**: Connection mode where the agent initiates an outbound WebSocket connection to the Server (for hosts behind NAT/firewall)
- **Agent_Manager**: The internal component that maintains connections to all registered agents and provides a uniform interface for route handlers
- **Agent_Client**: The interface abstraction that provides uniform Docker operations regardless of connection mode (local, direct, or edge)
- **Local_Client**: Implementation of Agent_Client that wraps the existing docker.Client for the local instance
- **Direct_Client**: Implementation of Agent_Client that communicates with a remote agent via HTTP
- **Edge_Client**: Implementation of Agent_Client that communicates through a multiplexed WebSocket connection
- **Enrollment**: The process of registering a new remote host with the Server, generating an agent token, and verifying the agent connection
- **Agent_Token**: A cryptographically random 32-byte token used to authenticate agent connections, stored as both a bcrypt hash (for verification) and AES-256-GCM encrypted (for DirectClient use)
- **Instance_Selector**: The UI dropdown in the sidebar that allows switching between managed instances
- **Credential_Scoping**: The two-tier model where registry credentials can be global (shared across all instances) or instance-specific
- **BBolt**: The embedded key-value database (go.etcd.io/bbolt) used by DockPal for persistent storage

## Requirements

### Requirement 1: Instance Data Model

**User Story:** As a server administrator, I want the database to store instance records with connection details and status, so that the system can track and manage multiple Docker hosts.

#### Acceptance Criteria

1. THE Server SHALL store Instance records in a dedicated "instances" BBolt bucket with fields: ID (string), Name (string, max 64 characters), Host (string), Port (integer, range 1–65535), Mode (one of "direct" or "edge"), AgentTokenHash (string), AgentTokenEncrypted ([]byte), AgentVersion (string), Status (one of "online", "offline", or "enrolling"), DockerVersion (string), OS (string), CPUCores (integer), TotalMemory (integer, bytes), LastSeen (Unix timestamp integer), and CreatedAt (Unix timestamp integer)
2. WHEN the Server starts, THE Server SHALL create the "instances" bucket if it does not exist
3. WHEN the Server starts, THE Server SHALL ensure a Local_Instance record exists with ID "local", Name "This Server", Mode "local", and Status "online"
4. THE Server SHALL add an InstanceID field to the Service struct, where an empty value indicates the service belongs to the Local_Instance
5. THE Server SHALL add an InstanceID field to the Domain struct, where an empty value indicates the domain belongs to the Local_Instance
6. THE Server SHALL add an InstanceID field to the RegistryCredential struct, where an empty value indicates global scope (available to all instances)
7. THE Server SHALL provide instance CRUD methods: SaveInstance, GetInstance, ListInstances, DeleteInstance, UpdateInstanceStatus, UpdateInstanceLastSeen, and UpdateInstanceInfo
8. IF GetInstance or DeleteInstance is called with an ID that does not exist in the "instances" bucket, THEN THE Server SHALL return a "not found" error
9. IF DeleteInstance is called with the ID "local" and the Local_Instance record exists, THEN THE Server SHALL return an error and SHALL NOT delete the Local_Instance record; IF the Local_Instance record does not exist in the database, THEN DeleteInstance SHALL allow the deletion to proceed without error
10. THE Server SHALL provide instance-scoped query methods: ListServicesByInstance and ListDomainsByInstance
11. WHEN listing services for the local instance (ID "local"), THE Server SHALL return services where InstanceID is empty that also match any other active query filters; WHEN listing services for any other instance, THE Server SHALL return only services where InstanceID matches the requested instance ID

### Requirement 2: Agent Client Interface

**User Story:** As a developer, I want a uniform interface for Docker operations across all instance types, so that route handlers can operate on any instance without knowing the connection mode.

#### Acceptance Criteria

1. THE Server SHALL define an AgentClient interface in the internal/agent package with methods for: ListContainers, InspectContainer, StartContainer, StopContainer, RestartContainer, RemoveContainer, EditContainer, GetContainerStats, ContainerLogs, DeployCompose, DeployComposeStreamed, ListImages, PullImage, PullImageWithAuth, RemoveImage, GetHostInfo, GetHostStats, Ping, and Close
2. THE Server SHALL implement a LocalClient that wraps the existing docker.Client and implements the AgentClient interface by delegating directly to docker.Client methods without network serialization or additional goroutines
3. THE Server SHALL implement a DirectClient that communicates with a remote agent via HTTP requests to the agent REST API
4. THE Server SHALL implement an EdgeClient that communicates through a multiplexed WebSocket connection managed by the Agent_Manager
5. WHEN the LocalClient receives a GetHostInfo request, THE LocalClient SHALL return host information by reading from /proc and system APIs (functions moved from routes.go)
6. WHEN the LocalClient receives a GetHostStats request, THE LocalClient SHALL return real-time CPU, memory, and disk statistics using the same logic currently in routes.go
7. WHEN the DirectClient sends a request, THE DirectClient SHALL include the decrypted agent token in the Authorization header as a Bearer token
8. WHEN the DirectClient connects to an agent, THE DirectClient SHALL accept self-signed TLS certificates for the MVP phase
9. IF the DirectClient does not receive a response from the agent within 30 seconds, THEN THE DirectClient SHALL return a timeout error to the caller
10. IF the EdgeClient does not receive a matching response (by request_id) within 30 seconds, THEN THE EdgeClient SHALL return a timeout error to the caller
11. WHEN a route handler requires an AgentClient for a given instance ID, THE AgentManager SHALL return the appropriate client (LocalClient for "local", DirectClient for direct-mode instances, EdgeClient for edge-mode instances) along with an error if the instance is not found or offline, allowing route handlers to decide how to proceed

### Requirement 3: Agent Manager

**User Story:** As a developer, I want a central connection manager that maintains and provides agent clients for all registered instances, so that route handlers can easily obtain the correct client for any instance.

#### Acceptance Criteria

1. THE Server SHALL implement a Manager struct in the internal/agent package that holds references to the LocalClient, a map of DirectClients, and a map of EdgeConnections, protected by a sync.RWMutex for concurrent access from multiple goroutines
2. WHEN GetClient is called with instance ID "local", THE Manager SHALL return the LocalClient
3. WHEN GetClient is called with a direct-mode instance ID, THE Manager SHALL return a DirectClient configured with the instance host, port, and decrypted agent token
4. WHEN GetClient is called with an edge-mode instance ID, THE Manager SHALL return an EdgeClient that sends requests through the registered WebSocket connection
5. IF GetClient is called with an instance ID that has no registered connection and the instance is in edge mode, THEN THE Manager SHALL return an error indicating the instance is offline
6. IF GetClient is called with an instance ID that does not exist in the database, THEN THE Manager SHALL return an error indicating the instance was not found
7. THE Manager SHALL provide a RegisterEdgeConnection method that stores a WebSocket connection for an edge-mode agent, replacing any previously registered connection for the same instance
8. THE Manager SHALL provide an UnregisterEdgeConnection method that removes an edge connection and marks the instance as offline
9. THE Manager SHALL provide a SendEdgeRequest method that sends a JSON request through an edge WebSocket and waits for a response matched by request_id
10. IF an edge request does not receive a response within 60 seconds, THEN THE Manager SHALL return a timeout error
11. WHEN the Server shuts down, THE Manager SHALL close all active WebSocket connections and release all DirectClient resources

### Requirement 4: Instance CRUD Routes

**User Story:** As a server administrator, I want API endpoints to create, list, update, and delete instances, so that I can manage which remote hosts are registered in the dashboard.

#### Acceptance Criteria

1. WHEN a POST request is made to /api/instances with a valid name (1–100 characters), host, port (1–65535), and mode ("direct" or "edge"), THE Server SHALL create a new instance record with status "enrolling" and return the instance details including the generated install command appropriate for the selected mode
2. WHEN creating an instance, THE Server SHALL generate a cryptographically random 32-byte agent token, store its bcrypt hash as AgentTokenHash, and store its AES-256-GCM encrypted form as AgentTokenEncrypted using the same derived encryption key as registry credential encryption
3. WHEN a GET request is made to /api/instances, THE Server SHALL return a list of all registered instances including each instance's id, name, host, port, mode, status, and last_seen timestamp
4. WHEN a GET request is made to /api/instances/:id for an existing instance, THE Server SHALL return the instance details including status, OS, Docker version, CPU cores, and total memory
5. WHEN a PUT request is made to /api/instances/:id with updated fields, THE Server SHALL update the instance name (1–100 characters), host, or port (1–65535) and return the updated instance record
6. WHEN a DELETE request is made to /api/instances/:id for a non-local instance, THE Server SHALL remove the instance record from the database and disconnect any active agent connection
7. WHEN a POST request is made to /api/instances/:id/test, THE Server SHALL attempt to connect to the agent within a 10-second timeout and return the connectivity result indicating success or the reason for failure
8. WHEN a POST request is made to /api/instances/:id/rotate-token for an existing instance, THE Server SHALL generate a new 32-byte agent token, update the stored bcrypt hash and AES-256-GCM encrypted token, and return the new install command
9. IF a DELETE request targets the local instance (ID "local"), THEN THE Server SHALL reject the request with HTTP 403 and an error message indicating the local instance cannot be deleted
10. IF a GET, PUT, DELETE, or POST request references an instance ID that does not exist, THEN THE Server SHALL return HTTP 404 with an error message indicating the instance was not found
11. IF a POST request to /api/instances contains an invalid mode value, a port outside 1–65535, or a name exceeding 100 characters, THEN THE Server SHALL return HTTP 400 with an error message indicating which field failed validation

### Requirement 5: Enrollment Flow

**User Story:** As a server administrator, I want to generate install commands for new instances and have agents automatically register when they connect, so that adding remote hosts is straightforward.

#### Acceptance Criteria

1. WHEN an instance is created in direct mode, THE Server SHALL generate a Docker run command containing the agent image "sdldev/dockpal-agent:latest", environment variables DOCKPAL_MODE=direct and DOCKPAL_TOKEN set to the plaintext token, port mapping 9273:9273, and a volume mount of /var/run/docker.sock:/var/run/docker.sock
2. WHEN an instance is created in edge mode, THE Server SHALL generate a Docker run command containing the agent image "sdldev/dockpal-agent:latest", environment variables DOCKPAL_MODE=edge, DOCKPAL_SERVER set to the Server WebSocket URL (wss://{server_host}:3012/api/agent/connect), and DOCKPAL_TOKEN set to the plaintext token, a volume mount of /var/run/docker.sock:/var/run/docker.sock, and no port mapping
3. WHEN an edge-mode agent connects to the /api/agent/connect WebSocket endpoint and sends a token message, THE Server SHALL compare the token against stored bcrypt hashes of all instances in "enrolling" or "offline" status, register the connection in the Agent_Manager for the matched instance, and update the instance status to "online"
4. IF an agent sends a token that does not match any stored bcrypt hash during enrollment, THEN THE Server SHALL close the WebSocket connection with a 4001 close code and an "authentication failed" close reason
5. WHEN an agent successfully enrolls, THE Server SHALL store the agent-reported host information (DockerVersion, OS, CPUCores, TotalMemory) in the instance record
6. WHEN an edge-mode agent disconnects, THE Server SHALL mark the instance status as "offline" and unregister the connection from the Agent_Manager
7. WHEN a direct-mode instance is created, THE Server SHALL attempt to verify connectivity by sending a POST request to https://{host}:{port}/agent/enroll with the plaintext token, and update the instance status to "online" upon a successful response
8. IF an edge-mode agent connects with a token matching an instance that already has an active connection, THEN THE Server SHALL close the existing connection before registering the new one; IF an edge-mode agent connects with a token that does not match any instance, THEN THE Server SHALL leave any existing connections untouched
9. IF the Server does not receive a token message from a newly connected edge agent within 10 seconds of WebSocket upgrade, THEN THE Server SHALL close the connection

### Requirement 6: Agent WebSocket Endpoint

**User Story:** As a developer, I want a WebSocket endpoint that edge-mode agents can connect to, so that agents behind NAT/firewall can maintain a persistent connection for receiving commands.

#### Acceptance Criteria

1. THE Server SHALL expose a GET /api/agent/connect endpoint that upgrades to a WebSocket connection for edge-mode agents
2. WHEN an edge agent connects, THE Server SHALL expect an initial authentication message containing the agent token within 10 seconds of connection establishment
3. IF the edge agent does not send a valid authentication message within 10 seconds, THEN THE Server SHALL send an error frame indicating authentication timeout and immediately close the WebSocket connection, enforcing the strict 10-second deadline regardless of network delays
4. WHEN authentication succeeds, THE Server SHALL enter a request/response loop where it sends AgentRequest messages (containing request_id, method, path, query, body) and receives AgentResponse messages (containing request_id, status, body, stream, chunk, data) matched by request_id
5. WHILE an edge agent is connected, THE Server SHALL expect a WebSocket ping frame from the agent at least every 30 seconds
6. IF no ping frame is received from an edge agent for 60 seconds, THEN THE Server SHALL mark the instance as offline and close the WebSocket connection
7. WHEN the Server sends a request through an edge WebSocket, THE Server SHALL include a UUID v4 request_id for response correlation
8. WHEN the Server receives an AgentResponse with stream set to true, THE Server SHALL continue reading subsequent AgentResponse messages with the same request_id until it receives a message with stream set to false, indicating the stream is complete
9. IF the Server receives a message that is not valid JSON or does not match the expected AgentResponse format, THEN THE Server SHALL discard the message and continue listening

### Requirement 7: Instance-Scoped Routes

**User Story:** As a server administrator, I want all container and deploy operations available per-instance through scoped API paths, so that I can manage containers on any registered host.

#### Acceptance Criteria

1. THE Server SHALL register an instance-scoped route group at /api/instances/:instance_id with middleware that resolves the AgentClient from the Agent_Manager and returns HTTP 404 if the instance is not found (taking priority over other error states) or HTTP 503 if the instance is offline
2. THE Server SHALL provide instance-scoped container routes: GET /containers, GET /containers/:id, POST /containers/:id/start, POST /containers/:id/stop, POST /containers/:id/restart, DELETE /containers/:id, PUT /containers/:id, GET /containers/:id/stats, GET /containers/:id/logs
3. THE Server SHALL provide instance-scoped deploy routes: POST /deploy/stream, POST /deploy/compose, POST /deploy/git
4. THE Server SHALL provide instance-scoped image routes: GET /images, POST /images/pull, DELETE /images/:id
5. THE Server SHALL provide instance-scoped host routes: GET /host/info, GET /host/stats, GET /system/info
6. THE Server SHALL provide instance-scoped service routes: GET /services, DELETE /services/:id
7. THE Server SHALL provide instance-scoped domain routes: GET /domains, POST /domains, DELETE /domains/:id
8. THE Server SHALL provide instance-scoped registry routes: GET /registries, POST /registries, GET /registries/:id, PUT /registries/:id, DELETE /registries/:id, POST /registries/:id/test
9. WHEN the instance-scoped /system/info endpoint is called, THE Server SHALL merge HostInfo (hostname, os, cpu_cores, docker_version) and HostStats (cpu_percent, used_ram, total_ram, used_disk, total_disk) from the AgentClient into the existing SystemInfo JSON format for frontend compatibility
10. WHEN the instance-scoped /deploy/git endpoint is called, THE Server SHALL perform the git clone on the Server side and send only the compose YAML to the agent

### Requirement 8: Backward-Compatible Routes

**User Story:** As an existing single-host user, I want all current API routes to continue working unchanged, so that upgrading to the multi-instance version requires no configuration changes.

#### Acceptance Criteria

1. THE Server SHALL maintain all existing routes (/api/containers, /api/containers/:id, /api/containers/:id/start, /api/containers/:id/stop, /api/containers/:id/restart, /api/containers/:id/stats, /api/containers/:id/logs, /api/deploy/stream, /api/deploy/stream/:id, /api/deploy/compose, /api/deploy/git, /api/images, /api/images/pull, /api/images/:id, /api/registries, /api/registries/:id, /api/registries/:id/test, /api/services, /api/services/:id, /api/templates, /api/templates/:id, /api/templates/:id/deploy, /api/templates/:id/deploy/stream, /api/files, /api/files/read, /api/files/write, /api/github/repos, /api/system/info, /api/login, /api/logout, /api/auth/reset-password) with identical HTTP methods, request body schemas, response JSON structures, and status codes
2. WHEN an existing route is called, THE Server SHALL delegate the operation through the Agent_Manager using the Local_Instance client, producing the same response body and status code as the current implementation
3. THE Server SHALL add an agentMgr parameter to the RegisterRoutes function while retaining the existing dockerClient parameter for backward compatibility
4. WHEN the Server starts with no remote instances configured, THE Server SHALL produce identical API responses (same JSON fields, same status codes, same WebSocket message formats) to the current single-host version for all existing routes, including when the local Docker daemon is unavailable (error responses must match current behavior)
5. IF the Agent_Manager fails to return the Local_Instance client during any route operation, THEN THE Server SHALL return an error response immediately with a server error status code indicating the local instance is unavailable

### Requirement 9: Credential Scoping

**User Story:** As a server administrator, I want registry credentials to be either globally shared or instance-specific with automatic fallback, so that I can share common credentials while allowing per-instance overrides.

#### Acceptance Criteria

1. THE Server SHALL treat RegistryCredential records with an empty InstanceID (empty string "") as global scope, making them available to all instances during credential lookup
2. THE Server SHALL treat RegistryCredential records with a non-empty InstanceID as instance-specific, making them available only to the instance whose ID matches that field
3. WHEN looking up a registry credential for a deploy operation, THE Server SHALL first check for an instance-specific credential matching the registry domain (case-insensitive, including registry alias resolution), then fall back to a global credential for the same domain, and proceed without authentication if no credential is found at either scope
4. WHEN deploying a compose file with images from multiple registries, THE Server SHALL resolve credentials for each distinct registry domain independently using the instance-then-global fallback, and include only registries with matched credentials in the registry_auths map sent to the agent
5. THE Server SHALL provide a FindRegistryCredentialByDomainAndInstance method that performs a case-insensitive search by registry domain and instance ID, checking registry aliases (e.g., "github.com" resolves to "ghcr.io"), and returns the most recently updated match
6. WHEN listing registries for an instance-scoped route, THE Server SHALL return both instance-specific and global credentials, with each entry indicating its scope (global or instance-specific) so that credentials for the same registry domain at different scopes are distinguishable

### Requirement 10: Frontend Instance Selector

**User Story:** As a server administrator, I want a dropdown in the sidebar to switch between managed instances, so that I can quickly navigate between different Docker hosts.

#### Acceptance Criteria

1. THE Frontend SHALL display an instance selector as a native HTML select element in the sidebar, positioned between the logo section and the navigation items
2. THE Frontend SHALL display each instance option in the selector with a status indicator (green circle for "online", red circle for "offline", yellow circle for "enrolling") followed by the instance name
3. THE Frontend SHALL always display the Local_Instance as the first option in the selector with the label "This Server" and a gear icon prefix
4. WHEN the user explicitly selects a different instance from the selector, THE Frontend SHALL navigate to the dashboard page, discard any open container detail view, edit mode, or deploy session state, and reload page data using instance-scoped API calls (i.e., /api/instances/{selectedInstanceID}/...); this navigation SHALL NOT be triggered by initial page load or automatic refreshes
5. WHEN the Frontend loads or is refreshed, THE Frontend SHALL restore the previously selected instance from localStorage; IF the stored instance ID is not found in the instances list, THEN THE Frontend SHALL fall back to the Local_Instance
6. THE Frontend SHALL include an "Instances" navigation item in the sidebar under the "Settings" group for accessing the instance management page
7. IF the currently selected remote instance status changes to "offline", THEN THE Frontend SHALL display the offline status indicator in the selector but SHALL NOT automatically switch away from the instance

### Requirement 11: Instance Management Page

**User Story:** As a server administrator, I want a dedicated page to view, add, test, and remove instances, so that I can manage my fleet of Docker hosts.

#### Acceptance Criteria

1. THE Frontend SHALL display an instances page listing all registered instances in a table with columns: name, host, mode (direct or edge), status (online/offline/enrolling), and last seen time displayed as a relative timestamp (e.g., "2 minutes ago")
2. THE Frontend SHALL provide an "Add Instance" dialog with fields for name (1 to 64 characters), host (valid hostname or IP address, direct mode only), port (1 to 65535, direct mode only defaulting to 9273), and mode selection (direct or edge)
3. WHEN the mode is set to edge in the Add Instance dialog, THE Frontend SHALL hide the host and port fields and not include them in the submission
4. WHEN an instance is created successfully, THE Frontend SHALL display the generated install command within the same dialog and provide a copy-to-clipboard button that copies the command text to the system clipboard
5. WHEN the "Test Connection" button is clicked for an instance, THE Frontend SHALL call POST /api/instances/:id/test and display a success toast if status is "ok" or an error toast containing the failure reason if status is not "ok"
6. THE Frontend SHALL provide a "Remove" button for each instance except the Local_Instance that triggers a confirmation dialog using the showConfirm pattern before calling DELETE /api/instances/:id
7. IF an instance status is offline, THEN THE Frontend SHALL display a warning badge next to the instance name and disable the "Test Connection" and "Remove" buttons for that instance
8. IF no remote instances are registered (only the Local_Instance exists), THEN THE Frontend SHALL display an empty state message prompting the user to add their first instance
9. THE Frontend SHALL disable the "Add Instance" dialog submit button and prevent submission until the name field is non-empty and, for direct mode, the host field is non-empty and the port is a valid integer between 1 and 65535

### Requirement 12: Instance-Aware Frontend Pages

**User Story:** As a server administrator, I want all dashboard pages to use instance-scoped API calls, so that container lists, deploys, and other operations reflect the currently selected instance.

#### Acceptance Criteria

1. THE Frontend SHALL provide an instanceApi helper method that builds API paths by prepending /api/instances/{selectedInstance}/ to the resource path, using the currently selected instance ID stored in application state
2. THE Frontend dashboard page SHALL use instanceApi to fetch containers and system info for the selected instance
3. THE Frontend containers page SHALL use instanceApi for all container operations (list, inspect, start, stop, restart, remove, edit, stats, logs)
4. THE Frontend deploy functionality SHALL use instanceApi for compose deploy, git deploy, and template deploy
5. THE Frontend images page SHALL use instanceApi for listing, pulling, and removing images
6. THE Frontend services page SHALL use instanceApi for listing and deleting services
7. THE Frontend registry page SHALL use instanceApi for credential CRUD operations
8. WHEN a WebSocket connection is needed (logs, stats streaming), THE Frontend SHALL construct the WebSocket URL by using the wss: protocol when the page is loaded over HTTPS and ws: otherwise, with the path /api/instances/{selectedInstance}/containers/{containerId}/logs or /stats, and the JWT token passed as a query parameter named "token"
9. WHILE a remote instance is selected or during app initialization before an instance is explicitly selected, THE Frontend SHALL hide the navigation items and page sections for Traefik domain management, Cloudflare tunnel creation and deletion, and auto-recovery toggle, so that these server-local-only features are not accessible until the Local_Instance is explicitly selected
10. WHEN the user switches from a remote instance back to the Local_Instance, THE Frontend SHALL restore visibility of Traefik domain management, Cloudflare tunnel, and auto-recovery features
11. IF an instanceApi call returns an HTTP error indicating the instance is unreachable or offline, THEN THE Frontend SHALL display an inline error message on the current page indicating the instance is unavailable, without navigating away from the page

### Requirement 13: System Info Refactor

**User Story:** As a developer, I want host information functions moved from routes.go to the agent/local package, so that system info is accessed uniformly through the AgentClient interface regardless of instance type.

#### Acceptance Criteria

1. THE Server SHALL move the getSystemInfo, getMemoryInfo, getCgroupMemoryUsage, getCPUPercent, and getHostname functions from routes.go to internal/agent/local.go
2. THE LocalClient SHALL implement GetHostInfo by reading hostname via os.Hostname(), OS via runtime.GOOS, CPU core count via runtime.NumCPU(), total memory from cgroup files or /proc/meminfo, and Docker version from the Docker daemon, returning a HostInfo struct with fields: Hostname, OS, CPUCores, TotalMemory, DockerVersion
3. THE LocalClient SHALL implement GetHostStats by computing CPU usage percentage from two /proc/stat readings taken 200ms apart, memory usage from cgroup or /proc/meminfo, and disk usage from syscall.Statfs on the root filesystem, returning a HostStats struct with fields: CPUPercent, UsedRAM, TotalRAM, UsedDisk, TotalDisk
4. IF GetHostInfo or GetHostStats cannot read a required system file or the Docker daemon is unreachable, THEN THE LocalClient SHALL return an error indicating which resource was unavailable; other conditions such as permission issues or malformed file contents SHALL NOT trigger these errors
5. THE Server route handler for GET /api/system/info SHALL call agentMgr.GetClient(instanceID).GetHostInfo() and GetHostStats(), then merge the results into the existing SystemInfo JSON response containing fields: hostname, os, cpu_cores, cpu_percent, total_ram, used_ram, total_disk, used_disk, docker_version
6. AFTER the refactor, THE Server route handlers SHALL NOT call getSystemInfo, getMemoryInfo, getCgroupMemoryUsage, getCPUPercent, or getHostname directly, accessing system info exclusively through the AgentClient interface

### Requirement 14: Deploy Stream Relay

**User Story:** As a server administrator, I want to see real-time deploy progress when deploying to remote instances, so that I have the same streaming experience as local deploys.

#### Acceptance Criteria

1. WHEN a streamed deploy is initiated on a remote instance, THE Server SHALL create a server-side DeploySession and relay each event from the agent to the browser WebSocket in the same DeployEvent JSON format (step, message, status, time fields) preserving the order received from the agent
2. IF the target instance uses direct mode, THEN THE Server SHALL open a WebSocket connection to the agent deploy stream endpoint (GET /agent/docker/deploy/stream/{deploy_id}) and forward each received JSON event to the browser-facing DeploySession Events channel
3. IF the target instance uses edge mode, THEN THE Server SHALL send the deploy request through the edge WebSocket channel and forward each streamed AgentResponse chunk as a DeployEvent to the browser-facing DeploySession Events channel
4. IF the target instance is the Local_Instance, THEN THE Server SHALL use the existing DeployComposeStreamed method that writes directly to the session without relay
5. THE Server SHALL maintain a deploy relay mapping (server_session_id to agent_session_id) and remove entries 30 seconds after the relay session reaches a terminal state (deploy completed, deploy errored, or relay terminated)
6. IF the agent does not send the first event within 60 seconds of the deploy stream request, THEN THE Server SHALL terminate the relay and send an error-status DeployEvent to the browser indicating a timeout
7. IF the agent WebSocket connection drops during an active relay, THEN THE Server SHALL send an error-status DeployEvent to the browser indicating connection loss and remove the relay mapping entry within 30 seconds
8. IF the browser WebSocket disconnects during an active relay, THEN THE Server SHALL stop reading from the agent deploy stream and remove the relay mapping entry within 30 seconds
9. IF a streamed deploy is initiated on an instance whose mode is not direct, edge, or local, THEN THE Server SHALL return an error immediately indicating the instance mode is invalid or unsupported

### Requirement 15: Main Initialization Changes

**User Story:** As a developer, I want the main entry point to initialize the Agent_Manager and wire it into the route registration, so that the multi-instance infrastructure is available at startup.

#### Acceptance Criteria

1. WHEN the Server starts, THE main function SHALL create an Agent_Manager instance by calling agent.NewManager with the database and docker client, after Docker client initialization and ping succeed
2. IF Agent_Manager creation fails, THEN THE main function SHALL terminate with a fatal log message indicating the failure reason
3. WHEN the Server starts, THE main function SHALL call EnsureLocalInstance on the database to create the local instance record (ID "local", Name "This Server", Mode "local", Status "online") if it does not already exist
4. IF EnsureLocalInstance fails, THEN THE main function SHALL terminate with a fatal log message indicating the failure reason
5. THE main function SHALL pass the Agent_Manager to RegisterRoutes as an additional parameter
6. WHEN the Server receives a shutdown signal, THE main function SHALL call Close on the Agent_Manager before closing the Docker client and database, to ensure all WebSocket connections and HTTP clients are released first
