# Phase 6: Sisa Coverage UI — Route Backend yang Belum Punya Halaman

> Dibuat 2026-08-20 setelah audit UI vs Go API. Backend punya ~75 route;
> UI baru cover: auth (login/logout/profile/password), containers (list/start/
> stop/restart/stats), images (list/pull/delete/prune), services (list/delete),
> templates (list/deploy/stream), dashboard health.

## Progress & Status

### ✅ Completed (Phase 6A-B-C-D-E) — SEMUA BATCH SELESAI

| Item | Details | Status |
|---|---|---|
| **RBAC foundation** | `isAdmin`/`isOperator` derived stores, Sidebar filter, 401 → logout | ✅ Done (Batch 0) |
| **Admin pages** | Users (change role), API Keys (create/delete/copy), Audit Logs (paginated), Registries (CRUD + test), Backup (trigger + results), System Info, Tunnel | ✅ Done (Batch A+E) |
| **Operator pages** | Webhooks (CRUD + copy URL), Domains (CRUD Traefik), Apps (auto-update list/trigger/history/toggle) | ✅ Done (Batch B) |
| **Deploy paths** | TemplatesPage extended: Compose mode (textarea YAML), Git mode (repo/branch/path) | ✅ Done (Batch C) |
| **Container detail** | ContainerPage (`/app/container/:id`): inspect stats, edit (name/restart), start/stop/restart/delete actions | ✅ Done (Batch D) |
| **Long tail** | System info tab, images pull-force/check-update, tunnel deploy/teardown, quick reset password | ✅ Done (Batch E) |

### ⏭️ Remaining (opsional, bukan bagian Phase 6)

- **Files browser** = phase terpisah (tree view, upload/download/write/delete per container —
  butuh ~2-3 hari sendiri)
- **WS container logs viewer** (`GET /containers/:id/logs` WebSocket) — pola sudah ada di
  DeployWizard, tinggal duplikasi ke ContainerPage bila diperlukan

## Batch plan

### Batch 0 — Fondasi RBAC + shared (0.5 hari)
- `lib/store.ts`: derived role store (`isAdmin`, `isOperator`) dari `currentUser.role`
- `Sidebar.svelte`: filter nav item per role (item admin hidden dari viewer/operator)
- `lib/api/client.ts`: handle 401 → clear token + redirect ke login state
- Types: `User` (admin view), `ApiKey`, `AuditEntry`, `Registry`, `Webhook`, `Domain`, `AppSummary` di `types/generated.ts`

### Batch A — Admin pages (1.5 hari)
1. **UsersPage**: table users, change role dropdown (`PUT /users/:username/role`), guard: tidak boleh ubah role sendiri
2. **ApiKeysPage**: list, create (tampilkan token sekali), delete, copy-to-clipboard
3. **AuditLogsPage**: table + filter (user/action/resource) + pagination client-side
4. **BackupPanel** (di SettingsPage): tombol trigger `POST /backup`, status toast
5. **RegistriesPage**: CRUD registries + tombol "Test connection" (`POST /registries/:id/test`), password field masked (write-only — backend tidak pernah return password)

### Batch B — Operator pages (1.5 hari)
1. **WebhooksPage**: list, create (pilih service), delete, copy URL + token. Catatan: URL webhook `/api/webhooks/deploy/:id` unauthenticated by design
2. **DomainsPage**: list, add, delete (Traefik integration)
3. **AppsPage** (auto-update): list apps dengan badge update-available, trigger update, riwayat per-app (`/apps/:name/updates`), stream progress via WS `/apps/updates/stream` (pakai `StreamClient` phase 4)

### Batch C — Deploy paths tambahan (1 hari)
1. **DeployWizard mode compose**: textarea YAML + nama → `POST /deploy/compose` (instance-scoped)
2. **DeployWizard mode git**: pilih repo (`GET /github/repos`), branch, path → `POST /deploy/git`
3. Tabs mode di TemplatesPage header: `Template | Compose | Git`

### Batch D — Container detail (1.5 hari)
1. **ContainerDetail** (expand di ContainersPage atau halaman sendiri):
   - inspect (`GET /containers/:id`) — env, mounts, network, labels
   - logs viewer — WS `GET /containers/:id/logs?tail=N` (pakai StreamClient pattern, auth via first-message)
   - edit modal (`PUT /containers/:id`) — rename, env, restart policy
   - update image (`POST /containers/:id/update-image`) dengan konfirmasi
   - delete dengan confirm modal (`DELETE /containers/:id`)

### Batch E — Long tail / opsional (1 hari)
1. **SystemInfo widget** (SettingsPage): `GET /system/info` + `GET /config`
2. **Images**: tombol per-row `pull-force`, `check` update (sudah ada list badge)
3. **Reset password** (`POST /auth/reset-password`) — cek apakah alur ini dipakai tanpa login; kalau admin-only, taruh di UsersPage
4. **Tunnel** (SettingsPage, admin): setup/teardown Cloudflare tunnel
5. **Files browser** — PALING BESAR, bisa jadi Phase 7 terpisah: tree view, read/write/upload/download. Jangan gabung batch E kalau waktu ketat.

## Urutan & dependensi

```
Batch 0 (fondasi) → Batch A & B (paralel mungkin) → Batch C → Batch D → Batch E
```

Total estimasi: **6-7 hari** fokus (tanpa Files browser; +2-3 hari kalau masuk).

## Definition of done per halaman

- [ ] Type di `generated.ts` match Go JSON (cek struct tag!)
- [ ] `npm run check` 0 errors, build pass
- [ ] Endpoint dipakai diverifikasi manual (curl atau UI) — termasuk shape response
- [ ] Role guard: tombol admin tidak muncul untuk non-admin
- [ ] Toast untuk sukses/error
- [ ] Konfirmasi untuk aksi destruktif (delete)

## Catatan teknis

- Semua mutation operator+ pakai instance-scoped (`/api/instances/:id/...`) bila
  multi-instance; single-instance boleh pakai legacy `/api/...` (backend dua-duanya hidup)
- WS endpoints butuh `?token=` query param (browser tidak bisa kirim header)
- Logs WS pakai auth first-message pattern (`authenticateWebSocketFirstMessage`)
- Jangan percaya docs lama — selalu cek struct tag Go (`json:"..."`) sebagai
  source of truth sebelum nulis type TS
