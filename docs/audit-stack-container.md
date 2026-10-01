# Audit — Fitur Stack & Container

**Tanggal:** 2026-10-01
**Cakupan:** Implementasi fitur *stack* (Dockge-style compose) dan *container* pada branch `main`
**Metode:** Code review oleh fleet reviewer independen (correctness, security, architecture, code quality, simplicity, product/UX, performance/reliability, telemetry, testing, API compatibility, documentation), lalu diverifikasi ulang langsung terhadap kode.
**Status:** ✅ **Diperbaiki (2026-10-01, dua batch)** — seluruh temuan Critical, High, Medium, dan Low sudah diperbaiki; hanya M9 (batasan protokol edge — mitigasi diterapkan, perbaikan akar = follow-up protokol) dan bagian swagger dari L11 yang tersisa. Verifikasi: `go build`, `go vet`, 16/16 paket `go test` lolos, `svelte-check` 0 error, 28/28 vitest lolos.

---

## Ringkasan

Audit menemukan **2 temuan Critical** dan **6 temuan High** yang berdampak langsung pada alur utama produk:

- Stream deploy stack **tidak pernah tertutup** → log deploy menggantung selamanya dan status stack tidak pernah refresh.
- Saat instance remote dipilih, halaman **Containers/Images** diam-diam membaca dan memutasi **host lokal**, bukan host yang dipilih.
- Kegagalan deploy lokal **sepenuhnya senyap** (tidak ada event error maupun log server).
- Cleanup deploy bisa **menghapus container lain yang sedang berjalan**.
- Path compose legacy dan path stack baru menulis **nama file berbeda di direktori yang sama**.
- Mutasi stack/container **tidak tercatat di audit log**.

Seluruh temuan di bawah sudah diverifikasi ke baris kode terkait.

---

## Daftar Temuan berdasarkan Severity

| ID | Severity | Ringkasan | Status |
|----|----------|-----------|--------|
| C1 | Critical | Stream deploy stack tidak pernah tertutup (`session.Done` tidak di-close) | ✅ Diperbaiki — `DeploySession.Close()` (sync.Once) dipanggil semua producer |
| C2 | Critical | Halaman container mengabaikan instance terpilih (mutasi ke host lokal) | ✅ Diperbaiki — `containersBasePath`/`imagesBasePath` di `lib/api/containers.ts`; 3 halaman instance-aware |
| H1 | High | Kegagalan deploy lokal senyap (tanpa event error / log) | ✅ Diperbaiki — emit event `"error"` di goroutine deploy |
| H2 | High | Cleanup deploy bisa force-remove container lain yang running | ✅ Diperbaiki — track container by ID hasil create, bukan nama prospektif |
| H3 | High | Compose legacy vs stack menulis nama file berbeda di direktori sama | ✅ Diperbaiki — konstanta bersama `docker.ComposeFileName = "compose.yaml"` |
| H4 | High | Tidak ada audit record untuk mutasi stack/container | ✅ Diperbaiki — `stack.*` & `container.*` (lokal + instance) tercatat; test `TestStackMutationsAreAudited`; isi compose/env tidak pernah dicatat |
| H5 | High | `stackError` membocorkan error mentah & tidak log apa pun | ✅ Diperbaiki — `slog.Warn` (method/path/status/error); substring matching dipertahankan dengan alasan terdokumentasi (error remote menyeberang transport sebagai string) |
| H6 | High | Test route stack bergantung environment & tidak menguji lifecycle deploy | ✅ Diperbaiki — `composecli.Available` jadi var stub-able (+cache); `StackUpStreamed` masuk seam `stackCLI`; test 501 + urutan event deploy sukses/gagal |
| M1 | Medium | `composecli.Available()` men-spawn subprocess tiap request | ✅ Diperbaiki — probe sekali, di-cache `sync.Once`; var untuk stub test |
| M2 | Medium | `session.Events` satu channel, tanpa single-reader guard, drop senyap | ✅ Diperbaiki — fan-out `Subscribe`/`Unsubscribe` per pembaca; drop dihitung (`DroppedEvents`) + di-log saat close |
| M3 | Medium | Map `stackLocks` tidak pernah dibersihkan | ✅ Diperbaiki — `CompareAndDelete` saat Unlock (race-safe) |
| M4 | Medium | Goroutine deploy bocor session saat hang/panic & abaikan cancel | ✅ Diperbaiki — `defer` removal + `recover()` + context timeout 15 menit di kedua handler |
| M5 | Medium | Kontrak "not found" `GetStack`/`GetStackFull` kontradiktif; guard deploy tidak efektif | ✅ Diperbaiki — cek `!s.Managed` eksplisit di `handleDeployStack` |
| M6 | Medium | Validasi edit container duplikat dengan aturan/error berbeda | ✅ Diperbaiki — `validator.ValidateRestartPolicy`/`ValidatePortMapping` dipakai kedua surface; duplikat lokal dihapus |
| M7 | Medium | Validasi nama service duplikat | ✅ Diperbaiki — helper `validateServiceName` bersama |
| M8 | Medium | Tidak ada test RBAC untuk route stack | ✅ Diperbaiki — `TestInstanceStackRBAC`: viewer 403 di semua mutasi, 200 di read; operator 200 di mutasi |
| M9 | Medium | Deploy Edge melaporkan sukses tanpa tahu hasil | ✅ Mitigasi — pesan jujur "deploy submitted — verify via stack status refresh" (perbaikan akar: multi-response streaming protokol edge = follow-up, lihat TODO `stacks-edge-stream`) |
| M10 | Medium | Kontrak status-code/response berbeda antar kedua surface | ✅ Diperbaiki — compose-missing kini 501 di kedua surface via `stackError` |
| L1 | Low | Parameter `emit func(string)` di `stackRun` mati | ✅ Diperbaiki — param dihapus, 7 call site disesuaikan |
| L2 | Low | `StatusConvert` order-dependent | ✅ Diperbaiki — deteksi semua state, campuran → `partial`; test baru |
| L3 | Low | `diagnoseDeployError` hanya di jalur legacy | ✅ Diperbaiki — diekspor sebagai `docker.DiagnoseDeployError`; jalur composecli memakainya untuk hint error |
| L4 | Low | Switch `adapter.Output` 6 cabang tak terjangkau | ✅ Diperbaiki — dipersempit ke cabang `network` yang benar-benar dipakai |
| L5 | Low | `streamLines` mengabaikan `scanner.Err()` | ✅ Diperbaiki — error di-emit sebagai event `"error"` |
| L6 | Low | UI mengabaikan `status`/`step` event deploy | ✅ Diperbaiki — log berwarna per status + banner terminal sukses/gagal di ComposePage |
| L7 | Low | Gap aksesibilitas (Modal, icon buttons, tabs, toast) | ✅ Diperbaiki — Modal `role=dialog`/`aria-modal`/focus restore; `aria-label` icon buttons (Button kini meneruskan); `role=tablist/tab/aria-selected`; toast `aria-live` |
| L8 | Low | Gating viewer di `ContainerPage` (tombol aksi tidak disembunyikan) | ✅ Diperbaiki — `{#if $isOperator}` di ContainerPage + ContainersPage |
| L9 | Low | `gofmt` drift | ✅ Diperbaiki — semua file yang disentuh kini gofmt-clean |
| L10 | Low | Casing JSON tag Go tidak konsisten | ✅ Diperbaiki — pemisahan sengaja didokumentasikan di doc comment `Stack` |
| L11 | Low | Gap dokumentasi (prasyarat compose, 501, lokasi file stack, dst.) | ✅ Diperbaiki (sebagian) — README: prasyarat compose plugin, perilaku 501, lokasi file stack, jumlah template aktual (38), metrics butuh auth; AGENTS.md "commit TBD" → `4cb776a`. Sisa: swagger endpoint stack (follow-up dokumentasi API) |
| L12 | Low | Pesan `errNoCLI` tidak actionable | ✅ Diperbaiki — pesan baru menyebut plugin + cara verifikasi |
| L13 | Low | `StackServices`/`ListComposeProjects` dijalankan ulang tiap request | ✅ Diperbaiki — cache 2 detik untuk `compose ls` + invalidasi pada mutasi |

Lokasi baris asli per temuan tersimpan di detail masing-masing bagian di bawah.

---

## Critical

### C1 — Stream deploy stack tidak pernah tertutup ✅ DIPERBAIKI

**Lokasi:** `internal/docker/deploy_stream.go:94`, `internal/composecli/bridge.go:40-57`, `internal/agent/local.go:493-502`, `internal/agent/direct_stacks.go:126`, `internal/agent/edge_stacks.go:103`, `internal/server/routes_helpers.go:423-442`

**Masalah:** Handler WebSocket deploy (`routes_helpers.go:423-442`) hanya berhenti ketika channel `session.Done` ditutup. Satu-satunya tempat yang menutupnya adalah jalur moby legacy `DeployComposeStreamed` (`defer close(session.Done)`). Seluruh jalur stack — `composecli.StackUpStreamed`, `LocalClient.DeployStackStreamed`, `DirectClient.DeployStackStreamed`, `EdgeClient.DeployStackStreamed` — **tidak pernah** menutupnya, dan `RemoveSession` juga tidak.

**Dampak:**
- Setiap deploy stack meninggalkan stream log menggantung tanpa akhir.
- Frontend (`svelte/src/lib/api/stacks.ts` → `watchDeploy`) mengandalkan `ws.onclose` untuk refresh status stack; karena server tidak pernah menutup, `loadStack()` tidak pernah terpanggil dan panel log deploy tetap "in progress".

**Bukti:** Verifikasi langsung — loop `for { select { … case <-session.Done: … } }` pada `routes_helpers.go:423` tidak akan pernah keluar untuk deploy stack.

**Perbaikan yang diterapkan (2026-10-01):**
- `DeploySession` mendapat metode `Close()` yang aman dipanggil berkali-kali (guard `sync.Once`, anti double-close).
- `defer session.Close()` dipasang di **semua** producer streaming: `composecli.StackUpStreamed`, `LocalClient.DeployStackStreamed`, `DirectClient.DeployStackStreamed`, `EdgeClient.DeployStackStreamed`, dan jalur moby legacy `DeployComposeStreamed` (`close(session.Done)` diganti `session.Close()`).

---

### C2 — Halaman container mengabaikan instance terpilih ✅ DIPERBAIKI

**Lokasi:** `svelte/src/components/pages/ContainersPage.svelte:28,41,44,58`, `svelte/src/components/pages/ContainerPage.svelte:18,50,66,79,92,105,124`; backend `internal/server/routes.go:741-1000`

**Masalah:** Kedua halaman meng-import / menurunkan `selectedInstance`, namun memanggil route lokal mentah `/api/containers...` (dan `/api/images...`), yang di backend di-hardcode ke agent `"local"`. Bandingkan dengan komponen saudaranya yang sudah instance-aware: `StatsChart.svelte:36-38`, `LogsViewer`, `ContainerTerminal`, `NavHeader`. Jadi ini kelalaian, bukan desain.

**Dampak:**
- Saat server remote dipilih di sidebar, halaman Containers/Images menampilkan workload **host lokal**.
- start/stop/restart/delete/edit/prune/pull mengeksekusi mutasi ke **host yang salah**.

**Bukti:** `ContainerPage.svelte:18` mendefinisikan `const instanceId = $derived(get(selectedInstance) || 'local')` tetapi `instanceId` tidak dipakai di pemanggilan API mana pun (`api.get(\`/containers/${containerId}\`)`, dst.).

**Perbaikan yang diterapkan (2026-10-01):**
- Helper baru `svelte/src/lib/api/containers.ts` berisi `containersBasePath(instanceId)` dan `imagesBasePath(instanceId)` (pola yang sama dengan `stacksBasePath`).
- `ContainersPage.svelte`, `ContainerPage.svelte`, dan `ImagesPage.svelte` kini menurunkan `basePath` dari `$selectedInstance` secara reaktif dan memakainya di seluruh pemanggilan API (list, start/stop/restart, delete, edit, pull, check, prune). Halaman me-reload data saat instance di sidebar diganti.
- `ImagesPage.svelte` (sebelumnya juga hardcoded `/images`) ikut diperbaiki — temuan asli hanya menyebut dua halaman pertama.

---

## High

### H1 — Kegagalan deploy lokal senyap ✅ DIPERBAIKI

**Lokasi:** `internal/server/routes_stacks.go:225-233`

**Masalah:** Goroutine `handleDeployStack` hanya emit `"done"` saat sukses dan **tidak emit apa pun** saat error. Mirror instance (`instance_stacks_routes.go:237-239`) minimal emit event `"error"`. Digabung dengan C1, deploy lokal yang gagal tidak memberi sinyal kegagalan apa pun.

**Perbaikan yang diterapkan (2026-10-01):** Goroutine kini emit `session.Emit("error", err.Error(), "error")` pada jalur gagal, menyamai handler instance. Sekaligus menutup M4: `defer` penjadwalan `RemoveSession` + `recover()` panic di kedua handler (`routes_stacks.go`, `instance_stacks_routes.go`).

---

### H2 — Cleanup deploy bisa force-remove container running milik orang lain ✅ DIPERBAIKI

**Lokasi:** `internal/docker/deploy_stream.go:177` (append sebelum create) + `:116-127` (cleanup)

**Masalah:** `createdContainers` di-append **sebelum** `createAndStartService`. Jika create gagal karena konflik nama dengan container lain yang **sedang berjalan**, `createAndStartService` dengan benar menolak menyentuhnya (`compose.go:455-465` hanya menghapus container yang tidak running), tetapi `cleanup()` di luar tetap force-remove container itu berdasarkan nama.

**Dampak:** Container live milik user bisa terhapus oleh deploy stack yang gagal.

**Perbaikan yang diterapkan (2026-10-01):** `createAndStartService` kini mengembalikan container ID hasil create; `DeployComposeStreamed` hanya men-track ID yang benar-benar dibuat (bukan nama prospektif), dan `cleanup()` me-remove by ID. Caller `DeployCompose` dan ketiga test `compose_cleanup_test.go` disesuaikan dengan signature baru.

---

### H3 — Path compose legacy vs stack menulis nama file berbeda ✅ DIPERBAIKI

**Lokasi:** `internal/docker/compose.go:332` (menulis `docker-compose.yml`) vs `internal/docker/stack_store.go:202` (menulis `compose.yaml`); pembacaan `stack_store.go:60-65,237`

**Masalah:** `SaveStack` (semua `/api/stacks`) menulis `compose.yaml`; `writeComposeFile` legacy (App-Install / `/api/deploy/compose`) menulis `docker-compose.yml` — keduanya ke direktori `composeBaseDir()/<name>` yang sama. Pembacaan memilih `compose.yaml` lebih dulu; `docker compose` kemudian dijalankan tanpa `-f`.

**Dampak:** Bug kelas "edit saya hilang" / YAML basi; satu direktori proyek bisa berisi dua file compose dengan presedensi ambigu; file legacy tidak pernah direkonsiliasi.

**Perbaikan yang diterapkan (2026-10-01):** Konstanta bersama `docker.ComposeFileName = "compose.yaml"` diekspor dari `stack_store.go` dan dipakai oleh `SaveStack`, `writeComposeFile`, serta label `dockpal.compose` di `createAndStartService`. Semua jalur kini konvergen ke `compose.yaml`; pembacaan tetap toleran terhadap nama lama via `acceptedComposeFileNames`.

---

### H4 — Tidak ada audit record untuk mutasi stack/container ✅ DIPERBAIKI (batch 2)

**Lokasi:** `internal/server/routes_stacks.go` (semua handler mutasi), `instance_stacks_routes.go`, handler container `routes.go:773-949` / `instance_scoped_routes.go`

**Masalah:** `api_key.create/delete`, `backup.create`, dan `instance.*` memanggil `LogAudit`. Operasi stack create/update/delete/deploy/up/down/update, penulisan global.env, dan container start/stop/restart/remove/edit tidak memanggilnya sama sekali.

**Dampak:** Resource dengan dampak terbesar (lifecycle seluruh stack, recreate container dengan `force`) tidak meninggalkan jejak forensik; audit log Admin buta terhadapnya.

**Perbaikan yang diterapkan (2026-10-01, batch 2):** `registerStackRoutes` kini menerima `database`; handler instance memakai `getDatabase(c)` via helper baru `auditInstanceStack`/`auditInstanceContainer`. Tercatat: `stack.create/update/delete/deploy/up/down/update`, `stack.globalenv`, `container.start/stop/restart/remove/edit` (lokal + instance, resource ber-scope `instances/<id>/...`). Isi compose/env TIDAK pernah dicatat — test `TestStackMutationsAreAudited` menegaskan entry ada dan tidak membocorkan isi. Event auth juga tercakup (AUTH-M8, via `auth.AuditHook`).

---

### H5 — `stackError` membocorkan error mentah & tidak log apa pun ✅ DIPERBAIKI (batch 2)

**Lokasi:** `internal/server/routes_stacks.go:58-70`

**Masalah:** Semua kegagalan stack mengalir lewat `stackError`, yang me-mapping status dengan string-matching dan mengembalikan `err.Error()` mentah ke client, tanpa log server. Error yang tidak match menjadi 400, menyamarkan fault internal yang sebenarnya.

**Perbaikan yang diterapkan (2026-10-01, batch 2):** `slog.Warn` (method/path/status/error) dicatat sebelum setiap respons error. Substring matching dipertahankan secara sengaja dan alasannya didokumentasikan di komentar fungsi: error instance menyeberang transport agent sebagai string biasa, sehingga `errors.Is` hanya akan menutup kegagalan lokal. Sekaligus menangani M10: pesan compose-missing dipetakan ke 501 di kedua surface.

---

### H6 — Test route stack bergantung environment & tidak menguji lifecycle deploy ✅ DIPERBAIKI (batch 2)

**Lokasi:** `internal/server/routes_stacks_test.go` (header + `stackTestEnv`), `TestDeployStackReturnsSessionID`

**Masalah:** Komentar header mengklaim `composecli.Available` di-stub, tetapi seam tersebut tidak ada — setiap handler memanggil `Available()` asli (`exec docker compose version`), sehingga test gagal di host tanpa plugin. `handleDeployStack` memanggil `composecli.StackUpStreamed` langsung (melewati seam `fakeCLI`), sehingga goroutine background menjalankan `docker compose up` asli.

**Perbaikan yang diterapkan (2026-10-01, batch 2):** (1) `composecli.Available` menjadi var yang di-cache (`sync.Once`) — stub-able; `TestStackRoutesReturn501WithoutComposeCLI` menegaskan 501 di 7 handler guarded. (2) `StackUpStreamed` masuk interface `stackCLI` (`docker.StackUpStreamedCLI`); `handleDeployStack` memanggilnya lewat seam sehingga `fakeCLI` meng-intercept — tidak ada lagi subprocess nyata. (3) `TestDeployStackEventSequence` menegaskan urutan event sukses (progress→done) dan gagal (error event — regression net H1) plus penutupan stream (C1). Komentar header yang stale diperbaiki.

---

## Medium

- **M1 — `composecli.Available()` men-spawn subprocess tiap request.** ✅ DIPERBAIKI (batch 2). Kini var yang di-cache `sync.Once` (probe sekali per proses) dan stub-able untuk test; `ResetAvailableCache` untuk kebutuhan re-probe di test.
- **M2 — `session.Events` satu channel bersama (buffer 50), tanpa single-reader guard, drop senyap.** ✅ DIPERBAIKI (batch 2). `DeploySession` kini fan-out: `Subscribe()` memberi tiap pembaca channel buffer sendiri; `Emit` broadcast non-blocking per subscriber; drop dihitung (`DroppedEvents`) dan di-log saat `Close`. Producer remote (direct/edge) dialihkan ke `EmitEvent` agar tidak melewati fan-out; WS handler memakai `Subscribe`/`Unsubscribe`.
- **M3 — Map `stackLocks` tidak pernah dibersihkan.** ✅ DIPERBAIKI (batch 2). `Unlock` kini men-evict entry via `sync.Map.CompareAndDelete` — race-safe terhadap lock yang di-reacquire di antara unlock dan delete.
- **M4 — Goroutine deploy bocor session saat hang/panic & abaikan cancel.** ✅ DIPERBAIKI (batch 1+2). Kedua handler `defer` penjadwalan `RemoveSession` (panic-safe) + `recover()` dengan `slog.Error`; batch 2 menambahkan `context.WithTimeout(15m)` sehingga compose yang hang tidak membocorkan goroutine selamanya.
- **M5 — Kontrak "not found" `GetStack`/`GetStackFull` kontradiktif; guard deploy tidak efektif.** ✅ DIPERBAIKI (batch 1). `handleDeployStack` kini mengecek `!s.Managed` secara eksplisit dan mengembalikan 404 "stack not found" sebelum membuat sesi deploy, alih-alih gagal low-level `chdir`.
- **M6 — Validasi edit container duplikat dengan aturan/error berbeda.** ✅ DIPERBAIKI (batch 2). `validator.ValidateRestartPolicy` dan `validator.ValidatePortMapping` baru dipakai kedua surface (`routes.go` + `instance_scoped_routes.go`); duplikat lokal `validateContainerName`/`validateEnvVarValue` (dengan komentar usang "avoid import cycles") dihapus — aturan & pesan error kini identik.
- **M7 — Validasi nama service duplikat** di `routes_stacks.go:281` dan `instance_stacks_routes.go:274`. ✅ DIPERBAIKI (batch 2) — helper `validateServiceName` bersama.
- **M8 — Tidak ada test RBAC untuk route stack.** ✅ DIPERBAIKI (batch 2). `instanceStackRouterAs(t, fake, role)` + `TestInstanceStackRBAC`: viewer → 200 di 4 route read, 403 di 8 route mutasi; operator → akses penuh.
- **M9 — Deploy Edge melaporkan sukses tanpa tahu hasil.** ✅ MITIGASI (batch 2). `EdgeClient.DeployStackStreamed` kini emit pesan jujur ("deploy started … outcome not streamed; verify via stack status refresh") dan event terminal `"done"` bertuliskan "Deploy submitted to agent — check stack status for the result" alih-alih "Deployed". Perbaikan akar (multi-response streaming di protokol edge) tetap follow-up — lihat TODO `stacks-edge-stream` di `edge_stacks.go`.
- **M10 — Kontrak status-code/response berbeda antara kedua surface.** ✅ DIPERBAIKI (batch 2). Pesan compose-missing kini dipetakan ke 501 oleh `stackError` di kedua surface (sebelumnya generik 400/503 di remote).

---

## Low

- **L1 — Parameter `emit func(string)` di `stackRun` mati.** ✅ DIPERBAIKI (batch 1) — param dihapus; 7 call site disesuaikan.
- **L2 — `StatusConvert` order-dependent.** ✅ DIPERBAIKI (batch 1) — parse seluruh state per-service; campuran apa pun → `partial` (order-independent); test diperluas.
- **L3 — `diagnoseDeployError` hanya di jalur legacy.** ✅ DIPERBAIKI (batch 2) — diekspor sebagai `docker.DiagnoseDeployError`; `StackUpStreamed` meng-emit hint yang sama pada kegagalan.
- **L4 — Switch `adapter.Output` punya 6 cabang tak terjangkau.** ✅ DIPERBAIKI (batch 2) — dipersempit ke cabang `network` yang benar-benar dipakai, dengan komentar cara menambah kembali.
- **L5 — `streamLines` mengabaikan `scanner.Err()`.** ✅ DIPERBAIKI (batch 1) — error scanner di-emit sebagai event `"error"` ("output truncated: …").
- **L6 — UI mengabaikan `status`/`step` event deploy.** ✅ DIPERBAIKI (batch 2) — `deployLogs` menyimpan status per baris (error merah / done hijau), plus banner terminal sukses/gagal dari event terakhir & `onDone`.
- **L7 — Gap aksesibilitas.** ✅ DIPERBAIKI (batch 2) — `Modal`: `role="dialog"`, `aria-modal`, `aria-labelledby`, fokus awal + pemulihan fokus, keydown hanya saat open; tombol ikon: `Button` meneruskan `aria-label` dan semua tombol ikon di ContainersPage diberi label; tab strip: `role="tablist"/tab/aria-selected`; toast: `aria-live="polite"` + tombol dismiss ber-`aria-label`.
- **L8 — Gating viewer di `ContainerPage`.** ✅ DIPERBAIKI (batch 2) — blok aksi dibungkus `{#if $isOperator}` di `ContainerPage` dan `ContainersPage`.
- **L9 — `gofmt` drift.** ✅ DIPERBAIKI (batch 1) — semua file yang disentuh kini gofmt-clean.
- **L10 — Casing JSON tag Go tidak konsisten per tipe** ✅ DIPERBAIKI (batch 2) — pemisahan sengaja (stack camelCase vs container snake_case) didokumentasikan di doc comment `Stack` sebagai kontrak load-bearing terhadap client TS.
- **L11 — Gap dokumentasi.** ✅ DIPERBAIKI (batch 2, sebagian) — README: prasyarat plugin compose, perilaku 501, lokasi file stack (`<data>/../compose/<stack>/`), jumlah template aktual (38), `/api/metrics` kini butuh auth; `AGENTS.md` "commit TBD" → `4cb776a`. Sisa: endpoint stack di `swagger_json.go` (follow-up dokumentasi API).
- **L12 — Pesan `errNoCLI`** ✅ DIPERBAIKI (batch 1) — kini menyebut plugin yang hilang + cara verifikasi (`docker compose version`).
- **L13 — `StackServices`/`ListComposeProjects` dijalankan ulang tiap list/detail/action** ✅ DIPERBAIKI (batch 2) — cache 2 detik untuk `compose ls` + `InvalidateComposeLsCache()` pada setiap mutasi stack; test yang menukar fake CLI meng-invalidasi agar tidak bocor antar-test.

---

## Bukan Temuan (sudah dicek, tidak perlu tindakan)

- WS `?token=` **tidak** bocor ke log — tidak ada `gin.Logger()`/middleware access-log yang terdaftar.
- Logika `TryLock`/`Unlock` benar (tanpa double-close/reentrancy; jalur streaming dan sync saling eksklusif).
- Penanganan path relatif `--env-file` di `StackComposeArgs` resolves dengan benar terhadap `cmd.Dir`.
- Handler WS stats/logs/exec container cancel saat disconnect (goroutine terbatas).
- JSON tag Go cocok persis dengan interface TS (`stacks.ts`); `{deploy_id}` cocok dengan konstruksi URL `watchDeploy`.
- `ApplyEnvVariables`, `RewriteImageDigest`, `SetServiceLabel`, dan round-trip `yaml-sync.ts` yang mempertahankan komentar semuanya terpakai/terjustifikasi.

---

## Urutan Prioritas Perbaikan — status akhir (2026-10-01, dua batch)

1. ✅ **C1** (stream deploy tidak pernah tertutup) dan **C2** (mutasi instance remote ke host lokal).
2. ✅ **H1–H5** — kegagalan deploy senyap, cleanup berbahaya, nama file compose ganda, audit hilang (H4, batch 2), kebocoran error (H5, batch 2).
3. ✅ **H6 + M8** — gap test ditutup: seam `Available` + `StackUpStreamed` via `stackCLI`, test 501, test urutan event deploy, test RBAC stack.
4. ✅ Seluruh Medium (M1–M10) dan Low (L1–L13) — M9 dimitigasi dengan pesan jujur (perbaikan akar = protokol edge, follow-up); L11 sisakan swagger endpoint stack (follow-up dokumentasi API).

**Verifikasi setelah perbaikan (kedua batch):** `go build ./...` & `go vet ./...` bersih; `go test ./... -count=1` 16/16 paket lolos; `make svelte-check` 0 error 0 warning; `make svelte-test` 28/28 lolos.
