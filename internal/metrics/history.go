package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sdldev/dockpal/internal/agent"
	"github.com/sdldev/dockpal/internal/db"
	bolt "go.etcd.io/bbolt"
)

// Sample is one point in the host metrics time series. Timestamp is Unix
// seconds; usage fields mirror agent.HostStats so the dashboard can render
// CPU/memory/disk history without any transformation.
type Sample struct {
	Timestamp  int64   `json:"ts"`
	CPUPercent float64 `json:"cpu_percent"`
	UsedRAM    uint64  `json:"used_ram"`
	TotalRAM   uint64  `json:"total_ram"`
	UsedDisk   uint64  `json:"used_disk"`
	TotalDisk  uint64  `json:"total_disk"`
	// Network I/O rates (bytes/sec); zero before this field existed.
	NetworkRxBps float64 `json:"network_rx_bps"`
	NetworkTxBps float64 `json:"network_tx_bps"`
}

// metricsBucket mirrors db's private bucketMetrics name — it must match
// exactly so reads/writes hit the same bbolt bucket. (Not exported from db
// because only this package owns the metrics series.)
var metricsBucket = []byte("metrics")

// metricsKeyPrefix namespaces a series by instance so a single bucket holds
// every server's history while still allowing a cheap per-instance scan.
const metricsKeyPrefix = "inst|"

// Default retention: 30 days, sampled every 30s. Rough size estimate per
// instance: 86,400 samples/month × ~80 bytes ≈ 7 MB — well within a single
// bbolt file.
const (
	RetentionDays     = 30
	DefaultInterval   = 30 * time.Second
	cleanupInterval   = 24 * time.Hour
	bulkInsertMaxRows = 500
)

// HistoryStore reads and writes the metrics time series on top of the
// existing db.DB handle (it does not open a second bbolt file).
type HistoryStore struct {
	database *db.DB
}

// NewHistoryStore wraps the shared db handle.
func NewHistoryStore(database *db.DB) *HistoryStore {
	return &HistoryStore{database: database}
}

// Append stores one sample. Keys are `inst|<instanceID>|<unix_ts>` so a
// cursor scan in time order is already sorted.
func (s *HistoryStore) Append(instanceID string, sample Sample) error {
	key := fmt.Sprintf("%s%s|%d", metricsKeyPrefix, instanceID, sample.Timestamp)
	payload, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	return s.database.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(metricsBucket).Put([]byte(key), payload)
	})
}

// Query returns samples for an instance in [from, to), in ascending time
// order. If maxPoints > 0 and the series is longer, the result is uniformly
// downsampled (every Nth point, always keeping the newest sample) so the
// frontend never receives more than it can draw.
func (s *HistoryStore) Query(instanceID string, from, to time.Time, maxPoints int) ([]Sample, error) {
	// endKey uses to.Unix()+1 so the "to" instant itself is included.
	startKey := []byte(fmt.Sprintf("%s%s|%d", metricsKeyPrefix, instanceID, from.Unix()))
	endKey := []byte(fmt.Sprintf("%s%s|%d", metricsKeyPrefix, instanceID, to.Unix()+1))

	var samples []Sample
	err := s.database.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(metricsBucket).Cursor()
		for k, v := c.Seek(startKey); k != nil && compareKeys(k, endKey) < 0; k, v = c.Next() {
			var sample Sample
			if err := json.Unmarshal(v, &sample); err != nil {
				continue // skip malformed rows rather than failing the whole range
			}
			samples = append(samples, sample)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if maxPoints > 0 && len(samples) > maxPoints {
		return downsample(samples, maxPoints), nil
	}
	return samples, nil
}

// PurgeOlderThan removes every sample with a timestamp older than cutoff,
// across all instances. Returns the number of rows deleted.
func (s *HistoryStore) PurgeOlderThan(cutoff time.Time) (int, error) {
	cutoffUnix := cutoff.Unix()
	deleted := 0
	err := s.database.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(metricsBucket)
		c := b.Cursor()
		var keys [][]byte
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var sample Sample
			if err := json.Unmarshal(v, &sample); err != nil {
				continue
			}
			if sample.Timestamp < cutoffUnix {
				keyCopy := append([]byte(nil), k...)
				keys = append(keys, keyCopy)
				deleted++
				if len(keys) >= bulkInsertMaxRows {
					for _, k2 := range keys {
						if err := b.Delete(k2); err != nil {
							return err
						}
					}
					keys = keys[:0]
				}
			}
		}
		for _, k2 := range keys {
			if err := b.Delete(k2); err != nil {
				return err
			}
		}
		return nil
	})
	return deleted, err
}

// compareKeys orders keys lexicographically (bbolt's natural key order).
func compareKeys(a, b []byte) int {
	if string(a) < string(b) {
		return -1
	}
	if string(a) > string(b) {
		return 1
	}
	return 0
}

// downsample keeps every Nth point plus the final (newest) sample.
func downsample(samples []Sample, maxPoints int) []Sample {
	if len(samples) <= maxPoints {
		return samples
	}
	// stride > 1 guarantees <= maxPoints results.
	stride := (len(samples) + maxPoints - 1) / maxPoints
	out := make([]Sample, 0, maxPoints)
	for i := 0; i < len(samples); i += stride {
		out = append(out, samples[i])
	}
	// Always keep the newest point so the chart's right edge is current.
	last := samples[len(samples)-1]
	if len(out) == 0 || out[len(out)-1].Timestamp != last.Timestamp {
		out = append(out, last)
	}
	return out
}

// --- background recorder -------------------------------------------------

// HistoryRecorder periodically records host metrics for every reachable
// instance into the time-series store, and purges rows past retention.
type HistoryRecorder struct {
	agentMgr *agent.Manager
	store    *HistoryStore
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// NewHistoryRecorder creates a recorder. interval defaults to DefaultInterval.
func NewHistoryRecorder(agentMgr *agent.Manager, database *db.DB, interval time.Duration) *HistoryRecorder {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &HistoryRecorder{
		agentMgr: agentMgr,
		store:    NewHistoryStore(database),
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the recorder loop in the background.
func (r *HistoryRecorder) Start() {
	go r.loop()
	log.Printf("Metrics history recorder started (interval %s, retention %dd)", r.interval, RetentionDays)
}

// Stop signals the recorder to exit and waits for it to finish.
func (r *HistoryRecorder) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.doneCh
	log.Println("Metrics history recorder stopped")
}

// instanceIDs lists every registered instance. Falls back to "local" when
// the database query fails (mirrors the Servers page's behavior).
func (r *HistoryRecorder) instanceIDs() []string {
	ids, err := r.agentMgr.ListInstances()
	if err != nil {
		return []string{"local"}
	}
	out := make([]string, 0, len(ids))
	for _, inst := range ids {
		out = append(out, inst.ID)
	}
	if len(out) == 0 {
		out = []string{"local"}
	}
	return out
}

func (r *HistoryRecorder) loop() {
	defer close(r.doneCh)
	ticker := time.NewTicker(r.interval)
	cleanup := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	defer cleanup.Stop()

	r.collect()
	for {
		select {
		case <-ticker.C:
			r.collect()
		case <-cleanup.C:
			if n, err := r.store.PurgeOlderThan(time.Now().AddDate(0, 0, -RetentionDays)); err != nil {
				log.Printf("Metrics history purge failed: %v", err)
			} else if n > 0 {
				log.Printf("Metrics history purged %d samples older than %d days", n, RetentionDays)
			}
		case <-r.stopCh:
			return
		}
	}
}

// collect records one sample per reachable instance. Unreachable instances
// are skipped — the chart will simply show a gap (no fake zero points).
func (r *HistoryRecorder) collect() {
	now := time.Now()
	for _, id := range r.instanceIDs() {
		client, err := r.agentMgr.GetClient(id)
		if err != nil {
			continue
		}
		stats, err := client.GetHostStats(context.Background())
		if err != nil {
			continue
		}
		sample := Sample{
			Timestamp:    now.Unix(),
			CPUPercent:   stats.CPUPercent,
			UsedRAM:      stats.UsedRAM,
			TotalRAM:     stats.TotalRAM,
			UsedDisk:     stats.UsedDisk,
			TotalDisk:    stats.TotalDisk,
			NetworkRxBps: stats.NetworkRxBps,
			NetworkTxBps: stats.NetworkTxBps,
		}
		if err := r.store.Append(id, sample); err != nil {
			log.Printf("Failed to record metrics sample for %s: %v", id, err)
		}
	}
}
