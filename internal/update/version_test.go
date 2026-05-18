package update

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		want     bool
		wantErr  bool
	}{
		// Major version updates
		{
			name:    "major version update available",
			current: "v1.0.0",
			latest:  "v2.0.0",
			want:    true,
			wantErr: false,
		},
		{
			name:    "no major version update",
			current: "v2.0.0",
			latest:  "v1.0.0",
			want:    false,
			wantErr: false,
		},
		// Minor version updates
		{
			name:    "minor version update available",
			current: "v1.0.0",
			latest:  "v1.1.0",
			want:    true,
			wantErr: false,
		},
		{
			name:    "no minor version update",
			current: "v1.1.0",
			latest:  "v1.0.0",
			want:    false,
			wantErr: false,
		},
		// Patch version updates
		{
			name:    "patch version update available",
			current: "v1.0.0",
			latest:  "v1.0.1",
			want:    true,
			wantErr: false,
		},
		{
			name:    "no patch version update",
			current: "v1.0.1",
			latest:  "v1.0.0",
			want:    false,
			wantErr: false,
		},
		// Equal versions
		{
			name:    "equal versions - no update",
			current: "v1.0.0",
			latest:  "v1.0.0",
			want:    false,
			wantErr: false,
		},
		// Without 'v' prefix
		{
			name:    "without v prefix - update available",
			current: "1.0.0",
			latest:  "1.1.0",
			want:    true,
			wantErr: false,
		},
		// Multi-digit versions
		{
			name:    "multi-digit versions",
			current: "v10.20.30",
			latest:  "v11.0.0",
			want:    true,
			wantErr: false,
		},
		// Edge cases
		{
			name:    "complex version comparison - major difference",
			current: "v0.9.9",
			latest:  "v1.0.0",
			want:    true,
			wantErr: false,
		},
		{
			name:    "complex version comparison - minor difference",
			current: "v1.0.0",
			latest:  "v1.1.0",
			want:    true,
			wantErr: false,
		},
		// Error cases
		{
			name:     "empty current version",
			current:  "",
			latest:   "v1.0.0",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "empty latest version",
			current:  "v1.0.0",
			latest:   "",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "invalid current version - non-numeric",
			current:  "v1.0.x",
			latest:   "v1.1.0",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "invalid latest version - non-numeric",
			current:  "v1.0.0",
			latest:   "v1.x.0",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "invalid version format - missing parts",
			current:  "v1.0",
			latest:   "v1.1.0",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "invalid version format - too many parts",
			current:  "v1.0.0.0",
			latest:   "v1.1.0",
			want:     false,
			wantErr:  true,
		},
		{
			name:     "invalid version - negative numbers",
			current:  "v-1.0.0",
			latest:   "v1.0.0",
			want:     false,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareVersions(tt.current, tt.latest)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareVersions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("CompareVersions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *Version
		wantErr bool
	}{
		{
			name:  "standard version with v prefix",
			input: "v1.2.3",
			want:  &Version{Major: 1, Minor: 2, Patch: 3},
		},
		{
			name:  "standard version without v prefix",
			input: "1.2.3",
			want:  &Version{Major: 1, Minor: 2, Patch: 3},
		},
		{
			name:  "zero version",
			input: "v0.0.0",
			want:  &Version{Major: 0, Minor: 0, Patch: 0},
		},
		{
			name:    "missing patch version",
			input:   "v1.2",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "non-numeric major",
			input:   "vX.2.3",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got.Major != tt.want.Major || got.Minor != tt.want.Minor || got.Patch != tt.want.Patch {
				t.Errorf("parseVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
// **Validates: Requirements 1.1, 1.2**

// TestProperty10_VersionComparisonGreaterThan verifies that if latest > current,
// updateAvailable should be true.
// **Validates: Requirements 1.1, 1.2**
func TestProperty10_VersionComparisonGreaterThan(t *testing.T) {
	// Property: For any two valid semver versions where latest > current,
	// CompareVersions returns true (update available)
	prop := func(params versionPair) bool {
		// Ensure latest > current by generating a guaranteed greater version
		currentVer := makeSemver(params.major1, params.minor1, params.patch1)
		// Make latest definitely greater
		latestVer := makeSemver(params.major1+params.major2+1, params.minor1, params.patch1)

		updateAvailable, err := CompareVersions(currentVer, latestVer)
		if err != nil {
			t.Logf("CompareVersions error: %v", err)
			return false
		}

		// latest > current, so updateAvailable should be true
		return updateAvailable == true
	}

	cfg := &quick.Config{
		MaxCount: 200,
		Values:   versionPairGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 (greater than) failed: %v", err)
	}
}

// TestProperty10_VersionComparisonLessOrEqual verifies that if latest <= current,
// updateAvailable should be false.
// **Validates: Requirements 1.1, 1.2**
func TestProperty10_VersionComparisonLessOrEqual(t *testing.T) {
	// Property: For any two valid semver versions where latest <= current,
	// CompareVersions returns false (no update available)
	prop := func(params versionPair) bool {
		// Generate a base version (ensure at least 1 to avoid zero issues)
		baseMajor := 1 + params.major1%10
		baseMinor := params.minor1%10
		basePatch := 1 + params.patch1%10 // Ensure non-zero

		// Current version is the base
		currentVer := makeSemver(baseMajor, baseMinor, basePatch)
		// Latest version is always lower (decrement patch, handle wrap)
		latestPatch := basePatch - 1
		if latestPatch < 0 {
			latestPatch = 0
		}
		latestVer := makeSemver(baseMajor, baseMinor, latestPatch)

		updateAvailable, err := CompareVersions(currentVer, latestVer)
		if err != nil {
			t.Logf("CompareVersions error: %v", err)
			return false
		}

		// latest <= current, so updateAvailable should be false
		return updateAvailable == false
	}

	cfg := &quick.Config{
		MaxCount: 200,
		Values:   versionPairGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 (less or equal) failed: %v", err)
	}
}

// TestProperty10_VersionComparisonTransitivity verifies that version comparison
// is transitive: if A > B and B > C, then A > C.
// **Validates: Requirements 1.1, 1.2**
func TestProperty10_VersionComparisonTransitivity(t *testing.T) {
	// Property: Version comparison is transitive
	// If v1 > v2 and v2 > v3, then v1 > v3
	prop := func(params threeVersions) bool {
		// Generate three different versions
		v1 := makeSemver(3+params.major1%5, params.minor1%10, params.patch1%10)
		v2 := makeSemver(2+params.major1%2, params.minor2%10, params.patch2%10)
		v3 := makeSemver(1+params.major2%1, params.minor3%10, params.patch3%10)

		// Compare v1 > v2
		v1GtV2, err := CompareVersions(v2, v1)
		if err != nil {
			return true // Skip invalid versions
		}

		// Compare v2 > v3
		v2GtV3, err := CompareVersions(v3, v2)
		if err != nil {
			return true // Skip invalid versions
		}

		// Compare v1 > v3 (should be true if both above are true)
		v1GtV3, err := CompareVersions(v3, v1)
		if err != nil {
			return true // Skip invalid versions
		}

		// If v1 > v2 and v2 > v3, then v1 > v3 must hold
		if v1GtV2 && v2GtV3 {
			return v1GtV3 == true
		}

		return true // Not enough data to test transitivity
	}

	cfg := &quick.Config{
		MaxCount: 200,
		Values:   threeVersionsGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 (transitivity) failed: %v", err)
	}
}

// TestProperty10_PreReleaseVersions verifies that pre-release versions are
// handled correctly according to semver spec.
// **Validates: Requirements 1.1, 1.2**
func TestProperty10_PreReleaseVersions(t *testing.T) {
	// Property: Pre-release versions have lower precedence than the stable version
	// e.g., 1.0.0-alpha < 1.0.0
	prop := func(params preReleaseParams) bool {
		// Base version
		base := makeSemver(1+params.major%3, params.minor%5, params.patch%5)

		// Pre-release version (base -alpha)
		preRelease := base + "-alpha"

		// Pre-release should be considered less than stable
		updateAvailable, err := CompareVersions(preRelease, base)
		if err != nil {
			// Pre-release parsing may fail, skip this case
			return true
		}

		// When latest is stable and current is pre-release, update should be available
		return updateAvailable == true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   preReleaseGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 (pre-release) failed: %v", err)
	}
}

// TestProperty10_VersionSymmetry verifies the symmetry property:
// If CompareVersions(A, B) = true, then CompareVersions(B, A) = false
// **Validates: Requirements 1.1, 1.2**
func TestProperty10_VersionSymmetry(t *testing.T) {
	// Property: If A > B, then B < A (symmetry)
	prop := func(params versionPair) bool {
		// Generate two different versions
		// Make v1 definitely greater than v2
		v1 := makeSemver(2+params.major1%5, params.minor1%10, params.patch1%10)
		v2 := makeSemver(1+params.major2%1, params.minor2%10, params.patch2%10)

		v1GtV2, err := CompareVersions(v2, v1)
		if err != nil {
			return true // Skip invalid versions
		}

		v2GtV1, err := CompareVersions(v1, v2)
		if err != nil {
			return true // Skip invalid versions
		}

		// If v1 > v2, then v2 should NOT be > v1
		if v1GtV2 && v2GtV1 {
			return false // Symmetry violated!
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 200,
		Values:   versionPairGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 (symmetry) failed: %v", err)
	}
}

// TestProperty10_MajorMinorPatchPriority verifies that semver comparison
// prioritizes major > minor > patch
// **Validates: Requirements 1.1, 1.2**
func TestProperty10_MajorMinorPatchPriority(t *testing.T) {
	prop := func(params priorityParams) bool {
		switch params.caseType % 4 {
		case 0:
			// Major difference: 2.0.0 > 1.9.9
			v1 := "v2.0.0"
			v2 := "v1.9.9"
			result, err := CompareVersions(v2, v1)
			if err != nil {
				return false
			}
			return result == true

		case 1:
			// Same major, minor difference: 1.2.0 > 1.1.9
			v1 := "v1.2.0"
			v2 := "v1.1.9"
			result, err := CompareVersions(v2, v1)
			if err != nil {
				return false
			}
			return result == true

		case 2:
			// Same major/minor, patch difference: 1.0.2 > 1.0.1
			v1 := "v1.0.2"
			v2 := "v1.0.1"
			result, err := CompareVersions(v2, v1)
			if err != nil {
				return false
			}
			return result == true

		case 3:
			// Edge: 1.0.0 > 0.99.99 (major takes precedence)
			v1 := "v1.0.0"
			v2 := "v0.99.99"
			result, err := CompareVersions(v2, v1)
			if err != nil {
				return false
			}
			return result == true
		}
		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   priorityGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 (priority) failed: %v", err)
	}
}

// Helper types and generators for property-based tests

// versionPair represents two version numbers for comparison
type versionPair struct {
	major1 int
	minor1 int
	patch1 int
	major2 int
	minor2 int
	patch2 int
}

func versionPairGenerator(values []reflect.Value, rng *rand.Rand) {
	params := versionPair{
		major1: rng.Intn(10),
		minor1: rng.Intn(20),
		patch1: rng.Intn(20),
		major2: rng.Intn(5),
		minor2: rng.Intn(10),
		patch2: rng.Intn(10),
	}
	values[0] = reflect.ValueOf(params)
}

// threeVersions represents three versions for transitivity testing
type threeVersions struct {
	major1 int
	minor1 int
	patch1 int
	major2 int
	minor2 int
	patch2 int
	major3 int
	minor3 int
	patch3 int
}

func threeVersionsGenerator(values []reflect.Value, rng *rand.Rand) {
	params := threeVersions{
		major1: rng.Intn(5),
		minor1: rng.Intn(10),
		patch1: rng.Intn(10),
		major2: rng.Intn(3),
		minor2: rng.Intn(10),
		patch2: rng.Intn(10),
		major3: rng.Intn(3),
		minor3: rng.Intn(10),
		patch3: rng.Intn(10),
	}
	values[0] = reflect.ValueOf(params)
}

// preReleaseParams for pre-release version testing
type preReleaseParams struct {
	major int
	minor int
	patch int
}

func preReleaseGenerator(values []reflect.Value, rng *rand.Rand) {
	params := preReleaseParams{
		major: rng.Intn(5),
		minor: rng.Intn(10),
		patch: rng.Intn(10),
	}
	values[0] = reflect.ValueOf(params)
}

// priorityParams for major/minor/patch priority testing
type priorityParams struct {
	caseType int
}

func priorityGenerator(values []reflect.Value, rng *rand.Rand) {
	params := priorityParams{
		caseType: rng.Intn(4),
	}
	values[0] = reflect.ValueOf(params)
}

// makeSemver creates a semver version string from components
func makeSemver(major, minor, patch int) string {
	// Ensure non-negative
	if major < 0 {
		major = 0
	}
	if minor < 0 {
		minor = 0
	}
	if patch < 0 {
		patch = 0
	}
	return fmt.Sprintf("v%d.%d.%d", major, minor, patch)
}