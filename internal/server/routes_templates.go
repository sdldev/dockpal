package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type TemplatePort struct {
	Label         string `json:"label"`
	Default       int    `json:"default"`
	ContainerPort int    `json:"container_port"`
}

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

var templateCache struct {
	mu       sync.RWMutex
	data     []Template
	loadedAt time.Time
}

// getCachedTemplates returns cached templates, reloading from disk if the TTL has expired.
func getCachedTemplates(ttl time.Duration) ([]Template, error) {
	templateCache.mu.RLock()
	if time.Since(templateCache.loadedAt) < ttl && templateCache.data != nil {
		defer templateCache.mu.RUnlock()
		return templateCache.data, nil
	}
	templateCache.mu.RUnlock()

	templateCache.mu.Lock()
	defer templateCache.mu.Unlock()
	if time.Since(templateCache.loadedAt) < ttl && templateCache.data != nil {
		return templateCache.data, nil
	}
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	templateCache.data = templates
	templateCache.loadedAt = time.Now()
	return templates, nil
}

func loadTemplates() ([]Template, error) {
	templates, err := loadTemplatesFromDir("templates")
	if err == nil && len(templates) > 0 {
		return templates, nil
	}

	templates, err = loadTemplatesFromDir("/opt/dockpal/templates")
	if err != nil {
		return nil, fmt.Errorf("no templates available: %w", err)
	}
	if len(templates) == 0 {
		return nil, fmt.Errorf("no template files found in fallback directory")
	}

	return templates, nil
}

// loadTemplatesFromDir reads all .json files in the given directory,
// unmarshals each into a Template, and returns the collected slice.
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