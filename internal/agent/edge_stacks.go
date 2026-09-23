package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sdldev/dockpal/internal/docker"
)

// Dockge-style stack operations for the remote agent in edge mode.
// Mirrors the agent's edge dispatcher (/docker/stacks* paths).

func (e *EdgeClient) ListStacks(ctx context.Context) ([]docker.Stack, error) {
	body, err := e.sendRequestRaw(ctx, "GET", "/docker/stacks", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Stacks []docker.Stack `json:"stacks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return resp.Stacks, nil
}

func (e *EdgeClient) GetStack(ctx context.Context, name string) (*docker.Stack, error) {
	resp, err := e.sendRequest(ctx, "GET", "/docker/stacks/"+name, nil, nil)
	if err != nil {
		return nil, err
	}
	return parseStackResponse[docker.Stack](resp)
}

// parseStackResponse is parseResponse but keeps the agent's error message in
// the error text, so the Server's stackError mapping (busy/exists/not found
// substrings) still works across the edge transport.
func parseStackResponse[T any](resp *AgentResponse) (*T, error) {
	if resp.Status >= 400 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.Status, string(resp.Body))
	}
	var result T
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	return &result, nil
}

func (e *EdgeClient) SaveStack(ctx context.Context, name, composeYAML, composeENV string, isAdd bool) (*docker.Stack, error) {
	reqBody := map[string]any{"name": name, "compose": composeYAML, "env": composeENV}
	var resp *AgentResponse
	var err error
	if isAdd {
		resp, err = e.sendRequest(ctx, "POST", "/docker/stacks", nil, reqBody)
	} else {
		resp, err = e.sendRequest(ctx, "PUT", "/docker/stacks/"+name, nil, reqBody)
	}
	if err != nil {
		return nil, err
	}
	return parseStackResponse[docker.Stack](resp)
}

func (e *EdgeClient) DeleteStack(ctx context.Context, name string) error {
	resp, err := e.sendRequest(ctx, "DELETE", "/docker/stacks/"+name, nil, nil)
	if err != nil {
		return err
	}
	if resp.Status >= 400 {
		return fmt.Errorf("request failed with status %d: %s", resp.Status, string(resp.Body))
	}
	return nil
}

func (e *EdgeClient) StackAction(ctx context.Context, name, action string) (*docker.Stack, error) {
	resp, err := e.sendRequest(ctx, "POST", "/docker/stacks/"+name+"/"+action, nil, map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseStackResponse[docker.Stack](resp)
}

func (e *EdgeClient) StackServiceAction(ctx context.Context, name, service, action string) (*docker.Stack, error) {
	resp, err := e.sendRequest(ctx, "POST", "/docker/stacks/"+name+"/services/"+service+"/"+action, nil, map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseStackResponse[docker.Stack](resp)
}

// DeployStackStreamed initiates a stack deploy on the agent and returns the
// deploy id. Chunk streaming over the edge transport is not supported yet —
// the manager routes exactly one response per request id (same limitation as
// the existing compose DeployComposeStreamed over edge). The UI still works:
// the deploy runs on the agent and status refreshes via GetStack.
//
// TODO(stacks-edge-stream): extend the edge protocol with multi-response
// streaming (pending map → channel per request id kept until Done) so deploy
// events reach the session for edge instances too.
func (e *EdgeClient) DeployStackStreamed(ctx context.Context, name, composeYAML, composeENV string, isAdd bool, session *docker.DeploySession) error {
	reqBody := map[string]any{
		"compose": composeYAML,
		"env":     composeENV,
		"is_add":  isAdd,
	}
	resp, err := e.sendRequest(ctx, "POST", "/docker/stacks/"+name+"/deploy", nil, reqBody)
	if err != nil {
		return err
	}
	if resp.Status >= 400 {
		return fmt.Errorf("deploy failed with status %d: %s", resp.Status, string(resp.Body))
	}
	var streamResp deployStreamResponse
	if err := json.Unmarshal(resp.Body, &streamResp); err != nil {
		return fmt.Errorf("failed to parse stream response: %w", err)
	}
	if streamResp.DeployID == "" {
		return fmt.Errorf("no deploy_id returned from agent")
	}
	session.Emit("deploy", "deploy started on agent ("+streamResp.DeployID+")", "running")
	return nil
}

func (e *EdgeClient) ListDockerNetworks(ctx context.Context) ([]string, error) {
	body, err := e.sendRequestRaw(ctx, "GET", "/docker/stacks/meta/networks", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Networks []string `json:"networks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return resp.Networks, nil
}

func (e *EdgeClient) GetGlobalEnv(ctx context.Context) (string, error) {
	body, err := e.sendRequestRaw(ctx, "GET", "/docker/stacks/meta/globalenv", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	return resp.Content, nil
}

func (e *EdgeClient) SetGlobalEnv(ctx context.Context, content string) error {
	resp, err := e.sendRequest(ctx, "PUT", "/docker/stacks/meta/globalenv", nil, map[string]any{"content": content})
	if err != nil {
		return err
	}
	if resp.Status >= 400 {
		return fmt.Errorf("request failed with status %d: %s", resp.Status, string(resp.Body))
	}
	return nil
}