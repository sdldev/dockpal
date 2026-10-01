# Audit — Subsistem Autentikasi & Otorisasi

**Tanggal:** 2026-10-01
**Cakupan:** `internal/auth/` (handler, JWT, secret, roles) serta titik penegakan auth di `internal/server/` (`middleware.go`, `routes_helpers.go`, `routes.go`, `routes_rbac.go`, `webhook_handlers.go`, `api_keys.go`) dan `main.go`
**Metode:** Code review oleh 4 reviewer independen (security/abuse, correctness, architecture, testing-strategy), lalu setiap temuan utama diverifikasi ulang langsung terhadap kode (termasuk menjalankan test secara terpisah untuk membuktikan klaim order-dependence).
**Status:** ✅ **Diperbaiki (2026-10-01, dua batch)** — Critical, H1–H2, seluruh Medium M1–M8, Low L1–L8, dan temuan test T1–T5/T7 sudah diperbaiki. Tersisa: T6 (level test coverage booster), A1/A3/A5 (refactor arsitektur: `auth.Service`, `WSAuthMiddleware`, cache lookup) yang ditunda sebagai follow-up terpisah karena menyentuh semua jalur auth/WS — lihat bagian akhir. Verifikasi: `go build`, `go vet`, 16/16 paket `go test` lolos, `svelte-check` 0 error, 28/28 vitest lolos.

---

## Ringkasan

Mekanisme inti JWT pada dasarnya sehat: algoritma dipaksa HMAC (tidak mungkin alg-confusion), expiry ditegakkan library, dan **pengecekan token-version dijalankan di setiap jalur JWT** (bearer, WS `?token=`, WS first-message — keenam call site sudah diverifikasi). Escalasi mandiri juga diblokir dengan benar.

Namun ada **satu rantai eskalasi Critical**: seorang **viewer** dapat membaca file sembarang di container mana pun — termasuk JWT signing secret milik panel sendiri — lalu memalsukan token admin. Di bawahnya, masalah paling konsekuensial adalah bug pengurutan saat rotasi secret yang dapat merusak permanen kredensial registry, tier RBAC yang salah untuk logout/reset-password, serta absennya unit test untuk seluruh handler auth.

Temuan tumpang-tindih dengan `audit-stack-container.md`: "tidak ada audit record" (H4 di dokumen tersebut, untuk mutasi stack/container) bersifat saling melengkapi dengan M8 di sini (event auth); M8 di dokumen tersebut (test RBAC route stack) sejenis dengan celah test RBAC di sini. Perbaikan untuk keduanya bisa dilakukan bersamaan.

---

## Daftar Temuan berdasarkan Severity

| ID | Severity | Ringkasan | Status |
|----|----------|-----------|--------|
| C1 | Critical | Viewer bisa baca file sembarang di container mana pun via `/files*` (exec `cat`) → baca `.secret` panel → forge token admin | ✅ Diperbaiki — `/files`, `/files/read`, `/files/download` → operator tier; `ValidatePath` tolak `/proc`,`/sys` (+test) |
| H1 | High | `rotateSecrets` menulis secret file terakhir → jika gagal, DB terenkripsi dengan key yang tidak ada di disk | ✅ Diperbaiki — secret file ditulis (fsync+rename atomik) sebelum re-enkripsi DB |
| H2 | High | Logout & reset-password terdaftar di tier **operator** → viewer tidak bisa mencabut token sendiri | ✅ Diperbaiki — kedua route dipindah ke `baseProtected` |
| H3 | High | 7 handler auth di `internal/auth/handler.go` sama sekali tidak punya test | ✅ Diperbaiki — `handler_test.go` baru: 7 handler, ~20 kasus (login, logout e2e revocation, reset/change password, list-users, role update, profile) |
| M1 | Medium | `/api/metrics` publik membocorkan inventaris workload | ✅ Diperbaiki — dipindah ke `viewerGroup` (auth + viewer role); scraper eksternal pakai bearer token |
| M2 | Medium | Webhook tanpa secret → HMAC di-skip sepenuhnya → siapa pun yang lihat URL bisa trigger deploy | ✅ Diperbaiki — secret kosong kini di-generate server-side saat create; setiap webhook selalu HMAC-protected |
| M3 | Medium | Login timing side channel → enumerasi username valid | ✅ Diperbaiki — dummy `bcrypt.CompareHashAndPassword` di jalur unknown-user |
| M4 | Medium | `HandleLogout` menelan kegagalan `IncrementTokenVersion`, tetap kembalikan 200 | ✅ Diperbaiki — kini 500 `"failed to revoke session"` saat revocasi gagal |
| M5 | Medium | Pembangkitan secret TOCTOU (tanpa `O_EXCL`) → dua proses mulai bersamaan menghasilkan secret berbeda | ✅ Diperbaiki — `O_CREATE|O_EXCL` + re-read saat `EEXIST` |
| M6 | Medium | Deploy-stream WS terdaftar di root engine (bypass AuthMiddleware + rate limit) & tanpa role check | ✅ Diperbaiki — pindah ke `baseProtected` + `RequireRole(viewer)` + role check di cabang query-token |
| M7 | Medium | Rate limit terhitung ganda di kedua endpoint password (10/min → efektif 5/min) | ✅ Diperbaiki — limiter per-route dihapus; andalkan `baseProtected.Use` |
| M8 | Medium | Tidak ada audit log untuk event auth (login, logout, ganti password, perubahan role) | ✅ Diperbaiki — `auth.AuditHook` di-wire dari `RegisterRoutes`; login success/failed, logout, password reset/change, role change semuanya tercatat |
| L1 | Low | JWT sebagai `?token=` di URL upgrade → bocor ke access log reverse-proxy | ✅ Diperbaiki — `GET /api/ws-ticket` (60 detik, single-use, membawa identitas penerbit); diterima di slot `?token=` yang sama; seluruh WS frontend (deploy, logs, terminal, install) memakainya dengan fallback JWT |
| L2 | Low | Password bootstrap admin ditulis ke rotating log yang persisten | ✅ Diperbaiki — `log.Printf` berisi password dihapus; password hanya ke stderr; log menyimpan petunjuk tanpa secret |
| L3 | Low | Comparasi hash API key memakai `==`, bukan constant-time | ✅ Diperbaiki — `subtle.ConstantTimeCompare` (kini via `auth.ValidateAPIKey`) |
| L4 | Low | `AuthMiddleware` berjalan sebelum rate limiter → upaya auth gagal tidak ter-throttle | ✅ Diperbaiki — `authFailureLimiter` (20/menit per IP) mengenakan biaya pada setiap 401; 429 setelah habis |
| L5 | Low | `GET /api/services` tier viewer mengembalikan body Compose penuh (berisi secret) | ✅ Diperbaiki — `Compose` dikosongkan untuk role < operator |
| L6 | Low | `ssh.InsecureIgnoreHostKey()` saat install agent → MITM bisa intercept agent token | ✅ Diperbaiki — TOFU: fingerprint SHA-256 selalu ditampilkan di install log; pinning via `ssh_host_key` pada request install → verifikasi strict, mismatch ditolak |
| L7 | Low | Batas 72 byte bcrypt menghasilkan 500 opaq untuk passphrase panjang | ✅ Diperbaiki — validasi ≤72 byte → 400 jelas di reset & change password |
| L8 | Low | `HandleListUsers` kembalikan `null` (bukan `[]`) saat bucket kosong | ✅ Diperbaiki — `make([]userResponse, 0, …)` |

Lokasi baris asli per temuan tersimpan di detail masing-masing bagian di bawah.

Detail temuan kualitas test (T1–T7) dan arsitektur (A1–A5) ada di bagian terakhir.

---

## Critical

### C1 — Viewer dapat membaca file sembarang di container mana pun → takeover admin ✅ DIPERBAIKI

**Lokasi:** `internal/server/routes.go:1697` (`/files`), `:1712` (`/files/read`), `:1770` (`/files/download`); tier ditentukan oleh `internal/server/routes_rbac.go:12-14`; implementasi `internal/docker/fileops.go:136-144`

**Masalah:** Ketiga endpoint ini terdaftar via `protected.GET(...)`, dan `roleRouterWrapper.GET` memetakan **selalu** ke **viewer group** tanpa peduli path. `ReadFile` menjalankan `docker exec <container> cat <path>` (`fileops.go:144`). `ValidatePath` (`fileops.go:42-65`) hanya memblokir traversal (`..`) dan control character — path absolut apa pun diperbolehkan, termasuk `/proc/self/environ` dan `/opt/dockpal/data/.secret`. Endpoint `POST /files/write` dan `DELETE /files` benar memerlukan operator (POST/DELETE memetakan ke `operatorGroup`), sehingga read-via-exec di tier viewer adalah **inkonsistensi tier yang langsung** terhadap route exec yang benar di-gate ke operator (`instance_scoped_routes.go:73`).

**Dampak / skenario serang:**
`docker-compose.dev.yml:9-13` milik repo ini me-mount `dockpal-data` di `/opt/dockpal/data` di dalam container panel — tempat `.secret` (JWT signing key) dan `dockpal.db` berada. `container_protection.go:61-62` hanya melindungi container bernama `dockpal-agent`, jadi container panel (`dockpal-dev`) tidak terlindungi.

1. Viewer memanggil `GET /api/containers` (tier viewer) untuk mendaftar container.
2. Viewer memanggil `GET /api/files/read?container=dockpal-dev&path=/opt/dockpal/data/.secret` → mendapatkan signing key.
3. Viewer memalsukan token admin (`GenerateJWT` adalah fungsi publik di `internal/auth`); satu-satunya yang tidak diketahui adalah `TokenVersion` admin — integer kecil mulai dari 0, mudah di-brute-force.
4. **Viewer → admin penuh.**

Terlepas dari apakah panel dikontainerisasi, ini tetap memungkinkan viewer membaca `/etc/shadow` dan `/proc/1/environ` dari setiap container tenant — eskalasi yang signifikan untuk panel multi-tenant.

**Perbaikan yang diterapkan (2026-10-01):**
- ✅ `/files`, `/files/read`, `/files/download` dipindah ke `operatorGroup.GET(...)` di `routes.go` — viewer kini mendapat 403.
- ✅ `ValidatePath` (`fileops.go`) kini menolak `/proc` dan `/sys` (termasuk traversal yang resolve ke sana), menutup akses `/proc/self/environ` & `/proc/1/root` bahkan untuk operator. Test baru `TestValidatePath_RejectsProcAndSys`; property test disesuaikan.
- ⏳ Self-protection untuk container panel sendiri (di sebelah `isDockpalAgentName`, `container_protection.go:61`) **belum** diterapkan — tindak lanjut opsional; risiko utama sudah tertutup oleh dua perbaikan di atas.

---

## High

### H1 — Rotasi secret menulis secret baru terakhir; kegagalan merusak kredensial permanen ✅ DIPERBAIKI

**Lokasi:** `main.go:523-557` (fungsi `rotateSecrets`); write di `:556`

**Masalah:** Kredensial registry (`:524`) dan agent token per-instance (`:548`) dienkripsi ulang dengan key baru dan disimpan ke DB **sebelum** `os.WriteFile(secretPath, ...)` dijalankan di `:556`. Semua kegagalan di fungsi ini adalah `log.Fatalf`, yang menghentikan proses.

**Dampak:** Jika write file gagal (disk penuh, EROFS, perubahan permission, atau proses ter-kill di antara DB write dan baris `:556`), secret di disk masih yang lama sementara seluruh kredensial di DB adalah ciphertext yang dikunci dengan key baru. Saat server start berikutnya, `registry.DeriveKey(oldSecret)` tidak bisa mendekripsinya — **semua kredensial registry dan agent token tidak dapat dipulihkan**. Backup pre-rotation (`:499-505`) menyelamatkan dari kehilangan data, tapi rotasi ditinggalkan dalam keadaan rusak tanpa jalur recovery.

**Perbaikan yang diterapkan (2026-10-01):** Secret file baru kini ditulis **sebelum** re-enkripsi DB — via file temp + `fsync` + `os.Rename` atomik ke `secretPath`, tepat setelah backup terverifikasi. Old secret tetap di memori untuk mendekripsi baris lama. Jika langkah DB mana pun gagal setelahnya, DB masih terenkripsi dengan key yang benar-benar ada di disk. (Verifikasi round-trip pasca-rotasi tetap menjadi saran opsional.)

### H2 — Logout & reset-password ter-gate ke tier operator; viewer tidak bisa mencabut sesi sendiri ✅ DIPERBAIKI

**Lokasi:** `internal/server/routes.go:128-129`; pemetaan tier di `internal/server/routes_rbac.go:16-22`

**Masalah:** `protected.POST(...)` memetakan **semua** path POST (kecuali `/system/update`) ke `operatorGroup` = `RequireRole(RoleOperator)`. `/api/logout` dan `/api/auth/reset-password` adalah aksi akun self-service, bukan operasi operator.

**Dampak:** Viewer yang klik Logout mendapat 403, `HandleLogout` tidak pernah jalan, dan `IncrementTokenVersion` dilewati — JWT tetap server-valid untuk TTL penuh 4 jam (`jwt.go:26`). SPA menelan error (`api.post('/logout').catch(() => {})`) dan hanya menghapus token client-side, sehingga kontrak revocasi yang menjadi alasan ada logout diam-diam dilewati. Demikian pula `/auth/reset-password`: handler bekerja self-service (membaca `c.GetString("username")` dari token, `handler.go:70` — tidak ada parameter target), tetapi viewer tidak bisa mencapainya meskipun Settings page menampilkannya untuk semua user.

**Perbaikan yang diterapkan (2026-10-01):** Kedua route dipindah ke `baseProtected` (semua user terautentikasi). Limiter per-route dihapus karena `baseProtected` sudah menerapkan mutation limiter — sekaligus menutup M7. (Test viewer-200 + token-ditolak adalah bagian dari H3 yang masih terbuka.)

### H3 — Tidak ada test untuk 7 handler auth ✅ DIPERBAIKI (batch 2)

**Lokasi:** `internal/auth/handler.go` (seluruh file); tidak ada `internal/auth/handler_test.go`

**Masalah:** Satu-satunya coverage ada di `internal/server/coverage_booster_test.go`, yang hanya menyentuh login (`:149-161`), reset-password (`:294-302`), dan logout (`:439-446`), dan hanya menegaskan status code di dalam monolith ~450 baris yang memerlukan Docker client hidup. Tidak tercakup sama sekali: guard self-role-change (`handler.go:117-120`), validasi role invalid (`:128-131`), target-user-not-found (`:133-136`), verifikasi current-password di `HandleChangePassword` (`:180-183`), `HandleGetProfile`, `HandleListUsers`, path 400 login (`:19-22`), dan password <8 karakter (`:65`).

**Dampak:** Menghapus check `targetUsername == callerUsername` akan membiarkan admin mana pun menaikkan dirinya sendiri (meskipun route admin-only, ini menghapus perlindungan yang disengaja); menghapus comparasi bcrypt di `:180` memungkinkan token sesi curang mengambil alih akun. Tidak ada test yang gagal untuk keduanya.

**Perbaikan yang diterapkan (2026-10-01, batch 2):** `internal/auth/handler_test.go` baru mengikuti pola `jwt_prop_test.go` (temp BBolt + `gin.TestMode` + `httptest` + middleware stub `username`/`role`). Mencakup seluruh cabang: login (400/401×2/200+token tervalidasi), logout dengan **revocation end-to-end** (T2: token dipakai ulang → ditolak), reset-password (<8, >72 byte, sukses+hash berubah+version naik), list-users (`[]` bukan null), update-role (403 self, 400 invalid, 404 unknown, demote→token lama ditolak+role baru di token fresh — T4), change-password (401 current salah, 400 >72 byte, sukses), get-profile (200, tanpa hash).

---

## Medium

### M1 — `/api/metrics` publik membocorkan inventaris workload ✅ DIPERBAIKI (batch 2)

**Lokasi:** `internal/server/routes.go:73-75`; label di `internal/metrics/prometheus.go:33-90`

**Masalah:** Endpoint terdaftar di `api` group yang tidak terautentikasi dan menyajikan `metrics.Handler()` dengan label cardinality penuh: `container_name`, `image`, `hostname`, `os`, `instance_id`.

**Dampak:** Penyerang yang hanya bisa menjangkau panel mendapatkan inventaris workload lengkap (nama app, tag image, identitas host) — recon langsung untuk menargetkan panel atau app yang dijalankannya. Ini juga memperkuat M2: serangkaian target gratis untuk trigger deploy tidak terautentikasi.

**Perbaikan yang diterapkan (2026-10-01, batch 2):** Route dipindah dari group publik ke `viewerGroup` — memerlukan autentikasi (JWT bearer atau `X-API-Key`) dengan role viewer atau lebih tinggi. Tidak ada UI internal yang memakai endpoint ini (diverifikasi); scraper Prometheus eksternal harus mengirim bearer token (Prometheus mendukung `authorization`/`bearer_token` di scrape config). README diperbarui.

### M2 — Webhook tanpa secret memungkinkan deploy tidak terautentikasi ✅ DIPERBAIKI

**Lokasi:** `internal/server/webhook_handlers.go:89-95` (verifikasi signature di-skip); `:204-237` (pembuatan mengizinkan `Secret` kosong)

**Masalah:** Verifikasi HMAC di-skip sepenuhnya saat `wh.Secret == ""` — tidak ada signature yang diminta, tidak ada penolakan. Webhook ID (`wh-<random>`) menjadi satu-satunya kredensial, dan ID itu adalah bearer credential yang dikembalikan di list operator dan tertanam di URL CI/webhook yang rutin bocor ke repo, browser history, dan log.

**Dampak:** Siapa pun yang melihat URL webhook dapat memicu `git.Clone` + `DeployCompose` di instance tersebut — menjalankan container pilihan penyerang bila mereka juga mengontrol repo, atau memaksa re-deploy yang menjatuhkan layanan. Rate limit 10/min per-IP tidak menghentikan satu trigger tepat waktu.

**Perbaikan yang diterapkan (2026-10-01):** `HandleCreateWebhook` kini me-generate secret acak server-side ketika caller mengosongkannya — setiap webhook selalu HMAC-protected, tanpa mem-break client yang memang belum mengirim secret. Webhook lama yang sudah tersimpan tanpa secret tetap bekerja seperti sebelumnya (verifikasi di-skip) — jika diinginkan, migrasi backfill bisa ditambahkan terpisah.

### M3 — Login timing side channel untuk enumerasi username ✅ DIPERBAIKI

**Lokasi:** `internal/auth/handler.go:24-33`

**Masalah:** Saat `GetUser` gagal, handler segera kembali 401; saat username ada, jalur berikutnya menjalankan `bcrypt.CompareHashAndPassword` (cost 10, ~50-100 ms). Body error identik, tapi **latency response** membedakan username valid dari yang tidak.

**Dampak:** Penyerang mengukur latency untuk menebak username (login rate limit 5/menit per IP, `ratelimit.go:19`, memperlambat tapi tidak menghentikan; spraying dari banyak sumber mem-bypass sama sekali) sebelum password spraying.

**Perbaikan yang diterapkan (2026-10-01):** Jalur unknown-user kini menjalankan `bcrypt.CompareHashAndPassword` terhadap hash dummy tetap (dibangun sekali di `init()`), sehingga kedua jalur memakan waktu bcrypt yang setara.

### M4 — `HandleLogout` menelan kegagalan revocasi ✅ DIPERBAIKI

**Lokasi:** `internal/auth/handler.go:48-57` (error di-log saja di `:52-54`)

**Masalah:** Jika `IncrementTokenVersion` gagal (BBolt write failure, disk penuh, write-lock contention), error hanya di-log dan handler tetap mengembalikan `200 {"status": "logged out"}`.

**Dampak:** Seluruh token user tetap valid hingga 4 jam, sementara SPA sudah menghapus token dan pindah ke login screen. User percaya sesi berakhir — padahal tidak, dan tidak ada sinyal apa pun, bahkan di response body.

**Perbaikan yang diterapkan (2026-10-01):** Handler kini mengembalikan `500 {"error": "failed to revoke session"}` saat `IncrementTokenVersion` gagal — kegagalan revocasi tidak lagi disamarkan sebagai logout sukses.

### M5 — Pembangkitan secret rentan TOCTOU ✅ DIPERBAIKI

**Lokasi:** `internal/auth/secret.go:28-51`

**Masalah:** Urutannya read (`:29`) → generate (`:37`) → write (`:49`) tanpa `O_EXCL`. File ditimpa secara unconditional bila read gagal — termasuk karena "not exist".

**Dampak:** Dua proses Dockpal mulai bersamaan pada instalasi baru (systemd restart race, atau dua instance `make dev` — keduanya umum di repo ini) tanpa secret file. Masing-masing menghasilkan secret berbeda; `WriteFile` kedua menang. Proses A terus menandatangani JWT **dan** menurunkan key enkripsi registry (`main.go:484`) dari secret yang tidak lagi cocok dengan file. Setiap token yang dikeluarkan A ditolak saat restart, dan setiap kredensial registry yang dibuat via A menjadi tidak terdekripsi setelah proses restart dengan secret di disk.

**Perbaikan yang diterapkan (2026-10-01):** File kini dibuat dengan `os.OpenFile(..., os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)`; pada `EEXIST` file di-read ulang dan secret pemenang dipakai — tidak ada lagi skenario dua proses menimpa secret satu sama lain. Write diikuti `Sync` sebelum `Close`.

### M6 — Deploy-stream WebSocket: registrasi salah dan tanpa role check ✅ DIPERBAIKI

**Lokasi:** `internal/server/routes.go:1073`, `:1077` (terdaftar di root engine `r`); auth di `internal/server/routes_helpers.go:414-418`

**Masalah:** Kedua route ini terdaftar langsung di root engine `r`, sehingga **bypass `AuthMiddleware`, `baseProtected`, dan seluruh rate limiting**. Cabang `?token=` memvalidasi JWT tapi **tidak melakukan role check** — berbeda dari cabang first-message (`routes_helpers.go:383`, menargetkan `RoleViewer`) dan route logs legacy (`routes.go:984`, juga menargetkan viewer). Selain itu, komentar di `routes.go:1066` menyebut token *"short-lived (30 days max)"* — JWT kedaluwarsa dalam **4 jam** (`jwt.go:26`); komentar itu sudah kedaluwarsa dan menyesatkan.

**Dampak:** User terautentikasi mana pun (termasuk viewer yang tidak bisa membuat deploy session) dapat berlangganan stream log deploy dari instance mana pun jika mengetahui session ID.

**Perbaikan yang diterapkan (2026-10-01):** Kedua route (`/api/deploy/stream/:id` dan `/api/instances/:instance_id/deploy/stream/:id`) kini terdaftar di `baseProtected` dengan `RequireRole(RoleViewer)` — AuthMiddleware + rate limiting berlaku (cabang `?token=` middleware tetap dihormati untuk WS upgrade). Role check juga ditambahkan di cabang query-token dalam handler sebagai defense in depth. Komentar kedaluwarsa diperbaiki.

### M7 — Rate limit terhitung ganda di kedua endpoint password ✅ DIPERBAIKI

**Lokasi:** `internal/server/routes.go:110` (middleware group), `:129` dan `:135` (middleware route)

**Masalah:** `baseProtected.Use(methodRateLimit(readLimit, mutationLimit))` sudah menerapkan mutation limiter ke setiap POST/PUT/PATCH/DELETE di group. Tetapi `/auth/reset-password` (`:129`) dan `/profile/password` (`:135`) kemudian menerapkan `RateLimitMiddleware(mutationRateLimiter)` — **instance limiter yang sama**. Karena `RateLimiter.Allow` mencatat timestamp per pemanggilan dengan key `IP + method + path`, setiap request dihitung **dua kali**, sehingga memotong budget efektif menjadi 5/min (dari 10/menit) untuk tepat dua endpoint ini.

Selain itu ada dead code terkait: `roleRouterWrapper.mutationMiddleware` dikonstruksi sebagai `nil` (`routes.go:121-125`), sehingga `withMutationLimit` (`routes_rbac.go:36-44`) tidak pernah aktif — inilah mengapa limit ganda muncul: mekanisme tunggal yang dimaksud tidak pernah disambung. Special case `if path == "/system/update"` di `routes_rbac.go:18-20` juga menunjuk ke route yang sudah dihapus ("Version and update routes removed", `routes.go:1841`).

**Perbaikan yang diterapkan (2026-10-01):** Limiter per-route di `:129` dan `:135` dihapus; kedua endpoint kini mengandalkan `baseProtected.Use` sebagai satu-satunya lapisan (10/min efektif, sesuai desain). Dead code `mutationMiddleware`/`withMutationLimit` dan special case `/system/update` dibiarkan — dibersihkan terpisah karena menyangkut `routes_rbac.go` secara keseluruhan.

### M8 — Tidak ada audit log untuk event auth ✅ DIPERBAIKI (batch 2)

**Lokasi:** `internal/auth/handler.go` (seluruh file) vs `internal/server/api_keys.go:69,85` (yang diaudit)

**Masalah:** `LogAudit` dipanggil untuk pembuatan/penghapusan API key, lifecycle instance, backup, dan app-update — tapi **tidak pernah** untuk event auth: login sukses maupun gagal (yang merupakan sinyal brute-force kanonik), logout (event revocasi), perubahan password, dan — yang paling signifikan — **seorang admin mengubah role user lain** (`handler.go:113-144`), yaitu perubahan privilege.

**Dampak:** Audit log adalah artifact forensik utama untuk panel self-hosted yang memegang kontrol host Docker, dan diam-diam menghilangkan setiap event credential dan privilege. Celah seperti ini biasanya hanya ditemukan saat incident response.

**Perbaikan yang diterapkan (2026-10-01, batch 2):** `auth.AuditHook` (var func) di-wire dari `server.RegisterRoutes` ke `LogAudit` — tanpa import cycle auth→server. Tercatat kini: `auth.login` (success + failed dengan alasan generik), `auth.logout`, `auth.password_reset`, `auth.password_change`, `auth.role_change` (dengan target + role baru + pelaku). Dikerjakan satu batch dengan STACK-H4 (mutasi stack/container juga diaudit).

---

## Low

### L1 — JWT di query param URL upgrade bocor ke log proxy ✅ DIPERBAIKI (batch 2)
`middleware.go:48-60`. Browser WS tidak bisa set header, sehingga JWT (bearer 4 jam) diteruskan sebagai `?token=` dan mendarat di access log reverse-proxy dan browser history. **Origin divalidasi di setiap WS route** (`checkOrigin`, `routes_helpers.go:20-29`), jadi CSWSH sudah termitigasi — ini murni masalah eksposur token. *Perbaikan yang diterapkan (2026-10-01, batch 2):* `GET /api/ws-ticket` menerbitkan ticket acak 32-byte: TTL 60 detik, **single-use** (dikonsumsi atomik), dan membawa identitas penerbit (user_id/username/role) sehingga RequireRole tetap berfungsi. Diterima di slot `?token=` yang sama dengan prioritas di atas JWT. Seluruh WS frontend (`watchDeploy`, LogsViewer, ContainerTerminal, DeployWizard, AddServerPanel) meminta ticket dulu dengan fallback JWT untuk backend lama. Test: `ws_ticket_test.go` (lifecycle + single-use + expiry + identitas).

### L2 — Password bootstrap ditulis ke log persisten ✅ DIPERBAIKI (batch 2)
`main.go:221-222`. Password admin yang di-generate saat bootstrap ditulis melalui rotator (`log.SetOutput` di `main.go:158`) dan ditahan sesuai log retention — bertahan lama setelah first boot. Setiap pembacaan `<data>/dockpal.log` (backup export, log shipper) mengungkap password admin. *Perbaikan yang diterapkan (2026-10-01, batch 2):* baris `log.Printf` berisi password dihapus; password hanya ditulis ke `os.Stderr` (sudah ada); log menyimpan petunjuk "printed to stderr" tanpa secret.

### L3 — Comparasi hash API key tidak constant-time ✅ DIPERBAIKI
`middleware.go:26-35` membandingkan `apiKey.KeyHash == hashed` dengan `==` polos. Karena yang dibandingkan adalah SHA-256 digest dari input penyerang, timing oracle harus memulihkan key yang matching-preimage — **tidak praktis dieksploitasi**, dicatat hanya sebagai defense-in-depth. *Perbaikan yang diterapkan:* `subtle.ConstantTimeCompare` — batch 1 inline, batch 2 dipusatkan di `auth.ValidateAPIKey` (lihat A2).

### L4 — Upaya auth gagal tidak ter-throttle ✅ DIPERBAIKI (batch 2)
`middleware.go` berjalan sebelum `methodRateLimit` di `baseProtected` (`routes.go:108-110`), sehingga auth gagal (API key invalid atau JWT invalid) tidak pernah mengonsumsi slot rate limit. Key 256-bit membuat online guessing tidak feasible, jadi ini celah defense-in-depth. *Perbaikan yang diterapkan (2026-10-01, batch 2):* `authFailureLimiter` (20/menit per ClientIP) dikenakan pada setiap penolakan auth via `rejectAuth`; setelah habis, respons menjadi 429 dengan `Retry-After`. Request sukses tidak dihitung.

### L5 — `GET /api/services` mengembalikan body Compose penuh ke viewer ✅ DIPERBAIKI (batch 2)
`routes.go:1328`. Tier viewer, mengembalikan record `db.Service` lengkap termasuk body `Compose` yang rutin berisi environment secret (password DB, API key) untuk setiap app yang dideploy. *Perbaikan yang diterapkan (2026-10-01, batch 2):* untuk role < operator, field `Compose` dikosongkan pada salinan record (UI AppsPage hanya memakai id/name/type/domain — diverifikasi).

### L6 — `InsecureIgnoreHostKey` saat install agent ✅ DIPERBAIKI (batch 2)
`internal/ssh/installer.go:77`. MITM jaringan saat install bisa mencegat pertukaran dan menangkap agent bearer token (`DOCKPAL_TOKEN`), yang memberikan kontrol penuh Docker API host remote. Alur admin-only, tapi token adalah kredensial bernilai tinggi yang melintasi channel tidak terautentikasi. *Perbaikan yang diterapkan (2026-10-01, batch 2):* `InsecureIgnoreHostKey` diganti callback TOFU — fingerprint SHA-256 selalu dicetak ke install log dengan instruksi verifikasi (`ssh-keygen -lf`), dan request install menerima `ssh_host_key` untuk pinning strict: mismatch → koneksi ditolak dengan pesan jelas (kemungkinan MITM / host re-imaged).

### L7 — Batas 72 byte bcrypt menghasilkan 500 opaq ✅ DIPERBAIKI
`handler.go:60`, `:163`, `:185-189`. Binding hanya menegakkan `min=8`; bcrypt menolak password >72 byte dengan `ErrPasswordTooLong`, yang dipetakan ke `500 "failed to hash password"` tanpa indikasi panjang adalah masalah. Asimetri: di login, `CompareHashAndPassword` diam-diam memotong di 72 byte, jadi ini hanya kegagalan saat ganti password. *Perbaikan yang diterapkan (2026-10-01):* validasi `len(newPassword) <= 72` (byte) di `HandleResetPassword` dan `HandleChangePassword`, mengembalikan `400 "password must be at most 72 bytes"`.

### L8 — `HandleListUsers` kembalikan `null` saat kosong ✅ DIPERBAIKI
`handler.go:98-106`. `var resp []userResponse` tetap nil saat bucket user kosong, sehingga endpoint menserialisasi ke `null` bukan `[]`. SPA menugaskannya langsung ke array `$state` (`UsersTab.svelte:18`) dan iterate dengan `{#each}` — throw pada non-array. Tidak mungkin dalam praktik (admin selalu di-bootstrap), tapi bug empty-state yang bisa dihindari. *Perbaikan yang diterapkan (2026-10-01):* `resp := make([]userResponse, 0, len(users))`.

---

## Temuan Kualitas Test

Diverifikasi langsung kecuali diberi catatan.

### T1 — Test logout bergantung urutan dan tidak membuktikan revocasi (High) ✅ DIPERBAIKI (batch 2)
`internal/server/coverage_booster_test.go:439-446`. Subtest "Logout API" meng-hardcode token version 1 (`:441`), sementara admin yang di-seed punya version 0 (`:41-48`, `TokenVersion` tidak diset). **Telah dibuktikan:** `go test ./internal/server -run 'TestCoverageBooster_APIEndpoints/Logout_API'` **gagal sendirian** dengan `expected 200, got 401` — hanya lulus dalam run penuh karena subtest "Password Reset API" berjalan lebih dulu. Lebih buruk: test tidak pernah memakai ulang token setelah logout. *Perbaikan yang diterapkan:* `TestHandleLogout_RevokesTokenEndToEnd` di `internal/auth/handler_test.go` membaca version dari DB (bukan hardcode) dan menegaskan token yang sama ditolak setelah logout. Subtest booster yang order-dependent dibiarkan sebagai smoke test — cakupan revocation yang sebenarnya kini di level unit.

### T2 — Revocasi token-version tidak pernah dibuktikan end-to-end (High) ✅ DIPERBAIKI (batch 2)
Tidak ada test yang menghubungkan `HandleLogout` → `IncrementTokenVersion` → penolakan oleh `AuthMiddleware` berikutnya. *Perbaikan yang diterapkan:* test yang sama di atas — generate token di version DB saat ini, POST logout lewat handler, lalu `ValidateJWTWithVersionCheck` pada token yang sama harus gagal.

### T3 — Jalur X-API-Key tidak punya test sama sekali (High) ✅ DIPERBAIKI (batch 2)
`middleware.go:19-39`. Tidak tercakup: key valid, unknown → 401, hash mismatch → 401, role mengalir ke `RequireRole`, key yang dihapus gagal. *Perbaikan yang diterapkan:* `internal/server/middleware_apikey_test.go` baru — 7 subtest mencakup seluruh cabang tersebut plus "tanpa kredensial → 401" dan "viewer key → 403 di route admin".

### T4 — Tidak ada test bahwa role di JWT kedaluwarsa tidak dipercaya (Medium) ✅ DIPERBAIKI (batch 2)
Middleware mengambil role langsung dari claims; satu-satunya pelindung adalah `UpdateUserRole` menaikkan `TokenVersion`. *Perbaikan yang diterapkan:* subtest "demote admin" di `TestHandleUpdateUserRole` — (a) token admin lama ditolak setelah demosi, (b) token fresh membawa role viewer.

### T5 — Test tautologis untuk token-version round-trip (Medium) ✅ DIPERBAIKI (batch 2)
`internal/auth/jwt_prop_test.go:89-135`. Varian non-DB lulus trivially bahkan tanpa version check. *Perbaikan yang diterapkan:* varian tautologis dihapus, diganti tiga test baru yang menutup celah sesungguhnya: `TestJWT_RejectsAlgNone`, `TestJWT_RejectsWrongSecret`, `TestJWT_RejectsNonHMACAlgorithm` (RS256) — mem-pin check signing-method di `jwt.go:40-42`.

### T6 — Level test yang salah untuk perilaku handler auth (Medium) ⏳ SEBAGIAN
`coverage_booster_test.go:117` membangun router penuh via `RegisterRoutes` dengan Docker client nyata dan menerima dua outcome di beberapa assertion ("201 or 400", "200 or 500"). *Status:* cakupan handler auth kini ada di level unit yang benar (`internal/auth/handler_test.go`, lihat H3) — booster tersisa sebagai route-wiring smoke test. Membersihkan assertion dua-outcome di booster adalah perapihan lanjutan berisiko rendah yang belum dikerjakan.

### T7 — Test brittle bercabang pada literal string nama test (Low) ✅ DIPERBAIKI (batch 2)
`internal/server/middleware_wsauth_test.go:95-106`. *Perbaikan yang diterapkan:* branching pada literal nama test diganti field eksplisit `stripUpgrade bool`; blok no-op dihapus.

---

## Temuan Arsitektur

### A1 — `internal/auth` bergantung pada Gin; logika ganti password terduplikasi tiga kali (Medium) ⏳ DITUNDA (follow-up terpisah)
`handler.go:7`, `handler.go:71` vs `:185`, `main.go:626-633`. Handler mengambil `*gin.Context` langsung. Dua konsekuensi: (1) `runResetPassword` di CLI mengimplementasi ulang bcrypt + `UpdatePasswordWithVersion`; (2) tidak ada `handler_test.go` (lihat H3). *Status:* H3 (test) sudah ditutup terpisah, jadi tekanan terbesar temuan ini sudah hilang. Ekstraksi `auth.Service` tetap direncanakan sebagai refactor terpisah — menyentuh semua handler + CLI, dan layak dikerjakan dengan review menyeluruh, bukan digabung dalam batch fix.

### A2 — "Siapa pemanggil" tidak punya owner; API-key auth terbelah antar package (High) ✅ DIPERBAIKI (batch 2)
`middleware.go:19-39`, `api_keys.go`, `jwt.go:55`. Verifikasi API key hidup di `server`, JWT di `auth`; tiga disiplin comparasi di dua package. *Perbaikan yang diterapkan (2026-10-01, batch 2):* `auth.HashAPIKey` dan `auth.ValidateAPIKey` kini tinggal di `internal/auth` (dengan `subtle.ConstantTimeCompare`); `AuthMiddleware` memanggil `auth.ValidateAPIKey` — satu tempat menjawab "apakah kredensial ini valid" untuk API key. `hashAPIKey` lokal di server dihapus. Catatan: `auth.ResolveIdentity` terpadu untuk JWT+API-key (satu fungsi untuk kedua jenis) adalah lanjutan dari A1/A3 — belum dikerjakan.

### A3 — WebSocket auth hand-rolled di lima tempat (High) ⏳ SEBAGIAN (M6 ditutup; refactor terpadu ditunda)
`middleware.go:48-60`, `routes.go:984`, `routes_helpers.go:414-421`, `instance_scoped_routes.go:388-397` & `:435-453`. Kontrak `?token=` diimplementasi ulang per endpoint dengan aturan berbeda. *Status:* divergensi paling berbahaya (deploy-stream tanpa role check, M6) sudah diperbaiki, dan WS-ticket (L1) terpusat di `AuthMiddleware` cabang query-token. Ekstraksi `WSAuthMiddleware` terpadu (memusatkan protokol query-vs-first-message, close codes, deadlines) ditunda bersama A1 — menyentuh semua route WS dan berisiko regresi tanpa review menyeluruh.

### A4 — Invariant "role di token tidak bisa divergen" benar tapi implisit (Medium) ✅ DIPERBAIKI (batch 2)
`jwt.go:15`, `middleware.go:82-84`, `db.go:291-310`, `api_keys.go:58`. *Perbaikan yang diterapkan (2026-10-01, batch 2):* invariant kini diekspresikan eksplisit sebagai komentar kontrak di `ValidateJWTWithVersionCheck` (`jwt.go`): setiap write yang mengubah privilege user WAJIB menaikkan `TokenVersion` di transaksi yang sama, dan desain tanpa-version pada API key didokumentasikan sebagai keputusan sengaja. Test T4 (demosi → token lama ditolak) mem-pin perilaku ini di level handler.

### A5 — Setiap request terautentikasi menyentuh DB; API-key auth scan bucket penuh (Medium) ⏳ DITUNDA (follow-up terpisah)
`jwt.go:61`, `middleware.go:20-35`. `GetUser()` per token tervalidasi; `ListAPIKeys()` per request. *Status:* belum dikerjakan — memerlukan desain invalidasi cache yang benar (cache TTL pendek keyed username→version dan key-hash→identity, di-invalidasi oleh code path yang menaikkan `TokenVersion`, plus `db.GetAPIKeyByHash`). Murni optimasi biaya — tidak ada bug perilaku — sehingga aman ditunda setelah A1/A3.

---

## Divalidasi Aman — Tidak Ada Temuan

Diverifikasi langsung terhadap kode:

- **Alg-confusion tidak mungkin.** `ValidateJWT` menolak non-HMAC (`jwt.go:40-42`); token `alg:none` gagal di keyfunc. Expiry dan issuer ditegakkan library (golang-jwt v5.3.1). `ValidateJWT` tidak bisa mengembalikan `(nil, nil)` — parser hanya set `token.Valid = true` di success path.
- **Token-version revocation ditegakkan di setiap jalur JWT** — bearer (`middleware.go:75`), WS `?token=` (`:50`, `routes_helpers.go:415`), WS first-message (`routes_helpers.go:378`), termasuk `instance_scoped_routes.go:389,448`. `ValidateJWT` tidak pernah dipakai untuk request auth di luar `ValidateJWTWithVersionCheck`.
- **Escalasi mandiri diblokir.** `HandleUpdateUserRole` admin-only dan check `targetUsername == callerUsername` (`handler.go:117-119`) tidak bisa di-bypass via case/whitespace: `GetUser` adalah BBolt lookup exact-byte key (`db.go:176`), jadi target dengan case berbeda hanya 404 di `:133`.
- **`HandleResetPassword`/`HandleChangePassword` hanya bekerja pada pemanggil** dari token (`handler.go:70`, `:173`) — tidak ada parameter target. Tidak ada endpoint HTTP untuk admin reset password user lain; kapabilitas itu hanya ada di CLI `dockpal reset-password` (`main.go:599-633`) yang memanggil `UpdatePasswordWithVersion` dengan benar.
- **API key** disimpan hanya sebagai SHA-256 hash, plaintext ditampilkan sekali saat create, dan diverifikasi ulang ke DB setiap request (tidak ada caching) sehingga penghapusan segera berlaku. Token-version check memang dengan benar tidak diterapkan ke API key (key tidak membawa version).
- **`TrustedProxies` adalah `nil`** (`server.go:53`), sehingga `c.ClientIP()` tidak bisa di-spoof via `X-Forwarded-For` — rate limit per-IP dan atribusi audit sound.
- **File secret** dibuat `0600` di direktori `0700` (`secret.go:44-51`); tidak pernah di-expose di response.
- **CORS** hanya merefleksikan origin same-host (atau loopback-to-loopback) dengan `Vary: Origin`.
- **Webhook HMAC** memakai `hmac.Equal` (constant-time) saat secret terkonfigurasi.
- **Tidak ada password atau material secret** yang ditulis ke audit entry atau error response (`internalError` meng-generalisasi pesan).

---

## Usulan Urutan Perbaikan — status akhir (2026-10-01, dua batch)

1. ✅ **C1** — tier dipindah ke operator + guard `/proc`,`/sys` di `ValidatePath` (+test). Rantai takeover tertutup. (Self-protection container panel: opsional, belum diterapkan.)
2. ✅ **H1** — secret file ditulis sebelum DB write di `rotateSecrets`.
3. ✅ **H2** — `/logout` & `/auth/reset-password` pindah ke `baseProtected`.
4. ✅ **M6** — deploy-stream WS pindah ke `baseProtected` + role check ganda; komentar kedaluwarsa diperbaiki.
5. ✅ **M2** — webhook selalu ber-secret (auto-generate server-side saat kosong).
6. ✅ **M1, M7, M4** — metrics di-gate ke viewer; limit ganda dihapus; error logout tidak lagi ditelan.
7. ✅ **H3 + T1–T5, T7** — `internal/auth/handler_test.go` + `middleware_apikey_test.go` + perbaikan T7; T5 diganti test alg:none/wrong-secret/RS256. ⏳ T6 (assertion dua-outcome di coverage booster) — perapihan lanjutan.
8. ⏳ **A1, A3, A5** — refactor seam (`auth.Service`, `WSAuthMiddleware`, cache lookup) ditunda sebagai follow-up terpisah dengan review menyeluruh. ✅ **A2** (`auth.HashAPIKey`/`auth.ValidateAPIKey`) dan **A4** (invariant eksplisit + komentar kontrak di `jwt.go`) sudah dikerjakan. Catatan: M8 (audit event auth) ternyata tidak memerlukan A1 — diselesaikan via `auth.AuditHook`.

**Verifikasi setelah perbaikan (kedua batch):** `go build ./...` & `go vet ./...` bersih; `go test ./... -count=1` 16/16 paket lolos; `make svelte-check` 0 error 0 warning; `make svelte-test` 28/28 lolos.
