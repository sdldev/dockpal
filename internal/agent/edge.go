package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/sdldev/dockpal/internal/docker"
)

// generateUUID generates a random UUID v4-like string.
func generateUUID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	bytes[6] = (bytes[6] & 0x0f) | 0x40 // Version 4
	bytes[8] = (bytes[8] & 0x3f) | 0x80 // Variant 10
	return hex.EncodeToString(bytes)
}

// ManagerInterface defines the interface for sending requests to edge agents.
// This will be implemented by the Manager struct defined in manager.go.
type ManagerInterface interface {
	SendEdgeRequest(instanceID string, req *AgentRequest) (*AgentResponse, error)
}

// EdgeClient communicates through a multiplexed WebSocket connection managed by the Manager.
type EdgeClient struct {
	instanceID string
	manager    ManagerInterface
}

// NewEdgeClient creates a new EdgeClient that communicates via the Manager's WebSocket.
func NewEdgeClient(instanceID string, manager ManagerInterface) *EdgeClient {
	return &EdgeClient{
		instanceID: instanceID,
		manager:    manager,
	}
}

// sendRequest sends an AgentRequest through the Manager and returns the response.
func (e *EdgeClient) sendRequest(ctx context.Context, method, path string, query map[string]string, body interface{}) (*AgentResponse, error) {
	var bodyJSON json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyJSON = b
	}

	req := &AgentRequest{
		RequestID: generateUUID(),
		Method:    method,
		Path:      path,
		Query:     query,
		Body:      bodyJSON,
	}

	return e.manager.SendEdgeRequest(e.instanceID, req)
}

// sendRequestRaw sends a request without a body and returns raw bytes for the response.
func (e *EdgeClient) sendRequestRaw(ctx context.Context, method, path string, query map[string]string) ([]byte, error) {
	resp, err := e.sendRequest(ctx, method, path, query, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// parseResponse parses the response body into the given type.
func parseResponse[T any](resp *AgentResponse) (*T, error) {
	if resp.Status >= 400 {
		return nil, fmt.Errorf("request failed with status %d", resp.Status)
	}
	var result T
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	return &result, nil
}

// Container operations

// ListContainers returns all containers, optionally including stopped containers.
func (e *EdgeClient) ListContainers(ctx context.Context, all bool) ([]docker.ContainerInfo, error) {
	query := map[string]string{"all": strconv.FormatBool(all)}
	body, err := e.sendRequestRaw(ctx, "GET", "/docker/containers", query)
	if err != nil {
		return nil, err
	}

	var containers []docker.ContainerInfo
	if err := json.Unmarshal(body, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse containers: %w", err)
	}
	return containers, nil
}

// InspectContainer returns detailed information about a container.
func (e *EdgeClient) InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error) {
	resp, err := e.sendRequest(ctx, "GET", "/docker/containers/"+id, nil, nil)
	if err != nil {
		return nil, err
	}
	return parseResponse[docker.ContainerDetail](resp)
}

// StartContainer starts a container.
func (e *EdgeClient) StartContainer(ctx context.Context, id string) error {
	_, err := e.sendRequestRaw(ctx, "POST", "/docker/containers/"+id+"/start", nil)
	return err
}

// StopContainer stops a container.
func (e *EdgeClient) StopContainer(ctx context.Context, id string) error {
	_, err := e.sendRequestRaw(ctx, "POST", "/docker/containers/"+id+"/stop", nil)
	return err
}

// RestartContainer restarts a container.
func (e *EdgeClient) RestartContainer(ctx context.Context, id string) error {
	_, err := e.sendRequestRaw(ctx, "POST", "/docker/containers/"+id+"/restart", nil)
	return err
}

// RemoveContainer removes a container.
func (e *EdgeClient) RemoveContainer(ctx context.Context, id string, force bool) error {
	query := map[string]string{"force": strconv.FormatBool(force)}
	_, err := e.sendRequestRaw(ctx, "DELETE", "/docker/containers/"+id, query)
	return err
}

// EditContainer updates a container's configuration.
func (e *EdgeClient) EditContainer(ctx context.Context, id string, req docker.ContainerEditRequest) (*docker.ContainerDetail, error) {
	resp, err := e.sendRequest(ctx, "PUT", "/docker/containers/"+id, nil, req)
	if err != nil {
		return nil, err
	}
	return parseResponse[docker.ContainerDetail](resp)
}

// GetContainerStats returns resource usage statistics for a container.
func (e *EdgeClient) GetContainerStats(ctx context.Context, id string) (*docker.ContainerStats, error) {
	resp, err := e.sendRequest(ctx, "GET", "/docker/containers/"+id+"/stats", nil, nil)
	if err != nil {
		return nil, err
	}
	return parseResponse[docker.ContainerStats](resp)
}

// ContainerLogs returns the logs of a container.
func (e *EdgeClient) ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error) {
	query := map[string]string{"tail": tail}
	resp, err := e.sendRequest(ctx, "GET", "/docker/containers/"+id+"/logs", query, nil)
	if err != nil {
		return nil, err
	}

	// Return the body as a ReadCloser
	if resp.Status >= 400 {
		return nil, fmt.Errorf("failed to get logs: status %d", resp.Status)
	}

	// Convert the body bytes to an io.ReadCloser
	return io.NopCloser(bytes.NewReader(resp.Body)), nil
}

// Compose operations

// deployComposeRequest is the request body for deploying compose.
type deployComposeRequest struct {
	Name          string            `json:"name"`
	ComposeYAML   string            `json:"compose_yaml"`
	RegistryAuths map[string]string `json:"registry_auths"`
}

// DeployCompose deploys a compose file to the remote agent.
func (e *EdgeClient) DeployCompose(ctx context.Context, name, composeYAML string, registryAuths map[string]string) error {
	reqBody := deployComposeRequest{
		Name:          name,
		ComposeYAML:   composeYAML,
		RegistryAuths: registryAuths,
	}

	_, err := e.sendRequest(ctx, "POST", "/docker/deploy/compose", nil, reqBody)
	return err
}

// deployStreamRequest is the request body for initiating a streamed deploy.
type deployStreamRequest struct {
	Name          string            `json:"name"`
	ComposeYAML   string            `json:"compose_yaml"`
	RegistryAuths map[string]string `json:"registry_auths"`
}

// deployStreamResponse is the response from initiating a streamed deploy.
type deployStreamResponse struct {
	DeployID string `json:"deploy_id"`
}

// DeployComposeStreamed deploys a compose file with streaming progress events.
// It initiates the deploy and then handles streaming responses by forwarding events to the session.
func (e *EdgeClient) DeployComposeStreamed(ctx context.Context, name, composeYAML string, session *docker.DeploySession, registryAuths map[string]string) error {
	// Step 1: Initiate the deploy and get a deploy_id
	reqBody := deployStreamRequest{
		Name:          name,
		ComposeYAML:   composeYAML,
		RegistryAuths: registryAuths,
	}

	initResp, err := e.sendRequest(ctx, "POST", "/docker/deploy/stream", nil, reqBody)
	if err != nil {
		return err
	}

	var streamResp deployStreamResponse
	if err := json.Unmarshal(initResp.Body, &streamResp); err != nil {
		return fmt.Errorf("failed to parse stream response: %w", err)
	}

	deployID := streamResp.DeployID
	if deployID == "" {
		return fmt.Errorf("no deploy_id returned from agent")
	}

	// Step 2: Connect to the streaming endpoint by sending a WebSocket request
	// Send request with Stream: true to indicate we want streaming
	streamQuery := map[string]string{"stream": "true"}

	// We'll receive multiple responses - first the connection confirmation, then chunks
	for {
		resp, err := e.sendRequest(ctx, "GET", "/docker/deploy/stream/"+deployID, streamQuery, nil)
		if err != nil {
			return fmt.Errorf("stream request failed: %w", err)
		}

		// Check if this is a streaming response
		if !resp.Stream {
			// Not a stream response - this is the final response
			if resp.Status >= 400 {
				return fmt.Errorf("deploy failed with status %d", resp.Status)
			}
			// Done with streaming
			return nil
		}

		// This is a streaming chunk - forward as DeployEvent
		if resp.Data != "" {
			event := docker.DeployEvent{
				Message: resp.Data,
			}
			select {
			case session.Events <- event:
			default:
				// Channel full, skip this event
			}
		}

		// Check if this was the last chunk
		if resp.Chunk == 0 || resp.Status >= 400 {
			// Final chunk or error
			if resp.Status >= 400 {
				return fmt.Errorf("deploy failed with status %d", resp.Status)
			}
			return nil
		}
	}
}

// Image operations

// ListImages returns all images on the remote agent.
func (e *EdgeClient) ListImages(ctx context.Context) ([]docker.ImageInfo, error) {
	body, err := e.sendRequestRaw(ctx, "GET", "/docker/images", nil)
	if err != nil {
		return nil, err
	}

	var images []docker.ImageInfo
	if err := json.Unmarshal(body, &images); err != nil {
		return nil, fmt.Errorf("failed to parse images: %w", err)
	}
	return images, nil
}

// PullImage pulls an image to the remote agent.
func (e *EdgeClient) PullImage(ctx context.Context, image string) error {
	query := map[string]string{"image": image}
	_, err := e.sendRequestRaw(ctx, "POST", "/docker/images/pull", query)
	return err
}

// PullImageWithAuth pulls an image with registry authentication.
func (e *EdgeClient) PullImageWithAuth(ctx context.Context, image, registryAuth string) error {
	query := map[string]string{"image": image, "auth": registryAuth}
	_, err := e.sendRequestRaw(ctx, "POST", "/docker/images/pull", query)
	return err
}

// RemoveImage removes an image from the remote agent.
func (e *EdgeClient) RemoveImage(ctx context.Context, id string) error {
	_, err := e.sendRequestRaw(ctx, "DELETE", "/docker/images/"+id, nil)
	return err
}

// Host operations

// GetHostInfo returns system information from the remote agent.
func (e *EdgeClient) GetHostInfo(ctx context.Context) (*HostInfo, error) {
	resp, err := e.sendRequest(ctx, "GET", "/host/info", nil, nil)
	if err != nil {
		return nil, err
	}
	return parseResponse[HostInfo](resp)
}

// GetHostStats returns resource usage from the remote agent.
func (e *EdgeClient) GetHostStats(ctx context.Context) (*HostStats, error) {
	resp, err := e.sendRequest(ctx, "GET", "/host/stats", nil, nil)
	if err != nil {
		return nil, err
	}
	return parseResponse[HostStats](resp)
}

// Connection

// Ping checks if the remote agent is reachable.
func (e *EdgeClient) Ping(ctx context.Context) error {
	_, err := e.sendRequestRaw(ctx, "GET", "/ping", nil)
	return err
}

// Close closes the edge client (no-op, connection lifecycle managed by Manager).
func (e *EdgeClient) Close() error {
	return nil
}