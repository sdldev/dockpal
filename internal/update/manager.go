package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

// State machine for a UI-requested system update. The flow is:
//
//	none --RequestUpdate--> requested --(helper runs update.sh)--> done | failed
//
// "running" is written by nothing in-process (the process is replaced
// mid-update); it is derived at read time when a request is in flight and the
// result file has not landed yet. The boot-time finalizer reconciles a state
// left over from the previous process incarnation.
const (
	StateNone      = "none"
	StateRequested = "requested"
	StateRunning   = "running"
	StateDone      = "done"
	StateFailed    = "failed"
)

// settingsKeyUpdateState holds the persisted State JSON.
const settingsKeyUpdateState = "system_update_state"

// TriggerFileName and ResultFileName live under the data dir (the only tree
// the hardened service can write) and are watched/created by the root updater.
const (
	TriggerFileName = "update-request.json"
	ResultFileName  = "update-result.json"
)

// ErrUpdateInFlight is returned when a second update is requested while one is
// already pending or running.
var ErrUpdateInFlight = errors.New("a system update is already in progress")

// ErrInvalidTarget is returned when the requested target is not a known,
// newer release.
var ErrInvalidTarget = errors.New("requested version is not a valid newer release")

// State is the persisted update status.
type State struct {
	// Status is one of the State* constants.
	Status string `json:"status"`
	// Target is the normalized version being installed.
	Target string `json:"target,omitempty"`
	// RequestedBy is the admin username that initiated the update.
	RequestedBy string `json:"requested_by,omitempty"`
	// RequestedAt is when the update was requested (Unix seconds).
	RequestedAt int64 `json:"requested_at,omitempty"`
	// FinishedAt is when a terminal state was reached (Unix seconds).
	FinishedAt int64 `json:"finished_at,omitempty"`
	// Message carries a human-readable error or result detail.
	Message string `json:"message,omitempty"`
}

// triggerPayload is written to the trigger file for the root helper.
type triggerPayload struct {
	Version     string `json:"version"`
	RequestedBy string `json:"requested_by"`
	RequestedAt int64  `json:"requested_at"`
}

// resultPayload is written by update.sh (via DOCKPAL_RESULT_FILE) and read
// back here to reconcile state.
type resultPayload struct {
	Status     string `json:"status"` // "done" | "failed"
	Target     string `json:"target"`
	ExitCode   int    `json:"exit_code"`
	Message    string `json:"message,omitempty"`
	FinishedAt int64  `json:"finished_at"`
}

// Manager coordinates update requests: it validates targets, persists state,
// and writes the trigger file the privileged updater consumes. It also
// reconciles the result file back into state after the process restarts.
type Manager struct {
	database *db.DB
	checker  *Checker
	dataDir  string
	// currentVersion is the running binary version (normalized).
	currentVersion string

	mu sync.Mutex
}

// NewManager builds a Manager. dataDir is the writable dockpal data root
// (DOCKPAL_DATA_DIR); currentVersion is the build version (already "v"-less).
func NewManager(database *db.DB, checker *Checker, dataDir, currentVersion string) *Manager {
	return &Manager{
		database:       database,
		checker:        checker,
		dataDir:        dataDir,
		currentVersion: normalizeVersion(currentVersion),
	}
}

// CurrentVersion returns the running binary version (normalized).
func (m *Manager) CurrentVersion() string { return m.currentVersion }

// UpdateAvailable reports whether the cached latest release is newer than the
// running binary.
func (m *Manager) UpdateAvailable() bool {
	latest := m.checker.Latest()
	if latest == nil {
		return false
	}
	return CompareVersions(latest.Version, m.currentVersion) > 0
}

// Status returns the current snapshot for the API: versions, availability,
// the cached release, and the state machine. It first reconciles any result
// file the updater may have written.
func (m *Manager) Status() (*StatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileLocked()

	state := m.loadStateLocked()
	latest := m.checker.Latest()

	resp := &StatusResponse{
		CurrentVersion: m.currentVersion,
		UpdateEnabled:  EnvEnabled(),
		State:          state.Status,
		StateDetail:    state,
	}
	if resp.State == "" {
		resp.State = StateNone
	}
	if latest != nil {
		resp.LatestVersion = latest.Version
		resp.Changelog = latest.Changelog
		resp.ChangelogURL = latest.ChangelogURL
		resp.PublishedAt = latest.PublishedAt
		resp.LastCheckedAt = latest.CheckedAt
		resp.UpdateAvailable = CompareVersions(latest.Version, m.currentVersion) > 0
	}
	return resp, nil
}

// StatusResponse is the API payload for GET /system/update/status.
type StatusResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateEnabled   bool   `json:"update_enabled"`
	LastCheckedAt   int64  `json:"last_checked_at,omitempty"`
	Changelog       string `json:"changelog,omitempty"`
	ChangelogURL    string `json:"changelog_url,omitempty"`
	PublishedAt     int64  `json:"published_at,omitempty"`
	State           string `json:"state"`
	StateDetail     State  `json:"state_detail"`
}

// RequestUpdate validates the target and transitions to "requested", writing
// the trigger file. username is the requesting admin for audit.
func (m *Manager) RequestUpdate(target, username string) (*State, error) {
	if !EnvEnabled() {
		return nil, errors.New("system update via UI is disabled")
	}
	target = normalizeVersion(target)
	if target == "" {
		return nil, ErrInvalidTarget
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Only one update may be in flight.
	state := m.loadStateLocked()
	if state.Status == StateRequested || state.Status == StateRunning {
		return nil, ErrUpdateInFlight
	}

	// Validate against the checker cache: the target must be a known release
	// and strictly newer than the running binary. This prevents a forged
	// request from planting an arbitrary version in the root-consumed trigger.
	latest := m.checker.Latest()
	if latest == nil || latest.Version != target {
		return nil, ErrInvalidTarget
	}
	if CompareVersions(target, m.currentVersion) <= 0 {
		return nil, ErrInvalidTarget
	}

	newState := State{
		Status:      StateRequested,
		Target:      target,
		RequestedBy: username,
		RequestedAt: time.Now().Unix(),
	}
	if err := m.writeTriggerLocked(target, username, newState.RequestedAt); err != nil {
		return nil, fmt.Errorf("write update trigger: %w", err)
	}
	if err := m.saveStateLocked(newState); err != nil {
		// Best effort rollback of the trigger file if we cannot persist state.
		_ = os.Remove(m.triggerPath())
		return nil, fmt.Errorf("persist update state: %w", err)
	}

	slog.Info("system update requested", "component", "update", "target", target, "by", username)
	cp := newState
	return &cp, nil
}

// FinalizeOnBoot reconciles state left over from the previous process. If the
// running binary now matches the requested target, the update succeeded; if a
// result file reports failure, it failed; a stale in-flight state with neither
// is left for reconcileLocked to time out. Called once during startup.
func (m *Manager) FinalizeOnBoot() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileLocked()
}

// reconcileLocked folds the updater's result file into the persisted state.
// Must be called with m.mu held.
func (m *Manager) reconcileLocked() {
	state := m.loadStateLocked()
	if state.Status != StateRequested && state.Status != StateRunning {
		return
	}

	// If we now run the target version, the swap succeeded.
	if m.currentVersion != "" && state.Target != "" && CompareVersions(m.currentVersion, state.Target) == 0 {
		state.Status = StateDone
		state.FinishedAt = time.Now().Unix()
		state.Message = fmt.Sprintf("updated to %s", state.Target)
		_ = m.saveStateLocked(state)
		_ = os.Remove(m.resultPath())
		slog.Info("system update completed", "component", "update", "target", state.Target)
		return
	}

	// Otherwise consult the result file written by update.sh.
	res, ok := m.readResultLocked()
	if !ok {
		return
	}
	if res.Status == "done" {
		// update.sh reported success but we don't run the target — treat as
		// done anyway (version strings may differ in format).
		state.Status = StateDone
		state.FinishedAt = res.FinishedAt
		state.Message = firstNonEmpty(res.Message, "update completed")
	} else {
		state.Status = StateFailed
		state.FinishedAt = res.FinishedAt
		state.Message = firstNonEmpty(res.Message, fmt.Sprintf("update failed (exit %d); rolled back", res.ExitCode))
	}
	_ = m.saveStateLocked(state)
	_ = os.Remove(m.resultPath())
	slog.Info("system update reconciled", "component", "update", "status", state.Status, "target", state.Target)
}

// loadStateLocked reads the persisted state, defaulting to StateNone.
func (m *Manager) loadStateLocked() State {
	if m.database == nil {
		return State{Status: StateNone}
	}
	data, err := m.database.GetSetting(settingsKeyUpdateState)
	if err != nil {
		return State{Status: StateNone}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{Status: StateNone}
	}
	if s.Status == "" {
		s.Status = StateNone
	}
	return s
}

// saveStateLocked persists the state.
func (m *Manager) saveStateLocked(s State) error {
	if m.database == nil {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return m.database.SetSetting(settingsKeyUpdateState, data)
}

// writeTriggerLocked atomically writes the trigger file the root .path unit
// watches. Write to a temp file then rename so systemd never sees a partial
// JSON document.
func (m *Manager) writeTriggerLocked(target, username string, at int64) error {
	payload := triggerPayload{Version: target, RequestedBy: username, RequestedAt: at}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	tmp := m.triggerPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.triggerPath()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readResultLocked reads and parses the updater's result file.
func (m *Manager) readResultLocked() (*resultPayload, bool) {
	data, err := os.ReadFile(m.resultPath())
	if err != nil {
		return nil, false
	}
	var res resultPayload
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, false
	}
	return &res, true
}

func (m *Manager) triggerPath() string { return filepath.Join(m.dataDir, TriggerFileName) }
func (m *Manager) resultPath() string  { return filepath.Join(m.dataDir, ResultFileName) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
