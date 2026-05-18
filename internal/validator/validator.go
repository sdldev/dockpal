package validator

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	containerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]+$`)
	envVarNameRegex    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	shellMetachars     = []string{";", "&", "|", "$", "`", "(", ")", "{", "}", "<", ">", "!", "\\", "'", "\"", "\n"}
)

// ValidateContainerName checks that a container name matches ^[a-zA-Z0-9][a-zA-Z0-9_.\-]+$
// and is at most 128 characters long.
func ValidateContainerName(name string) error {
	if len(name) == 0 || len(name) > 128 {
		return fmt.Errorf("container name must be 1-128 characters")
	}
	if !containerNameRegex.MatchString(name) {
		return fmt.Errorf("container name contains invalid characters")
	}
	return nil
}

// ValidateGitURL checks that a git URL uses https:// or git:// scheme
// and contains no shell metacharacters.
func ValidateGitURL(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "git://") {
		return fmt.Errorf("git URL must use https:// or git:// scheme")
	}
	for _, meta := range shellMetachars {
		if strings.Contains(url, meta) {
			return fmt.Errorf("git URL contains invalid characters")
		}
	}
	return nil
}

// ValidateEnvVarName checks that an environment variable name matches
// ^[a-zA-Z_][a-zA-Z0-9_]*$ and is at most 256 characters long.
func ValidateEnvVarName(name string) error {
	if len(name) == 0 || len(name) > 256 {
		return fmt.Errorf("env var name must be 1-256 characters")
	}
	if !envVarNameRegex.MatchString(name) {
		return fmt.Errorf("env var name contains invalid characters")
	}
	return nil
}
