package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// newTestDB opens a fresh bbolt-backed *DB inside t.TempDir(). The test
// database is closed automatically when the test finishes.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := New(path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// makeRecord builds a deterministic AppUpdateRecord. StartedAt is taken
// straight from the seq argument so tests can control ordering without
// depending on time.Now().
func makeRecord(app, instanceID, attemptID string, startedAt int64, stage AppUpdateStage) *AppUpdateRecord {
	return &AppUpdateRecord{
		AttemptID:   attemptID,
		InstanceID:  instanceID,
		App:         app,
		Stage:       stage,
		TriggeredBy: "test",
		StartedAt:   startedAt,
		UpdatedAt:   startedAt,
		Services: map[string]ServiceUpdateInfo{
			"web": {Image: "nginx:latest"},
		},
	}
}

func saveRecord(t *testing.T, d *DB, rec *AppUpdateRecord) {
	t.Helper()
	if err := d.SaveAppUpdate(rec); err != nil {
		t.Fatalf("SaveAppUpdate(%s): %v", rec.AttemptID, err)
	}
}

// TestSaveAndListAppUpdatesOrdering verifies ListAppUpdates returns the
// records newest-first by StartedAt and respects the limit argument.
//
// Validates: Requirements 5.1
func TestSaveAndListAppUpdatesOrdering(t *testing.T) {
	d := newTestDB(t)

	// Insert intentionally out of chronological order.
	timestamps := []int64{300, 100, 500, 200, 400}
	for i, ts := range timestamps {
		rec := makeRecord("app-a", "inst-1", fmt.Sprintf("attempt-%d", i), ts, StageCompleted)
		saveRecord(t, d, rec)
	}

	got, err := d.ListAppUpdates("app-a", 0)
	if err != nil {
		t.Fatalf("ListAppUpdates: %v", err)
	}
	if len(got) != len(timestamps) {
		t.Fatalf("expected %d records, got %d", len(timestamps), len(got))
	}

	wantOrder := []int64{500, 400, 300, 200, 100}
	for i, ts := range wantOrder {
		if got[i].StartedAt != ts {
			t.Errorf("ordering[%d]: expected StartedAt=%d, got %d", i, ts, got[i].StartedAt)
		}
	}

	// Limit honored.
	limited, err := d.ListAppUpdates("app-a", 3)
	if err != nil {
		t.Fatalf("ListAppUpdates(limit=3): %v", err)
	}
	if len(limited) != 3 {
		t.Fatalf("expected 3 limited records, got %d", len(limited))
	}
	for i, ts := range wantOrder[:3] {
		if limited[i].StartedAt != ts {
			t.Errorf("limited[%d]: expected StartedAt=%d, got %d", i, ts, limited[i].StartedAt)
		}
	}

	// Listing a different app returns nothing.
	other, err := d.ListAppUpdates("app-b", 0)
	if err != nil {
		t.Fatalf("ListAppUpdates(app-b): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("expected 0 records for unseen app, got %d", len(other))
	}
}

// TestGetAppUpdate verifies attempt-by-id lookup returns the saved record
// and returns (nil, nil) for an unknown id.
//
// Validates: Requirements 5.1
func TestGetAppUpdate(t *testing.T) {
	d := newTestDB(t)

	rec := makeRecord("app-a", "inst-1", "attempt-x", 1000, StagePulling)
	saveRecord(t, d, rec)

	got, err := d.GetAppUpdate("attempt-x")
	if err != nil {
		t.Fatalf("GetAppUpdate: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil record")
	}
	if got.AttemptID != rec.AttemptID || got.App != rec.App || got.Stage != rec.Stage {
		t.Errorf("unexpected record: %+v", got)
	}

	missing, err := d.GetAppUpdate("does-not-exist")
	if err != nil {
		t.Fatalf("GetAppUpdate(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing id, got %+v", missing)
	}
}

// TestAppendAppUpdateEvent verifies events are appended, the stage is updated,
// UpdatedAt is bumped, and missing ids surface as ErrAppUpdateNotFound.
//
// Validates: Requirements 5.1
func TestAppendAppUpdateEvent(t *testing.T) {
	d := newTestDB(t)

	rec := makeRecord("app-a", "inst-1", "attempt-1", 1000, StagePending)
	saveRecord(t, d, rec)

	ev := AppUpdateEvent{At: 1500, Stage: StagePulling, Message: "pulling images"}
	if err := d.AppendAppUpdateEvent("attempt-1", ev, StagePulling); err != nil {
		t.Fatalf("AppendAppUpdateEvent: %v", err)
	}

	got, err := d.GetAppUpdate("attempt-1")
	if err != nil {
		t.Fatalf("GetAppUpdate after append: %v", err)
	}
	if got == nil {
		t.Fatal("expected record after append")
	}
	if got.Stage != StagePulling {
		t.Errorf("expected stage=%s, got %s", StagePulling, got.Stage)
	}
	if got.UpdatedAt != ev.At {
		t.Errorf("expected UpdatedAt=%d, got %d", ev.At, got.UpdatedAt)
	}
	if len(got.Events) != 1 || got.Events[0] != ev {
		t.Errorf("expected exactly one matching event, got %+v", got.Events)
	}

	// A second append must accumulate events.
	ev2 := AppUpdateEvent{At: 2000, Stage: StageCompleted, Message: "done"}
	if err := d.AppendAppUpdateEvent("attempt-1", ev2, StageCompleted); err != nil {
		t.Fatalf("AppendAppUpdateEvent #2: %v", err)
	}
	got, err = d.GetAppUpdate("attempt-1")
	if err != nil {
		t.Fatalf("GetAppUpdate after second append: %v", err)
	}
	if len(got.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got.Events))
	}
	if got.Stage != StageCompleted || got.UpdatedAt != ev2.At {
		t.Errorf("unexpected post-append state: stage=%s updatedAt=%d", got.Stage, got.UpdatedAt)
	}

	// The per-app listing should observe the latest stage too.
	listed, err := d.ListAppUpdates("app-a", 0)
	if err != nil {
		t.Fatalf("ListAppUpdates after append: %v", err)
	}
	if len(listed) != 1 || listed[0].Stage != StageCompleted {
		t.Errorf("expected per-app listing to reflect latest stage, got %+v", listed)
	}

	// Missing id surfaces ErrAppUpdateNotFound.
	if err := d.AppendAppUpdateEvent("missing", ev, StagePulling); !errors.Is(err, ErrAppUpdateNotFound) {
		t.Errorf("expected ErrAppUpdateNotFound, got %v", err)
	}
}

// TestListAllAppUpdatesFiltersByInstance verifies global listing respects the
// instance filter and orders results newest-first across multiple apps.
//
// Validates: Requirements 5.1
func TestListAllAppUpdatesFiltersByInstance(t *testing.T) {
	d := newTestDB(t)

	// Two apps on inst-1, one app on inst-2.
	saveRecord(t, d, makeRecord("app-a", "inst-1", "a1", 100, StageCompleted))
	saveRecord(t, d, makeRecord("app-a", "inst-1", "a2", 300, StageCompleted))
	saveRecord(t, d, makeRecord("app-b", "inst-1", "b1", 200, StageCompleted))
	saveRecord(t, d, makeRecord("app-c", "inst-2", "c1", 400, StageCompleted))

	got, err := d.ListAllAppUpdates("inst-1", 0)
	if err != nil {
		t.Fatalf("ListAllAppUpdates(inst-1): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 inst-1 records, got %d", len(got))
	}
	wantOrder := []int64{300, 200, 100}
	for i, ts := range wantOrder {
		if got[i].StartedAt != ts {
			t.Errorf("ordering[%d]: expected StartedAt=%d, got %d", i, ts, got[i].StartedAt)
		}
		if got[i].InstanceID != "inst-1" {
			t.Errorf("ordering[%d]: expected InstanceID=inst-1, got %s", i, got[i].InstanceID)
		}
	}

	// Empty filter returns everything.
	all, err := d.ListAllAppUpdates("", 0)
	if err != nil {
		t.Fatalf("ListAllAppUpdates(\"\"): %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 records across all instances, got %d", len(all))
	}

	// Limit is applied after sorting.
	limited, err := d.ListAllAppUpdates("", 2)
	if err != nil {
		t.Fatalf("ListAllAppUpdates(limit=2): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 records, got %d", len(limited))
	}
	if limited[0].StartedAt != 400 || limited[1].StartedAt != 300 {
		t.Errorf("expected newest-first slice, got [%d, %d]", limited[0].StartedAt, limited[1].StartedAt)
	}
}

// TestPurgeOlderThanPerAppRetention verifies the per-app cap keeps the 100
// newest records and drops the rest, both from the per-app and by-id buckets.
//
// Validates: Requirements 5.4
func TestPurgeOlderThanPerAppRetention(t *testing.T) {
	d := newTestDB(t)

	const total = 150
	const retain = 100

	for i := 0; i < total; i++ {
		// StartedAt grows with i so the i==total-1 record is the newest.
		rec := makeRecord("app-a", "inst-1", fmt.Sprintf("attempt-%03d", i), int64(i+1), StageCompleted)
		saveRecord(t, d, rec)
	}

	deleted, err := d.PurgeOlderThan(retain, 0)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if deleted != total-retain {
		t.Errorf("expected %d deletions, got %d", total-retain, deleted)
	}

	remaining, err := d.ListAppUpdates("app-a", 0)
	if err != nil {
		t.Fatalf("ListAppUpdates: %v", err)
	}
	if len(remaining) != retain {
		t.Fatalf("expected %d remaining records, got %d", retain, len(remaining))
	}

	// The survivors must be the newest records (StartedAt 51..150) listed
	// newest-first.
	wantNewest := int64(total)
	wantOldest := int64(total - retain + 1)
	if remaining[0].StartedAt != wantNewest {
		t.Errorf("expected newest StartedAt=%d, got %d", wantNewest, remaining[0].StartedAt)
	}
	if remaining[len(remaining)-1].StartedAt != wantOldest {
		t.Errorf("expected oldest survivor StartedAt=%d, got %d", wantOldest, remaining[len(remaining)-1].StartedAt)
	}

	// The dropped attempts must also disappear from the by-id bucket.
	dropped, err := d.GetAppUpdate("attempt-000")
	if err != nil {
		t.Fatalf("GetAppUpdate(dropped): %v", err)
	}
	if dropped != nil {
		t.Errorf("expected dropped attempt to be absent from by-id bucket")
	}
	kept, err := d.GetAppUpdate("attempt-149")
	if err != nil {
		t.Fatalf("GetAppUpdate(kept): %v", err)
	}
	if kept == nil {
		t.Errorf("expected newest attempt to remain in by-id bucket")
	}
}

// TestPurgeOlderThanGlobalRetention verifies the global cap of 1000 trims
// across apps after the per-app cap has been applied.
//
// Validates: Requirements 5.4
func TestPurgeOlderThanGlobalRetention(t *testing.T) {
	d := newTestDB(t)

	const apps = 12
	const perApp = 100
	const globalCap = 1000
	const expectedTotal = apps * perApp

	// Use a strictly increasing StartedAt across all writes so the global
	// trim has a deterministic newest-first ordering.
	var seq int64
	for a := 0; a < apps; a++ {
		appName := fmt.Sprintf("app-%02d", a)
		for i := 0; i < perApp; i++ {
			seq++
			attemptID := fmt.Sprintf("%s-att-%03d", appName, i)
			saveRecord(t, d, makeRecord(appName, "inst-1", attemptID, seq, StageCompleted))
		}
	}

	deleted, err := d.PurgeOlderThan(perApp, globalCap)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if deleted != expectedTotal-globalCap {
		t.Errorf("expected %d deletions, got %d", expectedTotal-globalCap, deleted)
	}

	all, err := d.ListAllAppUpdates("", 0)
	if err != nil {
		t.Fatalf("ListAllAppUpdates: %v", err)
	}
	if len(all) > globalCap {
		t.Errorf("expected at most %d records globally, got %d", globalCap, len(all))
	}
	if len(all) != globalCap {
		t.Errorf("expected exactly %d records after global trim, got %d", globalCap, len(all))
	}

	// Newest-first invariant after trim: the 1000 survivors must be the 1000
	// largest StartedAt values, i.e. seq > expectedTotal-globalCap.
	cutoff := int64(expectedTotal - globalCap)
	for _, rec := range all {
		if rec.StartedAt <= cutoff {
			t.Errorf("unexpected old survivor: StartedAt=%d cutoff=%d attempt=%s", rec.StartedAt, cutoff, rec.AttemptID)
			break
		}
	}

	// Per-app cap must also still hold.
	for a := 0; a < apps; a++ {
		appName := fmt.Sprintf("app-%02d", a)
		recs, err := d.ListAppUpdates(appName, 0)
		if err != nil {
			t.Fatalf("ListAppUpdates(%s): %v", appName, err)
		}
		if len(recs) > perApp {
			t.Errorf("%s exceeds per-app cap: %d > %d", appName, len(recs), perApp)
		}
	}
}
