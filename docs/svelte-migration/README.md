# Svelte Migration Plan — Dockpal Monolith

Migrasi frontend Dockpal dari Alpine.js vanilla ke **Svelte (monolith — tetap single Go binary)**.

## Konsep

Svelte di-build jadi static HTML/CSS/JS → di-embed ke Go binary via `go:embed`. Hasil: tetap satu binary, tanpa Node.js runtime di production, tanpa service terpisah.

Build flow:
```bash
# Development
cd svelte && npm run dev     # Vite HMR di :5173
./dockpal server             # Backend di :3012

# Production  
cd svelte && npm run build   # Output ke dist/
make build                   # Go embed dist/ jadi dockpal binary
```

## Status

| Phase | Dokumen | Durasi | Status |
|---|---|---|---|
| 1 | [phase-1-setup.md](phase-1-setup.md) | 2-3 hari | ✅ Implemented & committed |
| 2 | [phase-2-components.md](phase-2-components.md) | 5-7 hari | ✅ Implemented & committed |
| 3 | [phase-3-auth-routing.md](phase-3-auth-routing.md) | 3-4 hari | ✅ Implemented & committed |
| 4 | [phase-4-data-integration.md](phase-4-data-integration.md) | 4-5 hari | 🔄 In Progress |
| 5 | [phase-5-build-deploy.md](phase-5-build-deploy.md) | 2-3 hari | 📋 Planned |

Total estimasi: 2-3 minggu development fokus. Sekarang Phase 4 (data integration + deployment wizard full wire).
