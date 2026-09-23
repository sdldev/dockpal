package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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

// acceptedComposeFileNames in read preference order (Dockge parity).
var acceptedComposeFileNames = []string{
	"compose.yaml",
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
}

var cli stackCLI

// RegisterStackCLI wires the compose CLI backend. Called once at startup.
func RegisterStackCLI(c stackCLI) { cli = c }

func errNoCLI() error {
	return errors.New("docker compose CLI integration not initialized")
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
// The compose file is always written as "compose.yaml".
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

	if err := writeFileAtomic(filepath.Join(dir, "compose.yaml"), composeYAML, 0644); err != nil {
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
				_ = os.Chown(filepath.Join(dir, "compose.yaml"), uid, gid)
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
func StatusConvert(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.HasPrefix(s, "created"):
		return StackStatusPartial
	case strings.Contains(s, "exited"):
		return StackStatusExited
	case strings.HasPrefix(s, "running"):
		return StackStatusRunning
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

// ListComposeProjects runs `docker compose ls --all --format json`.
func ListComposeProjects(ctx context.Context) ([]composeLsEntry, error) {
	if cli == nil {
		return nil, errNoCLI()
	}
	out, err := cli.Output(ctx, "", "ls", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseNDJSON[composeLsEntry](out)
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
func GetStackFull(ctx context.Context, name string) (*Stack, error) {
	s, err := GetStack(name)
	if err != nil {
		return nil, err
	}
	if !s.Managed {
		return nil, fmt.Errorf("stack %q not found", name)
	}
	if cli != nil {
		if projects, err := ListComposeProjects(ctx); err == nil {
			for _, p := range projects {
				if p.Name == name {
					s.Status = StatusConvert(p.Status)
					s.StatusText = p.Status
					break
				}
			}
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

func stackRun(ctx context.Context, name string, emit func(string), args ...string) error {
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
	return nil
}

// StackUp: docker compose up -d --remove-orphans
func StackUp(ctx context.Context, name string) error {
	return stackRun(ctx, name, nil, "up", "-d", "--remove-orphans")
}

// StackStop: docker compose stop
func StackStop(ctx context.Context, name string) error {
	return stackRun(ctx, name, nil, "stop")
}

// StackRestart: docker compose restart
func StackRestart(ctx context.Context, name string) error {
	return stackRun(ctx, name, nil, "restart")
}

// StackDown: docker compose down
func StackDown(ctx context.Context, name string) error {
	return stackRun(ctx, name, nil, "down")
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
	return os.RemoveAll(dir)
}

// StackServiceUp: docker compose up -d <service>
func StackServiceUp(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, nil, "up", "-d", service)
}

// StackServiceStop: docker compose stop <service>
func StackServiceStop(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, nil, "stop", service)
}

// StackServiceRestart: docker compose restart <service>
func StackServiceRestart(ctx context.Context, name, service string) error {
	return stackRun(ctx, name, nil, "restart", service)
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
