# Implementation Plan: Rebrand Dockara to Dockpal

## Overview

Rebrand mekanikal dari `dockara` ke `dockpal` dengan satu jalur backward-compat yang sempit (dual-read label container) dan migrasi data otomatis di installer. Implementasi mengikuti urutan node M1→M7 (Go layer), W1→W3 (Web layer), S1→S2 (Service unit + installer), R1 (README), V1→V4 (versioning + release artifacts + verifikasi + git remote) seperti didokumentasikan di section "Migration order / dependency graph" pada `design.md`.

Setiap task menyebutkan path file (relatif terhadap root repo), simbol/baris presis dari Components and Interfaces table, dan requirement IDs yang divalidasi. Urutan task menjamin `go build ./...` lulus pada setiap commit yang masuk akal; `go test ./...` lulus setelah task 7 (Checkpoint).

Bahasa implementasi: **Go 1.25** untuk semua source code, **Bash** untuk `installer.sh` dan `scripts/verify-rebrand.sh`.

## Tasks

- [x] 1. Migrasi Go module path
  - [x] 1.1 Update `go.mod` dan rewrite seluruh import path Go
    - Jalankan `go mod edit -module github.com/sdldev/dockpal` dari root repo
    - Jalankan `find . -type f -name '*.go' -print0 | xargs -0 sed -i 's|github.com/dockara/dockara|github.com/sdldev/dockpal|g'`
    - Jalankan `gofmt -w .` lalu `go mod tidy` untuk sinkronkan `go.sum`
    - Verifikasi `git grep 'github.com/dockara/dockara' -- '*.go' 'go.mod'` mengembalikan kosong
    - Verifikasi `go build ./...` exit code 0
    - _Requirements: 1.1, 1.2, 1.3, 1.5, 1.6, 11.1_

- [x] 2. Update path constants dan environment variables (Layer 2)
  - [x] 2.1 Update `main.go` path constants, env var lookups, dan tambahkan `mustAbs` helper
    - Edit konstanta lines 24-27: `defaultDataDir = "/opt/dockpal/data"`, `defaultDBPath = "/opt/dockpal/data/dockpal.db"`, `defaultLogPath = "/opt/dockpal/data/dockpal.log"` (biarkan `version` di task 14.1)
    - Ganti `os.Getenv("DOCKARA_DATA_DIR")` → `os.Getenv("DOCKPAL_DATA_DIR")` di line 66
    - Ganti `os.Getenv("DOCKARA_LOG_PATH")` → `os.Getenv("DOCKPAL_LOG_PATH")` di line 76
    - Ganti `os.Getenv("DOCKARA_DB_PATH")` → `os.Getenv("DOCKPAL_DB_PATH")` di lines 87 dan 163
    - Tambahkan helper `func mustAbs(name, value string) string` (lihat §Architecture di design.md), panggil setelah resolusi default untuk ketiga path env var
    - Update `os.MkdirAll(dataDir, 0755)` menjadi `0750` per Req 4.9
    - _Requirements: 3.1, 3.2, 3.3, 3.5, 3.6, 3.7, 3.8, 3.9, 4.1, 4.3, 4.4, 4.9, 4.10_

  - [x] 2.2 Property test untuk validasi absolute path env var
    - Refactor `mustAbs` ke package baru `internal/pathvalidate` (atau test main package via subprocess) agar dapat di-PBT-kan
    - Buat file `internal/pathvalidate/path_prop_test.go`
    - **Property 1: Path validation rejects non-absolute env values**
    - Gunakan `testing/quick` dengan `MaxCount: 100`; generator menghasilkan string non-`/`-prefixed
    - **Validates: Requirements 3.9**

  - [x] 2.3 Update default secret file path di `internal/auth/secret.go`
    - Edit konstanta `defaultSecretFilePath` di lines 12 dan 16: `/opt/dockara/data/.secret` → `/opt/dockpal/data/.secret`
    - _Requirements: 4.2_

  - [x] 2.4 Update Traefik config path di `internal/traefik/config.go` dan tambahkan non-existence handling
    - Edit `configPath` di line 43: `/opt/dockara/traefik/dynamic.yml` → `/opt/dockpal/traefik/dynamic.yml`
    - Tambahkan `os.Stat(configPath)` check di consumer site; jika `os.IsNotExist(err)` log peringatan yang menyebut path lalu return tanpa membatalkan startup
    - _Requirements: 4.7, 4.11_

  - [x] 2.5 Update Git repo clone path di `internal/git/deploy.go`
    - Edit `filepath.Join("/opt/dockara/repos", repoName)` di line 21: `/opt/dockara/repos` → `/opt/dockpal/repos`
    - _Requirements: 4.5_

  - [x] 2.6 Update path constants di `internal/docker/compose.go`
    - Replace ketiga occurrence `/opt/dockara/compose` → `/opt/dockpal/compose` (lihat references `writeComposeFile` line 258 dan dua lokasi lain)
    - _Requirements: 4.6_

  - [x] 2.7 Update templates fallback path di `internal/server/routes.go`
    - Edit `os.ReadFile("/opt/dockara/templates.json")` di line 71: `/opt/dockara/templates.json` → `/opt/dockpal/templates.json`
    - _Requirements: 4.8_

- [x] 3. Rename label writes ke namespace `dockpal.*` (Layer 3, write-side)
  - [x] 3.1 Update label writes di `internal/docker/compose.go::createAndStartService`
    - Pada `createAndStartService` (lines 271-282), ubah keempat label menjadi `dockpal.managed=true`, `dockpal.project=<projectName>`, `dockpal.compose=<composePath>`, `dockpal.service=<svcName>` (verbatim dari input)
    - Pastikan tidak ada label `dockara.*` ditulis di write path
    - _Requirements: 5.1_

  - [x] 3.2 Update container name dan labels di `internal/tunnel/cloudflare.go`
    - Edit konstanta `CloudflaredContainer` di line 16: `"dockara-cloudflared"` → `"dockpal-cloudflared"`
    - Pada deploy site (line 53), tulis label `dockpal.managed=true` dan tambahkan label baru `dockpal.tunnel=true` (Req 5.2)
    - _Requirements: 5.2, 5.6_

  - [x] 3.3 Update auto-recover label injection di `internal/server/routes.go`
    - Edit string literal di line 527 (deploy-from-compose flow): `dockara.auto-recover` → `dockpal.auto-recover`
    - _Requirements: 5.3_

  - [x] 3.4 Update error message di `internal/docker/deploy_stream.go`
    - Edit string `Dockara may need elevated privileges` di line 176: `Dockara` → `Dockpal`
    - _Requirements: 12.1_

- [x] 4. Implement dual-read untuk legacy label `dockara.*` (Layer 3, read-side)
  - [x] 4.1 Tambahkan helper `ListContainersWithAnyLabel` di `internal/docker/recovery.go`
    - Tambahkan method baru pada `*Client`: `ListContainersWithAnyLabel(ctx context.Context, labels []string) ([]ContainerInfo, error)` yang memanggil `ListContainersWithLabel` per label dan mendeduplikasi by container ID (lihat snippet di §Architecture design.md)
    - Tempatkan komentar `// LEGACY-DOCKARA: dual-read mendukung container yang ter-deploy oleh Dockara pra-rebrand; akan dihapus pada v0.3.0` di atas helper
    - _Requirements: 5.4, 5.5, 12.2_

  - [x] 4.2 Property test untuk dual-label union dengan deduplikasi
    - Extend `internal/docker/recovery_prop_test.go` dengan in-memory fake `Client` yang implement subset moby `ContainerList`
    - **Property 3: Dual-label reads return unique union of containers**
    - Gunakan `testing/quick` dengan `MaxCount ≥ 200`; generator menghasilkan set container dengan kombinasi label `{∅, labelA, labelB, both}`
    - Tandai fixture yang menggunakan `dockara.auto-recover=true` dengan `// LEGACY-DOCKARA: PBT exercise of dual-read backward-compat path`
    - **Validates: Requirements 5.4, 5.5**

  - [x] 4.3 Update `HealthMonitor.check` di `internal/docker/recovery.go` untuk dual-read
    - Update komentar di line 13 agar menjelaskan dual-read
    - Pada line 57 ganti pemanggilan `ListContainersWithLabel(ctx, "dockara.auto-recover=true")` menjadi `c.ListContainersWithAnyLabel(ctx, []string{"dockpal.auto-recover=true", "dockara.auto-recover=true"})`
    - Tempatkan `// LEGACY-DOCKARA: backward-compat read untuk container Dockara pra-rebrand` di atas pemanggilan
    - _Requirements: 5.4, 12.2_

  - [x] 4.4 Property test untuk HealthMonitor scan tanpa duplikasi
    - Extend `internal/docker/recovery_prop_test.go`
    - **Property 4: HealthMonitor scan evaluates each eligible container exactly once**
    - Gunakan `testing/quick` dengan `MaxCount ≥ 100`; generator container set di mana eligible subset `E` memiliki kombinasi label
    - Tandai legacy fixture dengan `// LEGACY-DOCKARA: ...`
    - **Validates: Requirements 5.4**

  - [x] 4.5 Update `StopCompose` dan `RemoveCompose` di `internal/docker/compose.go` untuk dual-read
    - Pada `StopCompose` (lines 399-403): ganti single filter `dockara.project=<projectName>` menjadi dua panggilan `ContainerList` (satu untuk `dockpal.project`, satu untuk `dockara.project`) lalu dedup by container ID sebelum `ContainerStop`
    - Pada `RemoveCompose` (lines 417-429): pola sama untuk `ContainerRemove`
    - Tempatkan komentar `// LEGACY-DOCKARA: dual-read backward-compat untuk container Dockara pra-rebrand; akan dihapus pada v0.3.0` di atas tiap dual-filter loop
    - _Requirements: 5.5, 12.2_

  - [x] 4.6 Property test untuk StopCompose/RemoveCompose unique union
    - Extend `internal/docker/compose_prop_test.go` dengan in-memory fake Docker client
    - **Property 5: StopCompose/RemoveCompose target unique union by project label**
    - Gunakan `testing/quick` dengan `MaxCount ≥ 100` per direction (Stop dan Remove sebagai sub-tests `t.Run`)
    - Tandai legacy fixture dengan `// LEGACY-DOCKARA: ...`
    - **Validates: Requirements 5.5**

  - [x] 4.7 Property test untuk container label writes verbatim
    - Extend `internal/docker/compose_prop_test.go`
    - **Property 2: Container label writes preserve input verbatim**
    - Gunakan `testing/quick` dengan `MaxCount ≥ 100`; generator string ASCII-printable non-kosong untuk `(projectName, svcName, composePath)`
    - Assert: container hasil `createAndStartService` memiliki tepat keempat label `dockpal.*` dengan value verbatim dan TIDAK memiliki label `dockara.*`
    - **Validates: Requirements 5.1, 5.2, 5.3, 5.6**

- [x] 5. Update CLI display strings dan startup logs (Layer 1, M5)
  - [x] 5.1 Update banner, help text, dan log strings di `main.go`
    - Replace seluruh occurrence `Dockara` / `dockara` di string literal output pada lines 33-58 (banner, help, subcommand listing) dan line 152 (startup log) menjadi `Dockpal` / `dockpal`
    - Update pesan sukses `resetPassword` agar berisi substring `Dockpal` (mis. `fmt.Println("Dockpal: password reset successfully")` per Req 2.5)
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5_

  - [x] 5.2 Update startup log di `internal/server/server.go`
    - Replace string `Dockara` di line 46 menjadi `Dockpal`
    - _Requirements: 2.4_

- [x] 6. Update test fixtures dan assertions non-PBT (M6)
  - [x] 6.1 Update assertions di test files non-PBT
    - Cari dan ganti `dockara` / `Dockara` → `dockpal` / `Dockpal` di file: `internal/auth/secret_test.go`, `internal/server/routes_test.go`, `internal/server/ratelimit_test.go`, `internal/docker/compose_test.go`, `internal/docker/fileops_test.go`, `internal/auth/jwt_prop_test.go`, `internal/server/ratelimit_prop_test.go`, `internal/docker/fileops_property_test.go`, `internal/server/routes_property_test.go`, `internal/logging/rotator_prop_test.go`, `internal/traefik/config_prop_test.go`, `internal/tunnel/cloudflare_prop_test.go`, `internal/validator/validator_prop_test.go`
    - Untuk fixture yang sengaja menguji jalur backward-compat (mis. setup container dengan label `dockara.*`), tambahkan komentar `// LEGACY-DOCKARA: <reason>`
    - File `recovery_prop_test.go` dan `compose_prop_test.go` sudah ditangani di task 4.2/4.4 dan 4.6/4.7 — skip di sini
    - _Requirements: 11.1, 11.5, 11.6, 12.2_

- [x] 7. Checkpoint - Verifikasi Go build dan test suite
  - Jalankan `go build ./...`, `go vet ./...`, dan `go test ./...` dari root repo
  - Pastikan exit code 0 untuk ketiganya dan tidak ada baris `--- FAIL` atau `FAIL` di output
  - Ensure all tests pass, ask the user if questions arise.

- [x] 8. Rebrand web HTML (W1)
  - [x] 8.1 Update tag `<title>` di `web/index.html`
    - Edit line 7: `<title>Dockara</title>` → `<title>Dockpal</title>`
    - _Requirements: 6.1_

  - [x] 8.2 Update heading login di `web/partials/login.html`
    - Edit `<h1>Dockara</h1>` di line 8: `Dockara` → `Dockpal`
    - _Requirements: 6.2_

  - [x] 8.3 Update brand sidebar di `web/partials/sidebar.html`
    - Edit `<span>Dockara</span>` di line 11: `Dockara` → `Dockpal`
    - _Requirements: 6.3_

  - [x] 8.4 Update copy template-config di `web/pages/template-config.html`
    - Edit teks di line 183: `Dockara will automatically restart` → `Dockpal will automatically restart`
    - _Requirements: 6.9_

- [x] 9. Rebrand web JavaScript namespace (W2)
  - [x] 9.1 Replace `window.Dockara`, `Dockara.*`, `dockaraApp`, dan `'dockara_token'` di seluruh module JS
    - Jalankan `find web -name '*.js' -print0 | xargs -0 sed -i -e 's/window\.Dockara/window.Dockpal/g' -e 's/Dockara\./Dockpal./g' -e "s/'dockara_token'/'dockpal_token'/g" -e 's/dockaraApp/dockpalApp/g'`
    - Mencakup `web/assets/app.js` dan 12 modul di `web/assets/modules/*.js` (auth, charts, computed, containers, dashboard, domains, files, images, services, state, templates, ui)
    - Verifikasi `grep -ri 'dockara' web/assets/` mengembalikan kosong
    - _Requirements: 6.4, 6.5, 6.7, 6.8_

- [x] 10. Wire web entry point (W3)
  - [x] 10.1 Update atribut `x-data` di `web/index.html`
    - Edit line 14: `x-data="dockaraApp()"` → `x-data="dockpalApp()"`
    - _Requirements: 6.6_

- [x] 11. Rename systemd service unit (S1)
  - [x] 11.1 Move dan edit `dockara.service` → `dockpal.service`
    - Jalankan `git mv dockara.service dockpal.service` dari root repo
    - Replace seluruh konten dengan template baru: `Description=Dockpal — Docker Management Platform`, `WorkingDirectory=/opt/dockpal`, `ExecStart=/usr/local/bin/dockpal server` (verbatim sesuai §Architecture design.md)
    - Verifikasi `grep -i dockara dockpal.service` mengembalikan kosong
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.6_

- [x] 12. Rewrite installer dengan Fresh + Upgrade paths (S2)
  - [x] 12.1 Implement Go-side conflict detection di `internal/installer/conflict.go`
    - Buat package `internal/installer` dengan file `conflict.go`
    - Export `func DetectConflicts(srcRoot, dstRoot string) (path, reason string, conflict bool, err error)` yang walk `srcRoot` dengan `filepath.WalkDir`, bandingkan setiap entry dengan counterpart di `dstRoot`: type mismatch (file vs dir) atau byte-content mismatch via `bytes.Equal` setelah `os.ReadFile`
    - Gunakan `os.Lstat` untuk type check; skip path yang absent di dst (no conflict)
    - _Requirements: 9.9_

  - [x] 12.2 Property test untuk conflict detection
    - Buat file `internal/installer/conflict_prop_test.go`
    - **Property 6: Conflict detection identifies all type/content mismatches**
    - Generator: synthetic file tree pair `(srcTree, dstTree)` sebagai map `path → {type, content}` materialized ke `t.TempDir()` lalu invoke `DetectConflicts`
    - Gunakan `testing/quick` dengan `MaxCount ≥ 200`
    - **Validates: Requirements 9.9**

  - [x] 12.3 Rewrite `installer.sh` dengan state machine Fresh + Upgrade
    - Replace ulang `installer.sh` dengan struktur: `detect_arch`, `install_dependencies` (tambahkan `jq` ke daftar deps), `install_docker`, `is_dockara_install`, `is_dockpal_install`, `migrate_cloudflared`, `migrate_data`, `detect_conflicts`, `download_binary`, `setup_systemd`, `main`
    - Update header config: `REPO="sdldev/dockpal"`, `VERSION="${DOCKPAL_VERSION:-latest}"`, `BINARY="$INSTALL_DIR/dockpal"`, `DATA_DIR="/opt/dockpal"`
    - Update download URL ke `https://github.com/sdldev/dockpal/releases/.../dockpal-linux-<arch>` dengan retry 3x dan timeout 60s per attempt
    - Implement Fresh path: `setup_directories` → `download_binary` → `setup_systemd` (write `/etc/systemd/system/dockpal.service` dengan heredoc berisi konten dari `dockpal.service`) → `daemon-reload` → `enable dockpal` → `start dockpal`
    - Implement Upgrade-in-place path: `systemctl stop dockpal` → `download_binary` → restart
    - Implement Upgrade-from-Dockara path:
      - `timeout 30 systemctl stop dockara` (exit 3 jika gagal/timeout, Req 9.2)
      - `migrate_cloudflared`: `docker inspect dockara-cloudflared --format '{{json .Config.Cmd}}' | jq -r '.[] | select(test("^[A-Za-z0-9._-]{30,}$"))'` untuk extract token, lalu `docker stop`, `docker rm`, `docker run --name dockpal-cloudflared --restart unless-stopped --label dockpal.managed=true --label dockpal.tunnel=true cloudflare/cloudflared:latest tunnel --no-autoupdate run --token <TOKEN>` (exit 4 jika gagal, simpan token ke `/opt/dockpal/.tunnel-token-recovery`, Req 5.7-5.8)
      - `migrate_data`: panggil `detect_conflicts /opt/dockara /opt/dockpal` (exit 5 jika konflik, Req 9.9); jika `/opt/dockpal/` tidak ada gunakan `mv /opt/dockara /opt/dockpal`; jika ada gunakan `rsync -a --ignore-existing /opt/dockara/ /opt/dockpal/` lalu `rm -rf /opt/dockara`
      - Rename file kunci di tujuan: `/opt/dockpal/data/dockara.db → dockpal.db`, `/opt/dockpal/data/dockara.log → dockpal.log`, dan pattern `dockara.log.* → dockpal.log.*` (jika file tidak ada, lanjut tanpa error per Req 9.5)
      - `rm -f /usr/local/bin/dockara` (Req 9.6)
      - `systemctl disable dockara` → `rm /etc/systemd/system/dockara.service` → `systemctl daemon-reload` (sekuensial, exit 1 jika ada step gagal, Req 9.7)
      - Lanjut ke Fresh path (download → install → start `dockpal.service`)
    - Tambahkan `# LEGACY-DOCKARA: deteksi/migrasi instalasi Dockara pra-rebrand` di atas tiap referensi `dockara.service`, `/opt/dockara`, `/usr/local/bin/dockara`, dan `dockara-cloudflared`
    - Dokumentasikan exit codes (1, 3, 4, 5, 6, 7, 8) di komentar header skrip
    - _Requirements: 3.4, 3.10, 5.7, 5.8, 5.9, 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, 8.7, 8.8, 8.9, 8.10, 8.11, 9.1, 9.2, 9.3, 9.4, 9.5, 9.6, 9.7, 9.8, 9.9, 9.10, 12.2_

- [x] 13. Rewrite README (R1)
  - [x] 13.1 Rewrite `README.md` dengan brand Dockpal dan section migrasi
    - Replace H1 menjadi `# 🐳 Dockpal`
    - Update quick install URL ke `https://raw.githubusercontent.com/sdldev/dockpal/main/installer.sh`
    - Update `git clone` instruction ke `https://github.com/sdldev/dockpal.git`, build output `dockpal`, install ke `/usr/local/bin/dockpal`
    - Update tabel env var: `DOCKPAL_DATA_DIR` (default `/opt/dockpal/data`), `DOCKPAL_DB_PATH` (default `<DATA_DIR>/dockpal.db`), `DOCKPAL_LOG_PATH` (default `<DATA_DIR>/dockpal.log`); JWT secret path ke `/opt/dockpal/data/.secret`
    - Update reset password command ke `dockpal reset-password`
    - Update systemd snippet ke `dockpal.service` + `systemctl enable --now dockpal`
    - Update label auto-recover dokumentasi ke `dockpal.auto-recover=true`
    - Update backup path ke `/opt/dockpal/data/`
    - Update project structure tree: nama root `dockpal/`
    - Tambahkan section `## Upgrading from Dockara` setelah Quick Install yang menjelaskan: (a) installer melakukan migrasi otomatis dari `/opt/dockara/` ke `/opt/dockpal/`, (b) installer mengganti unit systemd dari `dockara.service` ke `dockpal.service`, (c) installer menghapus biner lama `/usr/local/bin/dockpal`
    - Verifikasi pencarian `(?i)dockara` di README hanya menghasilkan match di section "Upgrading from Dockara"
    - _Requirements: 10.1, 10.2, 10.3, 10.4, 10.5, 10.6, 10.7, 10.8, 10.9_

- [x] 14. Bump version (V1)
  - [x] 14.1 Update konstanta `version` di `main.go`
    - Edit konstanta `version = "0.1.0"` di line 27 → `version = "0.2.0"`
    - _Requirements: 13.1_

- [x] 15. Update release workflow (V2)
  - [x] 15.1 Update `.github/workflows/release.yml`
    - Set `go-version` dari `'1.22'` ke `'1.25'` (konsisten dengan `go.mod` line `go 1.25.0`)
    - Rename ketiga build output: `dockpal-linux-amd64`, `dockpal-linux-arm64`, `dockpal-linux-armv7`
    - Update upload `files:` glob `dockara-linux-*` → `dockpal-linux-*`
    - _Requirements: 7.7, 7.8, 7.9, 13.2, 13.3_

- [x] 16. Static rebrand verification dan baseline PBT (V3)
  - [x] 16.1 Buat `scripts/verify-rebrand.sh`
    - Buat skrip Bash `set -euo pipefail` yang menjalankan langkah berikut, fail-fast pada non-zero exit:
      1. `go build ./...` (Req 1.3, 1.5)
      2. `go vet ./...` (Req 11.4)
      3. Assert `git ls-files dockara | wc -l` == 0 (Req 1.4)
      4. `! rg 'github.com/dockara/dockara' --type go` (Req 1.2, 11.1)
      5. `! rg DOCKARA_ -- ':!*.kiro/*' ':!*.auto-claude/*' ':!*.playwright-mcp/*'` (Req 12.5)
      6. `! rg -i 'dockara' web/` (Req 6.4 dst, 12.4)
      7. Cari match `(?i)dockara` di Production_Source (`*.go`, `installer.sh`, `*.service`, `.github/workflows/*.yml`, `templates/*`); untuk setiap match, validasi (a) match berasal dari Allowlist file (`installer.sh`, `internal/docker/recovery.go`, `internal/docker/compose.go`), dan (b) ada komentar `LEGACY-DOCKARA:` di baris yang sama atau baris di atasnya (Req 12.1, 12.2, 12.3, 12.6)
      8. README isolation: ekstrak section antara heading `## Upgrading from Dockara` dan heading `## ` berikutnya; assert match `(?i)dockara` di `README.md` HANYA berada di dalam section tersebut (Req 10.9)
    - `chmod +x scripts/verify-rebrand.sh`
    - _Requirements: 1.4, 11.1, 11.4, 12.1, 12.2, 12.3, 12.4, 12.5, 12.6, 10.9_

  - [x] 16.2 Capture baseline PBT iteration counts
    - Buat `scripts/pbt-baseline.txt` yang merekam value `MaxCount` per file `*_prop_test.go` dan `*_property_test.go` (gunakan `grep -rn 'MaxCount' internal/`)
    - File ini menjadi referensi Req 11.3 — verifikasi (manual atau di CI) bahwa nilai post-rebrand ≥ baseline
    - _Requirements: 11.3_

- [x] 17. Final checkpoint - verifikasi rebrand selesai
  - Jalankan `bash scripts/verify-rebrand.sh`
  - Jalankan `go build ./...`, `go test ./...`, `go vet ./...` dari root repo
  - Verifikasi binary build: `go build -o dockpal . && ./dockpal version` menghasilkan `Dockpal v0.2.0`
  - Ensure all tests pass, ask the user if questions arise.

- [x] 18. Konfigurasi Git remote (V4)
  - [x] 18.1 Set remote `origin` ke repository `sdldev/dockpal`
    - Jalankan `git remote set-url origin https://github.com/sdldev/dockpal.git`
    - Verifikasi `git remote get-url origin` mengembalikan persis `https://github.com/sdldev/dockpal.git`
    - Pastikan tidak ada remote lain yang URL-nya mengandung substring `dockara` (Req 14.3)
    - _Requirements: 14.1, 14.2, 14.3_

## Notes

- Tasks bertanda `*` (misal `2.2`, `4.2`) adalah optional property-based tests yang dapat di-skip untuk faster MVP. Task non-`*` adalah core implementation dan harus dieksekusi.
- Setiap task referensi requirement spesifik (granular sub-criteria) untuk traceability sesuai Req 12.
- Urutan task dirancang agar `go build ./...` lulus pada setiap commit setelah task selesai. `go test ./...` lulus penuh setelah Checkpoint 7 (test fixtures sudah di-update).
- Task 4.x (dual-read) memerlukan task 3.x selesai duluan agar label baru `dockpal.*` sudah dapat ditulis dan testable bersamaan dengan legacy `dockara.*`.
- Task 12.3 (installer rewrite) memerlukan task 11.1 (service unit baru) sebagai sumber konten heredoc, dan task 12.1 (Go-side conflict detection) sebagai referensi logika untuk impl Bash.
- Task 16.1 (verify script) memerlukan semua `LEGACY-DOCKARA:` markers sudah ditempatkan di task 4.x, 6.1, dan 12.3.
- Task 18.1 (git remote) dilakukan paling akhir agar `git push` rebrand artefak masuk ke repository baru `sdldev/dockpal`.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "2.3", "2.4", "2.5", "2.6", "2.7"] },
    { "id": 2, "tasks": ["2.2", "3.1", "3.2", "3.3", "3.4", "4.1", "5.1", "5.2", "8.1", "8.2", "8.3", "8.4", "9.1", "11.1", "12.1", "13.1"] },
    { "id": 3, "tasks": ["4.2", "4.3", "4.5", "4.7", "10.1", "12.2", "12.3", "14.1", "15.1", "16.2"] },
    { "id": 4, "tasks": ["4.4", "4.6", "6.1", "16.1"] },
    { "id": 5, "tasks": ["18.1"] }
  ]
}
```
