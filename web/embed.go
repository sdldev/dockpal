package web

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed index.html assets pages partials
var Assets embed.FS

// includeRegex matches <!--#include "filename"--> directives in HTML.
var includeRegex = regexp.MustCompile(`<!--#include\s+"([^"]+)"\s*-->`)

// AssembleHTML loads index.html and resolves <!--#include "..."--> directives
// recursively. Includes are resolved against the embedded filesystem root.
func AssembleHTML() (string, error) {
	data, err := Assets.ReadFile("index.html")
	if err != nil {
		return "", fmt.Errorf("failed to read index.html: %w", err)
	}
	return resolveIncludes(string(data), 0)
}

// resolveIncludes processes <!--#include--> directives recursively with
// a depth limit to prevent infinite loops.
func resolveIncludes(content string, depth int) (string, error) {
	if depth > 10 {
		return "", fmt.Errorf("include depth exceeded (cycle?)")
	}
	var firstErr error
	out := includeRegex.ReplaceAllStringFunc(content, func(match string) string {
		if firstErr != nil {
			return match
		}
		groups := includeRegex.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		path := filepath.Clean(groups[1])
		// Prevent escaping the embedded FS
		if strings.HasPrefix(path, "..") || strings.HasPrefix(path, "/") {
			firstErr = fmt.Errorf("invalid include path: %s", groups[1])
			return match
		}
		data, err := fs.ReadFile(Assets, path)
		if err != nil {
			firstErr = fmt.Errorf("failed to read include %s: %w", path, err)
			return match
		}
		resolved, err := resolveIncludes(string(data), depth+1)
		if err != nil {
			firstErr = err
			return match
		}
		return resolved
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}
