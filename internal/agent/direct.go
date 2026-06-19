package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
	"nhooyr.io/websocket"
)

// DirectClient implements AgentClient for communicating with a remote agent via HTTP/HTTPS.
type DirectClient struct {
	instanceID string
	baseURL    string
	httpClient *http.Client
	authToken  string
}

// NewDirectClient creates a new DirectClient that communicates with a remote agent.
func NewDirectClient(instanceID, host string, port int, authToken string) *DirectClient {
	baseURL := fmt.Sprintf("https://%s:%d", host, port)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	return &DirectClient{
		instanceID: instanceID,
		baseURL:    baseURL,
		httpClient: httpClient,
		authToken:  authToken,
	}
}

// makeRequest builds an HTTP request with proper headers and query params.
func (c *DirectClient) makeRequest(ctx context.Context, method, path string, query map[string]string, body io.Reader) (*http.Request, error) {
	reqURL := c.baseURL + path

	// Add query parameters
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Add(k, v)
		}
		reqURL += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.authToken)
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

// doRequest performs an HTTP request and returns the response body.
func (c *DirectClient) doRequest(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// Container operations

// ListContainers returns all containers, optionally including stopped containers.
func (c *DirectClient) ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/containers", map[string]string{"all": fmt.Sprintf("%v", all)}, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var containers []docker.ContainerInfo
	if err := json.Unmarshal(body, &containers); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return containers, nil
}

// InspectContainer returns detailed information about a container.
func (c *DirectClient) InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/containers/"+id, nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var detail docker.ContainerDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &detail, nil
}

// StartContainer starts a container.
func (c *DirectClient) StartContainer(ctx context.Context, id string) error {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/containers/"+id+"/start", nil, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// StopContainer stops a container.
func (c *DirectClient) StopContainer(ctx context.Context, id string) error {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/containers/"+id+"/stop", nil, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// RestartContainer restarts a container.
func (c *DirectClient) RestartContainer(ctx context.Context, id string) error {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/containers/"+id+"/restart", nil, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// RemoveContainer removes a container.
func (c *DirectClient) RemoveContainer(ctx context.Context, id string, force bool) error {
	req, err := c.makeRequest(ctx, "DELETE", "/agent/docker/containers/"+id, map[string]string{"force": fmt.Sprintf("%v", force)}, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// EditContainer updates a container's configuration.
func (c *DirectClient) EditContainer(ctx context.Context, id string, req docker.ContainerEditRequest) (*docker.ContainerDetail, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := c.makeRequest(ctx, "PUT", "/agent/docker/containers/"+id, nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}

	respBody, err := c.doRequest(httpReq)
	if err != nil {
		return nil, err
	}

	var detail docker.ContainerDetail
	if err := json.Unmarshal(respBody, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &detail, nil
}

// UpdateContainerImage force-pulls the container's current image and recreates it.
func (c *DirectClient) UpdateContainerImage(ctx context.Context, id string, registryAuth string) (*docker.ContainerDetail, error) {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/containers/"+id+"/update-image", map[string]string{"auth": registryAuth}, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var detail docker.ContainerDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &detail, nil
}

// GetContainerStats returns resource usage statistics for a container.
func (c *DirectClient) GetContainerStats(ctx context.Context, id string) (*docker.ContainerStats, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/containers/"+id+"/stats", nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var stats docker.ContainerStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &stats, nil
}

// ContainerLogs returns the logs of a container via WebSocket.
// The agent exposes a WebSocket endpoint at /agent/docker/containers/{id}/logs.
// We upgrade to WebSocket, authenticate via first message (JSON token), then stream log lines.
func (c *DirectClient) ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error) {
	wsURL := strings.Replace(c.baseURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL += "/agent/docker/containers/" + id + "/logs?tail=" + tail

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: c.httpClient,
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.authToken}},
	})
	if err != nil {
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	// Authenticate via first message (JSON-encoded token)
	authMsg, _ := json.Marshal(map[string]string{"token": c.authToken})
	if err := conn.Write(ctx, websocket.MessageText, authMsg); err != nil {
		conn.Close(websocket.StatusInternalError, "auth write failed")
		return nil, fmt.Errorf("websocket auth failed: %w", err)
	}

	// Read incoming messages and pipe them through a pipe
	pr, pw := io.Pipe()
	go func() {
		defer conn.Close(websocket.StatusNormalClosure, "")
		defer pw.Close()
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if _, err := pw.Write(msg); err != nil {
				return
			}
		}
	}()

	return pr, nil
}

// Compose operations

// DeployComposeRequest is the request body for deploying compose.
type DeployComposeRequest struct {
	Name          string            `json:"name"`
	ComposeYAML   string            `json:"compose"`
	RegistryAuths map[string]string `json:"registry_auths"`
	ForcePull     bool              `json:"force_pull"`
}

// DeployCompose deploys a compose file to the remote agent.
func (c *DirectClient) DeployCompose(ctx context.Context, name, composeYAML string, registryAuths map[string]string, forcePull bool) error {
	reqBody := DeployComposeRequest{
		Name:          name,
		ComposeYAML:   composeYAML,
		RegistryAuths: registryAuths,
		ForcePull:     forcePull,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := c.makeRequest(ctx, "POST", "/agent/docker/deploy/compose", nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// DeployStreamRequest is the request body for initiating a streamed deploy.
type DeployStreamRequest struct {
	Name          string            `json:"name"`
	ComposeYAML   string            `json:"compose"`
	RegistryAuths map[string]string `json:"registry_auths"`
	ForcePull     bool              `json:"force_pull"`
}

// DeployStreamResponse is the response from initiating a streamed deploy.
type DeployStreamResponse struct {
	DeployID string `json:"deploy_id"`
}

// DeployComposeStreamed deploys a compose file with streaming progress events.
func (c *DirectClient) DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, registryAuths map[string]string, forcePull bool) error {
	// Step 1: Initiate the deploy and get a deploy_id
	reqBody := DeployStreamRequest{
		Name:          name,
		ComposeYAML:   composeYAML,
		RegistryAuths: registryAuths,
		ForcePull:     forcePull,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := c.makeRequest(ctx, "POST", "/agent/docker/deploy/stream", nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}

	respBody, err := c.doRequest(req)
	if err != nil {
		return err
	}

	var streamResp DeployStreamResponse
	if err := json.Unmarshal(respBody, &streamResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	deployID := streamResp.DeployID
	if deployID == "" {
		return fmt.Errorf("no deploy_id returned from agent")
	}

	// Step 2: Connect to WebSocket for streaming events
	wsURL := strings.Replace(c.baseURL, "https://", "wss://", 1) + "/agent/docker/deploy/stream/" + deployID

	wsConn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: c.httpClient,
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + c.authToken},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")

	// Step 3: Read events from WebSocket and write to session.Events
	for {
		_, msg, err := wsConn.Read(ctx)
		if err != nil {
			// Connection closed, we're done
			break
		}

		var event docker.DeployEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			// Skip malformed messages
			continue
		}

		select {
		case session.Events <- event:
		default:
			// Channel full, skip
		}
	}

	return nil
}

// Image operations

// ListImages returns all images on the remote agent.
func (c *DirectClient) ListImages(ctx context.Context) ([]docker.ImageInfo, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/images", nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var images []docker.ImageInfo
	if err := json.Unmarshal(body, &images); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return images, nil
}

// PullImage pulls an image to the remote agent.
func (c *DirectClient) PullImage(ctx context.Context, image string) error {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/images/pull", map[string]string{"image": image}, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// PullImageWithAuth pulls an image with registry authentication.
func (c *DirectClient) PullImageWithAuth(ctx context.Context, image, registryAuth string) error {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/images/pull", map[string]string{"image": image, "auth": registryAuth}, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// RemoveImage removes an image from the remote agent.
func (c *DirectClient) RemoveImage(ctx context.Context, id string) error {
	req, err := c.makeRequest(ctx, "DELETE", "/agent/docker/images/"+id, nil, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// CheckImageUpdate queries the remote agent for an image update check.
func (c *DirectClient) CheckImageUpdate(ctx context.Context, image string) (*docker.ImageUpdateResult, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/images/check", map[string]string{"image": image}, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var result docker.ImageUpdateResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &result, nil
}

// ForcePullImage force-pulls an image on the remote agent.
func (c *DirectClient) ForcePullImage(ctx context.Context, image, registryAuth string) error {
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/images/pull-force", map[string]string{"image": image, "auth": registryAuth}, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// PruneImages prunes unused images on the remote agent.
func (c *DirectClient) PruneImages(ctx context.Context, danglingOnly bool) (*docker.PruneResult, error) {
	var result docker.PruneResult
	query := map[string]string{"dangling_only": strconv.FormatBool(danglingOnly)}
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/images/prune", query, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse prune result: %w", err)
	}
	return &result, nil
}

// App auto-update operations
//
// The remote agent's matching endpoints land in task 6.4. The bodies below
// follow the same shape as DeployCompose and other JSON endpoints so the
// agent-side handler can reuse the standard parse-and-route plumbing.

// ListApps returns app summaries from the remote agent.
func (c *DirectClient) ListApps(ctx context.Context) ([]docker.AppSummary, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/apps", nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var apps []docker.AppSummary
	if err := json.Unmarshal(body, &apps); err != nil {
		return nil, fmt.Errorf("failed to decode apps: %w", err)
	}
	return apps, nil
}

// ListAppUpdates returns App_Update_Records for one app on the remote agent.
func (c *DirectClient) ListAppUpdates(ctx context.Context, app string, limit int) ([]db.AppUpdateRecord, error) {
	query := map[string]string{}
	if limit > 0 {
		query["limit"] = fmt.Sprintf("%d", limit)
	}
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/apps/"+app+"/updates", query, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var recs []db.AppUpdateRecord
	if err := json.Unmarshal(body, &recs); err != nil {
		return nil, fmt.Errorf("failed to decode app updates: %w", err)
	}
	return recs, nil
}

// GetAppUpdate fetches one App_Update_Record by attempt id from the remote agent.
// Returns (nil, nil) when the record does not exist.
func (c *DirectClient) GetAppUpdate(ctx context.Context, attemptID string) (*db.AppUpdateRecord, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/apps/updates/"+attemptID, nil, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var rec db.AppUpdateRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("failed to decode record: %w", err)
	}
	return &rec, nil
}

// triggerAppRequest is the request body for triggering a manual app update.
type triggerAppRequest struct {
	App string `json:"app"`
}

// triggerAppResponse is the response from triggering a manual app update.
type triggerAppResponse struct {
	AttemptID string `json:"attempt_id"`
}

// TriggerAppUpdate runs the manual auto-update pipeline on the remote agent.
func (c *DirectClient) TriggerAppUpdate(ctx context.Context, app string) (string, error) {
	bodyBytes, err := json.Marshal(triggerAppRequest{App: app})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := c.makeRequest(ctx, "POST", "/agent/docker/apps/"+app+"/update", nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", err
	}

	respBody, err := c.doRequest(req)
	if err != nil {
		return "", err
	}

	var resp triggerAppResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	return resp.AttemptID, nil
}

// setAutoUpdateRequest is the request body for toggling the auto-update label.
type setAutoUpdateRequest struct {
	Enabled bool `json:"enabled"`
}

// SetAppAutoUpdate toggles the dockpal.auto-update label on a remote app.
func (c *DirectClient) SetAppAutoUpdate(ctx context.Context, app string, enabled bool) error {
	bodyBytes, err := json.Marshal(setAutoUpdateRequest{Enabled: enabled})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := c.makeRequest(ctx, "PATCH", "/agent/docker/apps/"+app+"/auto-update", nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// StreamAppUpdatesURL builds the SSE proxy URL for the remote agent's
// /apps/updates/stream endpoint and returns the URL plus the bearer token
// the caller must add to the request. The handler in the edge process uses
// this to set up an http.Client GET and copy the response body to its own
// SSE response writer.
func (c *DirectClient) StreamAppUpdatesURL() (string, string) {
	return c.baseURL + "/agent/docker/apps/updates/stream", c.authToken
}

// Host operations

// GetHostInfo returns system information from the remote agent.
func (c *DirectClient) GetHostInfo(ctx context.Context) (*HostInfo, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/host/info", nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var info HostInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &info, nil
}

// GetHostStats returns resource usage from the remote agent.
func (c *DirectClient) GetHostStats(ctx context.Context) (*HostStats, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/host/stats", nil, nil)
	if err != nil {
		return nil, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var stats HostStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &stats, nil
}

// Connection

// Ping checks if the remote agent is reachable.
func (c *DirectClient) Ping(ctx context.Context) error {
	req, err := c.makeRequest(ctx, "GET", "/agent/ping", nil, nil)
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	return err
}

// Close closes idle connections.
func (c *DirectClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}
