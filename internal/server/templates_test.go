package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTemplateJSON(id string) []byte {
	tmpl := Template{
		ID:          id,
		Name:        "Test " + id,
		Description: "A test template",
		Category:    "web",
		Icon:        "🐳",
		Compose:     "services:\n  " + id + ":\n    image: " + id + ":latest",
	}
	data, _ := json.MarshalIndent(tmpl, "", "  ")
	return data
}

func validTemplateWithPortsJSON(id string) []byte {
	tmpl := Template{
		ID:          id,
		Name:        "Test " + id,
		Description: "A test template with ports",
		Category:    "database",
		Icon:        "🗄️",
		Ports: []TemplatePort{
			{Label: "HTTP", Default: 8080, ContainerPort: 80},
		},
		Compose: "services:\n  " + id + ":\n    image: " + id + ":latest\n    ports:\n      - '8080:80'",
	}
	data, _ := json.MarshalIndent(tmpl, "", "  ")
	return data
}

func TestLoadTemplatesFromDir_ValidFiles(t *testing.T) {
	dir := t.TempDir()

	// Write two valid template files
	os.WriteFile(filepath.Join(dir, "nginx.json"), validTemplateJSON("nginx"), 0644)
	os.WriteFile(filepath.Join(dir, "postgres.json"), validTemplateWithPortsJSON("postgres"), 0644)

	templates, err := loadTemplatesFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(templates))
	}

	// Verify templates were loaded correctly (order not guaranteed)
	ids := map[string]bool{}
	for _, tmpl := range templates {
		ids[tmpl.ID] = true
		if tmpl.Name == "" {
			t.Errorf("template %s has empty name", tmpl.ID)
		}
		if tmpl.Compose == "" {
			t.Errorf("template %s has empty compose", tmpl.ID)
		}
	}
	if !ids["nginx"] {
		t.Error("expected nginx template to be loaded")
	}
	if !ids["postgres"] {
		t.Error("expected postgres template to be loaded")
	}
}

func TestLoadTemplatesFromDir_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	templates, err := loadTemplatesFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error for empty directory: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("expected 0 templates, got %d", len(templates))
	}
}

func TestLoadTemplatesFromDir_NonExistentDirectory(t *testing.T) {
	templates, err := loadTemplatesFromDir("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
	if templates != nil {
		t.Fatal("expected nil templates for non-existent directory")
	}
}

func TestLoadTemplatesFromDir_SkipsNonJSONAndSubdirectories(t *testing.T) {
	dir := t.TempDir()

	// Write a valid .json file
	os.WriteFile(filepath.Join(dir, "redis.json"), validTemplateJSON("redis"), 0644)

	// Write non-.json files that should be skipped
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Templates"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("some notes"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte(""), 0644)

	// Create a subdirectory that should be skipped
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)
	os.WriteFile(filepath.Join(dir, "subdir", "hidden.json"), validTemplateJSON("hidden"), 0644)

	templates, err := loadTemplatesFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template (only redis.json), got %d", len(templates))
	}
	if templates[0].ID != "redis" {
		t.Errorf("expected template ID 'redis', got '%s'", templates[0].ID)
	}
}

func TestLoadTemplatesFromDir_MalformedJSON(t *testing.T) {
	dir := t.TempDir()

	// Write a valid file first
	os.WriteFile(filepath.Join(dir, "valid.json"), validTemplateJSON("valid"), 0644)

	// Write a malformed JSON file
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"id": "broken", "name": invalid`), 0644)

	_, err := loadTemplatesFromDir(dir)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error should mention the problematic filename, got: %v", err)
	}
}

func TestLoadTemplates_FallbackBehavior(t *testing.T) {
	// Save original working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Create a temp workspace to control the "templates" directory
	workspace := t.TempDir()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	// Case 1: Local directory with templates should be used
	localDir := filepath.Join(workspace, "templates")
	os.MkdirAll(localDir, 0755)
	os.WriteFile(filepath.Join(localDir, "nginx.json"), validTemplateJSON("nginx"), 0644)

	templates, err := loadTemplates()
	if err != nil {
		t.Fatalf("expected loadTemplates to succeed with local dir, got: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template from local dir, got %d", len(templates))
	}
	if templates[0].ID != "nginx" {
		t.Errorf("expected template ID 'nginx', got '%s'", templates[0].ID)
	}

	// Case 2: Empty local directory should trigger fallback
	// Remove the template file to make local dir empty
	os.Remove(filepath.Join(localDir, "nginx.json"))

	// Without /opt/dockpal/templates available, this should error
	_, err = loadTemplates()
	if err == nil {
		// If /opt/dockpal/templates happens to exist on this system with templates, that's ok
		// Otherwise we expect an error
		t.Log("loadTemplates succeeded (fallback directory may exist on this system)")
	}

	// Case 3: Non-existent local directory should trigger fallback
	os.RemoveAll(localDir)

	_, err = loadTemplates()
	if err == nil {
		t.Log("loadTemplates succeeded (fallback directory may exist on this system)")
	} else {
		// Verify the error message indicates no templates available
		if !strings.Contains(err.Error(), "no templates available") && !strings.Contains(err.Error(), "no template files found") {
			t.Errorf("expected 'no templates available' error, got: %v", err)
		}
	}
}

func TestLoadTemplatesFromDir_TemplateFields(t *testing.T) {
	dir := t.TempDir()

	// Write a template with all fields including optional ones
	tmpl := Template{
		ID:          "full-template",
		Name:        "Full Template",
		Description: "Template with all fields",
		Category:    "monitoring",
		Icon:        "📊",
		EnvRequired: []string{"API_KEY", "SECRET"},
		Ports: []TemplatePort{
			{Label: "Web UI", Default: 3000, ContainerPort: 3000},
			{Label: "API", Default: 8080, ContainerPort: 8080},
		},
		Compose: "services:\n  app:\n    image: app:latest",
	}
	data, _ := json.MarshalIndent(tmpl, "", "  ")
	os.WriteFile(filepath.Join(dir, "full-template.json"), data, 0644)

	templates, err := loadTemplatesFromDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}

	loaded := templates[0]
	if loaded.ID != "full-template" {
		t.Errorf("expected ID 'full-template', got '%s'", loaded.ID)
	}
	if loaded.Name != "Full Template" {
		t.Errorf("expected Name 'Full Template', got '%s'", loaded.Name)
	}
	if len(loaded.EnvRequired) != 2 {
		t.Errorf("expected 2 env_required entries, got %d", len(loaded.EnvRequired))
	}
	if len(loaded.Ports) != 2 {
		t.Errorf("expected 2 ports, got %d", len(loaded.Ports))
	}
	if loaded.Ports[0].Label != "Web UI" {
		t.Errorf("expected first port label 'Web UI', got '%s'", loaded.Ports[0].Label)
	}
	if loaded.Ports[0].Default != 3000 {
		t.Errorf("expected first port default 3000, got %d", loaded.Ports[0].Default)
	}
	if loaded.Ports[0].ContainerPort != 3000 {
		t.Errorf("expected first port container_port 3000, got %d", loaded.Ports[0].ContainerPort)
	}
}
