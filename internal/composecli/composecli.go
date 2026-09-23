// Package composecli runs `docker compose` CLI commands for Dockge-style
// stack management, streaming combined output into a docker.DeploySession.
//
// The stack lifecycle (up/stop/restart/down/pull + per-service ops) is
// delegated to the official compose plugin instead of the moby API so the
// full compose spec (build, top-level volumes/networks, env_file, ...) works.
//
// Remote instances run the same engine inside the agent process (see the
// dockpal-agent repo's internal/composecli) and are reached through the
// AgentClient stack RPCs (Direct/Edge implementations).
package composecli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/sdldev/dockpal/internal/docker"
)

// runFunc is the executable entry point — swappable in tests.
var runFunc = run


// Available reports whether the docker CLI with the compose plugin exists.
func Available() bool {
	path, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	cmd := exec.Command(path, "compose", "version")
	return cmd.Run() == nil
}

// --- per-stack operation serialization ---

var stackLocks sync.Map // map[string]*stackLock

type stackLock struct {
	mu    sync.Mutex
	held  bool
	heldC chan struct{}
}

// TryLock acquires the per-stack lock without blocking. Returns false when
// another operation is already running for this stack (Dockge parity:
// "Another operation is already running").
func TryLock(stackName string) bool {
	v, _ := stackLocks.LoadOrStore(stackName, &stackLock{})
	l := v.(*stackLock)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held {
		return false
	}
	l.held = true
	l.heldC = make(chan struct{})
	return true
}

// Unlock releases the per-stack lock acquired via TryLock.
func Unlock(stackName string) {
	v, ok := stackLocks.Load(stackName)
	if !ok {
		return
	}
	l := v.(*stackLock)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = false
	close(l.heldC)
}

// Run executes `docker compose <args...>` with dir as the working directory,
// streaming combined stdout/stderr line-by-line into session (may be nil).
func Run(ctx context.Context, dir string, session *docker.DeploySession, args ...string) error {
	return runFunc(ctx, dir, session, args...)
}

// run is the default process-backed implementation.
func run(ctx context.Context, dir string, session *docker.DeploySession, args ...string) error {
	fullArgs := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // combined output

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start docker compose: %w", err)
	}

	// Drain output before Wait to avoid pipe-buffer deadlock.
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		streamLines(stdout, session)
	}()

	waitErr := cmd.Wait()
	<-scanDone

	if waitErr != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), waitErr)
	}
	return nil
}

func streamLines(r io.Reader, session *docker.DeploySession) {
	if session == nil {
		io.Copy(io.Discard, r)
		return
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		session.Emit("compose", line, "running")
	}
}

// Output runs `docker compose <args...>` and returns trimmed stdout.
// Intended for machine-readable invocations (e.g. `--format json`).
func Output(ctx context.Context, dir string, args ...string) (string, error) {
	fullArgs := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("docker compose %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// outputFunc mirror for tests.
var outputFunc = Output


// RunOutput is Output routed through the swappable outputFunc.
func RunOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return outputFunc(ctx, dir, args...)
}

// RunDockerOutput runs plain `docker <args...>` (no compose prefix) and
// returns trimmed stdout — for host-level queries like `docker network ls`.
func RunDockerOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
