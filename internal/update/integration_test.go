package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

// Test_SmokeDownloadAndVerify_Integration runs download plus verify end-to-end (skip install).
func Test_SmokeDownloadAndVerify_Integration(t *testing.T) {
	// Build a valid ELF binary for current GOARCH
	bin := make([]byte, 2<<20)
	bin[0] = 0x7f
	bin[1] = 'E'
	bin[2] = 'L'
	bin[3] = 'F'
	bin[4] = 2
	bin[5] = 1
	archMap := map[string]uint16{
		"amd64": EM_X86_64,
		"arm64": EM_AARCH64,
		"arm":   EM_ARM,
		"386":   EM_386,
	}
	em := archMap[runtime.GOARCH]
	if em == 0 {
		em = EM_X86_64
	}
	bin[18] = byte(em)
	bin[19] = byte(em >> 8)

	sha := sha256OfBytes(bin)
	assetName := assetNameForPlatform()
	checksumContent := fmt.Sprintf("%s  %s\n", sha, assetName)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(bin)))
			w.Write(bin)
		case "/checksums.txt":
			w.Write([]byte(checksumContent))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	tempPath := tmpDir + "/dockpal-new"
	binPath := tmpDir + "/dockpal"
	backupPath := tmpDir + "/dockpal.bak"
	svc := NewUpdateServiceWithBackends("v0.1.0", binPath, tempPath, backupPath, &osFS{}, nil, newScriptedSudoChecker([]bool{true}), newStubResolver())

	emit := func(p UpdateProgress) {}
	ctx := context.Background()
	if err := svc.downloadToTemp(ctx, ts.URL+"/"+assetName, emit); err != nil {
		t.Fatalf("download failed: %v", err)
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		t.Fatalf("temp file missing: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Fatalf("temp file not executable: %o", info.Mode())
	}
	if info.Size() < MinBinarySize || info.Size() > MaxBinarySize {
		t.Fatalf("size %d out of range", info.Size())
	}

	// Verify checksum
	if err := svc.verifyTempBinary(ctx, assetName, ts.URL+"/checksums.txt", emit); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

// Test_SmokeAssetNotFound_Integration asserts error when no matching asset exists.
func Test_SmokeAssetNotFound_Integration(t *testing.T) {
	release := GitHubRelease{
		Assets: []GitHubAsset{
			{Name: "dockpal-darwin-amd64", BrowserDownloadURL: "https://example.com/darwin"},
			{Name: "dockpal-windows-amd64", BrowserDownloadURL: "https://example.com/windows"},
		},
	}
	_, err := release.GetAssetForPlatform("linux", "amd64")
	if err == nil {
		t.Fatalf("expected error for missing asset")
	}
	if !strings.Contains(err.Error(), ErrAssetNotFoundForOSArch) {
		t.Fatalf("expected %s, got %v", ErrAssetNotFoundForOSArch, err)
	}
}

func sha256OfBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
