package validator

import (
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
)

// **Validates: Requirements 7.1, 7.2, 7.3**

// Property 8: Container Name Validation — accept iff matches regex and length ≤ 128
// **Validates: Requirements 7.1**

var containerNameOracle = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]+$`)

func TestProperty8_ContainerNameValidation(t *testing.T) {
	// Property: ValidateContainerName accepts a name iff it matches the regex AND length ≤ 128
	prop := func(name string) bool {
		err := ValidateContainerName(name)
		matchesRegex := containerNameOracle.MatchString(name)
		withinLength := len(name) >= 1 && len(name) <= 128

		shouldAccept := matchesRegex && withinLength
		accepted := err == nil

		return accepted == shouldAccept
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   containerNameGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 8 failed: %v", err)
	}
}

func containerNameGenerator(values []reflect.Value, rng *rand.Rand) {
	var name string
	switch rng.Intn(5) {
	case 0:
		name = generateValidContainerName(rng)
	case 1:
		invalidStarts := ".-_!@#"
		start := string(invalidStarts[rng.Intn(len(invalidStarts))])
		name = start + generateAlnum(rng, rng.Intn(10)+1)
	case 2:
		name = generateAlnum(rng, 3) + string([]byte{';'}) + generateAlnum(rng, 3)
	case 3:
		name = generateAlnum(rng, 130+rng.Intn(50))
	case 4:
		switch rng.Intn(3) {
		case 0:
			name = ""
		case 1:
			name = string(alnumChars[rng.Intn(len(alnumChars))])
		case 2:
			name = generateValidContainerName128(rng)
		}
	}
	values[0] = reflect.ValueOf(name)
}

func generateValidContainerName(rng *rand.Rand) string {
	length := rng.Intn(20) + 2 // at least 2 chars to match regex
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-"
	start := alnumChars[rng.Intn(len(alnumChars))]
	result := make([]byte, length)
	result[0] = start
	for i := 1; i < length; i++ {
		result[i] = chars[rng.Intn(len(chars))]
	}
	return string(result)
}

func generateValidContainerName128(rng *rand.Rand) string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-"
	result := make([]byte, 128)
	result[0] = alnumChars[rng.Intn(len(alnumChars))]
	for i := 1; i < 128; i++ {
		result[i] = chars[rng.Intn(len(chars))]
	}
	return string(result)
}

// Property 9: Git URL Validation — accept iff https/git scheme and no metacharacters
// **Validates: Requirements 7.2**

var metachars = []string{";", "&", "|", "$", "`", "(", ")", "{", "}", "<", ">", "!", "\\", "'", "\"", "\n"}

func TestProperty9_GitURLValidation(t *testing.T) {
	// Property: ValidateGitURL accepts a URL iff it starts with https:// or git://
	// AND contains no shell metacharacters
	prop := func(url string) bool {
		err := ValidateGitURL(url)

		hasValidScheme := strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "git://")
		hasMetachar := false
		for _, meta := range metachars {
			if strings.Contains(url, meta) {
				hasMetachar = true
				break
			}
		}

		shouldAccept := hasValidScheme && !hasMetachar
		accepted := err == nil

		return accepted == shouldAccept
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   gitURLGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 9 failed: %v", err)
	}
}

func gitURLGenerator(values []reflect.Value, rng *rand.Rand) {
	var url string
	switch rng.Intn(5) {
	case 0:
		url = "https://github.com/" + generateAlnum(rng, 5) + "/" + generateAlnum(rng, 5) + ".git"
	case 1:
		url = "git://github.com/" + generateAlnum(rng, 5) + "/" + generateAlnum(rng, 5) + ".git"
	case 2:
		schemes := []string{"http://", "ssh://", "ftp://", ""}
		url = schemes[rng.Intn(len(schemes))] + "github.com/" + generateAlnum(rng, 5)
	case 3:
		scheme := "https://"
		if rng.Intn(2) == 0 {
			scheme = "git://"
		}
		meta := metachars[rng.Intn(len(metachars))]
		url = scheme + "github.com/" + generateAlnum(rng, 3) + meta + generateAlnum(rng, 3)
	case 4:
		switch rng.Intn(3) {
		case 0:
			url = ""
		case 1:
			url = "https://"
		case 2:
			url = "git://" + generateAlnum(rng, 50)
		}
	}
	values[0] = reflect.ValueOf(url)
}

// Property 10: Environment Variable Name Validation — accept iff matches regex and length ≤ 256
// **Validates: Requirements 7.3**

var envVarNameOracle = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func TestProperty10_EnvVarNameValidation(t *testing.T) {
	// Property: ValidateEnvVarName accepts a name iff it matches the regex AND length ≤ 256
	prop := func(name string) bool {
		err := ValidateEnvVarName(name)
		matchesRegex := envVarNameOracle.MatchString(name)
		withinLength := len(name) >= 1 && len(name) <= 256

		shouldAccept := matchesRegex && withinLength
		accepted := err == nil

		return accepted == shouldAccept
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   envVarNameGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 failed: %v", err)
	}
}

func envVarNameGenerator(values []reflect.Value, rng *rand.Rand) {
	var name string
	switch rng.Intn(5) {
	case 0:
		name = generateValidEnvVarName(rng)
	case 1:
		name = string('0'+byte(rng.Intn(10))) + generateAlnum(rng, rng.Intn(10))
	case 2:
		specials := "-. =!@#"
		special := string(specials[rng.Intn(len(specials))])
		name = generateAlnum(rng, 3) + special + generateAlnum(rng, 3)
	case 3:
		name = generateValidEnvVarNameOfLength(rng, 257+rng.Intn(50))
	case 4:
		switch rng.Intn(3) {
		case 0:
			name = ""
		case 1:
			starts := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
			name = string(starts[rng.Intn(len(starts))])
		case 2:
			name = generateValidEnvVarNameOfLength(rng, 256)
		}
	}
	values[0] = reflect.ValueOf(name)
}

func generateValidEnvVarName(rng *rand.Rand) string {
	starts := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
	rest := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	length := rng.Intn(20) + 1
	result := make([]byte, length)
	result[0] = starts[rng.Intn(len(starts))]
	for i := 1; i < length; i++ {
		result[i] = rest[rng.Intn(len(rest))]
	}
	return string(result)
}

func generateValidEnvVarNameOfLength(rng *rand.Rand, length int) string {
	starts := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
	rest := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	result := make([]byte, length)
	result[0] = starts[rng.Intn(len(starts))]
	for i := 1; i < length; i++ {
		result[i] = rest[rng.Intn(len(rest))]
	}
	return string(result)
}

// Shared helpers

var alnumChars = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func generateAlnum(rng *rand.Rand, length int) string {
	result := make([]byte, length)
	for i := range result {
		result[i] = alnumChars[rng.Intn(len(alnumChars))]
	}
	return string(result)
}
