package server

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"
	"github.com/sdldev/dockpal/internal/docker"
)

// registerFleetRoutes serves the aggregated fleet poll for the servers page.
func registerFleetRoutes(deps *routeDeps) {
	deps.protected.GET("/fleet/summary", handleFleetSummary(deps.database, deps.agentMgr))
}

// fleetSystemInfo mirrors the merged HostInfo+HostStats JSON that
// GET /instances/:id/system/info returns, so the frontend can treat both
// identically.
type fleetSystemInfo struct {
	Hostname      string  `json:"hostname"`
	OS            string  `json:"os"`
	CPUCores      int     `json:"cpu_cores"`
	DockerVersion string  `json:"docker_version"`
	CPUPercent    float64 `json:"cpu_percent"`
	UsedRAM       uint64  `json:"used_ram"`
	TotalRAM      uint64  `json:"total_ram"`
	UsedDisk      uint64  `json:"used_disk"`
	TotalDisk     uint64  `json:"total_disk"`
	NetworkRxBps  float64 `json:"network_rx_bps"`
	NetworkTxBps  float64 `json:"network_tx_bps"`
}

// fleetInstanceRow is one servers-table row plus the live data the frontend
// used to fetch with three requests per instance on every poll.
type fleetInstanceRow struct {
	InstanceListItem
	SysInfo    *fleetSystemInfo       `json:"sys_info"`
	Containers []docker.ContainerInfo `json:"containers"`
	ImageCount int                    `json:"image_count"`
}

// handleFleetSummary aggregates the servers-page poll into one response:
// every registered instance with system info, containers and image count,
// collected concurrently through the AgentClient of each reachable instance.
// The frontend previously fanned out three requests per instance per poll,
// which tripped the read rate limit with a full fleet.
func handleFleetSummary(database *db.DB, agentMgr *agent.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instances, err := database.ListInstances()
		if err != nil {
			internalError(c, err)
			return
		}

		rows := make([]fleetInstanceRow, len(instances))
		var wg sync.WaitGroup
		// The Docker API has no bulk call and each agent is its own host, so
		// collection is one round-trip set per instance; bound the fan-out.
		sem := make(chan struct{}, 8)
		for i, inst := range instances {
			rows[i].InstanceListItem = instanceListRow(inst)
			// Reachability mirrors the frontend's isOnline: the local daemon
			// is always attempted, remote instances only when online.
			if inst.ID != "local" && inst.Status != "online" {
				continue
			}
			client, err := agentMgr.GetClient(inst.ID)
			if err != nil {
				log.Printf("fleet summary: no client for instance %s: %v", inst.ID, err)
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				collectFleetInstance(c.Request.Context(), client, &rows[i])
			}()
		}
		wg.Wait()

		c.JSON(http.StatusOK, gin.H{"instances": rows})
	}
}

// collectFleetInstance fills one row's live data. Each field is independent
// and best-effort: a failing call leaves it empty rather than failing the
// whole summary, mirroring the per-call catches in the old frontend fan-out.
func collectFleetInstance(ctx context.Context, client agent.AgentClient, row *fleetInstanceRow) {
	info, err := client.GetHostInfo(ctx)
	if err != nil {
		log.Printf("fleet summary %s: host info: %v", row.ID, err)
	} else {
		row.SysInfo = &fleetSystemInfo{
			Hostname:      info.Hostname,
			OS:            info.OS,
			CPUCores:      info.CPUCores,
			DockerVersion: info.DockerVersion,
		}
	}
	stats, err := client.GetHostStats(ctx)
	if err != nil {
		log.Printf("fleet summary %s: host stats: %v", row.ID, err)
	} else {
		if row.SysInfo == nil {
			row.SysInfo = &fleetSystemInfo{}
		}
		row.SysInfo.CPUPercent = stats.CPUPercent
		row.SysInfo.UsedRAM = stats.UsedRAM
		row.SysInfo.TotalRAM = stats.TotalRAM
		row.SysInfo.UsedDisk = stats.UsedDisk
		row.SysInfo.TotalDisk = stats.TotalDisk
		row.SysInfo.NetworkRxBps = stats.NetworkRxBps
		row.SysInfo.NetworkTxBps = stats.NetworkTxBps
	}
	containers, err := client.ListContainers(ctx, true)
	if err != nil {
		log.Printf("fleet summary %s: containers: %v", row.ID, err)
	} else {
		row.Containers = containers
	}
	images, err := client.ListImages(ctx)
	if err != nil {
		log.Printf("fleet summary %s: images: %v", row.ID, err)
	} else {
		row.ImageCount = len(images)
	}
}
