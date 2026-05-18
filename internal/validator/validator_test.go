package validator

import (
	"strings"
	"testing"
)

func TestValidateContainerName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "mycontainer", false},
		{"valid with dots", "my.container", false},
		{"valid with dashes", "my-container", false},
		{"valid with underscores", "my_container", false},
		{"valid starts with number", "1container", false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 129), true},
		{"max length", "a" + strings.Repeat("b", 127), false},
		{"starts with dot", ".container", true},
		{"starts with dash", "-container", true},
		{"contains space", "my container", true},
		{"contains shell char", "my;container", true},
		{"single char", "a", true}, // regex requires at least 2 chars
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContainerName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContainerName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateGitURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid https", "https://github.com/user/repo.git", false},
		{"valid git", "git://github.com/user/repo.git", false},
		{"http scheme", "http://github.com/user/repo.git", true},
		{"ssh scheme", "ssh://git@github.com/user/repo.git", true},
		{"no scheme", "github.com/user/repo.git", true},
		{"semicolon injection", "https://github.com/user/repo;rm -rf /", true},
		{"pipe injection", "https://github.com/user/repo|cat /etc/passwd", true},
		{"backtick injection", "https://github.com/user/repo`id`", true},
		{"dollar injection", "https://github.com/user/$HOME/repo", true},
		{"ampersand injection", "https://github.com/user/repo&&id", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGitURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGitURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEnvVarName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "MY_VAR", false},
		{"valid starts with underscore", "_MY_VAR", false},
		{"valid lowercase", "my_var", false},
		{"valid single char", "A", false},
		{"valid with numbers", "VAR123", false},
		{"empty", "", true},
		{"too long", strings.Repeat("A", 257), true},
		{"max length", strings.Repeat("A", 256), false},
		{"starts with number", "1VAR", true},
		{"contains dash", "MY-VAR", true},
		{"contains space", "MY VAR", true},
		{"contains dot", "MY.VAR", true},
		{"contains equals", "MY=VAR", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEnvVarName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEnvVarName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
