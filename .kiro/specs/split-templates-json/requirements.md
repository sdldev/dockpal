# Requirements Document

## Introduction

This document specifies the requirements for splitting the monolithic `templates/templates.json` file into individual JSON files per template. The system will load templates from a directory of individual files, maintain backward-compatible API responses, and provide a one-time migration script. A fallback mechanism ensures templates remain available when the local directory is unavailable.

## Glossary

- **Template_Loader**: The `loadTemplates()` and `loadTemplatesFromDir()` functions responsible for reading template files from the filesystem and returning them as a unified slice
- **Template_File**: An individual JSON file in the templates directory containing a single template object, named `<id>.json`
- **Templates_Directory**: The filesystem directory containing individual template JSON files (local: `templates/`, fallback: `/opt/dockpal/templates/`)
- **Migration_Script**: A one-time CLI utility (`cmd/split-templates`) that splits the monolithic `templates/templates.json` into individual files
- **Monolithic_File**: The original `templates/templates.json` containing a JSON array of all templates
- **Template**: A data structure representing a deployable Docker container/compose configuration with fields: id, name, description, category, icon, env_required, ports, compose

## Requirements

### Requirement 1: Individual Template File Format

**User Story:** As a contributor, I want each template stored as a separate JSON file named after its ID, so that I can add or edit templates independently without merge conflicts.

#### Acceptance Criteria

1. THE Template_Loader SHALL expect each Template_File to contain a single JSON object (not an array)
2. THE Template_File SHALL be named using the template's `id` field followed by the `.json` extension
3. WHEN a Template_File is loaded, THE Template_Loader SHALL verify that the `id` field in the JSON content matches the filename stem
4. THE Template_File SHALL contain all required fields: id, name, description, category, icon, and compose

### Requirement 2: Directory-Based Template Loading

**User Story:** As a system operator, I want templates loaded from individual files in a directory, so that the system can dynamically pick up new templates without code changes.

#### Acceptance Criteria

1. WHEN the Template_Loader reads the Templates_Directory, THE Template_Loader SHALL scan for all files with a `.json` extension
2. WHEN scanning the Templates_Directory, THE Template_Loader SHALL skip subdirectories and non-`.json` files
3. WHEN all `.json` files are read successfully, THE Template_Loader SHALL return an aggregated slice containing one Template per file
4. THE Template_Loader SHALL not guarantee any specific ordering of templates in the returned slice

### Requirement 3: Fallback Behavior

**User Story:** As a system operator, I want the system to fall back to a system-wide templates directory, so that templates remain available even if the local directory is missing or empty.

#### Acceptance Criteria

1. WHEN the local `templates/` directory is unreadable or does not exist, THE Template_Loader SHALL attempt to read from `/opt/dockpal/templates/`
2. WHEN the local `templates/` directory exists but contains no `.json` files, THE Template_Loader SHALL attempt to read from the fallback directory
3. IF both the local directory and the fallback directory are unavailable, THEN THE Template_Loader SHALL return an error indicating no templates are available
4. WHEN the fallback directory is used, THE Template_Loader SHALL apply the same file scanning and parsing logic as for the local directory

### Requirement 4: Migration from Monolithic File

**User Story:** As a developer, I want a one-time migration script to split the existing monolithic file into individual files, so that I can transition to the new format without manual work.

#### Acceptance Criteria

1. WHEN the Migration_Script is executed, THE Migration_Script SHALL read the Monolithic_File and parse it as a JSON array of templates
2. WHEN writing individual files, THE Migration_Script SHALL create one file per template named `<id>.json` with pretty-printed JSON (2-space indent)
3. WHEN all individual files are written, THE Migration_Script SHALL verify the round-trip by loading all files back and comparing the result set to the original
4. WHEN round-trip verification succeeds, THE Migration_Script SHALL remove the original Monolithic_File
5. IF round-trip verification fails, THEN THE Migration_Script SHALL abort without removing the Monolithic_File and report the discrepancy

### Requirement 5: API Backward Compatibility

**User Story:** As a frontend developer, I want the GET /api/templates endpoint to return the same JSON structure as before, so that no client-side changes are needed.

#### Acceptance Criteria

1. THE GET /api/templates endpoint SHALL return a JSON array of template objects
2. WHEN templates are loaded from individual files, THE endpoint SHALL return the same JSON structure as when loaded from the Monolithic_File
3. THE Template data model SHALL remain unchanged: id, name, description, category, icon, env_required (optional), ports (optional), and compose fields

### Requirement 6: Error Handling

**User Story:** As a system operator, I want clear error messages when template loading fails, so that I can quickly diagnose and fix issues.

#### Acceptance Criteria

1. IF a Template_File contains malformed JSON, THEN THE Template_Loader SHALL return an error identifying the problematic filename
2. IF a Template_File cannot be read due to permission errors, THEN THE Template_Loader SHALL return an error identifying the unreadable file
3. WHEN a malformed or unreadable file is encountered, THE Template_Loader SHALL fail fast and not return partial results
4. IF the Templates_Directory cannot be read, THEN THE Template_Loader SHALL include the directory path in the error message
