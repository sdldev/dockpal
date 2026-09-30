# Container Detail — Bug Findings (2026-09-30)

> Verifikasi langsung di browser terhadap halaman detail container
> (`/containers/:id`) pada commit `2df3f0b` (feat(containers): logs, env/config,
> and interactive terminal in container detail). Environment: dev server
> `http://localhost:3012`, container `adminer_adminer`, login admin.

## Ringkasan

Halaman detail memiliki 4 tab — **Overview / Logs / Env & Config / Terminal**.
Diverifikasi dengan browser sungguhan (klik, ketik di terminal, restart container,
buka modal Edit). Terminal, Env & Config, dan aksi container **berfungsi**;
**Logs dan Stats rusak** karena dua bug autentikasi yang berdiri sendiri.

| # | Bug | Severity | Komponen | Status |
|---|-----|----------|----------|--------|
| 1 | Tab Logs tidak pernah menampilkan log (WS auth mismatch) | **High** | `internal/server/instance_scoped_routes.go` | rusak |
| 2 | Stats selalu "No stats available" (token localStorage vs sessionStorage) | **High** | `svelte/src/components/Container/StatsChart.svelte` | rusak |
| 3 | Ports & Created kosong di Overview | Medium | `internal/docker/container.go` | rusak |
| 4 | Field `command` tidak ada di detail API | Low | `internal/docker/container.go` | hilang |
| 5 | Port terduplikasi di containers list | Low | `svelte/src/lib/format.ts` | rusak |

Saran urutan perbaikan: **#1 → #2** (keduanya kecil dan berdampak besar — Logs dan
Stats adalah fitur andalan halaman), lalu #3–#5 sebagai satu PR pembersihan.

---

## Yang sudah berfungsi (baseline regresi)

Diverifikasi dan **lulus** — jangan rusakkan saat memperbaiki bug di atas:

- **Navigasi & layout**: klik nama container di list → `/containers/:id`; header
  (nama, ID, status badge), tombol Edit/Start/Stop/Restart/Delete, nav tabs.
- **Env & Config**: 14 env variables ter-list, Networks (`bridge → 172.17.0.2`),
  section Mounts dengan count. Data mengalir dari endpoint detail
  (`GET /api/containers/:id`).
- **Terminal (exec)**: docker exec bekerja penuh melalui WS yang diautentikasi
  `?token=`. Shell prompt muncul; perintah `echo HELLO_FROM_DOCKPAL` diketik
  per-karakter dan kembali dengan output yang benar. Untuk instance `local`
  melalui `LocalClient`; instance edge memang belum didukung (lihat catatan).
- **Restart**: tombol Restart memicu API, container hidup kembali, dikonfirmasi
  via `docker inspect` (StartedAt berganti).
- **Edit modal**: terbuka dengan nama terisi + dropdown restart policy
  (`no`/`always`/`unless-stopped`/`on-failure`) dengan nilai saat ini terpilih.

Catatan non-bug: output terminal merender ANSI mentah secara harfiah
(mis. `\x1b[6n` terlihat di layar). Ini disengaja — komponen sengaja tidak
memakai xterm.js (`ContainerTerminal.svelte:4-8`); pendukung ANSI penuh bisa
ditambahkan kemudian tanpa menyentuh backend.

---

## Bug 1 — Tab Logs tidak pernah menampilkan log

**Severity: High** · Logs adalah salah satu fitur wajib yang diminta untuk
halaman ini, dan backend-nya sebenarnya sudah jalan — hanya autentikasinya yang
tidak cocok dengan frontend.

### Gejala

Tab Logs terlihat terhubung (indikator "streaming"), lalu setelah ~5 detik
berubah menjadi **"disconnected"** dan area log hanya menampilkan
**"Waiting for output…"** selamanya, meskipun container aktif menghasilkan log
(dikonfirmasi: `docker logs adminer_adminer` menampilkan request HTTP yang
sengaja dibuat selama pengujian).

### Root cause

Autentikasi WS untuk logs hanya menerima token sebagai **pesan JSON pertama**,
sedangkan frontend **hanya mengirim token via query parameter** dan tidak pernah
mengirim pesan tersebut.

`internal/server/instance_scoped_routes.go:385`:

```go
if !authenticateWebSocketFirstMessage(conn, c) {
	conn.Close()
	return
}
```

`authenticateWebSocketFirstMessage` (`internal/server/routes_helpers.go:371`)
hanya memanggil `conn.ReadJSON(&authMsg)` — tidak ada fallback ke
`c.Query("token")`. Karena client tidak pernah mengirim pesan itu, `ReadJSON`
blok 5 detik (read deadline), lalu server menutup koneksi dengan close code
`4001 "authentication required"`. Logs tidak pernah di-stream.

### Perbaikan

Handler exec di file yang sama sudah menerapkan pola yang benar
(`instance_scoped_routes.go:423-436`): cek query token dulu, jatuh ke pesan
pertama hanya jika tidak ada. Handler deploy stream juga memakai pola yang sama
(`routes_helpers.go:414-421`). Samakan handler logs:

```go
// Auth: query token (browser WS) or first {token} message (API clients).
authToken := c.Query("token")
if authToken == "" {
	if !authenticateWebSocketFirstMessage(conn, c) {
		conn.Close()
		return
	}
} else {
	claims, err := auth.ValidateJWTWithVersionCheck(authToken, jwtSecretFromContext(c), databaseFromContext(c))
	if err != nil || !auth.HasRole(claims.Role, auth.RoleViewer) {
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "authentication failed"))
		conn.Close()
		return
	}
}
```

Logs adalah operasi read-only, jadi `RoleViewer` adalah minimum yang tepat
(sama seperti `authenticateWebSocketFirstMessage` saat ini).

---

## Bug 2 — Stats selalu "No stats available (container not running)"

**Severity: High** · Muncul di dua tempat: tab Overview halaman detail **dan**
panel "Details" yang bisa di-expand di halaman daftar container.

### Gejala

Panel Container Stats menampilkan "Loading…" lalu
**"No stats available (container not running)"** terus-menerus, padahal container
`running` dan endpoint stats sendiri sehat — `GET /api/containers/:id/stats`
mengembalikan JSON CPU/memory yang valid saat dipanggil langsung dengan
`Authorization: Bearer` yang benar.

### Root cause

Token JWT disimpan di **sessionStorage**, tapi StatsChart membacanya dari
**localStorage** — yang selalu `null`. Akibatnya setiap request stats berangkat
tanpa header `Authorization` → `401 missing authorization header`.

`internal/...` bukan penyebabnya; ini murni frontend. Token disimpan di
`sessionStorage` (`svelte/src/lib/api/client.ts:18`, dengan komentar eksplisit:
"JWT stored in sessionStorage so a token never survives past the browser tab").
Yang salah baca:

- `svelte/src/components/Container/StatsChart.svelte:46` — `localStorage.getItem('dockpal_token')`
- `svelte/src/lib/api/stacks.ts:125` — `localStorage.getItem('dockpal_token') ?? ''` (WS stream stack logs)

Komponen lain sudah benar: `LogsViewer.svelte` dan `ContainerTerminal.svelte`
memakai `getToken()` dari `$lib/api/client`.

### Perbaikan

Ganti pembacaan localStorage dengan `getToken()`:

```ts
// StatsChart.svelte
import { getToken } from '$lib/api/client';
// ...
const token = getToken();
```

`StatsChart` sengaja memakai `fetch` mentah (bukan `api.get`) supaya 401
transient tidak memicu global logout — pertahankan perilaku itu, cukup ganti
sumber token-nya saja. Saat ini cabang 401 malah tidak pernah tercapai karena
401 bukan 400/404; setelah token benar, response.ok akan `true` dan grafik
hidup (CPU %, Memory %, Network I/O, polling 2.5 detik).

---

## Bug 3 — Ports & Created kosong di Overview

**Severity: Medium** · Section Details menampilkan Image, Restart policy, dan
Network mode, tapi **Ports dan tanggal created tidak pernah muncul**.

### Gejala & root cause

`InspectContainer` meng-hardcode keduanya — `internal/docker/container.go:154`:

```go
info := &ContainerDetail{
	ContainerInfo: ContainerInfo{
		// ...
		Ports:   []container.PortSummary{},
		Created: 0,
	},
	// ...
}
```

Network settings dan host config sudah di-inspect di fungsi yang sama (lihat
baris 124-145), jadi data sebenarnya sudah ada di tangan — hanya tidak
dimasukkan. Endpoint list (`ListContainers`, `container.go:92-93`) sudah
mengisi `Ports: ctr.Ports` dan `Created: ctr.Created`, sehingga port terlihat
di tabel container tapi menghilang begitu pengguna masuk ke detail.

### Perbaikan

Populasi dari inspect result. Ports publik ada di
`ctr.NetworkSettings.Ports` (tipe `nat.PortMap`); konversi ke
`[]container.PortSummary`:

```go
var ports []container.PortSummary
if ctr.NetworkSettings != nil {
	for port, bindings := range ctr.NetworkSettings.Ports {
		for _, b := range bindings {
			ports = append(ports, container.PortSummary{
				IP:          b.HostIP,
				PrivatePort: uint16(port.Int()),
				PublicPort:  uint16(...), // dari b.HostPort
				Type:        port.Proto(),
			})
		}
	}
}
```

`Created` cukup `ctr.Created`. Setelah ini, `{#if detail.ports...}` dan
`{#if detail.command}` di `ContainerPage.svelte` akan otomatis merender.

---

## Bug 4 — Field `command` tidak ada di detail API

**Severity: Low** · Frontend sudah siap menampilkannya, backend tidak
menyediakannya.

`ContainerPage.svelte:216-221` merender `{#if detail.command}` — tapi
`ContainerDetail` (`internal/docker/container.go:102-112`) tidak punya field
`Command`, jadi `{detail.command}` selalu `undefined`. Command tersedia di
inspect sebagai `ctr.Config.Cmd` (`[]string`).

### Perbaikan

Tambahkan field dan populasi:

```go
type ContainerDetail struct {
	ContainerInfo
	Platform      string                 `json:"platform"`
	Command       []string               `json:"command"`
	// ...
}

// di InspectContainer:
Command: ctr.Config.Cmd,
```

Frontend perlu join array (`detail.command.join(' ')`) — perbarui juga tipe
`ContainerDetail` di `svelte/src/lib/types/generated.ts`.

---

## Bug 5 — Port terduplikasi di containers list

**Severity: Low** · Bug tampilan murni.

### Gejala & root cause

Tabel container menampilkan `8082:8080/tcp` **dua kali** untuk satu port.
Docker melaporkan setiap port publik dua kali — sekali untuk IPv4 (`IP: 0.0.0.0`)
dan sekali untuk IPv6 (`IP: ::`) — dan frontend merender keduanya tanpa dedupe.
`ContainersPage.svelte:126` mengiterasi `container.ports.slice(0, 2)`, dan
`formatPort` (`svelte/src/lib/format.ts:6-10`) merender per-entry, sehingga
dua entri identik muncul.

### Perbaikan

Dedupe berdasarkan representasi port di `formatPorts` (efek samping: juga
memperbaiki tampilan detail setelah Bug 3 diperbaiki):

```ts
export function formatPorts(ports: PortSummary[] | undefined | null): string {
  if (!Array.isArray(ports) || ports.length === 0) return '—';
  const seen = new Set<string>();
  const out: string[] = [];
  for (const p of ports) {
    const s = formatPort(p);
    if (!seen.has(s)) { seen.add(s); out.push(s); }
  }
  return out.join(', ');
}
```

---

## Catatan investigasi (bukan bug)

Dua observasi selama verifikasi yang **bukan** bug — dicatat supaya tidak
diteliti ulang:

- **Log exec "edge agents" di backend** (`interactive terminal is not yet
  available for edge agents...`) muncul di `dockpal.log` pada 13:45 dan 13:59.
  Itu berasal dari sesi pengujian sebelumnya, bukan dari verifikasi ini.
  Instance `local` memakai `LocalClient` (`manager.go:59-61`), sehingga exec
  berjalan langsung. Pesan tersebut memang muncul untuk instance edge yang
  terhubung (`inst-c5a798090d78` / "latihan"), dan itu perilaku yang
  diinginkan — `EdgeClient.ExecAttachAndBridge` (`edge.go:192-197`) sengaja
  menolak karena edge transport (request/response multiplexed) tidak bisa
  membawa TTY interaktif.
- **Panel "Details" inline di halaman containers list** sempat terlihat
  tertutup sendiri sekali selama pengujian. Saat direproduksi dengan langkah
  bersih (klik Details, pantau 10+ detik), panel **tetap terbuka**. Kemungkinan
  artifact dari interaksi cepat selama pengujian, bukan bug yang reproducible.

## Environment verifikasi

- Commit: `2df3f0b` (HEAD saat verifikasi)
- Server: dev `http://localhost:3012` (`make dev`, data di `.data/`)
- Container uji: `adminer_adminer` (image `adminer:latest`, running, port
  `8082:8080/tcp`, network `bridge`)
- Instance terpilih: `local` ("This Server")
- Log container diuji dengan request HTTP ke `:8082`
