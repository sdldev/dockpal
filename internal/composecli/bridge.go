package composecli

import (
	"context"

	"github.com/sdldev/dockpal/internal/docker"
)

// adapter bridges composecli into docker.RegisterStackCLI without an import
// cycle (docker package defines the stackCLI seam, we implement it).
type adapter struct{}

func (adapter) Run(ctx context.Context, dir string, args ...string) error {
	return Run(ctx, dir, nil, args...)
}

func (adapter) Output(ctx context.Context, dir string, args ...string) (string, error) {
	// Only "network" (docker network ls) is a plain-`docker` subcommand today
	// — everything else goes through the compose plugin (audit L4: pruned the
	// unreachable image/volume/container/system/info/version branches; add one
	// back here if a caller ever needs it).
	if len(args) > 0 && args[0] == "network" {
		return RunDockerOutput(ctx, dir, args...)
	}
	return RunOutput(ctx, dir, args...)
}

func (adapter) TryLock(stackName string) bool { return TryLock(stackName) }
func (adapter) Unlock(stackName string)       { Unlock(stackName) }

func (adapter) StackUpStreamed(ctx context.Context, name string, session *docker.DeploySession) error {
	return StackUpStreamed(ctx, name, session)
}

// Register wires this package as the docker package's compose CLI backend.
// Call once at server startup.
func Register() {
	docker.RegisterStackCLI(adapter{})
}

// StackUpStreamed runs `docker compose up -d --remove-orphans` streaming
// output into the session, under the per-stack lock.
func StackUpStreamed(ctx context.Context, name string, session *docker.DeploySession) error {
	// Always terminate the stream — without this the WebSocket reader waits on
	// session.Done forever and the UI never refreshes (audit C1).
	defer session.Close()
	dir, err := docker.StackDir(name)
	if err != nil {
		return err
	}
	if !TryLock(name) {
		return errBusy
	}
	defer Unlock(name)
	session.Emit("up", "docker compose up -d --remove-orphans", "running")
	args := docker.StackComposeArgs(dir, "up", "-d", "--remove-orphans")
	if err := Run(ctx, dir, session, args...); err != nil {
		session.Emit("up", err.Error(), "error")
		// Offer the same actionable hints as the legacy moby deploy path
		// (audit-stack-container L3).
		if hint := docker.DiagnoseDeployError(err.Error()); hint != "" {
			session.Emit("hint", hint, "error")
		}
		return err
	}
	session.Emit("up", "Stack is up", "done")
	return nil
}

type busyError struct{}

func (busyError) Error() string { return "another operation is already running for this stack" }

var errBusy error = busyError{}

// IsBusy reports whether err means a concurrent stack operation holds the lock.
func IsBusy(err error) bool {
	_, ok := err.(busyError)
	return ok
}
