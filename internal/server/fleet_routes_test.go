package server

import (
	"context"
	"errors"
	"testing"

	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/docker"
)

// stubAgentClient implements only the four AgentClient methods the fleet
// summary consumes; the embedded interface panics on anything else, which
// doubles as a guard against the summary growing new dependencies silently.
type stubAgentClient struct {
	agent.AgentClient

	hostInfo   *agent.HostInfo
	hostInfoEr error
	hostStats  *agent.HostStats
	containers []docker.ContainerInfo
	imageCount int
}

func (s *stubAgentClient) GetHostInfo(context.Context) (*agent.HostInfo, error) {
	return s.hostInfo, s.hostInfoEr
}

func (s *stubAgentClient) GetHostStats(context.Context) (*agent.HostStats, error) {
	return s.hostStats, nil
}

func (s *stubAgentClient) ListContainers(context.Context, bool) ([]docker.ContainerInfo, error) {
	return s.containers, nil
}

func (s *stubAgentClient) ListImages(context.Context) ([]docker.ImageInfo, error) {
	return make([]docker.ImageInfo, s.imageCount), nil
}

func TestCollectFleetInstanceAggregatesBestEffort(t *testing.T) {
	row := &fleetInstanceRow{}
	row.ID = "inst-test"
	client := &stubAgentClient{
		hostInfo: &agent.HostInfo{Hostname: "host-1", OS: "linux", CPUCores: 4, DockerVersion: "29.8"},
		hostStats: &agent.HostStats{
			CPUPercent: 12.5, UsedRAM: 1, TotalRAM: 4,
			UsedDisk: 10, TotalDisk: 100,
			NetworkRxBps: 3.5, NetworkTxBps: 1.5,
		},
		containers: make([]docker.ContainerInfo, 3),
		imageCount: 7,
	}

	collectFleetInstance(context.Background(), client, row)

	if row.SysInfo == nil {
		t.Fatal("expected sys_info to be populated")
	}
	if row.SysInfo.Hostname != "host-1" || row.SysInfo.CPUCores != 4 {
		t.Errorf("host info not merged: %+v", row.SysInfo)
	}
	if row.SysInfo.CPUPercent != 12.5 || row.SysInfo.TotalRAM != 4 {
		t.Errorf("host stats not merged: %+v", row.SysInfo)
	}
	if row.SysInfo.NetworkRxBps != 3.5 || row.SysInfo.NetworkTxBps != 1.5 {
		t.Errorf("network rates not merged: %+v", row.SysInfo)
	}
	if len(row.Containers) != 3 {
		t.Errorf("expected 3 containers, got %d", len(row.Containers))
	}
	if row.ImageCount != 7 {
		t.Errorf("expected image count 7, got %d", row.ImageCount)
	}
}

func TestCollectFleetInstanceKeepsRowWhenInfoFails(t *testing.T) {
	row := &fleetInstanceRow{}
	row.ID = "inst-test"
	client := &stubAgentClient{
		hostInfo:   &agent.HostInfo{},
		hostInfoEr: errors.New("agent offline"),
		hostStats:  &agent.HostStats{CPUPercent: 5, TotalRAM: 8},
	}

	collectFleetInstance(context.Background(), client, row)

	if row.SysInfo == nil {
		t.Fatal("stats-only instance should still produce sys_info")
	}
	if row.SysInfo.Hostname != "" || row.SysInfo.CPUPercent != 5 {
		t.Errorf("expected stats-only sys_info, got %+v", row.SysInfo)
	}
	if row.Containers != nil || row.ImageCount != 0 {
		t.Errorf("unexpected live data: %v / %d", row.Containers, row.ImageCount)
	}
}
