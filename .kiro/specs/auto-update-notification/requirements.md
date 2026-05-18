# Requirements Document: Auto-Update Notification

## Introduction

Feature ini memungkinkan pengguna Dockpal untuk menerima notifikasi ketika ada versi baru tersedia, dan dapat melakukan update langsung dari UI tanpa menggunakan command line.

## Requirements

### Requirement 1: Version Check API

**User Story:** Sebagai sistem, saya ingin memeriksa versi terbaru dari GitHub releases secara otomatis.

#### Acceptance Criteria

1. THE Dockpal_Server SHALL expose endpoint `GET /api/system/version` yang mengembalikan JSON dengan:
   - `currentVersion`: string (version saat ini, misal "v0.2.0")
   - `latestVersion`: string (versi terbaru dari GitHub API)
   - `updateAvailable`: boolean (true jika latest > current)
   - `releaseNotes`: string (release notes dari GitHub, jika ada)
   - `downloadUrl`: string (URL untuk download binary)

2. WHEN endpoint dipanggil, THE Dockpal_Server SHALL melakukan HTTP GET ke `https://api.github.com/repos/sdldev/dockpal/releases/latest`

3. IF request ke GitHub API gagal (network error, rate limit), THE system SHALL return cached version info dari last successful check dengan flag `updateAvailable: null` (unknown)

4. THE endpoint SHALL cache hasil dari GitHub API selama 1 jam untuk mengurangi rate limit

### Requirement 2: Background Version Checker

**User Story:** Sebagai sistem, saya ingin memeriksa update secara berkala di background.

#### Acceptance Criteria

1. WHEN Dockpal_Server starts, THE system SHALL start background goroutine yang memeriksa versi terbaru setiap 6 jam

2. THE background checker SHALL menyimpan hasil check ke file cache di `<DATA_DIR>/version-cache.json` dengan format:
   ```json
   {
     "lastChecked": "2026-05-19T12:00:00Z",
     "latestVersion": "v0.2.1",
     "releaseNotes": "Bug fixes",
     "downloadUrl": "..."
   }
   ```

3. IF cached data exists, THE system SHALL serve version info dari cache pada startup sebelum first background check completes

### Requirement 3: UI Notification

**User Story:** Sebagai pengguna, saya ingin melihat notifikasi ketika ada versi baru tersedia.

#### Acceptance Criteria

1. WHEN pengguna login dan `updateAvailable: true`, THE UI SHALL menampilkan banner notification di bagian atas dengan:
   - teks: "Version vX.Y.Z available"
   - tombol: "Update Now" atau "Dismiss"

2. THE notification SHALL muncul di semua halaman (persistent header)

3. IF pengguna klik "Dismiss", THE notification SHALL disembunyikan untuk session ini (simpan ke localStorage)

4. IF pengguna klik "Update Now", THE system SHALL mendownload dan install versi baru

### Requirement 4: Update Execution

**User Story:** Sebagai pengguna, saya ingin melakukan update dengan satu klik dari UI.

#### Acceptance Criteria

1. WHEN pengguna klik "Update Now", THE UI SHALL:
   - Tampilkan loading indicator
   - Disable tombol selama proses
   - Show progress (downloading → installing → restarting)

2. THE update process SHALL:
   - Download binary dari GitHub release ke `/tmp/dockpal-new`
   - Replace `/usr/local/bin/dockpal` dengan binary baru
   - Restart dockpal service via `systemctl restart dockpal`

3. IF update berhasil, THE UI SHALL show success message dan reload halaman

4. IF update gagal, THE UI SHALL show error message dengan alasan (network error, permission denied, dll)

5. THE user yang melakukan update SHALL memiliki akses root (sudo) - jika tidak, show error "Update requires root privileges"

### Requirement 5: Security

**User Story:** Sebagai security, saya ingin memastikan update tidak bisa dieksekusi oleh sembarang user.

#### Acceptance Criteria

1. THE update API endpoint SHALL require authentication (harus login sebagai admin)

2. THE system SHALL verifybahwa current user memiliki sudo privileges sebelum eksekusi update

3. THE downloaded binary SHALL di-verify (ukuran minimal, executable bit) sebelum di-install

## Technical Notes

- Go version check: gunakan `semver` atau parsing sederhana
- GitHub API: `https://api.github.com/repos/sdldev/dockpal/releases/latest`
- Cache file: `<DATA_DIR>/version-cache.json`
- UI component: Toast notification di header
- Update script: implement di backend, UI hanya memanggil API