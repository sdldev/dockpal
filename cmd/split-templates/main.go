package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// TemplatePort mirrors the port definition used in the server package.
type TemplatePort struct {
	Label         string `json:"label"`
	Default       int    `json:"default"`
	ContainerPort int    `json:"container_port"`
}

// Template mirrors the template struct used in the server package.
type Template struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Icon        string         `json:"icon"`
	EnvRequired []string       `json:"env_required,omitempty"`
	Ports       []TemplatePort `json:"ports,omitempty"`
	Compose     string         `json:"compose"`
}

// loadTemplatesFromDir reads all .json files in the given directory,
// unmarshals each into a Template, and returns the collected slice.
// It skips the monolithic templates.json file (which contains an array, not a single object).
func loadTemplatesFromDir(dir string) ([]Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading templates directory %s: %w", dir, err)
	}

	var templates []Template

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// Skip the monolithic file during round-trip verification
		if entry.Name() == "templates.json" {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading template file %s: %w", filePath, err)
		}

		var tmpl Template
		if err := json.Unmarshal(data, &tmpl); err != nil {
			return nil, fmt.Errorf("parsing template file %s: %w", filePath, err)
		}

		templates = append(templates, tmpl)
	}

	return templates, nil
}

func main() {
	const (
		templatesDir  = "templates"
		monolithicFile = "templates/templates.json"
	)

	// Step 1: Read the monolithic file
	data, err := os.ReadFile(monolithicFile)
	if err != nil {
		log.Fatalf("Failed to read %s: %v", monolithicFile, err)
	}

	// Step 2: Unmarshal as array of templates
	var templates []Template
	if err := json.Unmarshal(data, &templates); err != nil {
		log.Fatalf("Failed to parse %s: %v", monolithicFile, err)
	}

	fmt.Printf("Read %d templates from %s\n", len(templates), monolithicFile)

	// Step 3: Write individual files
	for _, tmpl := range templates {
		filename := filepath.Join(templatesDir, tmpl.ID+".json")
		content, err := json.MarshalIndent(tmpl, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal template %s: %v", tmpl.ID, err)
		}
		// Append a trailing newline for clean file endings
		content = append(content, '\n')

		fmt.Printf("Writing %s.json...\n", tmpl.ID)
		if err := os.WriteFile(filename, content, 0644); err != nil {
			log.Fatalf("Failed to write %s: %v", filename, err)
		}
	}

	// Step 4: Verify round-trip
	// loadTemplatesFromDir skips templates.json (the monolithic file) so it only reads individual files
	loaded, err := loadTemplatesFromDir(templatesDir)
	if err != nil {
		log.Fatalf("Round-trip verification failed: %v", err)
	}

	if len(loaded) != len(templates) {
		log.Fatalf("Round-trip verification failed: expected %d templates, got %d. Aborting without removing original.",
			len(templates), len(loaded))
	}

	fmt.Printf("Round-trip verification passed: %d templates verified\n", len(loaded))

	// Step 5: Remove the original monolithic file
	if err := os.Remove(monolithicFile); err != nil {
		log.Fatalf("Failed to remove original file %s: %v", monolithicFile, err)
	}

	fmt.Printf("Migration complete: %d templates split into individual files\n", len(templates))
}
