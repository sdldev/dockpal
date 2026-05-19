# Implementation Plan: GHCR Private Registry Authentication

## Overview

This plan implements private GitHub Container Registry (ghcr.io) authentication for DockPal. It adds encrypted credential storage, a management API, a frontend UI page, and integration with the existing image pull and deploy flows. The implementation follows a bottom-up approach: crypto → database → business logic → API → frontend → integration.

## Tasks

- [x] 1. Create crypto module (`internal/registry/crypto.go`)
  - Create `internal/registry/crypto.go` with `DeriveKey`, `Encrypt`, `Decrypt`, and `zeroBytes` functions
  - Implement `DeriveKey(jwtSecret string) ([]byte, error)` using HKDF-SHA256 with salt `"dockpal-registry-v1"` and info `"dockpal-registry-encryption"` to derive a 32-byte key
  - Implement `Encrypt(plaintext []byte, key []byte) ([]byte, error)` using AES-256-GCM with a 12-byte random nonce from `crypto/rand`, returning `nonce || ciphertext || tag`
  - Implement `Decrypt(data []byte, key []byte) ([]byte, error)` that splits nonce from ciphertext and decrypts using AES-256-GCM
  - Implement `zeroBytes(b []byte)` helper that overwrites a byte slice with zeros for secure memory cleanup
  - Return descriptive errors on decryption failure (corrupted ciphertext) without exposing key or ciphertext values
  - **Requirements addressed:** REQ-5.1, REQ-5.4, REQ-5.5

- [x] 2. Add database bucket and CRUD methods for registry credentials
  - Add `bucketRegistries = []byte("registries")` to `internal/db/db.go` bucket declarations
  - Add `bucketRegistries` to the bucket creation loop in `db.New()`
  - Define `RegistryCredential` struct with fields: ID, Registry, Username, EncryptedToken ([]byte), CreatedAt, UpdatedAt, LastValidatedAt
  - Implement `SaveRegistryCredential(cred RegistryCredential) error` — stores JSON-marshaled credential keyed by ID
  - Implement `GetRegistryCredential(id string) (*RegistryCredential, error)` — returns error if not found
  - Implement `ListRegistryCredentials() ([]RegistryCredential, error)` — iterates bucket, returns all credentials
  - Implement `DeleteRegistryCredential(id string) error` — removes key from bucket
  - Implement `FindRegistryCredentialByDomain(domain string) (*RegistryCredential, error)` — case-insensitive scan, returns most recently updated if multiple match
  - **Requirements addressed:** REQ-1.4, REQ-1.5, REQ-2.3, REQ-7.4

- [x] 3. Create Registry Manager business logic (`internal/registry/registry.go`)
  - Create `internal/registry/registry.go` with `Manager` struct holding `*db.DB` and `cryptoKey []byte`
  - Implement `NewManager(database *db.DB, jwtSecret string) *Manager` that calls `DeriveKey` and stores the result
  - Implement `ValidatePAT(token string) error` supporting both `ghp_` (40 chars) and `github_pat_` (20+ chars) formats
  - Implement `ExtractDomain(imageRef string) string` that returns the registry domain from an image reference (returns empty for Docker Hub images)
  - Implement `MaskToken(token string) string` that returns `"****" + last 4 characters`
  - Implement `Create(req CreateRequest) (*CredentialSummary, error)` with field validation, PAT format check, duplicate detection (case-insensitive), token encryption, and ID generation
  - Implement `List() ([]CredentialSummary, error)` returning all credentials with masked tokens and status
  - Implement `Get(id string) (*CredentialSummary, error)` returning single credential with masked token
  - Implement `Update(id string, req UpdateRequest) error` with token format validation, encryption, and timestamp update
  - Implement `Delete(id string) error` confirming removal only after successful DB deletion
  - Implement `TestConnection(id string) (*TestResult, error)` with 30s timeout, Basic Auth to registry /v2/ endpoint, categorized error responses
  - Implement `GetAuthHeader(imageRef string) (string, error)` extracting domain, finding credential, decrypting token, building base64 Docker auth header
  - Ensure all functions that decrypt tokens use `defer zeroBytes(token)` for cleanup on both normal and abnormal termination
  - Return generic error message when crypto key is unavailable (no diagnostic details)
  - **Requirements addressed:** REQ-1.1–1.7, REQ-2.1–2.5, REQ-3.1–3.5, REQ-4.1–4.4, REQ-5.2, REQ-5.3

- [x] 4. Add API routes for registry management
  - Import `registry` package in `internal/server/routes.go`
  - Instantiate `registry.NewManager(database, jwtSecret)` in `RegisterRoutes`
  - Add `GET /api/registries` route calling `registryManager.List()`, returns JSON array
  - Add `POST /api/registries` route binding `CreateRequest`, calling `registryManager.Create()`, returns 201
  - Add `GET /api/registries/:id` route calling `registryManager.Get(id)`, returns 404 if not found
  - Add `PUT /api/registries/:id` route binding `UpdateRequest`, calling `registryManager.Update()`
  - Add `DELETE /api/registries/:id` route calling `registryManager.Delete()`
  - Add `POST /api/registries/:id/test` route calling `registryManager.TestConnection()`
  - All routes under `protected` group (require JWT auth)
  - **Requirements addressed:** REQ-1.1, REQ-1.3, REQ-2.1–2.5, REQ-4.1–4.4

- [x] 5. Integrate authenticated pull into Docker client
  - Add `PullImageWithAuth(ctx context.Context, image string, registryAuth string) error` to `internal/docker/images.go`
  - Modify `pullImageIfNeeded` in `internal/docker/compose.go` to accept an optional `registryAuth string` parameter
  - Update `DeployComposeStreamed` in `internal/docker/deploy_stream.go` to call `GetAuthHeader` per image and use `PullImageWithAuth`
  - Update `DeployCompose` similarly for non-streamed deploys
  - Update the `/images/pull` route handler to use `registryManager.GetAuthHeader` before pulling
  - On auth failure (401/403), emit hint: "Authentication failed for <registry> — credentials may be expired"
  - Fallback to unauthenticated pull when no credentials match
  - **Requirements addressed:** REQ-3.1–3.5, REQ-7.1–7.4

- [x] 6. Create frontend Registry page and module
  - Create `web/assets/modules/registry.js` with state and methods: `loadRegistries()`, `addRegistry()`, `deleteRegistry(id)`, `testRegistryConnection(id)`
  - Add registry state variables to `web/assets/modules/state.js` and add Registry nav item under a "Settings" group
  - Add `D.registry` to the modules array in `web/assets/app.js`
  - Add `<script src="/assets/modules/registry.js"></script>` to `web/index.html`
  - Create `web/pages/registry.html` with: page header, add button, form (registry URL default ghcr.io, username, token as password), test connection button, credentials table, empty state, confirmation dialog, toast notifications
  - Implement client-side validation: required fields, max lengths (253/100/255), error messages per field
  - Add `'registry'` case to `loadPageData()` to call `loadRegistries()`
  - **Requirements addressed:** REQ-6.1–6.9

- [x] 7. Wire up Registry Manager in main application startup
  - Ensure `registryManager` is created inside `RegisterRoutes` (or passed as parameter)
  - Verify the `registries` bucket is created on first startup via `db.New()`
  - Verify the application compiles and starts without errors
  - **Requirements addressed:** REQ-1.1, REQ-5.4

- [x] 8. Update deploy flow to pass registry manager
  - Make `registryManager` accessible to deploy handlers in `RegisterRoutes`
  - Update `POST /deploy/stream` handler to get auth headers for compose images
  - Update `POST /deploy/compose` handler similarly
  - Update `POST /templates/:id/deploy` and `POST /templates/:id/deploy/stream` handlers
  - Ensure deploy error messages include credential hints when auth fails for a known registry
  - **Requirements addressed:** REQ-7.1, REQ-7.2, REQ-7.3

## Task Dependency Graph

```json
{
  "waves": [
    {"tasks": [1, 2]},
    {"tasks": [3]},
    {"tasks": [4]},
    {"tasks": [5, 6, 7]},
    {"tasks": [8]}
  ]
}
```

- **Wave 1:** Tasks 1 (crypto) and 2 (database) can be done in parallel — no dependencies
- **Wave 2:** Task 3 (registry manager) depends on Tasks 1 and 2
- **Wave 3:** Task 4 (API routes) depends on Task 3
- **Wave 4:** Tasks 5 (docker integration), 6 (frontend), and 7 (wire up) depend on Task 4
- **Wave 5:** Task 8 (deploy flow) depends on Tasks 5 and 7

## Notes

- No new external dependencies required — uses stdlib `crypto/*` and existing `golang.org/x/crypto/hkdf`
- The `registries` BoltDB bucket is auto-created on startup, so no migration script is needed
- PAT validation supports both classic (`ghp_`) and fine-grained (`github_pat_`) token formats
- The feature is purely additive — existing unauthenticated pulls continue to work unchanged
