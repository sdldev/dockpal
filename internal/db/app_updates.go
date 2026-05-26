package db

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"go.etcd.io/bbolt"
)

// ErrAppUpdateNotFound is returned when an attempt cannot be located by id.
var ErrAppUpdateNotFound = errors.New("app update record not found")

// AppUpdateStage represents the lifecycle stage of an auto-update attempt
// for an App (compose project). It mirrors the state machine described in
// the auto-image-update design.
type AppUpdateStage string

const (
	// StagePending means the attempt has been planned but no work has started yet.
	StagePending AppUpdateStage = "pending"
	// StagePulling means images are being pulled from the registry.
	StagePulling AppUpdateStage = "pulling"
	// StageRecreating means the compose project is being redeployed.
	StageRecreating AppUpdateStage = "recreating"
	// StageVerifying means the post-deploy health probe is running.
	StageVerifying AppUpdateStage = "verifying"
	// StageCompleted means the attempt finished successfully.
	StageCompleted AppUpdateStage = "completed"
	// StageFailed means the attempt failed and could not be rolled back cleanly.
	StageFailed AppUpdateStage = "failed"
	// StageRolledBack means the attempt failed but the previous image was restored.
	StageRolledBack AppUpdateStage = "rolled_back"
)

// AppUpdateRecord persists one auto-update attempt for an App. Each record is
// stored under a composite key (app + reversed timestamp) so that listings
// per app are returned newest-first without an in-memory sort.
type AppUpdateRecord struct {
	AttemptID   string                       `json:"attempt_id"`
	InstanceID  string                       `json:"instance_id"`
	App         string                       `json:"app"`
	Services    map[string]ServiceUpdateInfo `json:"services"`
	Stage       AppUpdateStage               `json:"stage"`
	ErrorCode   string                       `json:"error_code,omitempty"`
	Message     string                       `json:"message,omitempty"`
	TriggeredBy string                       `json:"triggered_by"`
	StartedAt   int64                        `json:"started_at"`
	UpdatedAt   int64                        `json:"updated_at"`
	CompletedAt int64                        `json:"completed_at,omitempty"`
	Events      []AppUpdateEvent             `json:"events,omitempty"`
}

// ServiceUpdateInfo captures the image transition for a single service inside
// an App for one update attempt.
type ServiceUpdateInfo struct {
	Image          string `json:"image"`
	PreviousDigest string `json:"previous_digest,omitempty"`
	NewDigest      string `json:"new_digest,omitempty"`
}

// AppUpdateEvent records one stage transition during an attempt. Events are
// appended to the parent AppUpdateRecord and broadcast over the App_Update_Feed.
type AppUpdateEvent struct {
	At      int64          `json:"at"`
	Stage   AppUpdateStage `json:"stage"`
	Message string         `json:"message,omitempty"`
}

// AppUpdateStore is the persistence contract for AppUpdateRecord values.
// The interface is defined here so AutoUpdateWorker (and its tests) can
// substitute a fake implementation without depending on the concrete bbolt
// store in *DB.
type AppUpdateStore interface {
	// SaveAppUpdate persists rec, replacing any existing record with the same
	// AttemptID. Implementations write to both the per-app bucket and the
	// by-id lookup bucket within a single transaction.
	SaveAppUpdate(rec *AppUpdateRecord) error

	// AppendAppUpdateEvent appends ev to the record identified by attemptID,
	// updates its Stage to stage, and bumps UpdatedAt. It returns an error if
	// the record does not exist.
	AppendAppUpdateEvent(attemptID string, ev AppUpdateEvent, stage AppUpdateStage) error

	// ListAppUpdates returns up to limit records for app, ordered newest-first
	// by StartedAt. A non-positive limit means "no limit".
	ListAppUpdates(app string, limit int) ([]AppUpdateRecord, error)

	// ListAllAppUpdates returns up to limit records across all apps for the
	// given instanceID, ordered newest-first by StartedAt. An empty instanceID
	// returns records from every instance. A non-positive limit means "no limit".
	ListAllAppUpdates(instanceID string, limit int) ([]AppUpdateRecord, error)

	// GetAppUpdate fetches a single record by AttemptID. It returns (nil, nil)
	// when the record does not exist.
	GetAppUpdate(attemptID string) (*AppUpdateRecord, error)

	// PurgeOlderThan trims the store so each app retains at most retainPerApp
	// records and the total record count does not exceed retainGlobal. The
	// most recent records (by StartedAt) are kept. It returns the number of
	// deleted records. A non-positive limit means "no limit" for that bound.
	PurgeOlderThan(retainPerApp, retainGlobal int) (int, error)
}

// appUpdateKey encodes the per-app composite key used by bucketAppUpdates.
//
// Layout: app + 0x00 + bigEndian(math.MaxUint64 - uint64(unixMicro)).
//
// The 0x00 separator delimits the variable-length app name from the fixed
// 8-byte timestamp suffix, allowing prefix-scans by app without ambiguity.
// The reversed timestamp causes a forward bbolt cursor over a given prefix
// to yield records newest-first. unixMicro is treated as a non-negative
// value; pre-1970 timestamps would clamp to MaxInt64 and are not expected
// in practice.
func appUpdateKey(app string, unixMicro int64) []byte {
	if unixMicro < 0 {
		unixMicro = 0
	}
	appBytes := []byte(app)
	key := make([]byte, 0, len(appBytes)+1+8)
	key = append(key, appBytes...)
	key = append(key, 0x00)
	suffix := make([]byte, 8)
	binary.BigEndian.PutUint64(suffix, math.MaxUint64-uint64(unixMicro))
	key = append(key, suffix...)
	return key
}

// SaveAppUpdate persists rec to both the per-app bucket and the by-id lookup
// bucket within a single bbolt transaction. rec.AttemptID must be non-empty
// and rec.App must be non-empty.
//
// The per-app key is composed of (App, StartedAt) using appUpdateKey, so the
// caller is expected to populate StartedAt on first save and keep it stable
// across subsequent updates of the same attempt. UpdatedAt is bumped to the
// max of its current value and StartedAt to keep the record self-consistent.
func (d *DB) SaveAppUpdate(rec *AppUpdateRecord) error {
	if rec == nil {
		return errors.New("nil app update record")
	}
	if rec.AttemptID == "" {
		return errors.New("app update record missing attempt_id")
	}
	if rec.App == "" {
		return errors.New("app update record missing app")
	}

	if rec.UpdatedAt < rec.StartedAt {
		rec.UpdatedAt = rec.StartedAt
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal app update record: %w", err)
	}

	return d.db.Update(func(tx *bbolt.Tx) error {
		byApp := tx.Bucket(bucketAppUpdates)
		if byApp == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdates)
		}
		byID := tx.Bucket(bucketAppUpdatesByID)
		if byID == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdatesByID)
		}
		key := appUpdateKey(rec.App, rec.StartedAt)
		if err := byApp.Put(key, data); err != nil {
			return err
		}
		return byID.Put([]byte(rec.AttemptID), data)
	})
}

// ListAppUpdates returns up to limit records for app, ordered newest-first
// by StartedAt. The per-app bucket key is laid out so that a forward cursor
// over the prefix "<app>\x00" yields records from newest to oldest, so this
// implementation does not need an in-memory sort.
//
// A non-positive limit means "no limit".
func (d *DB) ListAppUpdates(app string, limit int) ([]AppUpdateRecord, error) {
	if app == "" {
		return nil, errors.New("list app updates: empty app")
	}

	prefix := append([]byte(app), 0x00)

	var out []AppUpdateRecord
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketAppUpdates)
		if b == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdates)
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil; k, v = c.Next() {
			// Stop when the key no longer shares the per-app prefix.
			if len(k) < len(prefix) || !bytes.Equal(k[:len(prefix)], prefix) {
				break
			}
			var rec AppUpdateRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("unmarshal app update record: %w", err)
			}
			out = append(out, rec)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListAllAppUpdates returns up to limit records across every app, optionally
// filtered by instanceID. Records are ordered newest-first by StartedAt.
//
// An empty instanceID disables the filter and returns records from every
// instance. A non-positive limit means "no limit". The per-app bucket layout
// only orders within an app, so this method performs a full bucket scan and
// sorts the result in memory.
func (d *DB) ListAllAppUpdates(instanceID string, limit int) ([]AppUpdateRecord, error) {
	var out []AppUpdateRecord
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketAppUpdates)
		if b == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdates)
		}
		return b.ForEach(func(_, v []byte) error {
			var rec AppUpdateRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("unmarshal app update record: %w", err)
			}
			if instanceID != "" && rec.InstanceID != instanceID {
				return nil
			}
			out = append(out, rec)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetAppUpdate fetches a single record by AttemptID from the by-id lookup
// bucket. It returns (nil, nil) when the record does not exist, matching the
// AppUpdateStore contract.
func (d *DB) GetAppUpdate(attemptID string) (*AppUpdateRecord, error) {
	if attemptID == "" {
		return nil, errors.New("get app update: empty attempt id")
	}

	var rec AppUpdateRecord
	var found bool
	err := d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketAppUpdatesByID)
		if b == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdatesByID)
		}
		data := b.Get([]byte(attemptID))
		if data == nil {
			return nil
		}
		found = true
		return json.Unmarshal(data, &rec)
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &rec, nil
}

// AppendAppUpdateEvent appends ev to the record identified by attemptID,
// updates Stage to stage, and bumps UpdatedAt to max(currentUpdatedAt, ev.At).
// The mutation is performed in a single bbolt.Update transaction and the
// updated record is written back to both the by-id bucket and the per-app
// bucket so that subsequent ListAppUpdates calls observe the latest stage.
//
// Returns ErrAppUpdateNotFound when the attemptID is unknown.
func (d *DB) AppendAppUpdateEvent(attemptID string, ev AppUpdateEvent, stage AppUpdateStage) error {
	if attemptID == "" {
		return errors.New("append app update event: empty attempt id")
	}

	return d.db.Update(func(tx *bbolt.Tx) error {
		byID := tx.Bucket(bucketAppUpdatesByID)
		if byID == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdatesByID)
		}
		byApp := tx.Bucket(bucketAppUpdates)
		if byApp == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdates)
		}

		data := byID.Get([]byte(attemptID))
		if data == nil {
			return ErrAppUpdateNotFound
		}

		var rec AppUpdateRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("unmarshal app update record: %w", err)
		}

		rec.Events = append(rec.Events, ev)
		rec.Stage = stage
		if ev.At > rec.UpdatedAt {
			rec.UpdatedAt = ev.At
		}

		updated, err := json.Marshal(&rec)
		if err != nil {
			return fmt.Errorf("marshal app update record: %w", err)
		}

		if err := byID.Put([]byte(attemptID), updated); err != nil {
			return err
		}
		return byApp.Put(appUpdateKey(rec.App, rec.StartedAt), updated)
	})
}

// PurgeOlderThan trims the app-updates store so each app retains at most
// retainPerApp records and the total record count does not exceed
// retainGlobal. The most recent records (by StartedAt) are kept.
//
// A non-positive value for either limit disables that bound (e.g. passing
// retainPerApp <= 0 skips per-app trimming, and retainGlobal <= 0 skips the
// global trim). Passing both as zero or negative values is a no-op.
//
// All deletes are performed in a single bbolt.Update transaction so the
// operation is atomic. For every dropped record, the entry is removed from
// both the per-app bucket (keyed via appUpdateKey) and the by-id bucket
// (keyed by AttemptID). The returned int is the total number of records
// deleted across both passes.
func (d *DB) PurgeOlderThan(retainPerApp, retainGlobal int) (int, error) {
	if retainPerApp <= 0 && retainGlobal <= 0 {
		return 0, nil
	}

	deleted := 0
	err := d.db.Update(func(tx *bbolt.Tx) error {
		byApp := tx.Bucket(bucketAppUpdates)
		if byApp == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdates)
		}
		byID := tx.Bucket(bucketAppUpdatesByID)
		if byID == nil {
			return fmt.Errorf("bucket %s not initialized", bucketAppUpdatesByID)
		}

		// Load every record and group by App. We keep StartedAt and AttemptID
		// alongside the App so we can rebuild the per-app and by-id keys for
		// deletion without re-decoding.
		grouped := make(map[string][]AppUpdateRecord)
		if err := byApp.ForEach(func(_, v []byte) error {
			var rec AppUpdateRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("unmarshal app update record: %w", err)
			}
			grouped[rec.App] = append(grouped[rec.App], rec)
			return nil
		}); err != nil {
			return err
		}

		// Pass 1: per-app trim. Sort each app's records newest-first and drop
		// everything past retainPerApp. The survivors flow into the global pass.
		var survivors []AppUpdateRecord
		for app, recs := range grouped {
			sort.Slice(recs, func(i, j int) bool {
				return recs[i].StartedAt > recs[j].StartedAt
			})

			keepCount := len(recs)
			if retainPerApp > 0 && keepCount > retainPerApp {
				keepCount = retainPerApp
			}

			for i := keepCount; i < len(recs); i++ {
				if err := deleteAppUpdate(byApp, byID, &recs[i]); err != nil {
					return err
				}
				deleted++
			}

			survivors = append(survivors, recs[:keepCount]...)
			// Free the slice once consumed to keep memory bounded for very
			// large stores. Not strictly required for correctness.
			_ = app
		}

		// Pass 2: global trim. Sort the survivors newest-first and drop
		// everything past retainGlobal.
		if retainGlobal > 0 && len(survivors) > retainGlobal {
			sort.Slice(survivors, func(i, j int) bool {
				return survivors[i].StartedAt > survivors[j].StartedAt
			})
			for i := retainGlobal; i < len(survivors); i++ {
				if err := deleteAppUpdate(byApp, byID, &survivors[i]); err != nil {
					return err
				}
				deleted++
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// deleteAppUpdate removes a single record from both the per-app bucket and
// the by-id lookup bucket. It is a small helper used by PurgeOlderThan to
// keep the deletion paths consistent.
func deleteAppUpdate(byApp, byID *bbolt.Bucket, rec *AppUpdateRecord) error {
	if err := byApp.Delete(appUpdateKey(rec.App, rec.StartedAt)); err != nil {
		return err
	}
	if rec.AttemptID != "" {
		if err := byID.Delete([]byte(rec.AttemptID)); err != nil {
			return err
		}
	}
	return nil
}
