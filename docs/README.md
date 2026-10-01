# Dokumentasi Dockpal

Folder ini berisi catatan audit dan dokumen internal yang tidak termasuk dokumentasi
pengguna (lihat `../README.md`).

## Daftar Dokumen

| Dokumen | Tanggal | Ringkasan |
|---------|---------|-----------|
| [audit-auth.md](./audit-auth.md) | 2026-10-01 | Audit subsistem autentikasi & otorisasi: 1 Critical, 3 High, 8 Medium, 8 Low, plus 7 temuan kualitas test dan 5 temuan arsitektur. **Status: diperbaiki 2026-10-01 (dua batch)** — seluruh Critical/High/Medium/Low dan T1–T5/T7 ✅; tersisa T6 (perapihan) dan A1/A3/A5 (refactor arsitektur, follow-up terpisah). |
| [audit-stack-container.md](./audit-stack-container.md) | 2026-10-01 | Audit fitur *stack* (Dockge-style compose) & *container*: 2 Critical, 6 High, 10 Medium, dan 13 Low. **Status: diperbaiki 2026-10-01 (dua batch)** — seluruh temuan ✅; M9 dimitigasi (akar = protokol edge, follow-up), swagger endpoint stack = follow-up dokumentasi API. |

## Konvensi

- Nama file: `audit-<area>.md`, `design-<area>.md`, atau `notes-<topik>.md`.
- Setiap temuan audit mencantumkan **Severity**, **Location** (`path:line`), **Masalah**,
  **Dampak**, dan **Perbaikan**.
- Verifikasi temuan ditandai eksplisit bila sudah dicek langsung terhadap kode.
- Status perbaikan ditandai per temuan: ✅ **Diperbaiki** (dengan tanggal & ringkasan
  perubahan) atau ⏳ **Belum** (dengan rencana perbaikan). Lokasi baris (`path:line`)
  merujuk ke kode saat audit dilakukan dan bisa bergeser setelah perbaikan.
