package metrics

import (
	"testing"
	"time"

	"github.com/sdldev/dockpal/internal/db"
)

func newHistoryTestDB(t *testing.T) *db.DB {
	t.Helper()
	tmpDir := t.TempDir()
	database, err := db.New(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func sampleAt(ts int64, cpu float64) Sample {
	return Sample{Timestamp: ts, CPUPercent: cpu, UsedRAM: 1, TotalRAM: 2, UsedDisk: 3, TotalDisk: 4}
}

func TestHistoryStore_AppendAndQuery(t *testing.T) {
	database := newHistoryTestDB(t)
	store := NewHistoryStore(database)
	now := time.Now()

	// Insert samples spanning 2 hours
	for i := 0; i < 10; i++ {
		ts := now.Add(-time.Duration(10-i) * time.Minute).Unix()
		if err := store.Append("local", sampleAt(ts, float64(i))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	samples, err := store.Query("local", now.Add(-2*time.Hour), now, 0)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(samples) != 10 {
		t.Fatalf("got %d samples, want 10", len(samples))
	}
	// ascending time order
	for i := 1; i < len(samples); i++ {
		if samples[i].Timestamp <= samples[i-1].Timestamp {
			t.Fatalf("samples not in ascending order at %d", i)
		}
	}
}

func TestHistoryStore_QueryFiltersRange(t *testing.T) {
	database := newHistoryTestDB(t)
	store := NewHistoryStore(database)
	now := time.Now()

	// 3 samples: 2h ago, 1h ago, now
	points := []time.Time{now.Add(-2 * time.Hour), now.Add(-1 * time.Hour), now}
	for _, p := range points {
		if err := store.Append("local", sampleAt(p.Unix(), 0)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// Only the last hour should match
	samples, err := store.Query("local", now.Add(-90*time.Minute), now, 0)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2 (last hour only)", len(samples))
	}
}

func TestHistoryStore_QueryDownsamples(t *testing.T) {
	database := newHistoryTestDB(t)
	store := NewHistoryStore(database)
	now := time.Now()

	// 100 points
	for i := 0; i < 100; i++ {
		ts := now.Add(-time.Duration(100-i) * time.Minute).Unix()
		if err := store.Append("local", sampleAt(ts, float64(i))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	samples, err := store.Query("local", now.Add(-2*time.Hour), now, 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(samples) > 11 {
		t.Fatalf("got %d samples, want at most 11 (10 + newest)", len(samples))
	}
	// Newest point must be the actual latest sample
	newest := samples[len(samples)-1]
	if newest.CPUPercent != 99 {
		t.Fatalf("newest sample has cpu %.1f, want 99", newest.CPUPercent)
	}
}

func TestHistoryStore_QueryIsolatesInstances(t *testing.T) {
	database := newHistoryTestDB(t)
	store := NewHistoryStore(database)
	now := time.Now()

	store.Append("local", sampleAt(now.Unix(), 10))
	store.Append("latihan", sampleAt(now.Unix(), 20))

	local, _ := store.Query("local", now.Add(-time.Hour), now, 0)
	remote, _ := store.Query("latihan", now.Add(-time.Hour), now, 0)
	if len(local) != 1 || local[0].CPUPercent != 10 {
		t.Fatalf("local series wrong: %+v", local)
	}
	if len(remote) != 1 || remote[0].CPUPercent != 20 {
		t.Fatalf("latihan series wrong: %+v", remote)
	}
}

func TestHistoryStore_PurgeOlderThan(t *testing.T) {
	database := newHistoryTestDB(t)
	store := NewHistoryStore(database)
	now := time.Now()

	// 5 old (31d+) + 3 recent
	for i := 0; i < 5; i++ {
		ts := now.AddDate(0, 0, -31-i).Unix()
		store.Append("local", sampleAt(ts, 0))
	}
	for i := 0; i < 3; i++ {
		ts := now.Add(-time.Duration(3-i) * time.Hour).Unix()
		store.Append("local", sampleAt(ts, 0))
	}

	deleted, err := store.PurgeOlderThan(now.AddDate(0, 0, -RetentionDays))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("deleted %d, want 5", deleted)
	}

	remaining, _ := store.Query("local", now.AddDate(0, 0, -40), now, 0)
	if len(remaining) != 3 {
		t.Fatalf("remaining %d, want 3", len(remaining))
	}
}

func TestHistoryStore_PurgeOlderThanAcrossInstances(t *testing.T) {
	database := newHistoryTestDB(t)
	store := NewHistoryStore(database)
	old := time.Now().AddDate(0, 0, -31).Unix()

	store.Append("local", sampleAt(old, 0))
	store.Append("latihan", sampleAt(old, 0))

	deleted, _ := store.PurgeOlderThan(time.Now().AddDate(0, 0, -RetentionDays))
	if deleted != 2 {
		t.Fatalf("deleted %d, want 2 (both instances)", deleted)
	}
}
