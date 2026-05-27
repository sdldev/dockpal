package docker

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// ImageUpdateResult holds the outcome of comparing a local image against the registry.
type ImageUpdateResult struct {
	HasUpdate    bool   `json:"has_update"`
	LocalDigest  string `json:"local_digest,omitempty"`
	RemoteDigest string `json:"remote_digest,omitempty"`
	Error        string `json:"error,omitempty"`
	CheckedAt    int64  `json:"checked_at"`
}

type ImageInfo struct {
	ID           string `json:"id"`
	Repo         string `json:"repo"`
	Tag          string `json:"tag"`
	Size         string `json:"size"`
	Created      string `json:"created"`
	RepoDigest   string `json:"repo_digest,omitempty"`
	HasUpdate    bool   `json:"has_update,omitempty"`
	RemoteDigest string `json:"remote_digest,omitempty"`
}

func (c *Client) ListImages(ctx context.Context) ([]ImageInfo, error) {
	result, err := c.cli.ImageList(ctx, client.ImageListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	images := make([]ImageInfo, 0, len(result.Items))
	for _, img := range result.Items {
		repo := "<none>"
		tag := "<none>"
		if len(img.RepoTags) > 0 && img.RepoTags[0] != "<none>:<none>" {
			parts := strings.SplitN(img.RepoTags[0], ":", 2)
			repo = parts[0]
			if len(parts) > 1 {
				tag = parts[1]
			}
		}
		images = append(images, ImageInfo{
			ID:      img.ID[:16],
			Repo:    repo,
			Tag:     tag,
			Size:    formatSize(img.Size),
			Created: fmt.Sprintf("%d", img.Created),
		})
	}
	return images, nil
}

func (c *Client) PullImage(ctx context.Context, image string) error {
	reader, err := c.cli.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	return nil
}

// PullImageWithAuth pulls an image with optional registry authentication.
// If registryAuth is empty, it falls back to an unauthenticated pull.
func (c *Client) PullImageWithAuth(ctx context.Context, image string, registryAuth string) error {
	opts := client.ImagePullOptions{}
	if registryAuth != "" {
		opts.RegistryAuth = registryAuth
	}
	reader, err := c.cli.ImagePull(ctx, image, opts)
	if err != nil {
		errMsg := err.Error()
		if registryAuth != "" && (strings.Contains(errMsg, "401") || strings.Contains(errMsg, "403") ||
			strings.Contains(errMsg, "unauthorized") || strings.Contains(errMsg, "denied")) {
			// Extract domain from image for the hint
			domain := extractImageDomain(image)
			return fmt.Errorf("authentication failed for %s — credentials may be expired: %w", domain, err)
		}
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	return nil
}

// CheckImageUpdate queries the registry manifest digest for an image and
// compares it with the locally cached image's RepoDigest.
// registryAuth is the base64-encoded Docker auth config (empty for public images).
func (c *Client) CheckImageUpdate(ctx context.Context, image string, registryAuth string) (*ImageUpdateResult, error) {
	now := time.Now().Unix()

	// 1. Inspect local image to get its digest
	localInspect, err := c.cli.ImageInspect(ctx, image)
	if err != nil {
		return &ImageUpdateResult{
			Error:     fmt.Sprintf("local image not found: %v", err),
			CheckedAt: now,
		}, nil
	}

	localDigest := ""
	if len(localInspect.RepoDigests) > 0 {
		parts := strings.SplitN(localInspect.RepoDigests[0], "@", 2)
		if len(parts) == 2 {
			localDigest = parts[1]
		}
	}

	// 2. Query registry for remote digest
	opts := client.DistributionInspectOptions{}
	if registryAuth != "" {
		opts.EncodedRegistryAuth = registryAuth
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	distResult, err := c.cli.DistributionInspect(ctx, image, opts)
	if err != nil {
		return &ImageUpdateResult{
			LocalDigest: localDigest,
			Error:       fmt.Sprintf("registry query failed: %v", err),
			CheckedAt:   now,
		}, nil
	}

	remoteDigest := string(distResult.Descriptor.Digest)
	hasUpdate := false
	if localDigest != "" && remoteDigest != "" && localDigest != remoteDigest {
		hasUpdate = true
	}

	return &ImageUpdateResult{
		HasUpdate:    hasUpdate,
		LocalDigest:  localDigest,
		RemoteDigest: remoteDigest,
		CheckedAt:    now,
	}, nil
}

// ForcePullImage unconditionally pulls an image, ignoring any locally cached version.
// registryAuth is the base64-encoded Docker auth config (empty for public images).
func (c *Client) ForcePullImage(ctx context.Context, image string, registryAuth string) error {
	opts := client.ImagePullOptions{}
	if registryAuth != "" {
		opts.RegistryAuth = registryAuth
	}
	reader, err := c.cli.ImagePull(ctx, image, opts)
	if err != nil {
		return fmt.Errorf("failed to force pull image: %w", err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	return nil
}

// InspectRepoDigest returns the digest portion (after the `@`) of the local
// image's first non-empty RepoDigests entry. When the local image has no
// pull history (RepoDigests is empty) it returns ("", nil) so callers can
// fall back to a tag-based rollback path. When the moby client is nil
// (e.g. an unconfigured *Client used in tests) it returns ("", nil) without
// performing an inspect.
//
// Used by the AutoUpdateWorker rollback path (task 3.7) to capture the
// previously-running image digest for a service before the recreate.
func (c *Client) InspectRepoDigest(ctx context.Context, image string) (string, error) {
	if c == nil || c.cli == nil {
		return "", nil
	}
	inspect, err := c.cli.ImageInspect(ctx, image)
	if err != nil {
		return "", err
	}
	if len(inspect.RepoDigests) == 0 {
		return "", nil
	}
	for _, rd := range inspect.RepoDigests {
		if rd == "" {
			continue
		}
		parts := strings.SplitN(rd, "@", 2)
		if len(parts) == 2 && parts[1] != "" {
			return parts[1], nil
		}
	}
	return "", nil
}

// extractImageDomain extracts the registry domain from an image reference.
func extractImageDomain(image string) string {
	parts := strings.SplitN(image, "/", 2)
	if len(parts) >= 2 && strings.Contains(parts[0], ".") {
		return parts[0]
	}
	return "registry"
}

func (c *Client) RemoveImage(ctx context.Context, id string) error {
	_, err := c.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: true})
	return err
}

// PruneResult holds the outcome of an image prune operation.
type PruneResult struct {
	ImagesDeleted  int   `json:"images_deleted"`
	SpaceReclaimed int64 `json:"space_reclaimed"`
}

// PruneImages removes unused images. When danglingOnly is true, only
// dangling images (<none>:<none>) are removed. Otherwise all images not
// referenced by any container are pruned.
func (c *Client) PruneImages(ctx context.Context, danglingOnly bool) (*PruneResult, error) {
	f := make(client.Filters)
	f.Add("dangling", strconv.FormatBool(danglingOnly))
	result, err := c.cli.ImagePrune(ctx, client.ImagePruneOptions{Filters: f})
	if err != nil {
		return nil, fmt.Errorf("failed to prune images: %w", err)
	}
	return &PruneResult{
		ImagesDeleted:  len(result.Report.ImagesDeleted),
		SpaceReclaimed: int64(result.Report.SpaceReclaimed),
	}, nil
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
