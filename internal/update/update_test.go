package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.0.0", "2.1.0", -1},
		{"2.1.0", "2.0.0", 1},
		{"2.0.0", "2.0.0", 0},
		{"v2.0.0", "2.0.0", 0},
		{"2.0", "2.0.0", 0},
		{"2.10.0", "2.9.9", 1},
		{"1.0.0", "10.0.0", -1},
		{"3.0.0-rc1", "3.0.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	if got := normalizeVersion("v2.1.0"); got != "2.1.0" {
		t.Errorf("got %q", got)
	}
	if got := normalizeVersion("Dockpal v2.1.0"); got != "2.1.0" {
		t.Errorf("got %q", got)
	}
}

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func newReleaseServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     tag,
			"body":         "release notes",
			"html_url":     "https://example.test/release/" + tag,
			"published_at": time.Now().UTC().Format(time.RFC3339),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckerFetchAndCache(t *testing.T) {
	srv := newReleaseServer(t, "v2.5.0")
	database := openTestDB(t)
	c := NewChecker(database, "sdldev/dockpal", srv.URL, 0)

	c.CheckNow(context.Background())
	latest := c.Latest()
	if latest == nil {
		t.Fatal("expected latest to be populated")
	}
	if latest.Version != "2.5.0" {
		t.Errorf("version = %q want 2.5.0", latest.Version)
	}
	if latest.ChangelogURL == "" {
		t.Error("expected changelog url")
	}

	// Persisted cache should reload into a fresh checker.
	c2 := NewChecker(database, "sdldev/dockpal", srv.URL, 0)
	c2.Start(context.Background())
	defer c2.Stop()
	if got := c2.Latest(); got == nil || got.Version != "2.5.0" {
		t.Errorf("persisted cache not reloaded: %+v", got)
	}
}

func TestCheckerKeepsCacheOnFailure(t *testing.T) {
	database := openTestDB(t)
	good := newReleaseServer(t, "v2.5.0")
	c := NewChecker(database, "sdldev/dockpal", good.URL, 0)
	c.CheckNow(context.Background())

	// Point at a dead server; the cache must survive.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	c.apiBase = bad.URL
	c.CheckNow(context.Background())
	if got := c.Latest(); got == nil || got.Version != "2.5.0" {
		t.Errorf("cache cleared on failure: %+v", got)
	}
}

func TestManagerRequestUpdateValidatesTarget(t *testing.T) {
	srv := newReleaseServer(t, "v2.5.0")
	database := openTestDB(t)
	checker := NewChecker(database, "sdldev/dockpal", srv.URL, 0)
	checker.CheckNow(context.Background())

	dir := t.TempDir()
	m := NewManager(database, checker, dir, "2.0.0")

	// Unknown target (not the cached latest) must be rejected.
	if _, err := m.RequestUpdate("9.9.9", "admin"); err == nil {
		t.Error("expected error for unknown target")
	}
	// Older/equal target rejected.
	if _, err := m.RequestUpdate("2.0.0", "admin"); err == nil {
		t.Error("expected error for non-newer target")
	}

	// Valid target accepted, trigger file written.
	st, err := m.RequestUpdate("v2.5.0", "admin")
	if err != nil {
		t.Fatalf("RequestUpdate: %v", err)
	}
	if st.Status != StateRequested || st.Target != "2.5.0" {
		t.Errorf("unexpected state %+v", st)
	}
	data, err := os.ReadFile(filepath.Join(dir, TriggerFileName))
	if err != nil {
		t.Fatalf("trigger file not written: %v", err)
	}
	var payload triggerPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("trigger file not json: %v", err)
	}
	if payload.Version != "2.5.0" {
		t.Errorf("trigger version = %q", payload.Version)
	}

	// Second request while in flight must fail.
	if _, err := m.RequestUpdate("2.5.0", "admin"); err != ErrUpdateInFlight {
		t.Errorf("expected ErrUpdateInFlight, got %v", err)
	}
}

func TestManagerTriggerDirRedirect(t *testing.T) {
	srv := newReleaseServer(t, "v2.5.0")
	database := openTestDB(t)
	checker := NewChecker(database, "sdldev/dockpal", srv.URL, 0)
	checker.CheckNow(context.Background())

	dataDir := t.TempDir()
	triggerDir := t.TempDir()
	t.Setenv(EnvTriggerDir, triggerDir)
	m := NewManager(database, checker, dataDir, "2.0.0")

	if _, err := m.RequestUpdate("v2.5.0", "admin"); err != nil {
		t.Fatalf("RequestUpdate: %v", err)
	}
	// The trigger must land in the redirected dir, not the data dir.
	if _, err := os.Stat(filepath.Join(triggerDir, TriggerFileName)); err != nil {
		t.Errorf("trigger not in trigger dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, TriggerFileName)); err == nil {
		t.Error("trigger leaked into the data dir despite the redirect")
	}

	// The result file must also be read from the redirected dir.
	res := resultPayload{Status: "done", Target: "2.5.0", ExitCode: 0, Message: "updated", FinishedAt: time.Now().Unix()}
	data, _ := json.Marshal(res)
	if err := os.WriteFile(filepath.Join(triggerDir, ResultFileName), data, 0644); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(database, checker, dataDir, "2.0.0") // env still set
	m2.FinalizeOnBoot()
	status, err := m2.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateDone {
		t.Errorf("state = %q want done (result read from trigger dir)", status.State)
	}
	// Consumed result file is removed from the redirected dir.
	if _, err := os.Stat(filepath.Join(triggerDir, ResultFileName)); !os.IsNotExist(err) {
		t.Errorf("result file not consumed from trigger dir: %v", err)
	}
}

func TestManagerReconcileSuccessOnBoot(t *testing.T) {
	srv := newReleaseServer(t, "v2.5.0")
	database := openTestDB(t)
	checker := NewChecker(database, "sdldev/dockpal", srv.URL, 0)
	checker.CheckNow(context.Background())
	dir := t.TempDir()

	// Request at old version.
	m := NewManager(database, checker, dir, "2.0.0")
	if _, err := m.RequestUpdate("2.5.0", "admin"); err != nil {
		t.Fatalf("RequestUpdate: %v", err)
	}

	// Simulate post-restart: new Manager now running the target version.
	m2 := NewManager(database, checker, dir, "2.5.0")
	m2.FinalizeOnBoot()
	status, err := m2.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateDone {
		t.Errorf("state = %q want done", status.State)
	}
}

func TestManagerReconcileFailureResult(t *testing.T) {
	srv := newReleaseServer(t, "v2.5.0")
	database := openTestDB(t)
	checker := NewChecker(database, "sdldev/dockpal", srv.URL, 0)
	checker.CheckNow(context.Background())
	dir := t.TempDir()

	m := NewManager(database, checker, dir, "2.0.0")
	if _, err := m.RequestUpdate("2.5.0", "admin"); err != nil {
		t.Fatalf("RequestUpdate: %v", err)
	}

	// Simulate a failed update.sh run that rolled back (still old version).
	res := resultPayload{Status: "failed", Target: "2.5.0", ExitCode: 6, Message: "health check failed", FinishedAt: time.Now().Unix()}
	data, _ := json.Marshal(res)
	if err := os.WriteFile(filepath.Join(dir, ResultFileName), data, 0644); err != nil {
		t.Fatal(err)
	}

	m2 := NewManager(database, checker, dir, "2.0.0")
	status, err := m2.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateFailed {
		t.Errorf("state = %q want failed", status.State)
	}
	if status.StateDetail.Message == "" {
		t.Error("expected failure message")
	}
}

func TestStatusResponseDefaults(t *testing.T) {
	database := openTestDB(t)
	checker := NewChecker(database, "sdldev/dockpal", "http://127.0.0.1:0", 0)
	m := NewManager(database, checker, t.TempDir(), "2.0.0")
	status, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.CurrentVersion != "2.0.0" {
		t.Errorf("current = %q", status.CurrentVersion)
	}
	if status.State != StateNone {
		t.Errorf("state = %q want none", status.State)
	}
	if status.UpdateAvailable {
		t.Error("no cache -> no update available")
	}
}
