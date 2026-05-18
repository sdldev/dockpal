package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/moby/moby/api/types/container"
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
	ID      string                  `json:"id"`
	Name    string                  `json:"name"`
	Image   string                  `json:"image"`
	Status  string                  `json:"status"`
	State   string                  `json:"state"`
	Ports   []container.PortSummary `json:"ports"`
	Created int64                   `json:"created"`
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
	if ctr.HostConfig != nil {
		networkMode = string(ctr.HostConfig.NetworkMode)
		restartPolicy = string(ctr.HostConfig.RestartPolicy.Name)
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
