package docker

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
)

// Property 11: Compose Port and Volume Parsing
// **Validates: Requirements 8.2, 8.3**

// TestProperty_ParsePort_ValidSpecProducesCorrectBinding verifies that for any
// valid port numbers (1-65535), ParsePort produces a PortBinding with matching values.
func TestProperty_ParsePort_ValidSpecProducesCorrectBinding(t *testing.T) {
	f := func(hostPort, containerPort uint16) bool {
		// Constrain to valid port range 1-65535
		if hostPort == 0 || containerPort == 0 {
			return true // skip invalid
		}
		spec := fmt.Sprintf("%d:%d", hostPort, containerPort)
		pb, err := ParsePort(spec)
		if err != nil {
			return false
		}
		return pb.HostPort == int(hostPort) &&
			pb.ContainerPort == int(containerPort) &&
			pb.Protocol == "tcp"
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Property violated - valid port spec produced incorrect binding: %v", err)
	}
}

// TestProperty_ParsePort_ProtocolSuffixPreserved verifies that protocol suffixes
// (tcp/udp) are correctly parsed from valid port specifications.
func TestProperty_ParsePort_ProtocolSuffixPreserved(t *testing.T) {
	f := func(hostPort, containerPort uint16, useUDP bool) bool {
		if hostPort == 0 || containerPort == 0 {
			return true
		}
		proto := "tcp"
		if useUDP {
			proto = "udp"
		}
		spec := fmt.Sprintf("%d:%d/%s", hostPort, containerPort, proto)
		pb, err := ParsePort(spec)
		if err != nil {
			return false
		}
		return pb.HostPort == int(hostPort) &&
			pb.ContainerPort == int(containerPort) &&
			pb.Protocol == proto
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Property violated - protocol suffix not preserved: %v", err)
	}
}

// TestProperty_ParsePort_SinglePortSetsHostAndContainer verifies that a single
// port number sets both HostPort and ContainerPort to the same value.
func TestProperty_ParsePort_SinglePortSetsHostAndContainer(t *testing.T) {
	f := func(port uint16) bool {
		if port == 0 {
			return true
		}
		spec := fmt.Sprintf("%d", port)
		pb, err := ParsePort(spec)
		if err != nil {
			return false
		}
		return pb.HostPort == int(port) && pb.ContainerPort == int(port)
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("Property violated - single port did not set both host and container: %v", err)
	}
}

// TestProperty_ParseVolume_ValidSpecProducesCorrectMount verifies that for any
// valid host:container volume spec, ParseVolume produces a VolumeMount with matching paths.
func TestProperty_ParseVolume_ValidSpecProducesCorrectMount(t *testing.T) {
	// Generate safe path segments (no colons, no empty)
	genSafePath := func(r *rand.Rand) string {
		segments := r.Intn(4) + 1
		parts := make([]string, segments)
		for i := range parts {
			length := r.Intn(8) + 1
			seg := make([]byte, length)
			for j := range seg {
				seg[j] = "abcdefghijklmnopqrstuvwxyz0123456789_-."[r.Intn(37)]
			}
			parts[i] = string(seg)
		}
		return "/" + strings.Join(parts, "/")
	}

	config := &quick.Config{MaxCount: 300}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < config.MaxCount; i++ {
		hostPath := genSafePath(rng)
		containerPath := genSafePath(rng)
		spec := hostPath + ":" + containerPath

		vm, err := ParseVolume(spec)
		if err != nil {
			t.Fatalf("Property violated - valid volume spec %q produced error: %v", spec, err)
		}
		if vm.HostPath != hostPath {
			t.Fatalf("Property violated - host path mismatch: got %q, want %q", vm.HostPath, hostPath)
		}
		if vm.ContainerPath != containerPath {
			t.Fatalf("Property violated - container path mismatch: got %q, want %q", vm.ContainerPath, containerPath)
		}
		if vm.ReadOnly {
			t.Fatalf("Property violated - should not be readonly without :ro suffix")
		}
	}
}

// TestProperty_ParseVolume_ReadOnlyFlag verifies that the :ro suffix correctly
// sets ReadOnly=true and :rw sets ReadOnly=false.
func TestProperty_ParseVolume_ReadOnlyFlag(t *testing.T) {
	genSafePath := func(r *rand.Rand) string {
		length := r.Intn(8) + 1
		seg := make([]byte, length)
		for j := range seg {
			seg[j] = "abcdefghijklmnopqrstuvwxyz"[r.Intn(26)]
		}
		return "/" + string(seg)
	}

	config := &quick.Config{MaxCount: 300}
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < config.MaxCount; i++ {
		hostPath := genSafePath(rng)
		containerPath := genSafePath(rng)
		isRO := rng.Intn(2) == 0

		mode := "rw"
		if isRO {
			mode = "ro"
		}
		spec := hostPath + ":" + containerPath + ":" + mode

		vm, err := ParseVolume(spec)
		if err != nil {
			t.Fatalf("Property violated - valid volume spec %q produced error: %v", spec, err)
		}
		if vm.ReadOnly != isRO {
			t.Fatalf("Property violated - ReadOnly mismatch for spec %q: got %v, want %v", spec, vm.ReadOnly, isRO)
		}
	}
}

// Property 12: Compose Dependency Ordering
// **Validates: Requirements 8.6**

// TestProperty_ResolveStartOrder_DependenciesBeforeDependents verifies that
// for any valid DAG of services, every dependency appears before its dependent
// in the resolved start order.
func TestProperty_ResolveStartOrder_DependenciesBeforeDependents(t *testing.T) {
	// Generate random DAG of services with depends_on relationships
	genDAG := func(r *rand.Rand) *ComposeFile {
		numServices := r.Intn(8) + 2 // 2-9 services
		serviceNames := make([]string, numServices)
		for i := range serviceNames {
			serviceNames[i] = fmt.Sprintf("svc%d", i)
		}

		services := make(map[string]ComposeService)
		for i, name := range serviceNames {
			svc := ComposeService{Image: "alpine"}
			// A service can only depend on services defined before it (ensures DAG)
			if i > 0 {
				numDeps := r.Intn(min(i, 3)) // 0 to min(i, 2) dependencies
				if numDeps > 0 {
					deps := make([]interface{}, numDeps)
					used := make(map[int]bool)
					for j := 0; j < numDeps; j++ {
						idx := r.Intn(i)
						for used[idx] {
							idx = r.Intn(i)
						}
						used[idx] = true
						deps[j] = serviceNames[idx]
					}
					svc.DependsOn = deps
				}
			}
			services[name] = svc
		}

		return &ComposeFile{Services: services}
	}

	config := &quick.Config{MaxCount: 500}
	rng := rand.New(rand.NewSource(123))
	for i := 0; i < config.MaxCount; i++ {
		cf := genDAG(rng)
		order, err := ResolveStartOrder(cf)
		if err != nil {
			t.Fatalf("Property violated - valid DAG produced error: %v", err)
		}

		// Check all services are present
		if len(order) != len(cf.Services) {
			t.Fatalf("Property violated - order has %d services, expected %d", len(order), len(cf.Services))
		}

		// Build index map
		indexOf := make(map[string]int)
		for idx, name := range order {
			indexOf[name] = idx
		}

		// Verify every dependency appears before its dependent
		for name, svc := range cf.Services {
			deps := parseDependsOn(svc.DependsOn)
			for _, dep := range deps {
				if indexOf[dep] >= indexOf[name] {
					t.Fatalf("Property violated - dependency %q (index %d) not before %q (index %d)",
						dep, indexOf[dep], name, indexOf[name])
				}
			}
		}
	}
}

// TestProperty_ResolveStartOrder_AllServicesPresent verifies that the resolved
// order contains exactly all services defined in the compose file.
func TestProperty_ResolveStartOrder_AllServicesPresent(t *testing.T) {
	f := func(numServices uint8) bool {
		n := int(numServices)%6 + 1 // 1-6 services
		services := make(map[string]ComposeService)
		for i := 0; i < n; i++ {
			services[fmt.Sprintf("svc%d", i)] = ComposeService{Image: "alpine"}
		}
		cf := &ComposeFile{Services: services}

		order, err := ResolveStartOrder(cf)
		if err != nil {
			return false
		}
		if len(order) != n {
			return false
		}

		// All service names must be in the order
		orderSet := make(map[string]bool)
		for _, name := range order {
			orderSet[name] = true
		}
		for name := range services {
			if !orderSet[name] {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("Property violated - not all services present in order: %v", err)
	}
}

// Property 13: Invalid YAML Rejection
// **Validates: Requirements 8.7**

// TestProperty_ParseComposeFile_NonYAMLReturnsError verifies that random
// non-YAML strings always produce an error from ParseComposeFile.
func TestProperty_ParseComposeFile_NonYAMLReturnsError(t *testing.T) {
	// Generate strings that are clearly not valid YAML service definitions
	genInvalidYAML := func(r *rand.Rand) string {
		// Create strings with unbalanced braces, random binary, etc.
		patterns := []string{
			"{{{",
			"[[[not yaml",
			"\x00\x01\x02binary",
			":::invalid:::",
			"{{{\nnot: [valid\nyaml: {{{",
			"services:\n  - not a map",
		}
		return patterns[r.Intn(len(patterns))]
	}

	rng := rand.New(rand.NewSource(77))
	for i := 0; i < 200; i++ {
		input := genInvalidYAML(rng)
		_, err := ParseComposeFile(input)
		if err == nil {
			t.Fatalf("Property violated - non-YAML input %q was accepted", input)
		}
	}
}

// TestProperty_ParseComposeFile_MissingServicesReturnsError verifies that
// valid YAML without a services key always returns an error.
func TestProperty_ParseComposeFile_MissingServicesReturnsError(t *testing.T) {
	// Generate valid YAML that lacks a "services" key with content
	f := func(version string) bool {
		// Clean the version string to be YAML-safe
		clean := ""
		for _, c := range version {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' {
				clean += string(c)
			}
		}
		if clean == "" {
			clean = "3"
		}
		yaml := fmt.Sprintf("version: \"%s\"\nnetworks:\n  default:\n    driver: bridge\n", clean)
		_, err := ParseComposeFile(yaml)
		return err != nil // must return error
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - YAML without services was accepted: %v", err)
	}
}

// TestProperty_ParseComposeFile_EmptyServicesReturnsError verifies that
// a compose file with an empty services map always returns an error.
func TestProperty_ParseComposeFile_EmptyServicesReturnsError(t *testing.T) {
	f := func(version string) bool {
		clean := ""
		for _, c := range version {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' {
				clean += string(c)
			}
		}
		if clean == "" {
			clean = "3"
		}
		yaml := fmt.Sprintf("version: \"%s\"\nservices:\n", clean)
		_, err := ParseComposeFile(yaml)
		return err != nil // must return error
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("Property violated - empty services was accepted: %v", err)
	}
}
