package docker

import (
	"context"
	"log"
	"sort"

	"github.com/sdldev/dockpal/internal/db"
)

// AppSummary is the response shape used by `GET /apps`. One entry represents
// one compose project (App), aggregated from all containers carrying the
// `dockpal.project=<name>` label. Per-service details live in Services.
//
// InstanceID is zero-valued here; the HTTP handler layer (task 5.3) sets it
// per request so this docker-layer function stays free of per-instance
// concerns. LastUpdate is populated by ListApps when an AppUpdateStore is
// provided.
type AppSummary struct {
	Name       string              `json:"name"`
	InstanceID string              `json:"instance_id,omitempty"`
	Services   []AppServiceSummary `json:"services"`
	AutoUpdate bool                `json:"auto_update"`
	HasUpdate  bool                `json:"has_update"`
	LastUpdate *db.AppUpdateRecord `json:"last_update,omitempty"`
}

// AppServiceSummary describes one service inside an App and folds the
// ImageUpdateMonitor cache into the response so the UI can render the
// `Update available` badge without a second round-trip.
type AppServiceSummary struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	HasUpdate    bool   `json:"has_update"`
	LocalDigest  string `json:"local_digest,omitempty"`
	RemoteDigest string `json:"remote_digest,omitempty"`
}

// ListApps lists every container that carries the `dockpal.project` label,
// groups them by project, and folds in update-status information from the
// supplied ImageUpdateMonitor along with the most recent update record from
// the supplied AppUpdateStore. Apps are returned sorted by Name ascending so
// the UI renders a stable order.
//
// monitor may be nil, in which case all `has_update`, `local_digest`, and
// `remote_digest` fields are left empty.
//
// store may be nil, in which case `last_update` is left as nil. When non-nil,
// the most recent record per app is fetched via ListAppUpdates(app, 1). A
// store error for one app is logged and treated as "no record" so the rest
// of the response still renders.
//
// Empty results return a non-nil empty slice so the JSON encoder emits `[]`
// rather than `null`.
func (c *Client) ListApps(ctx context.Context, monitor *ImageUpdateMonitor, store db.AppUpdateStore) ([]AppSummary, error) {
	containers, err := c.ListContainersWithLabel(ctx, "dockpal.project")
	if err != nil {
		return nil, err
	}

	// Group containers by project. Each project tracks its services keyed by
	// the `dockpal.service` label so duplicate replicas collapse to one entry
	// (the first occurrence wins).
	type projectAccum struct {
		name       string
		autoUpdate bool
		services   map[string]AppServiceSummary
	}
	projects := make(map[string]*projectAccum)

	for _, ctr := range containers {
		project := ctr.Labels["dockpal.project"]
		if project == "" {
			// Belt-and-braces: ListContainersWithLabel filters on the key
			// presence already, but a defensive skip protects against a
			// container that has the key with an empty value.
			continue
		}

		acc, ok := projects[project]
		if !ok {
			acc = &projectAccum{
				name:     project,
				services: make(map[string]AppServiceSummary),
			}
			projects[project] = acc
		}

		if ctr.Labels["dockpal.auto-update"] == "true" {
			acc.autoUpdate = true
		}

		svcName := ctr.Labels["dockpal.service"]
		if svcName == "" {
			// Fall back to the container name so a project with un-labelled
			// services still surfaces something useful in the UI.
			svcName = ctr.Name
		}
		if _, exists := acc.services[svcName]; exists {
			// First container per service wins; replicas are coalesced.
			continue
		}

		svc := AppServiceSummary{
			Name:  svcName,
			Image: ctr.Image,
			State: ctr.State,
		}
		if monitor != nil {
			if status := monitor.GetStatus(ctr.Image); status != nil && status.Result != nil {
				svc.HasUpdate = status.Result.HasUpdate
				svc.LocalDigest = status.Result.LocalDigest
				svc.RemoteDigest = status.Result.RemoteDigest
			}
		}
		acc.services[svcName] = svc
	}

	apps := make([]AppSummary, 0, len(projects))
	for _, acc := range projects {
		services := make([]AppServiceSummary, 0, len(acc.services))
		for _, s := range acc.services {
			services = append(services, s)
		}
		sort.Slice(services, func(i, j int) bool {
			return services[i].Name < services[j].Name
		})

		hasUpdate := false
		for _, s := range services {
			if s.HasUpdate {
				hasUpdate = true
				break
			}
		}

		summary := AppSummary{
			Name:       acc.name,
			Services:   services,
			AutoUpdate: acc.autoUpdate,
			HasUpdate:  hasUpdate,
		}

		if store != nil {
			recs, err := store.ListAppUpdates(acc.name, 1)
			if err != nil {
				// A store error for one app should not fail the whole
				// response. Log and continue with an empty LastUpdate so
				// the UI still renders the rest of the dashboard.
				log.Printf("[apps] last update lookup failed for %q: %v", acc.name, err)
			} else if len(recs) > 0 {
				rec := recs[0]
				summary.LastUpdate = &rec
			}
		}

		apps = append(apps, summary)
	}

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Name < apps[j].Name
	})

	return apps, nil
}
