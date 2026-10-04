package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Dockge-style stack management on top of the `docker compose` CLI.
//
// A "stack" is a directory under composeBaseDir() containing a compose file
// (compose.yaml preferred; docker-compose.yml variants accepted on read) plus
// a per-stack `.env` file. Unlike upstream Dockge — whose save() never writes
// the .env — SaveStack persists both, so edits survive restarts.
//
// Discovery is the union of the on-disk directories (status "draft") and
// `docker compose ls --all --format json` (live status + unmanaged stacks).

// Stack status values (Dockge parity, serialized to JSON).
const (
	StackStatusUnknown = "unknown"
	StackStatusDraft   = "draft"   // files on disk, not deployed yet
	StackStatusRunning = "running" // all containers running
	StackStatusExited  = "exited"  // at least one container exited
	StackStatusPartial = "partial" // created or mixed state
)

// Stack is one compose project.
//
// Note (audit-stack-container L10): stack DTOs deliberately use camelCase
// JSON tags (statusText, composeYAML, containerName) to match the Dockge-
// style TypeScript client in svelte/src/lib/api/stacks.ts, while container
// DTOs (container.go) use snake_case for moby API parity. This split is
// load-bearing for the TS client — keep it, and keep new stack fields
// camelCase.
type Stack struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	StatusText  string         `json:"statusText"`
	Managed     bool           `json:"managed"` // directory exists under composeBaseDir()
	ComposeYAML string         `json:"composeYAML,omitempty"`
	ComposeENV  string         `json:"composeENV,omitempty"`
	Services    []StackService `json:"services,omitempty"`
}

// StackService is one service's runtime state inside a stack.
type StackService struct {
	Name          string `json:"name"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	State         string `json:"state"`  // running | exited | created | ...
	Health        string `json:"health"` // healthy | unhealthy | ""
}

var stackNameRegex = regexp.MustCompile(`^[a-z0-9_-]+$`)

// ComposeFileName is the canonical compose file name written by every Dockpal
// code path (stack store AND the legacy App-Install writer) so one project
// directory never contains two competing compose files (audit H3).
const ComposeFileName = "compose.yaml"

// acceptedComposeFileNames in read preference order (Dockge parity).
var acceptedComposeFileNames = []string{
	ComposeFileName,
	"docker-compose.yaml",
	"docker-compose.yml",
	"compose.yml",
}

// stackCLI is the seam the composecli package plugs into (set in init via
// RegisterStackCLI) to avoid an import cycle composecli <-> docker.
type stackCLI interface {
	Run(ctx context.Context, dir string, args ...string) error
	Output(ctx context.Context, dir string, args ...string) (string, error)
	TryLock(stackName string) bool
	Unlock(stackName string)
	// StackUpStreamed runs `compose up -d --remove-orphans` streaming events
	// into the session and closing it on every exit path. It lives on the
	// seam so tests can intercept deploys (audit-stack-container H6).
	StackUpStreamed(ctx context.Context, name string, session *DeploySession) error
}

var cli stackCLI

// RegisterStackCLI wires the compose CLI backend. Called once at startup.
func RegisterStackCLI(c stackCLI) { cli = c }

// StackUpStreamedCLI routes a streamed stack deploy through the registered
// CLI backend — the testable counterpart of calling the composecli package
// directly.
func StackUpStreamedCLI(ctx context.Context, name string, session *DeploySession) error {
	if cli == nil {
		defer session.Close()
		return errNoCLI()
	}
	return cli.StackUpStreamed(ctx, name, session)
}

func errNoCLI() error {
	return errors.New("docker compose plugin not available on this host — install the Docker Compose CLI plugin (verify with 'docker compose version') to manage stacks")
}

// ExternalStackFiles returns the compose YAML of a project Dockpal does not
// manage (its files live outside composeBaseDir()). The file(s) recorded by
// `compose ls` are read directly and concatenated when compose used several
// -f files — the same multi-file view Dockge renders. Direct reads keep this
// working even when the panel's filesystem view differs from the deployer's
// (a hardened service user that may not traverse the project's directory,
// e.g. files owned by root under /opt). If no file is readable, it falls
// back to `compose -f … config`, which asks the CLI to render the merged
// config instead. The result is always treated read-only by callers.
func ExternalStackFiles(ctx context.Context, configFiles string) (string, error) {
	if configFiles == "" {
		return "", errors.New("no compose file recorded for this project")
	}
	var files []string
	for _, f := range strings.Split(configFiles, ",") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return "", errors.New("no compose file recorded for this project")
	}
	var sb strings.Builder
	readAny := false
	for i, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if i > 0 && readAny {
			sb.WriteString("\n# --- " + f + " ---\n")
		}
		sb.Write(b)
		readAny = true
	}
	if readAny {
		return sb.String(), nil
	}
	if cli == nil {
		return "", errNoCLI()
	}
	args := make([]string, 0, len(files)*2+1)
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "config")
	// No working dir: the file paths are absolute already, and the project
	// directory may not be traversable by the panel user (chdir would fail
	// with EACCES before compose even runs — the original adminer case).
	return cli.Output(ctx, "", args...)
}

// ValidateStackName enforces Dockge-style lowercase stack names.
func ValidateStackName(name string) error {
	if !stackNameRegex.MatchString(name) {
		return errors.New("stack name must be lowercase [a-z0-9_-]")
	}
	return nil
}

// StackDir resolves (and validates) the stack directory path.
func StackDir(name string) (string, error) {
	if err := ValidateStackName(name); err != nil {
		return "", err
	}
	return composeProjectDir(name)
}

// composeFilePathIn returns the compose file present in dir, preferring
// acceptedComposeFileNames order. "" if none exists.
func composeFilePathIn(dir string) string {
	for _, fn := range acceptedComposeFileNames {
		p := filepath.Join(dir, fn)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// validateComposeYAML parses the YAML and ensures `services` (if present) is a mapping.
func validateComposeYAML(content string) error {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return fmt.Errorf("invalid compose YAML: %w", err)
	}
	if doc == nil {
		return errors.New("compose YAML is empty")
	}
	if svc, ok := doc["services"]; ok && svc != nil {
		if _, isMap := svc.(map[string]any); !isMap {
			return errors.New("services must be an object")
		}
	}
	return nil
}

// validateEnvFile sanity-checks .env content: every non-empty,
// non-comment line must contain '='.
func validateEnvFile(content string) error {
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			return fmt.Errorf("invalid .env format at line %d", lineNo)
		}
	}
	return nil
}

// writeFileAtomic writes content to path via a temp file + rename.
func writeFileAtomic(path string, content string, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// SaveStack validates and persists a stack's compose file and .env.
// isAdd=true requires the directory to not exist yet; isAdd=false requires it to exist.
// The compose file is always written as ComposeFileName ("compose.yaml").
func SaveStack(name, composeYAML, composeENV string, isAdd bool) error {
	if err := ValidateStackName(name); err != nil {
		return err
	}
	if err := validateComposeYAML(composeYAML); err != nil {
		return err
	}
	if err := validateEnvFile(composeENV); err != nil {
		return err
	}

	dir, err := StackDir(name)
	if err != nil {
		return err
	}

	_, statErr := os.Stat(dir)
	if isAdd && statErr == nil {
		return fmt.Errorf("stack %q already exists", name)
	}
	if !isAdd && statErr != nil {
		return fmt.Errorf("stack %q not found", name)
	}
	if isAdd {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create stack directory: %w", err)
		}
	}

	if err := writeFileAtomic(filepath.Join(dir, ComposeFileName), composeYAML, 0644); err != nil {
		return fmt.Errorf("write compose file: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, ".env"), composeENV, 0644); err != nil {
		return fmt.Errorf("write .env file: %w", err)
	}

	// PUID/PGID ownership parity with Dockge save().
	if puid, pgid := os.Getenv("PUID"), os.Getenv("PGID"); puid != "" && pgid != "" {
		var uid, gid int
		if _, err := fmt.Sscanf(puid, "%d", &uid); err == nil {
			if _, err := fmt.Sscanf(pgid, "%d", &gid); err == nil {
				_ = os.Chown(dir, uid, gid)
				_ = os.Chown(filepath.Join(dir, ComposeFileName), uid, gid)
			}
		}
	}
	return nil
}

// GetStack loads one stack from disk (compose + .env). Returns
// Managed=false with empty content when the directory does not exist
// (unmanaged stack known only to docker).
func GetStack(name string) (*Stack, error) {
	dir, err := StackDir(name)
	if err != nil {
		return nil, err
	}
	s := &Stack{Name: name, Status: StackStatusUnknown}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return s, nil
	}
	s.Managed = true

	if cf := composeFilePathIn(dir); cf != "" {
		if b, err := os.ReadFile(cf); err == nil {
			s.ComposeYAML = string(b)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".env")); err == nil {
		s.ComposeENV = string(b)
	}
	return s, nil
}

// --- docker compose ls / ps JSON shapes ---

type composeLsEntry struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

// GlobalEnvPath returns the global.env file path (shared by all stacks).
func GlobalEnvPath() string {
	return filepath.Join(composeBaseDir(), "global.env")
}

// GetGlobalEnv reads global.env content ("" when absent).
func GetGlobalEnv() (string, error) {
	b, err := os.ReadFile(GlobalEnvPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// SetGlobalEnv validates and writes global.env (creates the base dir).
func SetGlobalEnv(content string) error {
	if err := validateEnvFile(content); err != nil {
		return err
	}
	if err := os.MkdirAll(composeBaseDir(), 0755); err != nil {
		return err
	}
	return writeFileAtomic(GlobalEnvPath(), content, 0644)
}

// StackComposeArgs prepends `--env-file` flags to compose args, mirroring
// Dockge's getComposeOptions: only when global.env exists, yielding
// `compose --env-file ./.env --env-file ../global.env <args...>`.
func StackComposeArgs(dir string, args ...string) []string {
	if _, err := os.Stat(GlobalEnvPath()); err != nil {
		return args
	}
	out := make([]string, 0, len(args)+4)
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		out = append(out, "--env-file", "./.env")
	}
	out = append(out, "--env-file", "../global.env")
	out = append(out, args...)
	return out
}

type composePsEntry struct {
	Service string `json:"Service"`
	Name    string `json:"Name"`
	Image   string `json:"Image"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// StatusConvert maps `docker compose ls` status text to a stack status.
// Examples: "running(2)", "exited(1), running(1)", "created(1)".
// The text lists one state per service in arbitrary order, so detection is
// order-independent: a single uniform state maps directly, any mixture is
// "partial" (except any "exited", which is the actionable failure signal).
func StatusConvert(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return StackStatusUnknown
	}
	var states []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Strip the count suffix: "running(2)" → "running".
		if idx := strings.Index(part, "("); idx >= 0 {
			part = strings.TrimSpace(part[:idx])
		}
		states = append(states, part)
	}
	if len(states) == 0 {
		return StackStatusUnknown
	}
	seen := map[string]bool{}
	for _, st := range states {
		seen[st] = true
	}
	switch {
	case seen["exited"] || seen["dead"]:
		return StackStatusExited
	case len(seen) == 1 && seen["running"]:
		return StackStatusRunning
	case len(seen) == 1 && (seen["created"] || seen["paused"] || seen["restarting"]):
		return StackStatusPartial
	case seen["running"] || seen["created"]:
		// Mixed states (e.g. created+running) — order-independent partial.
		return StackStatusPartial
	default:
		return StackStatusUnknown
	}
}

// parseNDJSON decodes line-delimited JSON; also tolerates a JSON array
// (older compose versions sometimes wrap output in one).
func parseNDJSON[T any](out string) ([]T, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	if strings.HasPrefix(out, "[") {
		var arr []T
		if err := json.Unmarshal([]byte(out), &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var items []T
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("parse %q: %w", line, err)
		}
		items = append(items, item)
	}
	return items, nil
}

// composeLsCache memoizes `docker compose ls` briefly: list/detail/action
// handlers all call ListComposeProjects, and each uncached call spawns a
// subprocess (audit-stack-container L13). 2s is short enough that a
// container crashing mid-request still shows up on the next poll.
var composeLsCache struct {
	mu      sync.Mutex
	expires time.Time
	entries []composeLsEntry
}

const composeLsCacheTTL = 2 * time.Second

// InvalidateComposeLsCache drops the cached project list — called after any
// stack mutation so the next read reflects the mutation immediately.
func InvalidateComposeLsCache() {
	composeLsCache.mu.Lock()
	composeLsCache.expires = time.Time{}
	composeLsCache.entries = nil
	composeLsCache.mu.Unlock()
}

// ListComposeProjects runs `docker compose ls --all --format json`.
func ListComposeProjects(ctx context.Context) ([]composeLsEntry, error) {
	if cli == nil {
		return nil, errNoCLI()
	}
	composeLsCache.mu.Lock()
	if time.Now().Before(composeLsCache.expires) {
		entries := composeLsCache.entries
		composeLsCache.mu.Unlock()
		return entries, nil
	}
	composeLsCache.mu.Unlock()

	out, err := cli.Output(ctx, "", "ls", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	entries, err := parseNDJSON[composeLsEntry](out)
	if err != nil {
		return nil, err
	}
	composeLsCache.mu.Lock()
	composeLsCache.entries = entries
	composeLsCache.expires = time.Now().Add(composeLsCacheTTL)
	composeLsCache.mu.Unlock()
	return entries, nil
}

// StackServices runs `docker compose ps --format json` inside the stack dir.
func StackServices(ctx context.Context, name string) ([]StackService, error) {
	if cli == nil {
		return nil, errNoCLI()
	}
	dir, err := StackDir(name)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		return nil, nil // no dir → no services
	}
	out, err := cli.Output(ctx, dir, "ps", "--format", "json")
	if err != nil {
		return nil, err
	}
	entries, err := parseNDJSON[composePsEntry](out)
	if err != nil {
		return nil, err
	}
	services := make([]StackService, 0, len(entries))
	for _, e := range entries {
		services = append(services, StackService{
			Name:          e.Service,
			ContainerName: e.Name,
			Image:         e.Image,
			State:         e.State,
			Health:        e.Health,
		})
	}
	return services, nil
}

// ListStacks merges the on-disk stack directories with `docker compose ls`.
func ListStacks(ctx context.Context) ([]*Stack, error) {
	if cli == nil {
		return nil, errNoCLI()
	}
	stacks := map[string]*Stack{}
	order := []string{}

	// 1. Filesystem scan — draft stacks (Dockge: CREATED_FILE).
	base := composeBaseDir()
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(base, e.Name())
			if composeFilePathIn(dir) == "" {
				continue
			}
			st := &Stack{Name: e.Name(), Status: StackStatusDraft, StatusText: "draft", Managed: true}
			stacks[e.Name()] = st
			order = append(order, e.Name())
		}
	}

	// 2. docker compose ls — authoritative status + unmanaged stacks.
	projects, err := ListComposeProjects(ctx)
	if err != nil {
		// CLI hiccup: still return the filesystem view.
		if len(stacks) > 0 {
			return orderedStacks(stacks, order), nil
		}
		return nil, err
	}
	for _, p := range projects {
		st, ok := stacks[p.Name]
		if !ok {
			st = &Stack{Name: p.Name}
			stacks[p.Name] = st
			order = append(order, p.Name)
		}
		st.Status = StatusConvert(p.Status)
		st.StatusText = p.Status
		// Managed if the config file lives under our base dir.
		if p.ConfigFiles != "" && strings.HasPrefix(filepath.Clean(p.ConfigFiles), filepath.Clean(base)+string(os.PathSeparator)) {
			st.Managed = true
		}
	}
	return orderedStacks(stacks, order), nil
}

func orderedStacks(m map[string]*Stack, order []string) []*Stack {
	out := make([]*Stack, 0, len(m))
	for _, name := range order {
		out = append(out, m[name])
	}
	return out
}

// GetStackFull returns the stack with runtime status and services filled in.
//
// A stack whose directory does not exist under composeBaseDir() is not
// automatically "not found": `docker compose ls` may still know it as an
// external project (created outside Dockpal — e.g. deployed by hand on the
// host, then discovered). Those come back with Managed=false and whatever
// compose file content the CLI reports, so the UI can show a read-only view
// instead of a 404 that contradicts the Stacks list.
func GetStackFull(ctx context.Context, name string) (*Stack, error) {
	s, err := GetStack(name)
	if err != nil {
		return nil, err
	}
	if cli != nil {
		if projects, err := ListComposeProjects(ctx); err == nil {
			for _, p := range projects {
				if p.Name != name {
					continue
				}
				s.Status = StatusConvert(p.Status)
				s.StatusText = p.Status
				if !s.Managed {
					// External project: surface its compose file read-only.
					if content, err := ExternalStackFiles(ctx, p.ConfigFiles); err == nil {
						s.ComposeYAML = content
					} else {
						log.Printf("stacks: external %q compose file unavailable: %v", name, err)
					}
				}
				break
			}
		}
		if !s.Managed && s.Status == StackStatusUnknown {
			// Not on disk and not known to docker → genuinely absent.
			return nil, fmt.Errorf("stack %q not found", name)
		}
		if s.Status == StackStatusUnknown {
			// On disk but not known to docker → draft (Dockge CREATED_FILE).
			s.Status = StackStatusDraft
			s.StatusText = "draft"
		}
		if svcs, err := StackServices(ctx, name); err == nil {
			s.Services = svcs
		}
	}
	// No CLI at all → still report draft for on-disk stacks.
	if s.Status == StackStatusUnknown {
		s.Status = StackStatusDraft
		s.StatusText = "draft"
	}
	return s, nil
}

// --- Lifecycle operations (Dockge command parity) ---

func stackRun(ctx context.Context, name string, args ...string) error {
	if cli == nil {
		return errNoCLI()
	}
	dir, err := StackDir(name)
	if err != nil {
		return err
	}
	if !cli.TryLock(name) {
		return errors.New("another operation is already running for this stack")
	}
	defer cli.Unlock(name)
	args = StackComposeArgs(dir, args...)
	if err := cli.Run(ctx, dir, args...); err != nil {
		return err
	}
	// State changed → next list must re-probe (L13 cache).
	InvalidateComposeLsCache()
	return nil
}

// StackUp: docker compose up -d --remove-orphans
func StackUp(ctx context.Context, name string) error {
	return stackRun(ctx, name, "up", "-d", "--remove-orphans")
}

// StackStop: docker compose stop
func StackStop(ctx context.Context, name string) error {
	return stackRun(ctx, name, "stop")
}

// StackRestart: docker compose restart
func StackRestart(ctx context.Context, name string) error {
	return stackRun(ctx, name, "restart")
}

// StackRecreate: docker compose up -d --remove-orphans --force-recreate
//
// Unlike restart, this re-reads the compose file and recreates containers so
// config changes that live in the container spec (network_mode, ports,
// volumes, cap_add, ...) actually take effect — restart alone keeps the old
// container config (issue #29).
func StackRecreate(ctx context.Context, name string) error {
	return stackRun(ctx, name, "up", "-d", "--remove-orphans", "--force-recreate")
}

// StackDown: docker compose down
func StackDown(ctx context.Context, name string) error {
	return stackRun(ctx, name, "down")
}

// StackUpdate: docker compose pull, then up -d --remove-orphans if the
// stack is currently running (Dockge update() semantics).
func StackUpdate(ctx context.Context, name string) error {
	if cli == nil {
		return errNoCLI()
	}
	dir, err := StackDir(name)
	if err != nil {
		return err
	}
	if !cli.TryLock(name) {
		return errors.New("another operation is already running for this stack")
	}
	defer cli.Unlock(name)
	if err := cli.Run(ctx, dir, StackComposeArgs(dir, "pull")...); err != nil {
		return err
	}
	running := false
	if projects, err := ListComposeProjects(ctx); err == nil {
		for _, p := range projects {
			if p.Name == name && StatusConvert(p.Status) == StackStatusRunning {
				running = true
				break
			}
		}
	}
	if running {
		return cli.Run(ctx, dir, StackComposeArgs(dir, "up", "-d", "--remove-orphans")...)
	}
	return nil
}

// StackDelete: docker compose down --remove-orphans, then remove the directory.
func StackDelete(ctx context.Context, name string) error {
	if cli == nil {
		return errNoCLI()
	}
	dir, err := StackDir(name)
	if err != nil {
		return err
	}
	if !cli.TryLock(name) {
		return errors.New("another operation is already running for this stack")
	}
	defer cli.Unlock(name)
	if _, statErr := os.Stat(dir); statErr == nil {
		_ = cli.Run(ctx, dir, StackComposeArgs(dir, "down", "--remove-orphans")...) // best effort
	}
	err = os.RemoveAll(dir)
	InvalidateComposeLsCache()
	return err
}

// StackServiceUp: docker compose up -d <service>
func StackServiceUp(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, "up", "-d", service)
}

// StackServiceStop: docker compose stop <service>
func StackServiceStop(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, "stop", service)
}

// StackServiceRestart: docker compose restart <service>
func StackServiceRestart(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, "restart", service)
}

// StackServiceRecreate: docker compose up -d --force-recreate <service>
// Recreates a single service's container so compose config changes for that
// service take effect (issue #29).
func StackServiceRecreate(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, "up", "-d", "--force-recreate", service)
}

// ListDockerNetworks returns host docker network names (sorted, without the
// built-in none/host/bridge), for the network editor dropdown.
func ListDockerNetworks(ctx context.Context) ([]string, error) {
	if cli == nil {
		return nil, errNoCLI()
	}
	out, err := cli.Output(ctx, "", "network", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{"none": true, "host": true, "bridge": true}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || skip[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
