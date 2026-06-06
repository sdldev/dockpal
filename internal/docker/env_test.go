package docker

import (
	"os"
	"testing"
)

func TestSubstituteComposeEnv(t *testing.T) {
	os.Setenv("TEST_VAR1", "value1")
	defer os.Unsetenv("TEST_VAR1")
	
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no vars",
			input:    "image: nginx:latest",
			expected: "image: nginx:latest",
		},
		{
			name:     "simple var",
			input:    "image: $TEST_VAR1",
			expected: "image: value1",
		},
		{
			name:     "braced var",
			input:    "image: ${TEST_VAR1}",
			expected: "image: value1",
		},
		{
			name:     "default value used",
			input:    "image: ${TEST_VAR2:-nginx:alpine}",
			expected: "image: nginx:alpine",
		},
		{
			name:     "default value ignored if set",
			input:    "image: ${TEST_VAR1:-nginx:alpine}",
			expected: "image: value1",
		},
		{
			name:     "complex string",
			input:    "image: ${REGISTRY:-ghcr.io}/user/app:${TAG:-latest}",
			expected: "image: ghcr.io/user/app:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SubstituteComposeEnv(tt.input)
			if result != tt.expected {
				t.Errorf("SubstituteComposeEnv() = %v, want %v", result, tt.expected)
			}
		})
	}
}
