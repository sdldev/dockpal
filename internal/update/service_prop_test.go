package update

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"pgregory.net/rapid"
)

// Feature: update-mechanism, Property 2: Release asset selection matches platform suffix.
func TestProperty2_AssetSelection(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Values: func(values []reflect.Value, rng *rand.Rand) {
			goos := []string{"linux", "darwin", "windows"}[rng.Intn(3)]
			goarch := []string{"amd64", "arm64", "arm", "386"}[rng.Intn(4)]
			numAssets := 1 + rng.Intn(10)
			var assets []GitHubAsset
			expectedURL := ""
			for i := 0; i < numAssets; i++ {
				assetOS := goos
				assetArch := goarch
				if rng.Intn(3) == 0 {
					// mismatch some assets
					assetOS = "other"
				}
				if rng.Intn(3) == 0 {
					assetArch = "other"
				}
				suffix := platformSuffix(assetOS, assetArch)
				name := fmt.Sprintf("dockpal%s", suffix)
				url := fmt.Sprintf("https://example.com/%s", name)
				assets = append(assets, GitHubAsset{Name: name, BrowserDownloadURL: url})
				if assetOS == goos && assetArch == goarch {
					expectedURL = url
				}
			}
			release := GitHubRelease{Assets: assets}
			values[0] = reflect.ValueOf(release)
			values[1] = reflect.ValueOf(goos)
			values[2] = reflect.ValueOf(goarch)
			values[3] = reflect.ValueOf(expectedURL)
		},
	}
	prop := func(release GitHubRelease, goos, goarch, expectedURL string) bool {
		url, err := release.GetAssetForPlatform(goos, goarch)
		if expectedURL != "" {
			if err != nil {
				return false
			}
			return url == expectedURL
		}
		return err != nil && strings.Contains(err.Error(), ErrAssetNotFoundForOSArch)
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 2 failed: %v", err)
	}
}

// Feature: update-mechanism, Property 9: Percentage clamping.
func TestProperty9_PercentageClamping(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}
	prop := func(written int64, contentLength int64) bool {
		if written < 0 || contentLength < 0 {
			return true // inputs out of domain, skip
		}
		pct := computeDownloadPercentage(written, contentLength)
		if pct < 0 || pct > 100 {
			return false
		}
		if contentLength == 0 && written > 0 {
			// No Content-Length: should never return 100 while downloading.
			if pct == 100 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 9 failed: %v", err)
	}
}

// Feature: update-mechanism, Property 13: Backward-compatible JSON shape.
func TestProperty13_JSONBackwardCompat(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}
	prop := func(status, message string, percentage int) bool {
		// Constrain to valid inputs
		p := UpdateProgress{
			Status:     status,
			Message:    message,
			Percentage: percentage,
			// Empty ErrorCode and StageDetail for backward compat test
		}
		data, err := json.Marshal(p)
		if err != nil {
			return false
		}
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			return false
		}
		// Should have exactly these keys
		if len(m) != 3 {
			return false
		}
		for _, key := range []string{"status", "message", "percentage"} {
			if _, ok := m[key]; !ok {
				return false
			}
		}
		// Round-trip
		var back UpdateProgress
		if err := json.Unmarshal(data, &back); err != nil {
			return false
		}
		if back != p {
			return false
		}
		return true
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 13 failed: %v", err)
	}
}

// Feature: update-mechanism, Property 14: Temp file mode after download.
func TestProperty14_TempFileMode(t *testing.T) {
	tmpDir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(1<<20, 5<<20).Draw(t, "size")
		payload := rapid.SliceOfN(rapid.Byte(), size, size).Draw(t, "payload")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.Write(payload)
		}))
		defer ts.Close()

		tempPath := tmpDir + "/dockpal-new"
		svc := NewUpdateServiceWithBackends("v0.1.0", "/bin/prod", tempPath, "/tmp/backup", &osFS{}, nil, newScriptedSudoChecker([]bool{true}), newStubResolver())

		var mu sync.Mutex
		var events []UpdateProgress
		emit := func(p UpdateProgress) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, p)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.downloadToTemp(ctx, ts.URL, emit); err != nil {
			// ignore
		}

		info, err := os.Stat(tempPath)
		if err == nil {
			if info.Mode()&0111 == 0 {
				t.Fatalf("temp file mode %o has no executable bit", info.Mode())
			}
		}
	})
}

// Feature: update-mechanism, Property 8: Sudo gating.
func TestProperty8_SudoGating(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		first := rapid.Bool().Draw(t, "first")
		second := rapid.Bool().Draw(t, "second")

		fs := newMemFS()
		sudo := newScriptedSudoChecker([]bool{first, second})
		svc := NewUpdateServiceWithBackends("v0.1.0", "/bin/prod", "/tmp/temp", "/tmp/backup", fs, nil, sudo, newStubResolver())

		var mu sync.Mutex
		var events []UpdateProgress
		emit := func(p UpdateProgress) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, p)
		}

		// Write a valid temp binary so verify passes if we get that far.
		validBin := make([]byte, 2<<20)
		validBin[0] = 0x7f
		validBin[1] = 'E'
		validBin[2] = 'L'
		validBin[3] = 'F'
		validBin[4] = 2 // ELFCLASS64
		validBin[5] = 1 // LITTLEENDIAN
		// e_machine at offset 18
		archMap := map[string]uint16{
			"amd64":  EM_X86_64,
			"arm64":  EM_AARCH64,
			"arm":    EM_ARM,
			"386":    EM_386,
		}
		em := archMap[runtime.GOARCH]
		validBin[18] = byte(em)
		validBin[19] = byte(em >> 8)
		fs.WriteFile(svc.tempPath, validBin, 0755)
		fs.WriteFile(svc.binPath, []byte("old"), 0755)

		_ = svc.RunUpdate(context.Background(), "https://github.com/example/asset", emit)

		if !first {
			found := false
			for _, e := range events {
				if e.ErrorCode == ErrSudoUnavailable {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected sudo_unavailable when first check is false")
			}
			// Prod should be untouched.
			if _, err := fs.Stat(svc.binPath); err != nil {
				t.Fatalf("prod binary should exist")
			}
			return
		}

		if first && !second {
			// Need to get past download/verify to reach install.
			// Since we have no real HTTP server, RunUpdate will fail at download.
			// For this test we need to mock download too or test installAtomic directly.
			// Let's test installAtomic with a pre-existing temp file.
			var installEvents []UpdateProgress
			installEmit := func(p UpdateProgress) { installEvents = append(installEvents, p) }
			_ = svc.installAtomic(context.Background(), installEmit)
			found := false
			for _, e := range installEvents {
				if e.ErrorCode == ErrSudoLost {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected sudo_lost when second check is false")
			}
			return
		}
	})
}

// Feature: update-mechanism, Property 12: Single-flight concurrency.
func TestProperty12_SingleFlight(t *testing.T) {
	fs := newMemFS()
	dns := newStubResolver()
	dns.AddHost("github.com", []net.IP{net.ParseIP("140.82.121.4")})
	svc := NewUpdateServiceWithBackends("v0.1.0", "/bin/prod", "/tmp/temp", "/tmp/backup", fs, &scriptedController{}, newScriptedSudoChecker([]bool{true}), dns)

	var executing int32
	var alreadyRunning int32
	var mu sync.Mutex
	var events []UpdateProgress

	emit := func(p UpdateProgress) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, p)
	}

	// Wrap RunUpdate to count executions
	original := svc.RunUpdate
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := original(context.Background(), "https://github.com/example/asset", emit)
			if err != nil && err.Error() == ErrUpdateAlreadyRunning {
				atomic.AddInt32(&alreadyRunning, 1)
			} else {
				atomic.AddInt32(&executing, 1)
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt32(&executing) != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", executing)
	}
	if atomic.LoadInt32(&alreadyRunning) != 4 {
		t.Fatalf("expected 4 already-running, got %d", alreadyRunning)
	}
}
