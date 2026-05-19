# Requirements Document

## Introduction

This feature integrates [reflex](https://github.com/cespare/reflex), a lightweight Go file-watching tool, into the DockPal development workflow. The goal is to automate rebuild, restart, and test execution whenever source files change during development, reducing manual iteration cycles and improving developer productivity.

## Glossary

- **Reflex**: A Go-based file-watching tool (github.com/cespare/reflex) that monitors directories and reruns commands when files matching specified patterns change
- **Watcher**: A single reflex process monitoring a set of file patterns and executing a command on change
- **Reflex_Config**: A configuration file (`reflex.conf`) at the project root that defines multiple watchers with their patterns and commands
- **Go_Source**: Files matching `*.go` in the project root and `internal/` directory tree
- **Web_Asset**: Files in the `web/` directory including HTML, CSS, and JavaScript
- **DockPal_Binary**: The compiled Go binary named `dockpal` produced by `go build`
- **Dev_Server**: The DockPal server process running locally during development via `./dockpal server`

## Requirements

### Requirement 1: Reflex Configuration File

**User Story:** As a developer, I want a reflex configuration file in the project root, so that all file watchers are defined declaratively and shared across the team.

#### Acceptance Criteria

1. THE Reflex_Config SHALL define all watcher entries in a single `reflex.conf` file located at the project root, with one entry per watcher defined in Requirements 2 through 5
2. WHEN a developer clones the repository and runs `reflex -c reflex.conf`, THE Reflex_Config SHALL start all watchers without errors, requiring no additional setup beyond installing reflex and the Go toolchain, and containing only relative paths with no machine-specific or environment-variable references
3. THE Reflex_Config SHALL include a comment line preceding each watcher entry that states the watcher's name and the file pattern it monitors
4. THE Reflex_Config SHALL use valid reflex configuration syntax where each entry consists of a regex pattern, optional flags, and a command, parseable by reflex without syntax errors

### Requirement 2: Go Source Rebuild Watcher

**User Story:** As a developer, I want the Go binary to automatically rebuild when I change Go source files, so that I always have an up-to-date binary without manual compilation.

#### Acceptance Criteria

1. WHEN a Go_Source file is created, modified, or deleted, THE Watcher SHALL execute `go build -o dockpal .` to rebuild the DockPal_Binary after a debounce period of 500 milliseconds from the last detected change
2. THE Watcher SHALL monitor all `*.go` files recursively in the project root directory and all its subdirectories, excluding the `vendor/` directory and files matching the `*_generated.go` or `*.pb.go` naming patterns
3. IF the build fails, THEN THE Watcher SHALL display the compiler error output to the developer's terminal and preserve the last successfully built DockPal_Binary unchanged
4. WHEN the build succeeds, THE Watcher SHALL display a success message to the developer's terminal indicating the build completion time

### Requirement 3: Server Auto-Restart Watcher

**User Story:** As a developer, I want the DockPal server to automatically restart when the binary is rebuilt, so that I can immediately test my changes in the browser.

#### Acceptance Criteria

1. WHEN the DockPal_Binary file (`./dockpal`) changes, THE Watcher SHALL stop the running Dev_Server process and start a new Dev_Server process within 5 seconds of detecting the change
2. THE Watcher SHALL use reflex's `-s` (start-service) flag to manage the long-running Dev_Server process lifecycle, sending SIGTERM to the running process before starting the replacement
3. THE Watcher SHALL pass the `server` subcommand when starting the DockPal_Binary
4. IF the Dev_Server process exits with a non-zero exit code within 3 seconds of starting, THEN THE Watcher SHALL display the stderr output to the developer's terminal
5. IF no Dev_Server process is currently running when the DockPal_Binary file changes, THEN THE Watcher SHALL start a new Dev_Server process without attempting termination

### Requirement 4: Web Asset Change Notification Watcher

**User Story:** As a developer, I want to be notified when web assets change, so that I know to refresh my browser to see frontend updates.

#### Acceptance Criteria

1. WHEN a Web_Asset file is modified, THE Watcher SHALL print a notification message to the terminal containing the text "changed:" followed by the relative path of the file that was modified (relative to the project root)
2. THE Watcher SHALL monitor all files with extensions `.html`, `.css`, and `.js` in the `web/` directory recursively, excluding the `web/assets/vendor/` directory
3. THE Watcher SHALL use the `{}` substitution to display the specific filename that triggered the change, where `{}` is replaced by the path of the changed file as provided by the underlying file-watching mechanism
4. IF no files matching `.html`, `.css`, or `.js` are found in the `web/` directory, THEN THE Watcher SHALL print an error message indicating that no watchable files were found and exit with a non-zero status code

### Requirement 5: Test Runner Watcher

**User Story:** As a developer, I want relevant tests to run automatically when Go source files change, so that I get immediate feedback on regressions.

#### Acceptance Criteria

1. WHEN a Go_Source file changes, THE Watcher SHALL execute `go test ./...` to run the full test suite
2. WHEN a Go_Source file change is detected, THE Watcher SHALL wait at least 2 seconds before executing tests to allow the rebuild watcher to complete and avoid running tests against stale binaries
3. IF tests fail, THEN THE Watcher SHALL display the test failure output to the developer's terminal
4. WHEN all tests pass, THE Watcher SHALL display the test success summary output to the developer's terminal

### Requirement 6: Developer Setup Documentation

**User Story:** As a new contributor, I want clear instructions on how to install and use reflex with this project, so that I can start developing quickly.

#### Acceptance Criteria

1. THE Reflex_Config SHALL be accompanied by a development section in the README or a dedicated `DEVELOPMENT.md` file that states Go 1.25+ as a prerequisite for installing reflex
2. THE documentation SHALL include the reflex installation command: `go install github.com/cespare/reflex@latest`
3. THE documentation SHALL include the command `reflex -c reflex.conf` as the single command to start all watchers, and state that it must be run from the project root directory
4. THE documentation SHALL list each of the 4 watchers (Go source rebuild, server auto-restart, web asset notification, and test runner) with a one-line purpose statement and the file glob pattern each monitors
5. IF the documentation is placed in a dedicated `DEVELOPMENT.md` file, THEN THE README SHALL contain a reference linking to that file from the existing development or manual install section

### Requirement 7: Selective Watcher Execution

**User Story:** As a developer, I want to run individual watchers independently, so that I can focus on specific tasks without all watchers running simultaneously.

#### Acceptance Criteria

1. THE documentation SHALL provide individual inline reflex commands for each of the 4 watchers (Go rebuild, server restart, web asset notification, test runner) that can be run standalone without the `reflex.conf` file
2. THE Reflex_Config SHALL organize watchers so that each entry contains its own complete regex pattern and command with no references to other entries in the file
3. WHEN a developer runs `reflex -c reflex.conf`, THE Reflex tool SHALL start all configured watchers simultaneously
4. WHEN a developer runs a single watcher's inline command from the documentation, THE Reflex tool SHALL execute only that watcher's pattern matching and command without requiring other watchers to be running
5. IF the test runner watcher is run independently without the rebuild watcher, THEN THE test runner watcher SHALL execute tests against the existing DockPal_Binary without waiting for a rebuild
