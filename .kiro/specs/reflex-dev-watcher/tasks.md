# Implementation Plan: Reflex Dev Watcher

## Overview

Integrate reflex file-watching into the DockPal development workflow by creating a `reflex.conf` configuration file with 4 watchers (Go rebuild, server restart, web notification, test runner), a `DEVELOPMENT.md` guide, a README link, and unit tests validating the regex patterns.

## Tasks

- [x] 1. Create reflex configuration file
  - [x] 1.1 Create `reflex.conf` at project root with all 4 watcher entries
    - Create the file with proper reflex config syntax
    - Add comment lines preceding each watcher entry stating name and pattern
    - Entry 1: Go Source Rebuild — `-r '\.go$' -R '^vendor/' -R '_generated\.go$' -R '\.pb\.go$'` triggering `go build -o dockpal .` with success message
    - Entry 2: Server Auto-Restart — `-sr '^dockpal$'` triggering `./dockpal server`
    - Entry 3: Web Asset Notification — `-r '^web/.*\.(html|css|js)$' -R '^web/assets/vendor/'` triggering `echo "⟳ changed: {}"`
    - Entry 4: Test Runner — `-r '\.go$' -R '^vendor/' -R '_generated\.go$' -R '\.pb\.go$'` triggering `sleep 2 && go test ./...`
    - Use only relative paths, no machine-specific or environment-variable references
    - Each entry must be self-contained with its own complete regex pattern and command
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 2.1, 2.2, 2.3, 2.4, 3.1, 3.2, 3.3, 3.4, 3.5, 4.1, 4.2, 4.3, 5.1, 5.2, 5.3, 5.4, 7.2_

- [x] 2. Create developer documentation
  - [x] 2.1 Create `DEVELOPMENT.md` at project root
    - Add Prerequisites section listing Go 1.25+, Docker (running), and reflex
    - Add Installing Reflex section with `go install github.com/cespare/reflex@latest`
    - Add Running All Watchers section with `reflex -c reflex.conf` and note about running from project root
    - Add Watchers Overview table listing all 4 watchers with purpose and pattern
    - Add Running Individual Watchers section with standalone inline commands for each watcher
    - Add Workflow Tips section with known limitations (e.g., 2s delay assumption)
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 7.1, 7.4, 7.5_

  - [x] 2.2 Modify `README.md` to link to `DEVELOPMENT.md`
    - Add a "Development" subsection after the "Manual Install" section
    - Include text: `See [DEVELOPMENT.md](DEVELOPMENT.md) for setting up the file-watching development workflow with reflex.`
    - _Requirements: 6.5_

- [x] 3. Checkpoint - Verify configuration and documentation
  - Ensure all tests pass, ask the user if questions arise.

- [x] 4. Add unit tests for regex pattern validation
  - [x] 4.1 Create `reflex_patterns_test.go` in project root or appropriate test location
    - Write Go test functions validating each regex pattern compiles without error
    - Test Go source include pattern (`\.go$`) matches expected files (e.g., `main.go`, `internal/auth/handler.go`) and rejects non-Go files
    - Test vendor exclusion pattern (`^vendor/`) matches vendor paths and does not match `internal/vendor/x.go`
    - Test generated file exclusion patterns (`_generated\.go$`, `\.pb\.go$`) match intended files
    - Test binary pattern (`^dockpal$`) matches only the binary name, not variants like `dockpal-linux-amd64`
    - Test web asset pattern (`^web/.*\.(html|css|js)$`) matches HTML/CSS/JS under `web/` and rejects `.go` files
    - Test web vendor exclusion (`^web/assets/vendor/`) matches vendored frontend files
    - _Requirements: 1.4, 2.2, 4.2_

  - [x] 4.2 Write property test for regex pattern correctness
    - **Property 1: Regex pattern correctness for file matching**
    - Generate random valid Go source file paths and verify include pattern matches while exclusion patterns do not match; generate excluded paths and verify at least one exclusion pattern matches
    - **Validates: Requirements 2.2**

- [x] 5. Final checkpoint - Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional and can be skipped for faster MVP
- Each task references specific requirements for traceability
- Checkpoints ensure incremental validation
- The implementation language is Go, matching the project's existing toolchain
- Property test validates universal correctness of regex patterns from the design's Correctness Properties section
- Unit tests validate specific examples and edge cases for regex patterns

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "2.2"] },
    { "id": 2, "tasks": ["4.1"] },
    { "id": 3, "tasks": ["4.2"] }
  ]
}
```
