package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// **Validates: Requirements 2.3, 4.3**

// Property 1: Round-trip equivalence
// For any valid []Template slice, marshaling each template to an individual JSON file
// and re-loading via loadTemplatesFromDir produces a set-equivalent []Template
// (same elements, order-independent).

func TestPropertyRoundTripEquivalence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random slice of templates with unique IDs
		templates := genTemplateSlice(rt)

		// Write each template to a temp directory as <id>.json
		dir := t.TempDir()
		for _, tmpl := range templates {
			data, err := json.MarshalIndent(tmpl, "", "  ")
			if err != nil {
				rt.Fatalf("failed to marshal template %s: %v", tmpl.ID, err)
			}
			filename := filepath.Join(dir, tmpl.ID+".json")
			if err := os.WriteFile(filename, data, 0644); err != nil {
				rt.Fatalf("failed to write template file %s: %v", filename, err)
			}
		}

		// Load them back via loadTemplatesFromDir
		loaded, err := loadTemplatesFromDir(dir)
		if err != nil {
			rt.Fatalf("loadTemplatesFromDir failed: %v", err)
		}

		// Assert set-equivalence: same number of templates
		if len(loaded) != len(templates) {
			rt.Fatalf("expected %d templates, got %d", len(templates), len(loaded))
		}

		// Sort both slices by ID for comparison
		sortByID(templates)
		sortByID(loaded)

		for i := range templates {
			assertTemplateEqual(rt, templates[i], loaded[i])
		}
	})
}

// genTemplateSlice generates a random slice of templates with unique, valid-filename IDs.
func genTemplateSlice(t *rapid.T) []Template {
	count := rapid.IntRange(0, 20).Draw(t, "templateCount")
	if count == 0 {
		return []Template{}
	}

	usedIDs := make(map[string]bool)
	templates := make([]Template, 0, count)

	for i := 0; i < count; i++ {
		id := genUniqueID(t, usedIDs)
		usedIDs[id] = true
		tmpl := genTemplate(t, id)
		templates = append(templates, tmpl)
	}

	return templates
}

// genUniqueID generates a unique ID that is a valid filename (lowercase alphanumeric with hyphens).
func genUniqueID(t *rapid.T, used map[string]bool) string {
	for {
		id := genValidID(t)
		if !used[id] {
			return id
		}
	}
}

// genValidID generates a valid template ID: lowercase alphanumeric with hyphens,
// starting and ending with alphanumeric, length 1-20.
func genValidID(t *rapid.T) string {
	length := rapid.IntRange(1, 20).Draw(t, "idLength")
	chars := make([]byte, length)

	// First char: alphanumeric only
	chars[0] = genAlphaNum(t)

	// Middle chars: alphanumeric or hyphen
	for i := 1; i < length-1; i++ {
		if rapid.Bool().Draw(t, "useHyphen") {
			chars[i] = '-'
		} else {
			chars[i] = genAlphaNum(t)
		}
	}

	// Last char: alphanumeric only (if length > 1)
	if length > 1 {
		chars[length-1] = genAlphaNum(t)
	}

	return string(chars)
}

func genAlphaNum(t *rapid.T) byte {
	const alphaNum = "abcdefghijklmnopqrstuvwxyz0123456789"
	idx := rapid.IntRange(0, len(alphaNum)-1).Draw(t, "alphaNumIdx")
	return alphaNum[idx]
}

// genTemplate generates a random Template with the given ID.
func genTemplate(t *rapid.T, id string) Template {
	categories := []string{"web", "database", "cache", "monitoring", "automation", "storage", "devtools", "messaging", "security", "analytics"}
	icons := []string{"🐳", "🌐", "🗄️", "📊", "🔧", "💾", "🚀", "📨", "🔒", "📈"}

	catIdx := rapid.IntRange(0, len(categories)-1).Draw(t, "categoryIdx")
	iconIdx := rapid.IntRange(0, len(icons)-1).Draw(t, "iconIdx")

	tmpl := Template{
		ID:          id,
		Name:        rapid.StringMatching(`[A-Z][a-zA-Z0-9 ]{0,29}`).Draw(t, "name"),
		Description: rapid.StringMatching(`[A-Za-z][A-Za-z0-9 ,.-]{0,99}`).Draw(t, "description"),
		Category:    categories[catIdx],
		Icon:        icons[iconIdx],
		Compose:     "services:\n  " + id + ":\n    image: " + id + ":latest",
	}

	// Optionally add EnvRequired
	if rapid.Bool().Draw(t, "hasEnvRequired") {
		envCount := rapid.IntRange(1, 5).Draw(t, "envCount")
		tmpl.EnvRequired = make([]string, envCount)
		for i := 0; i < envCount; i++ {
			tmpl.EnvRequired[i] = rapid.StringMatching(`[A-Z][A-Z_]{0,14}`).Draw(t, "envVar")
		}
	}

	// Optionally add Ports
	if rapid.Bool().Draw(t, "hasPorts") {
		portCount := rapid.IntRange(1, 4).Draw(t, "portCount")
		tmpl.Ports = make([]TemplatePort, portCount)
		for i := 0; i < portCount; i++ {
			tmpl.Ports[i] = TemplatePort{
				Label:         rapid.StringMatching(`[A-Z][a-zA-Z ]{0,14}`).Draw(t, "portLabel"),
				Default:       rapid.IntRange(1, 65535).Draw(t, "portDefault"),
				ContainerPort: rapid.IntRange(1, 65535).Draw(t, "containerPort"),
			}
		}
	}

	return tmpl
}

func sortByID(templates []Template) {
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].ID < templates[j].ID
	})
}

func assertTemplateEqual(t *rapid.T, expected, actual Template) {
	if expected.ID != actual.ID {
		t.Fatalf("ID mismatch: expected %q, got %q", expected.ID, actual.ID)
	}
	if expected.Name != actual.Name {
		t.Fatalf("Name mismatch for %s: expected %q, got %q", expected.ID, expected.Name, actual.Name)
	}
	if expected.Description != actual.Description {
		t.Fatalf("Description mismatch for %s: expected %q, got %q", expected.ID, expected.Description, actual.Description)
	}
	if expected.Category != actual.Category {
		t.Fatalf("Category mismatch for %s: expected %q, got %q", expected.ID, expected.Category, actual.Category)
	}
	if expected.Icon != actual.Icon {
		t.Fatalf("Icon mismatch for %s: expected %q, got %q", expected.ID, expected.Icon, actual.Icon)
	}
	if expected.Compose != actual.Compose {
		t.Fatalf("Compose mismatch for %s: expected %q, got %q", expected.ID, expected.Compose, actual.Compose)
	}

	// Compare EnvRequired
	if len(expected.EnvRequired) != len(actual.EnvRequired) {
		t.Fatalf("EnvRequired length mismatch for %s: expected %d, got %d", expected.ID, len(expected.EnvRequired), len(actual.EnvRequired))
	}
	for i := range expected.EnvRequired {
		if expected.EnvRequired[i] != actual.EnvRequired[i] {
			t.Fatalf("EnvRequired[%d] mismatch for %s: expected %q, got %q", i, expected.ID, expected.EnvRequired[i], actual.EnvRequired[i])
		}
	}

	// Compare Ports
	if len(expected.Ports) != len(actual.Ports) {
		t.Fatalf("Ports length mismatch for %s: expected %d, got %d", expected.ID, len(expected.Ports), len(actual.Ports))
	}
	for i := range expected.Ports {
		if expected.Ports[i].Label != actual.Ports[i].Label {
			t.Fatalf("Ports[%d].Label mismatch for %s: expected %q, got %q", i, expected.ID, expected.Ports[i].Label, actual.Ports[i].Label)
		}
		if expected.Ports[i].Default != actual.Ports[i].Default {
			t.Fatalf("Ports[%d].Default mismatch for %s: expected %d, got %d", i, expected.ID, expected.Ports[i].Default, actual.Ports[i].Default)
		}
		if expected.Ports[i].ContainerPort != actual.Ports[i].ContainerPort {
			t.Fatalf("Ports[%d].ContainerPort mismatch for %s: expected %d, got %d", i, expected.ID, expected.Ports[i].ContainerPort, actual.Ports[i].ContainerPort)
		}
	}
}
