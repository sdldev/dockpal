package server

import (
	"context"
	"errors"
	"strings"

	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/docker"
)

const dockpalAgentProtectionReason = "Dockpal agent container cannot be removed from Dockpal"

var errProtectedDockpalAgentContainer = errors.New(dockpalAgentProtectionReason)

func markProtectedContainerInfo(info *docker.ContainerInfo) {
	if isProtectedDockpalAgentInfo(*info) {
		info.Protected = true
		info.ProtectionReason = dockpalAgentProtectionReason
	}
}

func markProtectedContainerDetail(detail *docker.ContainerDetail) {
	if isProtectedDockpalAgentContainer(detail) {
		detail.Protected = true
		detail.ProtectionReason = dockpalAgentProtectionReason
	}
}

func markProtectedContainerInfos(containers []docker.ContainerInfo) {
	for i := range containers {
		markProtectedContainerInfo(&containers[i])
	}
}

func ensureContainerRemovable(ctx context.Context, client agent.AgentClient, containerID string) error {
	detail, err := client.InspectContainer(ctx, containerID)
	if err != nil {
		return err
	}
	if isProtectedDockpalAgentContainer(detail) {
		return errProtectedDockpalAgentContainer
	}
	return nil
}

func isProtectedDockpalAgentContainer(detail *docker.ContainerDetail) bool {
	if detail == nil {
		return false
	}
	if !isDockpalAgentName(detail.Name) {
		return false
	}
	return isDockpalAgentImage(detail.Image) || hasDockpalAgentEnv(detail.Env)
}

func isProtectedDockpalAgentInfo(info docker.ContainerInfo) bool {
	return isDockpalAgentName(info.Name) && isDockpalAgentImage(info.Image)
}

func isDockpalAgentName(name string) bool {
	return strings.EqualFold(strings.TrimPrefix(name, "/"), "dockpal-agent")
}

func isDockpalAgentImage(image string) bool {
	image = strings.ToLower(image)
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "registry-1.docker.io/")
	return strings.Contains(image, "sdldev/dockpal-agent")
}

func hasDockpalAgentEnv(env []string) bool {
	for _, item := range env {
		for _, prefix := range []string{"DOCKPAL_MODE=", "DOCKPAL_TOKEN=", "DOCKPAL_SERVER=", "DOCKPAL_EDGE_SERVER="} {
			if strings.HasPrefix(item, prefix) {
				return true
			}
		}
	}
	return false
}
