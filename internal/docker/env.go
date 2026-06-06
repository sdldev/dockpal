package docker

import (
	"os"
	"regexp"
)

var envRe = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)(?::-([^}]*))?\}|\$([a-zA-Z0-9_]+)`)

// SubstituteComposeEnv replaces bash-style variable references like ${VAR} or ${VAR:-default}
// with their corresponding environment variable values, falling back to defaults if present.
func SubstituteComposeEnv(yamlContent string) string {
	return envRe.ReplaceAllStringFunc(yamlContent, func(m string) string {
		matches := envRe.FindStringSubmatch(m)
		if len(matches) == 0 {
			return m
		}

		var key, defaultVal string
		if matches[1] != "" {
			key = matches[1]
			defaultVal = matches[2]
		} else if matches[3] != "" {
			key = matches[3]
		} else {
			return m
		}

		val := os.Getenv(key)
		if val == "" && defaultVal != "" {
			return defaultVal
		}
		return val
	})
}
