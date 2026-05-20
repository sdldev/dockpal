package validator

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	containerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]*$`)
	envVarNameRegex    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	domainRegex        = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9\-]*[a-z0-9])?)*$`)
	branchNameRegex    = regexp.MustCompile(`^[a-zA-Z0-9/_.\-]+$`)
	shellMetachars     = []string{";", "&", "|", "$", "`", "(", ")", "{", "}", "<", ">", "!", "\\", "'", "\"", "\n"}
	yamlDangerous      = []string{"\n", "\r"}
)

// ValidateContainerName checks that a container name is 1-128 characters and Docker-safe.
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

// ValidateEnvVarValue checks that an environment variable value does not
// contain newlines or other characters that could inject YAML directives.
func ValidateEnvVarValue(value string) error {
	for _, ch := range yamlDangerous {
		if strings.Contains(value, ch) {
			return fmt.Errorf("env var value contains forbidden characters (newlines)")
		}
	}
	if len(value) > 4096 {
		return fmt.Errorf("env var value must be at most 4096 characters")
	}
	return nil
}

// ValidateDomain checks that a domain name is safe for interpolation
// into Traefik config. Only lowercase alphanumeric, dots, and hyphens.
func ValidateDomain(domain string) error {
	if len(domain) == 0 || len(domain) > 253 {
		return fmt.Errorf("domain must be 1-253 characters")
	}
	if !domainRegex.MatchString(domain) {
		return fmt.Errorf("domain contains invalid characters")
	}
	return nil
}

// ValidateBranchName checks that a git branch name is safe.
func ValidateBranchName(branch string) error {
	if len(branch) == 0 || len(branch) > 200 {
		return fmt.Errorf("branch name must be 1-200 characters")
	}
	if !branchNameRegex.MatchString(branch) {
		return fmt.Errorf("branch name contains invalid characters")
	}
	return nil
}
