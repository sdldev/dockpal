package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/moby/moby/api/types/container"
	apinetwork "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type Client struct {
	cli *client.Client
}

func NewClient() (*Client, error) {
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

type ContainerInfo struct {
	ID               string                  `json:"id"`
	Name             string                  `json:"name"`
	Image            string                  `json:"image"`
	Status           string                  `json:"status"`
	State            string                  `json:"state"`
	Ports            []container.PortSummary `json:"ports"`
	Created          int64                   `json:"created"`
	Protected        bool                    `json:"protected,omitempty"`
	ProtectionReason string                  `json:"protection_reason,omitempty"`
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerInfo, error) {
	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: all})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	containers := make([]ContainerInfo, len(result.Items))
	for i, ctr := range result.Items {
		name := ""
		if len(ctr.Names) > 0 {
			name = ctr.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}
		containers[i] = ContainerInfo{
			ID:      ctr.ID[:12],
			Name:    name,
			Image:   ctr.Image,
			Status:  ctr.Status,
			State:   string(ctr.State),
			Ports:   ctr.Ports,
			Created: ctr.Created,
		}
	}
	return containers, nil
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

func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerDetail, error) {
	result, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	ctr := result.Container

	name := ctr.Name
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}

	networks := make(map[string]string)
	if ctr.NetworkSettings != nil {
		for netName, ep := range ctr.NetworkSettings.Networks {
			networks[netName] = ep.IPAddress.String()
		}
	}

	status := ""
	if ctr.State != nil {
		status = string(ctr.State.Status)
	}

	networkMode := ""
	restartPolicy := ""
	var memoryLimit int64
	var nanoCPUs int64
	if ctr.HostConfig != nil {
		networkMode = string(ctr.HostConfig.NetworkMode)
		restartPolicy = string(ctr.HostConfig.RestartPolicy.Name)
		memoryLimit = ctr.HostConfig.Memory
		nanoCPUs = ctr.HostConfig.NanoCPUs
	}

	info := &ContainerDetail{
		ContainerInfo: ContainerInfo{
			ID:      ctr.ID[:12],
			Name:    name,
			Image:   ctr.Config.Image,
			Status:  status,
			State:   status,
			Ports:   []container.PortSummary{},
			Created: 0,
		},
		Platform:      ctr.Platform,
		Env:           ctr.Config.Env,
		Mounts:        ctr.Mounts,
		NetworkMode:   networkMode,
		RestartPolicy: restartPolicy,
		Networks:      networks,
		MemoryLimit:   memoryLimit,
		NanoCPUs:      nanoCPUs,
	}

	return info, nil
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	_, err := c.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (c *Client) StopContainer(ctx context.Context, id string) error {
	timeout := 10
	_, err := c.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	return err
}

func (c *Client) RestartContainer(ctx context.Context, id string) error {
	timeout := 10
	_, err := c.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: &timeout})
	return err
}

// RemoveContainer stops (if running) and removes a container.
// When force is true, it kills the container immediately rather than gracefully stopping.
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	if force {
		// Force remove handles all states: running, paused, stuck
		_, err := c.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
		return err
	}
	// Graceful: try to stop first, then remove
	timeout := 10
	c.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	_, err := c.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
	return err
}

func (c *Client) ContainerLogs(ctx context.Context, id string, tail string) (io.ReadCloser, error) {
	result, err := c.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
		Follow:     true,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type ContainerStats struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsage   uint64  `json:"memory_usage"`
	MemoryLimit   uint64  `json:"memory_limit"`
	MemoryPercent float64 `json:"memory_percent"`
	NetworkRx     uint64  `json:"network_rx"`
	NetworkTx     uint64  `json:"network_tx"`
}

// ServerVersion returns the Docker daemon version string.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	ver, err := c.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get docker version: %w", err)
	}
	return ver.Version, nil
}

func (c *Client) GetContainerStats(ctx context.Context, id string) (*ContainerStats, error) {
	result, err := c.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: false})
	if err != nil {
		return nil, fmt.Errorf("failed to get container stats: %w", err)
	}
	defer result.Body.Close()

	var v container.StatsResponse
	if err := json.NewDecoder(result.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("failed to decode stats: %w", err)
	}

	stats := &ContainerStats{}

	cpuDelta := v.CPUStats.CPUUsage.TotalUsage - v.PreCPUStats.CPUUsage.TotalUsage
	systemDelta := v.CPUStats.SystemUsage - v.PreCPUStats.SystemUsage
	if systemDelta > 0 && cpuDelta > 0 {
		numCPU := len(v.CPUStats.CPUUsage.PercpuUsage)
		if numCPU == 0 {
			numCPU = 1
		}
		stats.CPUPercent = (float64(cpuDelta) / float64(systemDelta)) * float64(numCPU) * 100.0
	}

	stats.MemoryUsage = v.MemoryStats.Usage
	stats.MemoryLimit = v.MemoryStats.Limit
	if stats.MemoryLimit > 0 {
		stats.MemoryPercent = float64(stats.MemoryUsage) / float64(stats.MemoryLimit) * 100.0
	}

	if v.Networks != nil {
		for _, net := range v.Networks {
			stats.NetworkRx += net.RxBytes
			stats.NetworkTx += net.TxBytes
		}
	}

	return stats, nil
}

// RenameContainer renames a container.
func (c *Client) RenameContainer(ctx context.Context, id, newName string) error {
	_, err := c.cli.ContainerRename(ctx, id, client.ContainerRenameOptions{NewName: newName})
	return err
}

// UpdateContainer applies in-place updates (restart policy, resource limits).
func (c *Client) UpdateContainer(ctx context.Context, id string, opts client.ContainerUpdateOptions) error {
	_, err := c.cli.ContainerUpdate(ctx, id, opts)
	return err
}

// ContainerEditRequest represents the set of changes to apply to a container.
// In-place fields (name, restart_policy, memory_limit, cpu_limit) are applied
// without recreation. Recreate fields (image, env, ports, volumes) require
// stopping and recreating the container.
type ContainerEditRequest struct {
	// In-place fields
	Name          *string  `json:"name,omitempty"`
	RestartPolicy *string  `json:"restart_policy,omitempty"`
	MemoryLimit   *int64   `json:"memory_limit,omitempty"` // bytes, 0 = unlimited
	CPULimit      *float64 `json:"cpu_limit,omitempty"`    // fractional CPUs, e.g. 1.5

	// Recreate fields
	Image   *string          `json:"image,omitempty"`
	Env     *[]string        `json:"env,omitempty"`
	Ports   *[]PortMapping   `json:"ports,omitempty"`
	Volumes *[]VolumeMapping `json:"volumes,omitempty"`
}

// PortMapping represents a host:container port mapping.
type PortMapping struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"` // tcp or udp
}

// VolumeMapping represents a host:container volume mount.
type VolumeMapping struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only"`
}

// EditContainer applies edits to a container. It determines which changes
// require in-place updates vs recreation and applies them accordingly.
func (c *Client) EditContainer(ctx context.Context, id string, req ContainerEditRequest) (*ContainerDetail, error) {
	// First, inspect the container to verify it exists and get the full ID + name.
	// This handles truncated IDs (12-char) by letting Docker resolve them early.
	preInspect, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}
	// Use the full container ID for all subsequent operations
	fullID := preInspect.Container.ID
	containerName := preInspect.Container.Name
	if len(containerName) > 0 && containerName[0] == '/' {
		containerName = containerName[1:]
	}

	needsRecreate := req.Image != nil || req.Env != nil || req.Ports != nil || req.Volumes != nil

	// Apply in-place updates first
	if req.Name != nil {
		if err := c.RenameContainer(ctx, fullID, *req.Name); err != nil {
			return nil, fmt.Errorf("failed to rename container: %w", err)
		}
		containerName = *req.Name
	}

	if req.RestartPolicy != nil || req.MemoryLimit != nil || req.CPULimit != nil {
		updateOpts := client.ContainerUpdateOptions{}
		if req.RestartPolicy != nil {
			updateOpts.RestartPolicy = &container.RestartPolicy{Name: container.RestartPolicyMode(*req.RestartPolicy)}
		}
		if req.MemoryLimit != nil || req.CPULimit != nil {
			resources := container.Resources{}
			if req.MemoryLimit != nil {
				resources.Memory = *req.MemoryLimit
				if *req.MemoryLimit > 0 {
					// Docker requires MemorySwap >= Memory; set to -1 (unlimited swap)
					resources.MemorySwap = -1
				} else {
					// 0 = unlimited, also reset swap
					resources.MemorySwap = 0
				}
			}
			if req.CPULimit != nil {
				resources.NanoCPUs = int64(*req.CPULimit * 1e9)
			}
			updateOpts.Resources = &resources
		}
		if err := c.UpdateContainer(ctx, fullID, updateOpts); err != nil {
			return nil, fmt.Errorf("failed to update container: %w", err)
		}
	}

	if needsRecreate {
		if err := c.recreateContainer(ctx, fullID, req); err != nil {
			return nil, fmt.Errorf("failed to recreate container: %w", err)
		}
	}

	// Return updated detail — after recreate the ID has changed,
	// so look up by container name which is stable across recreations.
	return c.InspectContainer(ctx, containerName)
}

// recreateContainer stops, removes, and recreates a container with merged config.
func (c *Client) recreateContainer(ctx context.Context, id string, req ContainerEditRequest) error {
	// Inspect current container to get full config
	result, err := c.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("failed to inspect container for recreation: %w", err)
	}

	ctr := result.Container
	if ctr.Config == nil || ctr.HostConfig == nil {
		return fmt.Errorf("container inspect returned incomplete data")
	}

	// Determine the container name
	name := ctr.Name
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}
	if req.Name != nil {
		name = *req.Name
	}

	// Merge environment variables
	env := ctr.Config.Env
	if req.Env != nil {
		env = *req.Env
	}

	// Merge image
	image := ctr.Config.Image
	if req.Image != nil {
		image = *req.Image
	}

	// Merge port bindings
	portBindings := ctr.HostConfig.PortBindings
	exposedPorts := ctr.Config.ExposedPorts
	if req.Ports != nil {
		portBindings = apinetwork.PortMap{}
		exposedPorts = apinetwork.PortSet{}
		for _, pm := range *req.Ports {
			proto := pm.Protocol
			if proto == "" {
				proto = "tcp"
			}
			portKey, _ := apinetwork.ParsePort(fmt.Sprintf("%d/%s", pm.ContainerPort, proto))
			exposedPorts[portKey] = struct{}{}
			portBindings[portKey] = append(portBindings[portKey], apinetwork.PortBinding{
				HostIP:   netip.IPv4Unspecified(),
				HostPort: fmt.Sprintf("%d", pm.HostPort),
			})
		}
	}

	// Merge volume bindings
	binds := ctr.HostConfig.Binds
	if req.Volumes != nil {
		binds = make([]string, 0, len(*req.Volumes))
		for _, vm := range *req.Volumes {
			bind := vm.HostPath + ":" + vm.ContainerPath
			if vm.ReadOnly {
				bind += ":ro"
			}
			binds = append(binds, bind)
		}
	}

	// Build new container config
	newConfig := &container.Config{
		Image:        image,
		Env:          env,
		ExposedPorts: exposedPorts,
		Labels:       ctr.Config.Labels,
		Cmd:          ctr.Config.Cmd,
		Entrypoint:   ctr.Config.Entrypoint,
		WorkingDir:   ctr.Config.WorkingDir,
		User:         ctr.Config.User,
		Tty:          ctr.Config.Tty,
		OpenStdin:    ctr.Config.OpenStdin,
		AttachStdin:  ctr.Config.AttachStdin,
		AttachStdout: ctr.Config.AttachStdout,
		AttachStderr: ctr.Config.AttachStderr,
	}

	// Build new host config
	restartPolicy := ctr.HostConfig.RestartPolicy
	if req.RestartPolicy != nil {
		restartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(*req.RestartPolicy)}
	}

	newHostConfig := &container.HostConfig{
		RestartPolicy: restartPolicy,
		PortBindings:  portBindings,
		Binds:         binds,
		NetworkMode:   ctr.HostConfig.NetworkMode,
		Privileged:    ctr.HostConfig.Privileged,
		CapAdd:        ctr.HostConfig.CapAdd,
		CapDrop:       ctr.HostConfig.CapDrop,
		ExtraHosts:    ctr.HostConfig.ExtraHosts,
	}

	// Apply resource limits from request
	if req.MemoryLimit != nil {
		newHostConfig.Memory = *req.MemoryLimit
		newHostConfig.MemorySwap = -1 // unlimited swap to satisfy Docker constraint
	} else if ctr.HostConfig.Memory > 0 {
		newHostConfig.Memory = ctr.HostConfig.Memory
		if ctr.HostConfig.MemorySwap != 0 {
			newHostConfig.MemorySwap = ctr.HostConfig.MemorySwap
		} else {
			newHostConfig.MemorySwap = -1
		}
	}
	if req.CPULimit != nil {
		newHostConfig.NanoCPUs = int64(*req.CPULimit * 1e9)
	} else if ctr.HostConfig.NanoCPUs != 0 {
		newHostConfig.NanoCPUs = ctr.HostConfig.NanoCPUs
	}

	// Build network config (preserve existing network connections)
	var networkConfig *apinetwork.NetworkingConfig
	if ctr.NetworkSettings != nil && len(ctr.NetworkSettings.Networks) > 0 {
		endpointsConfig := make(map[string]*apinetwork.EndpointSettings)
		for netName := range ctr.NetworkSettings.Networks {
			endpointsConfig[netName] = &apinetwork.EndpointSettings{}
		}
		networkConfig = &apinetwork.NetworkingConfig{
			EndpointsConfig: endpointsConfig,
		}
	}

	// Check if container was running before recreation
	wasRunning := ctr.State != nil && strings.ToLower(string(ctr.State.Status)) == "running"

	// Stop and remove the old container
	if wasRunning {
		timeout := 10
		c.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	}
	removeOpts := client.ContainerRemoveOptions{
		RemoveVolumes: false, // preserve volumes
		Force:         true,
	}
	if _, err := c.cli.ContainerRemove(ctx, id, removeOpts); err != nil {
		return fmt.Errorf("failed to remove old container: %w", err)
	}

	// Create new container
	createOpts := client.ContainerCreateOptions{
		Name:             name,
		Config:           newConfig,
		HostConfig:       newHostConfig,
		NetworkingConfig: networkConfig,
	}
	createResult, err := c.cli.ContainerCreate(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}

	// Start the new container if the old one was running
	if wasRunning {
		if _, err := c.cli.ContainerStart(ctx, createResult.ID, client.ContainerStartOptions{}); err != nil {
			return fmt.Errorf("container created but failed to start: %w", err)
		}
	}

	return nil
}
