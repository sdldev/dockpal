package tunnel

import (
	"context"
	"fmt"
	"regexp"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	// CloudflaredImage is the official Cloudflare tunnel Docker image.
	CloudflaredImage = "cloudflare/cloudflared:latest"
	// CloudflaredContainer is the name used for the managed tunnel container.
	CloudflaredContainer = "dockpal-cloudflared"
)

// tokenRegex validates that a tunnel token contains only safe characters.
var tokenRegex = regexp.MustCompile(`^[a-zA-Z0-9\-_.]+$`)

// CloudflareTunnel manages the lifecycle of a cloudflared container.
type CloudflareTunnel struct {
	docker *client.Client
}

// NewCloudflareTunnel creates a new CloudflareTunnel instance.
func NewCloudflareTunnel(docker *client.Client) *CloudflareTunnel {
	return &CloudflareTunnel{docker: docker}
}

// ValidateTunnelToken checks that the token is non-empty and contains only valid characters.
func ValidateTunnelToken(token string) error {
	if token == "" {
		return fmt.Errorf("tunnel token is required")
	}
	if !tokenRegex.MatchString(token) {
		return fmt.Errorf("tunnel token contains invalid characters")
	}
	return nil
}

// Deploy creates and starts a cloudflared container with the given tunnel token.
func (ct *CloudflareTunnel) Deploy(ctx context.Context, token string) error {
	if err := ValidateTunnelToken(token); err != nil {
		return err
	}

	cfg := &container.Config{
		Image: CloudflaredImage,
		Cmd:   []string{"tunnel", "--no-autoupdate", "run", "--token", token},
		Labels: map[string]string{
			"dockpal.managed": "true",
			"dockpal.tunnel":  "true",
		},
	}

	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	createOpts := client.ContainerCreateOptions{
		Name:       CloudflaredContainer,
		Image:      CloudflaredImage,
		Config:     cfg,
		HostConfig: hostCfg,
	}

	resp, err := ct.docker.ContainerCreate(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create cloudflared container: %w", err)
	}

	if _, err := ct.docker.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start cloudflared container: %w", err)
	}

	return nil
}

// Remove stops and removes the cloudflared container.
func (ct *CloudflareTunnel) Remove(ctx context.Context) error {
	timeout := 10
	ct.docker.ContainerStop(ctx, CloudflaredContainer, client.ContainerStopOptions{Timeout: &timeout})

	_, err := ct.docker.ContainerRemove(ctx, CloudflaredContainer, client.ContainerRemoveOptions{Force: true})
	if err != nil {
		return fmt.Errorf("failed to remove cloudflared container: %w", err)
	}
	return nil
}
