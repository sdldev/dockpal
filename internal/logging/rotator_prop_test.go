package logging

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/quick"
)

// **Validates: Requirements 13.2, 13.4**

// Property 15: Log File Retention Invariant
// Verify rotated file count never exceeds 5 for any sequence of rotations.
// **Validates: Requirements 13.2, 13.4**

func TestProperty15_LogFileRetentionInvariant(t *testing.T) {
	// Property: For any number of rotations N, the number of rotated log files
	// (.1, .2, ..., .N) never exceeds maxFiles (5).
	prop := func(params rotationParams) bool {
		tmpDir, err := os.MkdirTemp("", "rotator-prop-test-*")
		if err != nil {
			t.Logf("failed to create temp dir: %v", err)
			return false
		}
		defer os.RemoveAll(tmpDir)

		logPath := filepath.Join(tmpDir, "test.log")
		maxSize := int64(100) // small maxSize to trigger rotations easily

		lr, err := NewLogRotator(logPath, maxSize, DefaultMaxFiles)
		if err != nil {
			t.Logf("failed to create rotator: %v", err)
			return false
		}
		defer lr.Close()

		// Write enough data to trigger params.rotations rotations.
		// Each write of (maxSize + 1) bytes triggers a rotation.
		data := make([]byte, maxSize+1)
		for i := range data {
			data[i] = 'A'
		}

		for i := 0; i < params.rotations; i++ {
			_, err := lr.Write(data)
			if err != nil {
				t.Logf("write failed at rotation %d: %v", i, err)
				return false
			}

			// After each rotation, count rotated files
			rotatedCount := countRotatedFiles(tmpDir, logPath)
			if rotatedCount > DefaultMaxFiles {
				t.Logf("rotation %d: found %d rotated files, max allowed is %d",
					i, rotatedCount, DefaultMaxFiles)
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 200,
		Values:   rotationParamsGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 15 failed: %v", err)
	}
}

// rotationParams holds the generated number of rotations to perform
type rotationParams struct {
	rotations int // number of rotations to trigger, in [1, 20]
}

func rotationParamsGenerator(values []reflect.Value, rng *rand.Rand) {
	params := rotationParams{
		rotations: 1 + rng.Intn(20), // 1 to 20 rotations
	}
	values[0] = reflect.ValueOf(params)
}

// countRotatedFiles counts files matching the pattern <logPath>.N
func countRotatedFiles(dir, logPath string) int {
	count := 0
	baseName := filepath.Base(logPath)
	for i := 1; i <= 100; i++ { // check up to 100 to detect any overflow
		rotatedName := fmt.Sprintf("%s.%d", baseName, i)
		rotatedPath := filepath.Join(dir, rotatedName)
		if _, err := os.Stat(rotatedPath); err == nil {
			count++
		}
	}
	return count
}
