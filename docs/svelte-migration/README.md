# Svelte Migration — Dockpal Monolith

Migrasi frontend Dockpal dari Alpine.js vanilla ke **Svelte 5 (monolith — tetap single Go binary)**.

> ⚠️ **Docs phase 1-5 ditulis sebagai rencana awal (SvelteKit). Implementasi aktual
> menyimpang di beberapa titik — ringkasan aktual ada di bawah; tiap dokumen phase
> diberi banner deviasi. Anggap dokumen phase sebagai referensi niat awal, bukan
> deskripsi sistem yang terbangun.**

## Konsep (aktual)

Svelte 5 **plain Vite SPA** (bukan SvelteKit) di-build jadi static HTML/CSS/JS →
di-copy ke `web/svelteDist/` → di-embed ke Go binary via `go:embed` → di-serve
di `/`. **Migrasi selesai:** UI Alpine legacy sudah dihapus seluruhnya; SPA adalah
satu-satunya UI. URL routing client-side (`svelte/src/lib/router.ts`) menyinkronkan
`currentPage` dengan browser history, jadi deep link (`/dashboard`, `/fleet`,
`/containers/:id`) langsung jalan; path legacy (`/profile`, `/registry`, `/deploy`,
`/instances`, `/add-instance`) di-alias agar bookmark lama tetap valid.

Build flow:

```bash
# Development
cd svelte && npm run dev     # Vite HMR di :5173 (proxy /api + /ws ke :3012)
make dev                     # Backend di :3012

# Production (single binary, SPA di /)
make svelte-embed            # build frontend + copy ke web/svelteDist/
make build                   # Go embed → ./dockpal
# atau sekaligus:
make prod-build
```

## Struktur aktual

```text
svelte/
├── index.html                  # Vite entry, base /
├── vite.config.ts              # base '/', alias $lib, proxy dev
└── src/
    ├── main.ts                 # mount(App) + import app.css
    ├── app.css                 # @import "tailwindcss" (Tailwind v4, Vite plugin)
    ├── App.svelte              # auth bootstrap + state-based routing ($currentPage)
    ├── lib/
    │   ├── api/client.ts       # fetch wrapper + JWT localStorage
    │   ├── api/stream.ts       # WebSocket StreamClient (reconnect)
    │   ├── store.ts            # stores: user, routing, services, deploy, stats
    │   └── types/              # api.ts + generated.ts (match Go JSON)
    └── components/
        ├── ui/                 # Button, Modal (Svelte 5 snippets)
        ├── layout/             # Sidebar, ToastContainer
        ├── deploy/             # DeployWizard (tabs + WS deploy log)
        ├── Container/          # StatsChart (polling stats)
        └── pages/              # Login, Dashboard, TemplatesPage, ContainersPage
```

Go side: `web/embed.go` (`SvelteAssets`, `all:svelteDist`) + `main.go`
(StaticFS `/assets` + `GET /` + NoRoute SPA fallback; warn log bila belum di-build).

## Status

| Phase | Dokumen | Status aktual |
|---|---|---|
| 1 | [phase-1-setup.md](phase-1-setup.md) | ✅ Done — deviasi: plain Vite, bukan SvelteKit; Tailwind v4 build pipeline (bukan CDN, bukan v3) |
| 2 | [phase-2-components.md](phase-2-components.md) | ✅ Done — Button/Modal/Dashboard/Templates/DeployWizard/Toast; `<slot>` → snippets |
| 3 | [phase-3-auth-routing.md](phase-3-auth-routing.md) | ✅ Done — login/logout/profile + guard; routing state-based (`currentPage` store), bukan file-based |
| 4 | [phase-4-data-integration.md](phase-4-data-integration.md) | ✅ Done — types, StreamClient, stats polling, ContainersPage + StatsChart, deploy WS log di wizard |
| 5 | [phase-5-build-deploy.md](phase-5-build-deploy.md) | 🔄 Partial — embed + Makefile targets done; CI workflow, systemd, install script belum |
| 6 | [phase-6-remaining-coverage.md](phase-6-remaining-coverage.md) | ✅ Done — semua batch (0-A-B-C-D-E): RBAC, admin, operator, deploy compose/git, container detail, long tail. Smoke-tested vs live backend |
| 7 | — (cleanup) | ✅ Done — legacy Alpine UI dihapus total (`web/index.html`, `web/pages`, `web/partials`, `web/assets`); SPA dilayani di `/` dengan URL router client-side (`lib/router.ts`); parity dashboard/fleet/container-detail diverifikasi |

## Deviasi utama dari rencana awal

1. **SvelteKit → plain Vite SPA.** Router file-based, `+page.svelte`, adapter —
   tidak ada. Routing via store `currentPage` + `{#if}` di `App.svelte`.
   Alasan: lebih ringan untuk di-embed, tanpa SSR/prerender complexity.
2. **Tailwind v4** via `@tailwindcss/vite` (`@import "tailwindcss"`), tanpa
   tailwind.config.js/postcss.config.js terpisah.
3. **Serve di `/`** setelah parity tercapai — legacy Alpine UI dihapus total;
   routing client-state ditingkatkan jadi URL router sungguhan (`lib/router.ts`)
   dengan deep link + back/forward + alias path legacy.
4. **WebSocket URL**: `/api/instances/:id/deploy/stream/:deployId?token=…`
   (bukan `/api/v1/…` seperti tertulis di draf awal).

## Verifikasi terakhir

- `npm run check`: 0 errors, 0 warnings
- `npm run test:unit`: pass
- `npm run build`: OK
- `go build` + `go vet`: OK; runtime test di browser: `/`, `/dashboard`, `/fleet`, `/containers/:id` (deep link + reload), back/forward, alias legacy (`/profile`, `/instances`, `/deploy`) → semua render halaman yang benar; login flow + live stats jalan
