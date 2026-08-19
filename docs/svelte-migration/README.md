# Svelte Migration Plan — Dockpal Monolith

Migrasi frontend Dockpal dari Alpine.js vanilla ke **Svelte (monolith — tetap single Go binary)**.

## Konsep

Svelte di-build jadi static HTML/CSS/JS → di-embed ke Go binary via `go:embed`. Hasil: tetap satu binary, tanpa Node.js runtime di production, tanpa service terpisah.

```
dockpal/
├── main.go
├── go.mod
├── svelte/           # Source frontend (dev only, butuh Node)
│   ├── package.json
│   ├── vite.config.ts
│   └── src/
├── web/              # Output build Svelte → di-embed Go
│   ├── index.html
│   └── assets/
└── templates/
```

Build flow:

```bash
# Development
cd svelte && npm run dev     # Vite HMR di :5173, proxy API ke :3012
./dockpal server             # Backend di :3012

# Production
cd svelte && npm run build   # Output ke ../web/
make build                   # Go binary embed web/
```

## Phases

| Phase | Dokumen | Durasi | Isi |
|---|---|---|---|
| 1 | [phase-1-setup.md](phase-1-setup.md) | 2-3 hari | SvelteKit setup, TypeScript, API client, build pipeline |
| 2 | [phase-2-components.md](phase-2-components.md) | 5-7 hari | Komponen UI: Button, Table, Modal, Deploy Wizard |
| 3 | [phase-3-auth-routing.md](phase-3-auth-routing.md) | 3-4 hari | Auth flow, route guards, layout, toast |
| 4 | [phase-4-data-integration.md](phase-4-data-integration.md) | 4-5 hari | Store polling, WebSocket stream, chart, search |
| 5 | [phase-5-build-deploy.md](phase-5-build-deploy.md) | 2-3 hari | CI/CD, Makefile, systemd, monitoring |

**Total estimasi: 2-3 minggu** development fokus.

## Prinsip

1. **Backend tidak berubah** — semua endpoint API Go tetap sama, hanya frontend di-replace.
2. **Per-phase commit** — tiap phase selesai = commit + test suite hijau.
3. **Alpine.js tetap jalan** selama migrasi — fallback kalau build Svelte gagal.
4. **TypeScript strict mode** sejak awal.
5. **TDD** — test component behavior, bukan implementation detail.

## Risiko

| Risiko | Mitigasi |
|---|---|
| Bundle size naik | Vite code-splitting, manualChunks |
| WebSocket auth | Token via query param (pola sudah ada di backend) |
| Traefik/domain logic kompleks | Migrasi terakhir setelah core stabil |
| Dev butuh Node.js | CI install Node otomatis, binary prod tidak butuh |

## Status

- [ ] Phase 1 — Setup
- [ ] Phase 2 — Components
- [ ] Phase 3 — Auth & Routing
- [ ] Phase 4 — Data Integration
- [ ] Phase 5 — Build & Deploy
