package docker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// HealthMonitor periodically checks for containers labeled with
// dockpal.auto-recover=true that are in exited/dead state and restarts them.
type HealthMonitor struct {
	client     *Client
	ticker     *time.Ticker
	stop       chan struct{}
	failures   map[string]int // containerID -> consecutive failure count in current cycle
	failuresMu sync.Mutex
}

// NewHealthMonitor creates a new HealthMonitor with a 60-second check interval.
func NewHealthMonitor(client *Client) *HealthMonitor {
	return &HealthMonitor{
		client:   client,
		ticker:   time.NewTicker(60 * time.Second),
		stop:     make(chan struct{}),
		failures: make(map[string]int),
	}
}

// Start begins the background health monitoring goroutine.
func (hm *HealthMonitor) Start() {
	go hm.run()
}

// Stop terminates the health monitoring goroutine.
func (hm *HealthMonitor) Stop() {
	close(hm.stop)
	hm.ticker.Stop()
}

func (hm *HealthMonitor) run() {
	for {
		select {
		case <-hm.stop:
			return
		case <-hm.ticker.C:
			hm.check()
		}
	}
}

func (hm *HealthMonitor) check() {
	ctx := context.Background()
	containers, err := hm.client.ListContainersWithLabel(ctx, "dockpal.auto-recover=true")
	if err != nil {
		log.Printf("[recovery] failed to list containers: %v", err)
		return
	}

	// Reset failure counts at the start of each cycle
	hm.failuresMu.Lock()
	hm.failures = make(map[string]int)
	hm.failuresMu.Unlock()

	for _, ctr := range containers {
		if ctr.State != "exited" && ctr.State != "dead" {
			continue
		}

		hm.failuresMu.Lock()
		attempts := hm.failures[ctr.ID]
		hm.failuresMu.Unlock()

		if attempts >= 3 {
			log.Printf("[recovery] CRITICAL: container %s (%s) failed 3 restart attempts, skipping until next cycle", ctr.Name, ctr.ID)
			continue
		}

		if err := hm.client.StartContainer(ctx, ctr.ID); err != nil {
			hm.failuresMu.Lock()
			hm.failures[ctr.ID]++
			hm.failuresMu.Unlock()
			log.Printf("[recovery] failed to restart container %s (attempt %d/3): %v", ctr.Name, attempts+1, err)
		} else {
			log.Printf("[recovery] restarted container %s at %s", ctr.Name, time.Now().Format(time.RFC3339))
		}
	}
}

// ListContainersWithLabel lists all containers (including stopped) that have the specified label.
func (c *Client) ListContainersWithLabel(ctx context.Context, label string) ([]ContainerInfo, error) {
	f := make(client.Filters)
	f = f.Add("label", label)

	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
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
			ID:            ctr.ID[:12],
			Name:          name,
			Image:         ctr.Image,
			Status:        ctr.Status,
			State:         string(ctr.State),
			Ports:         ctr.Ports,
			Created:       ctr.Created,
			RestartPolicy: "",
			Labels:        ctr.Labels,
		}
	}
	return containers, nil
}
