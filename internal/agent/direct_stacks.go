package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/sdldev/dockpal/internal/docker"
	"nhooyr.io/websocket"
)

// Dockge-style stack operations for the remote agent in direct mode.
// Mirrors the agent's /agent/docker/stacks* endpoints (see dockpal-agent
// internal/server/stacks.go).

func (c *DirectClient) ListStacks(ctx context.Context) ([]docker.Stack, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/stacks", nil, nil)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Stacks []docker.Stack `json:"stacks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return resp.Stacks, nil
}

func (c *DirectClient) GetStack(ctx context.Context, name string) (*docker.Stack, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/stacks/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var stack docker.Stack
	if err := json.Unmarshal(body, &stack); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &stack, nil
}

func (c *DirectClient) SaveStack(ctx context.Context, name, composeYAML, composeENV string, isAdd bool) (*docker.Stack, error) {
	reqBody := map[string]any{"name": name, "compose": composeYAML, "env": composeENV}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	path := "/agent/docker/stacks"
	method := "POST"
	if !isAdd {
		path = "/agent/docker/stacks/" + url.PathEscape(name)
		method = "PUT"
	}
	req, err := c.makeRequest(ctx, method, path, nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var stack docker.Stack
	if err := json.Unmarshal(body, &stack); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &stack, nil
}

func (c *DirectClient) DeleteStack(ctx context.Context, name string) error {
	req, err := c.makeRequest(ctx, "DELETE", "/agent/docker/stacks/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return err
	}
	_, err = c.doRequest(req)
	return err
}

func (c *DirectClient) StackAction(ctx context.Context, name, action string) (*docker.Stack, error) {
	req, err := c.makeRequest(ctx, "POST",
		"/agent/docker/stacks/"+url.PathEscape(name)+"/"+url.PathEscape(action), nil, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var stack docker.Stack
	if err := json.Unmarshal(body, &stack); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &stack, nil
}

func (c *DirectClient) StackServiceAction(ctx context.Context, name, service, action string) (*docker.Stack, error) {
	req, err := c.makeRequest(ctx, "POST",
		"/agent/docker/stacks/"+url.PathEscape(name)+"/services/"+url.PathEscape(service)+"/"+url.PathEscape(action),
		nil, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var stack docker.Stack
	if err := json.Unmarshal(body, &stack); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &stack, nil
}

// DeployStackStreamed saves (when composeYAML non-empty) then runs
// `docker compose up -d --remove-orphans` on the agent, bridging its deploy
// WebSocket into the shared session — same two-phase pattern as
// DeployComposeStreamed.
func (c *DirectClient) DeployStackStreamed(ctx context.Context, name, composeYAML, composeENV string, isAdd bool, session *docker.DeploySession) error {
	reqBody := map[string]any{
		"compose": composeYAML,
		"env":     composeENV,
		"is_add":  isAdd,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := c.makeRequest(ctx, "POST", "/agent/docker/stacks/"+url.PathEscape(name)+"/deploy", nil, strings.NewReader(string(bodyBytes)))
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

	wsURL := strings.Replace(c.baseURL, "https://", "wss://", 1) + "/agent/docker/stacks/deploy/stream/" + deployID
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

	for {
		_, msg, err := wsConn.Read(ctx)
		if err != nil {
			break
		}
		var event docker.DeployEvent
		if err := json.Unmarshal(msg, &event); err != nil {
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

func (c *DirectClient) ListDockerNetworks(ctx context.Context) ([]string, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/stacks/meta/networks", nil, nil)
	if err != nil {
		return nil, err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Networks []string `json:"networks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return resp.Networks, nil
}

func (c *DirectClient) GetGlobalEnv(ctx context.Context) (string, error) {
	req, err := c.makeRequest(ctx, "GET", "/agent/docker/stacks/meta/globalenv", nil, nil)
	if err != nil {
		return "", err
	}
	body, err := c.doRequest(req)
	if err != nil {
		return "", err
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	return resp.Content, nil
}

func (c *DirectClient) SetGlobalEnv(ctx context.Context, content string) error {
	reqBody := map[string]any{"content": content}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := c.makeRequest(ctx, "PUT", "/agent/docker/stacks/meta/globalenv", nil, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	_, err = c.doRequest(req)
	return err
}