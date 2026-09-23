package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStackCLI implements the stackCLI seam for tests.
type fakeStackCLI struct {
	runs   [][]string // recorded Run args
	runErr error

	lsOut  string
	lsErr  error
	psOut  string
	psErr  error
	locked map[string]bool
}

func (f *fakeStackCLI) Run(ctx context.Context, dir string, args ...string) error {
	f.runs = append(f.runs, append([]string{dir}, args...))
	return f.runErr
}

func (f *fakeStackCLI) Output(ctx context.Context, dir string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	if strings.HasPrefix(joined, "ls") {
		return f.lsOut, f.lsErr
	}
	if strings.HasPrefix(joined, "ps") {
		return f.psOut, f.psErr
	}
	return "", fmt.Errorf("unexpected args: %s", joined)
}

func (f *fakeStackCLI) TryLock(name string) bool {
	if f.locked == nil {
		f.locked = map[string]bool{}
	}
	if f.locked[name] {
		return false
	}
	f.locked[name] = true
	return true
}

func (f *fakeStackCLI) Unlock(name string) { delete(f.locked, name) }

func withFakeCLI(t *testing.T, f *fakeStackCLI) {
	t.Helper()
	prev := cli
	cli = f
	t.Cleanup(func() { cli = prev })
}

func withTempComposeBase(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKPAL_DATA_DIR", dataDir)
	return root
}

const testCompose = `services:
  web:
    image: nginx:latest
    restart: unless-stopped
    ports:
      - "8080:80"
`

func TestValidateStackName(t *testing.T) {
	valid := []string{"nginx", "my-stack", "stack_1", "a1"}
	for _, n := range valid {
		if err := ValidateStackName(n); err != nil {
			t.Errorf("expected %q valid, got %v", n, err)
		}
	}
	invalid := []string{"", "Nginx", "my stack", "../etc", "a/b", "stack!"}
	for _, n := range invalid {
		if err := ValidateStackName(n); err == nil {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestStatusConvert(t *testing.T) {
	cases := map[string]string{
		"running(2)":             StackStatusRunning,
		"Running(1)":             StackStatusRunning,
		"exited(1)":              StackStatusExited,
		"exited(1), running(1)":  StackStatusExited,
		"created(1)":             StackStatusPartial,
		"paused(1)":              StackStatusUnknown,
		"":                       StackStatusUnknown,
	}
	for in, want := range cases {
		if got := StatusConvert(in); got != want {
			t.Errorf("StatusConvert(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSaveAndGetStack(t *testing.T) {
	withTempComposeBase(t)

	env := "IMAGE_TAG=latest\n# comment\n"
	if err := SaveStack("demo", testCompose, env, true); err != nil {
		t.Fatalf("SaveStack(isAdd): %v", err)
	}

	// isAdd on existing dir must fail.
	if err := SaveStack("demo", testCompose, env, true); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}

	s, err := GetStack("demo")
	if err != nil {
		t.Fatalf("GetStack: %v", err)
	}
	if !s.Managed || s.Name != "demo" {
		t.Errorf("unexpected stack: %+v", s)
	}
	if s.ComposeYAML != testCompose {
		t.Errorf("compose mismatch:\n%s", s.ComposeYAML)
	}
	if s.ComposeENV != env {
		t.Errorf("env mismatch: %q", s.ComposeENV)
	}

	// Files written with expected names.
	dir, _ := StackDir("demo")
	for _, fn := range []string{"compose.yaml", ".env"} {
		if _, err := os.Stat(filepath.Join(dir, fn)); err != nil {
			t.Errorf("expected %s to exist: %v", fn, err)
		}
	}

	// Update path (isAdd=false).
	newCompose := testCompose + "\n# updated\n"
	if err := SaveStack("demo", newCompose, "", false); err != nil {
		t.Fatalf("SaveStack(update): %v", err)
	}
	s, _ = GetStack("demo")
	if !strings.Contains(s.ComposeYAML, "# updated") {
		t.Errorf("update not persisted")
	}

	// Update on missing stack must fail.
	if err := SaveStack("ghost", testCompose, "", false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestSaveStackValidation(t *testing.T) {
	withTempComposeBase(t)

	if err := SaveStack("BadName", testCompose, "", true); err == nil {
		t.Error("expected name validation error")
	}
	if err := SaveStack("ok", "services: [not-an-object]", "", true); err == nil || !strings.Contains(err.Error(), "services must be an object") {
		t.Errorf("expected services-object error, got %v", err)
	}
	if err := SaveStack("ok2", "services:\n\tbad-tab:", "", true); err == nil {
		t.Error("expected YAML parse error")
	}
	if err := SaveStack("ok3", testCompose, "JUST_A_LINE\n", true); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Errorf("expected .env format error, got %v", err)
	}
}

func TestGetStackAcceptsLegacyFileNames(t *testing.T) {
	withTempComposeBase(t)
	dir, err := StackDir("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(testCompose), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := GetStack("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if s.ComposeYAML != testCompose {
		t.Errorf("legacy compose file not read")
	}
}

func TestListStacksMergesDraftAndLive(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("drafty", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStackCLI{
		lsOut: `{"Name":"drafty","Status":"running(1)","ConfigFiles":""}` + "\n" +
			`{"Name":"external","Status":"exited(2)","ConfigFiles":"/other/place/docker-compose.yml"}`,
	}
	withFakeCLI(t, fake)

	stacks, err := ListStacks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Stack{}
	for _, s := range stacks {
		byName[s.Name] = s
	}
	if len(byName) != 2 {
		t.Fatalf("expected 2 stacks, got %d", len(byName))
	}
	if byName["drafty"].Status != StackStatusRunning || !byName["drafty"].Managed {
		t.Errorf("drafty: %+v", byName["drafty"])
	}
	if byName["external"].Status != StackStatusExited || byName["external"].Managed {
		t.Errorf("external should be exited+unmanaged: %+v", byName["external"])
	}
}

func TestListStacksWithoutComposeCLI(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("drafty", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStackCLI{lsErr: errors.New("compose not available")}
	withFakeCLI(t, fake)

	// Filesystem view still returned.
	stacks, err := ListStacks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stacks) != 1 || stacks[0].Status != StackStatusDraft {
		t.Fatalf("unexpected: %+v", stacks)
	}
}

func TestParseNDJSON(t *testing.T) {
	nd := "{\"a\":1}\n{\"a\":2}\n"
	type item struct {
		A int `json:"a"`
	}
	items, err := parseNDJSON[item](nd)
	if err != nil || len(items) != 2 || items[1].A != 2 {
		t.Fatalf("ndjson: %v %+v", err, items)
	}
	arr, err := parseNDJSON[item](`[{"a":1},{"a":2}]`)
	if err != nil || len(arr) != 2 {
		t.Fatalf("array: %v %+v", err, arr)
	}
	empty, err := parseNDJSON[item]("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %+v", err, empty)
	}
}

func TestLifecycleCommands(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("demo", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStackCLI{lsOut: `{"Name":"demo","Status":"running(1)","ConfigFiles":""}`}
	withFakeCLI(t, fake)
	ctx := context.Background()

	if err := StackUp(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := StackStop(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := StackRestart(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := StackDown(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := StackServiceUp(ctx, "demo", "web"); err != nil {
		t.Fatal(err)
	}
	if err := StackServiceStop(ctx, "demo", "web"); err != nil {
		t.Fatal(err)
	}
	if err := StackServiceRestart(ctx, "demo", "web"); err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"up", "-d", "--remove-orphans"},
		{"stop"},
		{"restart"},
		{"down"},
		{"up", "-d", "web"},
		{"stop", "web"},
		{"restart", "web"},
	}
	if len(fake.runs) != len(want) {
		t.Fatalf("runs: %v", fake.runs)
	}
	for i, w := range want {
		got := fake.runs[i][1:] // strip dir
		if strings.Join(got, " ") != strings.Join(w, " ") {
			t.Errorf("run %d: got %v want %v", i, got, w)
		}
		dir := fake.runs[i][0]
		if !strings.HasSuffix(dir, filepath.Join("compose", "demo")) {
			t.Errorf("run %d: unexpected dir %s", i, dir)
		}
	}
}

func TestStackUpdateOnlyUpsWhenRunning(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("demo", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Running stack → pull + up.
	fake := &fakeStackCLI{lsOut: `{"Name":"demo","Status":"running(1)","ConfigFiles":""}`}
	withFakeCLI(t, fake)
	if err := StackUpdate(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if len(fake.runs) != 2 || strings.Join(fake.runs[0][1:], " ") != "pull" ||
		strings.Join(fake.runs[1][1:], " ") != "up -d --remove-orphans" {
		t.Fatalf("running update runs: %v", fake.runs)
	}

	// Stopped stack → pull only.
	fake2 := &fakeStackCLI{lsOut: `{"Name":"demo","Status":"exited(1)","ConfigFiles":""}`}
	withFakeCLI(t, fake2)
	if err := StackUpdate(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if len(fake2.runs) != 1 || strings.Join(fake2.runs[0][1:], " ") != "pull" {
		t.Fatalf("stopped update runs: %v", fake2.runs)
	}
}

func TestStackDeleteRemovesDir(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("demo", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStackCLI{}
	withFakeCLI(t, fake)
	if err := StackDelete(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	dir, _ := StackDir("demo")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir should be removed, stat err: %v", err)
	}
	// down --remove-orphans attempted first.
	if len(fake.runs) != 1 || strings.Join(fake.runs[0][1:], " ") != "down --remove-orphans" {
		t.Fatalf("runs: %v", fake.runs)
	}
}

func TestStackLockRejectsConcurrent(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("demo", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStackCLI{locked: map[string]bool{"demo": true}}
	withFakeCLI(t, fake)
	err := StackUp(context.Background(), "demo")
	if err == nil || !strings.Contains(err.Error(), "another operation") {
		t.Fatalf("expected busy error, got %v", err)
	}
}

func TestStackServices(t *testing.T) {
	withTempComposeBase(t)
	if err := SaveStack("demo", testCompose, "", true); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStackCLI{
		psOut: `{"Service":"web","Name":"demo-web-1","Image":"nginx:latest","State":"running","Health":"healthy"}`,
	}
	withFakeCLI(t, fake)
	svcs, err := StackServices(context.Background(), "demo")
	if err != nil || len(svcs) != 1 {
		t.Fatalf("%v %+v", err, svcs)
	}
	s := svcs[0]
	if s.Name != "web" || s.ContainerName != "demo-web-1" || s.Health != "healthy" {
		t.Errorf("unexpected service: %+v", s)
	}
}
