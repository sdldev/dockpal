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
	// Subcommands that don't belong to `docker compose` (network, image, ...)
	// run as plain `docker <args>`; compose subcommands get the prefix.
	if len(args) > 0 {
		switch args[0] {
		case "network", "image", "volume", "container", "system", "info", "version":
			return RunDockerOutput(ctx, dir, args...)
		}
	}
	return RunOutput(ctx, dir, args...)
}

func (adapter) TryLock(stackName string) bool { return TryLock(stackName) }
func (adapter) Unlock(stackName string)       { Unlock(stackName) }

// Register wires this package as the docker package's compose CLI backend.
// Call once at server startup.
func Register() {
	docker.RegisterStackCLI(adapter{})
}

// StackUpStreamed runs `docker compose up -d --remove-orphans` streaming
// output into the session, under the per-stack lock.
func StackUpStreamed(ctx context.Context, name string, session *docker.DeploySession) error {
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
