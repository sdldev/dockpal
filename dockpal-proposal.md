# Proposal Strategis & Teknis untuk Dockpal
**Dari sudut pandang: Investor & Senior Engineer**
**Tanggal: 20 Mei 2026**
**Versi yang dianalisis: v0.4.0**

---

> Dokumen ini disusun berdasarkan analisis mendalam terhadap repository `sdldev/dockpal` — mencakup arsitektur, kualitas kode, potensi bisnis, dan roadmap pengembangan. Tujuannya satu: membantu Dockpal tumbuh dari tool homelab yang bagus menjadi platform infra yang dipercaya tim engineering profesional.

---

## Daftar Isi

1. [Ringkasan Eksekutif](#1-ringkasan-eksekutif)
2. [Penilaian Teknis Kode](#2-penilaian-teknis-kode)
3. [Kelemahan & Risiko Saat Ini](#3-kelemahan--risiko-saat-ini)
4. [Rekomendasi Investor](#4-rekomendasi-investor)
5. [Rekomendasi Engineer](#5-rekomendasi-engineer)
6. [Roadmap 12 Bulan](#6-roadmap-12-bulan)
7. [Model Bisnis yang Direkomendasikan](#7-model-bisnis-yang-direkomendasikan)
8. [Kesimpulan](#8-kesimpulan)

---

## 1. Ringkasan Eksekutif

Dockpal adalah platform manajemen Docker berbasis single binary yang ditulis dalam Go. Dengan filosofi **zero external dependency** dan **embedded UI**, Dockpal berhasil menawarkan pengalaman deployment yang sangat sederhana: satu perintah `curl | bash`, langsung jalan.

### Kekuatan Utama

- Single binary, zero dependency — instalasi paling sederhana di kelasnya
- Codebase Go yang bersih dan modular dengan struktur `internal/` yang baik
- Fitur lengkap untuk skala personal dan tim kecil (monitoring, deploy, log, tunnel)
- 11 rilis dalam waktu singkat — menunjukkan momentum pengembangan yang aktif
- Enkripsi credential dengan AES-256-GCM — fondasi keamanan yang benar

### Kesimpulan Awal

**Dockpal punya DNA yang tepat.** Fondasi teknis solid, filosofi produk jelas, dan ada kebutuhan pasar yang nyata. Namun ada gap signifikan antara kondisi saat ini dan standar yang dibutuhkan untuk dipakai di lingkungan production tim profesional. Gap ini bisa ditutup — dokumen ini menjelaskan caranya.

---

## 2. Penilaian Teknis Kode

### 2.1 Scorecard

| Aspek | Skor | Status |
|---|---|---|
| Struktur & organisasi package | 82/100 | Baik |
| Naming convention & readability | 80/100 | Baik |
| Separation of concerns | 78/100 | Baik |
| Error handling | 70/100 | Cukup |
| Credential & enkripsi | 80/100 | Baik |
| Input validation | 75/100 | Baik |
| JWT implementation | 72/100 | Cukup |
| Context propagation | 65/100 | Perlu perbaikan |
| Interface & abstraksi | 60/100 | Perlu perbaikan |
| Dependency injection | 40/100 | Lemah |
| Unit test coverage | 15/100 | Kritis |
| Integration test | 10/100 | Kritis |
| Testability arsitektur | 30/100 | Kritis |
| Systemd hardening | 15/100 | Kritis |
| Permission model (RBAC) | 20/100 | Kritis |
| Dokumentasi kode (godoc) | 25/100 | Lemah |
| **Overall** | **67/100** | **Solid untuk personal, belum production-grade** |

### 2.2 Yang Sudah Benar

**Struktur package idiomatik.** Penggunaan `internal/` dengan sub-package per domain (auth, docker, server, traefik, tunnel, registry, git, update) mengikuti Go project layout standar. Tiap package punya satu tanggung jawab yang terdefinisi jelas.

**Graceful shutdown diimplementasi dengan benar.** Pattern `signal.Notify` + `context.WithCancel` + sequential shutdown (scheduler → server) adalah cara yang tepat dan sering diabaikan developer pemula.

**Log rotation mandiri.** Rotasi log di 2MB dengan retensi 5 file adalah keputusan pragmatis yang baik — tidak bergantung tool eksternal, bekerja otomatis.

**Enkripsi AES-256-GCM untuk credentials.** Ini cipher yang tepat: authenticated encryption yang melindungi dari tampering, bukan sekadar enkripsi biasa.

**Dev workflow dengan reflex.** Setup file-watching yang rapi mempersingkat siklus develop-test secara signifikan.

### 2.3 Masalah Teknis yang Ditemukan

#### Masalah 1: Tidak Ada Interface Boundary — Kode Tidak Testable

`dockerClient` adalah concrete struct dari Moby SDK yang langsung diinjeksi ke route handler. Untuk menulis unit test pada handler, kamu butuh Docker daemon nyata yang berjalan.

```go
// ❌ Kondisi sekarang — tidak testable tanpa Docker daemon
func RegisterRoutes(r *gin.Engine, client *docker.Client, ...) { ... }

// ✅ Seharusnya — mockable dengan interface
type ContainerManager interface {
    ListContainers(ctx context.Context) ([]types.Container, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string, timeout *int) error
    GetContainerLogs(ctx context.Context, id string) (io.ReadCloser, error)
}

func RegisterRoutes(r *gin.Engine, mgr ContainerManager, ...) { ... }
```

Tanpa interface ini, test coverage yang bermakna hampir tidak mungkin dicapai tanpa menulis ulang arsitektur.

#### Masalah 2: Version String Hardcoded di Dua Tempat

Di `main.go` ditemukan `version = "0.3.3"` sementara GitHub releases sudah di `v0.4.0`. Ini tanda tidak ada single source of truth.

```go
// ❌ Kondisi sekarang
const version = "0.3.3"

// ✅ Seharusnya — inject saat build
var version = "dev" // di-override saat build

// Build command:
// go build -ldflags "-X main.version=$(git describe --tags --always)" -o dockpal .
```

#### Masalah 3: `log.Fatalf` Terlalu Agresif — Tidak Testable

```go
// ❌ Kondisi sekarang — mematikan proses, tidak bisa di-test
if err := database.EnsureDefaultAdmin(string(defaultHash)); err != nil {
    log.Fatalf("Failed to create default admin: %v", err)
}

// ✅ Seharusnya — return error, biarkan caller memutuskan
func initServer(cfg Config) error {
    if err := database.EnsureDefaultAdmin(hash); err != nil {
        return fmt.Errorf("create default admin: %w", err)
    }
    return nil
}
```

#### Masalah 4: Dua Library WebSocket Dipakai Sekaligus

Di `go.mod` ditemukan duplikasi:

```
github.com/gorilla/websocket v1.5.3
nhooyr.io/websocket v1.8.17
```

Ini menambah binary size dan menandakan inkonsistensi internal. Harus dipilih satu dan distandarisasi di seluruh codebase.

#### Masalah 5: Systemd Service Berjalan sebagai Root Tanpa Hardening

```ini
# ❌ dockpal.service sekarang
User=root
Group=root
# Tidak ada security directive apapun

# ✅ Seharusnya
User=dockpal
Group=docker
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/dockpal
CapabilityBoundingSet=
```

Menjalankan server HTTP sebagai `root` penuh tanpa pembatasan adalah risiko keamanan yang tidak perlu — jika ada RCE, attacker langsung mendapat root shell.

#### Masalah 6: Context Tidak Selalu Diteruskan ke Docker SDK

Request context dari Gin tidak konsisten diteruskan ke Docker SDK call. Ini berarti Docker operation tidak bisa di-cancel saat client disconnect, berpotensi menyebabkan goroutine leak pada operasi long-running.

```go
// ❌ Context hilang
client.ContainerStart(context.Background(), id, ...)

// ✅ Gunakan request context
client.ContainerStart(c.Request.Context(), id, ...)
```

---

## 3. Kelemahan & Risiko Saat Ini

### 3.1 Risiko Keamanan

| Risiko | Tingkat | Penjelasan |
|---|---|---|
| Single admin tanpa RBAC | **Kritis** | Tidak ada pembagian izin — semua user bisa lakukan semua hal |
| HTTP only, no native HTTPS | **Tinggi** | JWT token dan credential bisa disadap tanpa proxy |
| `User=root` di systemd | **Tinggi** | RCE = full root access ke server |
| Auto-update tanpa rollback | **Sedang** | Update gagal = downtime tanpa jalan kembali |
| Tidak ada audit log | **Sedang** | Tidak bisa trace siapa melakukan apa |

### 3.2 Risiko Engineering

| Risiko | Tingkat | Penjelasan |
|---|---|---|
| Test coverage ~0% | **Kritis** | Setiap perubahan bisa memecah sesuatu tanpa diketahui |
| Tidak ada interface untuk mock | **Tinggi** | Penambahan fitur semakin sulit di-test |
| BBolt tidak bisa di-query | **Sedang** | Fitur search/filter kompleks akan menjadi bottleneck |
| Version hardcoded | **Sedang** | Rawan human error saat release |

### 3.3 Risiko Bisnis

| Risiko | Tingkat | Penjelasan |
|---|---|---|
| Tidak ada multi-user | **Kritis** | Tidak bisa masuk ke market team/enterprise |
| Positioning tidak jelas vs Portainer | **Tinggi** | Tanpa differentiator kuat, sulit bersaing |
| 0 stars, 0 forks | **Sedang** | Komunitas belum terbentuk, traction minim |
| Dokumentasi sangat minimal | **Sedang** | Barrier masuk untuk contributor dan pengguna baru tinggi |

---

## 4. Rekomendasi Investor

### 4.1 Thesis Investasi

Dockpal bermain di segmen yang **underserved**: tim engineering kecil (5–30 orang), startup, dan agency yang mengelola puluhan VPS. Portainer terlalu berat dan kompleks untuk mereka; tools sederhana seperti Lazydocker tidak punya UI web. **Dockpal mengisi celah ini dengan tepat.**

Gap ke production-readiness bukan hambatan fatal — ini adalah **execution risk** yang bisa diatasi dengan roadmap yang jelas dan disiplin.

### 4.2 Kondisi Investasi (Prerequisites)

Sebelum komitmen investasi, tiga hal ini harus diselesaikan terlebih dahulu:

1. **Multi-user dengan RBAC dasar** — minimum tiga role: Admin, Operator, Viewer. Ini adalah blocker terkeras untuk enterprise adoption.
2. **Audit log minimal** — siapa melakukan apa, kapan, dari IP mana. Ini syarat compliance dasar.
3. **Test coverage minimal 40%** pada modul `auth/`, `docker/`, dan `server/` — sebagai bukti komitmen terhadap kualitas jangka panjang.

### 4.3 Positioning yang Direkomendasikan

**Jangan bersaing langsung dengan Portainer.** Portainer sudah mature, punya enterprise tier, punya community besar. Dockpal harus punya positioning yang berbeda dan lebih tajam:

> *"Dockpal adalah cara paling cepat untuk mengelola Docker di VPS Linux — dari install sampai production-ready dalam 60 detik, tanpa setup tambahan apapun."*

Target segmen prioritas:
- **Agency web/digital** yang mengelola 5–50 VPS klien
- **Startup tahap awal** yang belum butuh Kubernetes tapi butuh lebih dari CLI
- **Tim DevOps kecil** di perusahaan menengah yang ingin self-hosted, bukan SaaS

### 4.4 Differentiator yang Harus Dibangun

**Fleet management sebagai killer feature.** Satu dashboard untuk melihat dan mengelola semua server — deploy stack yang sama ke 10 server sekaligus, monitoring aggregated, alert terpusat. Ini yang tidak dimiliki tool sederhana lainnya dan yang paling banyak dibutuhkan agency dan tim DevOps kecil.

**Template marketplace sebagai moat jangka panjang.** Template yang sekarang hardcoded di repo harus dijadikan ekosistem terbuka. Developer bisa publish template, pengguna install dengan satu klik. Network effect yang terbangun dari ini membuat Dockpal makin valuable seiring waktu.

**API publik yang terdokumentasi.** Integrasi dengan GitHub Actions, GitLab CI, dan tool CI/CD lainnya adalah syarat untuk masuk ke workflow engineering modern. Tanpa ini, Dockpal tidak bisa menjadi bagian dari pipeline tim.

---

## 5. Rekomendasi Engineer

### 5.1 Prioritas Perbaikan Kode (Urut Berdasarkan Dampak)

#### Prioritas 1 — Buat Kode Testable (Minggu 1–3)

Ini adalah investasi paling penting. Tanpa testability, semua perbaikan lain akan rapuh.

**Langkah konkret:**

1. Definisikan interface untuk `DockerManager`, `Database`, dan `AuthService`
2. Pisahkan `main.go` menjadi fungsi `runServer() error` yang bisa ditest
3. Tulis test untuk 5 happy-path endpoint paling kritis: login, list containers, start/stop container, deploy stack
4. Setup GitHub Actions untuk menjalankan `go test ./...` dan `go vet ./...` di setiap PR

```go
// Contoh interface yang harus dibuat
type DockerManager interface {
    ListContainers(ctx context.Context) ([]types.Container, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string, timeout *int) error
    DeployStack(ctx context.Context, name, yaml string) error
}

type UserStore interface {
    GetUser(username string) (*User, error)
    UpdatePassword(username, hash string) error
    EnsureDefaultAdmin(hash string) error
}
```

#### Prioritas 2 — Perbaiki Keamanan Fundamental (Minggu 2–4)

```go
// Tambahkan middleware role check
func RequireRole(roles ...string) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims := GetClaims(c)
        if !hasRole(claims, roles...) {
            c.AbortWithStatusJSON(403, gin.H{"error": "insufficient permissions"})
            return
        }
        c.Next()
    }
}

// Gunakan di routes
r.DELETE("/containers/:id", RequireRole("admin", "operator"), handler.DeleteContainer)
r.GET("/containers", RequireRole("admin", "operator", "viewer"), handler.ListContainers)
```

```ini
# Perbaiki systemd service
[Service]
User=dockpal
Group=docker
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/opt/dockpal
```

#### Prioritas 3 — Perbaiki Build Pipeline (Minggu 1)

```makefile
# Makefile
VERSION := $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

build:
	go build \
		-ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)" \
		-o dockpal .

test:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

.PHONY: build test lint
```

#### Prioritas 4 — Standarisasi WebSocket Library (Minggu 2)

Pilih satu library WebSocket dan migrasi semua penggunaannya. Rekomendasi: pertahankan `gorilla/websocket` karena lebih mature dan banyak digunakan, hapus `nhooyr.io/websocket`.

#### Prioritas 5 — Tambahkan Audit Log (Minggu 3–4)

```go
type AuditEntry struct {
    Timestamp time.Time `json:"timestamp"`
    UserID    string    `json:"user_id"`
    Action    string    `json:"action"`
    Resource  string    `json:"resource"`
    Details   string    `json:"details"`
    IP        string    `json:"ip"`
    Success   bool      `json:"success"`
}

// Middleware audit
func AuditMiddleware(db *db.DB) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Next()
        // Log setelah handler selesai
        entry := AuditEntry{
            Timestamp: time.Now(),
            UserID:    GetUserID(c),
            Action:    c.Request.Method,
            Resource:  c.FullPath(),
            IP:        c.ClientIP(),
            Success:   c.Writer.Status() < 400,
        }
        db.SaveAuditEntry(entry)
    }
}
```

### 5.2 Hal yang Tidak Boleh Diubah

- **Pertahankan single binary deployment** — ini janji utama Dockpal, apapun fitur baru harus tetap bisa di-bundle
- **Jangan ganti BBolt dengan PostgreSQL** — BBolt sudah cukup, cukup tambahkan indexing yang lebih baik jika diperlukan
- **Jangan migrasi ke React/Vue sekarang** — Alpine.js bekerja dengan baik dan fast; perbaiki struktur JS-nya secara incremental

### 5.3 Technical Debt yang Harus Dilacak

Buat issue di GitHub untuk melacak technical debt berikut secara eksplisit:

| Item | Prioritas | Estimasi |
|---|---|---|
| Tulis interface untuk DockerManager | P0 | 2 hari |
| Setup CI dengan `go test` di setiap PR | P0 | 4 jam |
| Perbaiki systemd `User=root` | P0 | 2 jam |
| Fix version hardcoded di main.go | P1 | 1 jam |
| Hapus duplikasi WebSocket library | P1 | 1 hari |
| Tambahkan godoc ke semua exported function | P2 | 3 hari |
| Migrasi ke single WebSocket library | P1 | 1 hari |
| Audit dan hapus indirect dependency tidak perlu | P2 | 1 hari |
| Implementasi RBAC dasar (3 role) | P0 | 1 minggu |
| Audit log middleware | P1 | 3 hari |

---

## 6. Roadmap 12 Bulan

### Fase 1 — Foundation (Bulan 1–2): "Layak Dipercaya"

Fokus: keamanan dan testability. Tidak ada fitur baru sampai fondasi ini kokoh.

- Implementasi multi-user dan RBAC (Admin / Operator / Viewer)
- Audit log dasar (siapa, apa, kapan, dari mana)
- Perbaiki systemd hardening (`User=dockpal`, `NoNewPrivileges`, dll)
- Setup CI/CD dengan test otomatis di setiap PR
- Tambahkan unit test untuk modul `auth/`, `docker/`, `server/` minimal 40% coverage
- Perbaiki version injection via build flags
- Standarisasi ke satu WebSocket library
- HTTPS built-in via flag `--tls` dengan Let's Encrypt otomatis

**Deliverable:** Dockpal bisa dipakai oleh tim kecil dengan akuntabilitas yang jelas.

### Fase 2 — Growth (Bulan 3–4): "Layak Diintegrasikan"

Fokus: masuk ke workflow engineering modern.

- REST API publik yang terdokumentasi (OpenAPI/Swagger)
- Webhook untuk trigger deploy dari CI/CD eksternal (GitHub Actions, GitLab CI)
- Notifikasi alert: email dan Slack ketika container crash atau resource kritis
- Dashboard monitoring yang bisa di-embed sebagai iframe
- Perbaiki template system — pisahkan dari binary, load dari direktori konfigurasi

**Deliverable:** Dockpal bisa diintegrasikan ke pipeline engineering yang ada.

### Fase 3 — Scale (Bulan 5–6): "Layak Dibayar"

Fokus: fleet management dan ekosistem.

- Fleet management: kelola banyak server dari satu dashboard
- Deploy stack yang sama ke multiple server sekaligus
- Monitoring aggregated lintas server
- Template system yang ekstensible (load dari URL, format standar)
- Dokumentasi lengkap: architecture guide, API reference, contribution guide

**Deliverable:** Dockpal menjadi pilihan utama untuk agency dan tim DevOps kecil.

### Fase 4 — Monetization (Bulan 7–12): "Layak Diinvestasi"

Fokus: model bisnis yang sustainable.

- **Dockpal Pro** (self-hosted, lisensi tahunan): SSO/SAML, audit log dengan retention panjang, RBAC granular, SLA support
- **Dockpal Cloud** (hosted): dashboard terpusat, $X per server/bulan, tumbuh seiring bisnis pelanggan
- **Template Marketplace**: ekosistem kontributor, bagi revenue dengan pembuat template
- Program early adopter: 20 perusahaan dengan feedback loop langsung ke pengembang

---

## 7. Model Bisnis yang Direkomendasikan

### Open Core Model

```
Dockpal OSS (MIT)           Dockpal Pro (Komersial)
─────────────────           ──────────────────────────
✓ Core Docker management    + Multi-user & RBAC granular
✓ Deploy dari template      + SSO / SAML integration
✓ Monitoring dasar          + Audit log dengan retention
✓ Cloudflare Tunnel         + Fleet management (multi-server)
✓ Single-user               + Priority support & SLA
✓ Community support         + Custom template repository
                            + Webhook & API prioritas
```

### Proyeksi Harga

| Tier | Target | Harga |
|---|---|---|
| OSS | Individual, homelab | Gratis selamanya |
| Pro Self-hosted | Tim 5–50 orang | $49/bulan atau $490/tahun per organisasi |
| Cloud | Agency, startup | $9/server/bulan (min 3 server) |
| Enterprise | Perusahaan >100 orang | Custom, negosiasi |

### Kenapa Model Ini

Pengguna homelab mendapat tool gratis selamanya — mereka adalah komunitas yang akan menyebarkan word-of-mouth. Tim profesional yang butuh akuntabilitas dan integrasi membayar harga yang terjangkau. Agency yang mengelola banyak server membayar sesuai skala penggunaan. Tidak ada yang dipaksa upgrade untuk fitur dasar.

---

## 8. Kesimpulan

### Sebagai Investor

Dockpal adalah proyek dengan **potensi nyata di segmen yang tepat**. Filosopi "zero dependency single binary" adalah differentiator yang sulit ditiru oleh tool yang sudah bloated. Momentum pengembangan aktif (11 rilis, codebase yang berkembang) menunjukkan developer yang committed.

**Saya akan investasi dengan syarat:** tiga milestone di Fase 1 diselesaikan terlebih dahulu — multi-user RBAC, audit log, dan test coverage minimal 40%. Ini bukan permintaan yang tidak masuk akal; ini adalah bukti bahwa developer memahami perbedaan antara "kode yang berjalan" dan "kode yang bisa dipercaya".

### Sebagai Engineer

Dockpal ditulis oleh developer yang **paham Go dan paham masalah yang ingin diselesaikan**. Kode bersih, mudah dibaca, dan struktur modularnya sudah benar. Yang kurang bukan kemampuan — yang kurang adalah disiplin engineering: test, interface boundary, dan security hardening.

Gap ini bisa ditutup. Estimasi saya: **seorang engineer berpengalaman bisa membawa Dockpal ke standar production-ready dalam 2 bulan kerja penuh**, jika fokus pada prioritas yang tepat.

### Pesan Akhir untuk Developer Dockpal

> Kalian sudah melewati bagian tersulit: membangun sesuatu yang benar-benar berfungsi dan berguna. Sekarang tugas berikutnya adalah membuatnya **bisa dipercaya** — oleh pengguna, oleh tim, oleh investor. Jarak antara "works on my machine" dan "runs our production" bukan soal fitur baru. Itu soal disiplin: test yang membuktikan kode benar, audit yang membuktikan siapa yang melakukan apa, dan keamanan yang membuktikan kalian peduli dengan data pengguna.
>
> Fondasi yang kalian bangun bagus. Sekarang saatnya membangun kepercayaan di atasnya.

---

*Dokumen ini disusun berdasarkan analisis publik terhadap repository `github.com/sdldev/dockpal` pada tanggal 20 Mei 2026. Semua rekomendasi bersifat konstruktif dan bertujuan membantu pengembangan Dockpal ke depan.*
