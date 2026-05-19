# Implementation Plan: Split Templates JSON

## Overview

Split the monolithic `templates/templates.json` into individual JSON files per template, update the loader to read from a directory of files, create a migration script, and add comprehensive tests. The API remains backward-compatible.

## Tasks

- [x] 1. Implement directory-based template loader
  - [x] 1.1 Create `loadTemplatesFromDir()` function in `internal/server/routes.go`
    - Implement `loadTemplatesFromDir(dir string) ([]Template, error)` that scans a directory for `.json` files
    - Read each `.json` file, unmarshal into a `Template` struct
    - Skip subdirectories and non-`.json` files
    - Fail fast on malformed JSON with error identifying the problematic file
    - Return aggregated `[]Template` slice
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 6.1, 6.2, 6.3, 6.4_

  - [x] 1.2 Update `loadTemplates()` to use `loadTemplatesFromDir()`
    - Replace the current monolithic file read with a call to `loadTemplatesFromDir("templates")`
    - If local directory fails or returns empty, fall back to `loadTemplatesFromDir("/opt/dockpal/templates")`
    - Return error if both directories are unavailable or empty
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 5.1, 5.2, 5.3_

- [x] 2. Checkpoint - Verify loader compiles and existing tests pass
  - Ensure all tests pass, ask the user if questions arise.

- [x] 3. Create migration script
  - [x] 3.1 Create `cmd/split-templates/main.go`
    - Read `templates/templates.json` and unmarshal as `[]Template`
    - For each template, write `templates/<id>.json` with pretty-printed JSON (2-space indent)
    - Verify round-trip: load all written files back via `loadTemplatesFromDir` and compare count
    - On success, remove the original `templates/templates.json`
    - On failure, abort without removing the original and report the discrepancy
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 1.1, 1.2_

  - [x] 3.2 Run the migration script to split `templates/templates.json` into individual files
    - Execute `go run cmd/split-templates/main.go` from the project root
    - Verify that individual `.json` files are created in `templates/`
    - Verify that `templates/templates.json` is removed
    - _Requirements: 4.3, 4.4_

- [x] 4. Checkpoint - Verify migration completed and API still works
  - Ensure all tests pass, ask the user if questions arise.

- [x] 5. Write unit tests for the template loader
  - [x] 5.1 Create `internal/server/templates_test.go` with unit tests
    - Test `loadTemplatesFromDir` with a temp directory containing valid `.json` files
    - Test `loadTemplatesFromDir` with empty directory (returns empty slice, no error)
    - Test `loadTemplatesFromDir` with non-existent directory (returns error)
    - Test `loadTemplatesFromDir` skips non-`.json` files and subdirectories
    - Test `loadTemplatesFromDir` fails on malformed JSON (returns error with filename)
    - Test `loadTemplates` fallback behavior (local → system-wide)
    - _Requirements: 2.1, 2.2, 2.3, 3.1, 3.2, 3.3, 6.1, 6.3_

- [x] 6. Write property-based tests for the template loader
  - [x] 6.1 Write property test for round-trip equivalence
    - **Property 1: Round-trip equivalence**
    - Generate random `[]Template` slices, write each to a temp dir as `<id>.json`, load via `loadTemplatesFromDir`, assert set-equivalence (same elements, order-independent)
    - **Validates: Requirements 2.3, 4.3**

  - [x] 6.2 Write property test for ID-filename consistency
    - **Property 2: ID-filename consistency**
    - Generate random templates, write to files named `<id>.json`, read back and verify each template's `id` field matches the filename stem
    - **Validates: Requirements 1.2, 1.3**

- [x] 7. Final checkpoint - Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- Checkpoints ensure incremental validation
- Property tests validate universal correctness properties from the design document
- Unit tests validate specific examples and edge cases
- The migration script (3.2) must be run once to actually split the file — this is a code-execution task, not just a write task
- The `loadTemplatesFromDir` function is exported-friendly for testing but stays unexported since it's internal to the server package

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["3.1"] },
    { "id": 3, "tasks": ["3.2"] },
    { "id": 4, "tasks": ["5.1"] },
    { "id": 5, "tasks": ["6.1", "6.2"] }
  ]
}
```
