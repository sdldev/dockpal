package db

import (
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/quick"
	"time"
)

// Property 1: Instance persistence round-trip
// **Validates: Requirements 1.1, 1.7**

// TestProperty_InstancePersistenceRoundTrip verifies that an Instance can be
// saved and retrieved with all fields preserved correctly.
func TestProperty_InstancePersistenceRoundTrip(t *testing.T) {
	prop := func(params instanceParams) bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		// Create instance with generated params
		instance := Instance{
			ID:                  params.ID,
			Name:                params.Name,
			Host:                params.Host,
			Port:                params.Port,
			Mode:                params.Mode,
			AgentTokenHash:      params.AgentTokenHash,
			AgentTokenEncrypted: params.AgentTokenEncrypted,
			AgentVersion:        params.AgentVersion,
			Status:              params.Status,
			DockerVersion:       params.DockerVersion,
			OS:                  params.OS,
			CPUCores:            params.CPUCores,
			TotalMemory:         params.TotalMemory,
			LastSeen:            params.LastSeen,
			CreatedAt:           params.CreatedAt,
		}

		// Save the instance
		if err := database.SaveInstance(instance); err != nil {
			t.Logf("failed to save instance: %v", err)
			return false
		}

		// Retrieve the instance
		retrieved, err := database.GetInstance(params.ID)
		if err != nil {
			t.Logf("failed to get instance: %v", err)
			return false
		}

		// Verify all fields match
		if retrieved.ID != instance.ID {
			t.Logf("ID mismatch: got %q, want %q", retrieved.ID, instance.ID)
			return false
		}
		if retrieved.Name != instance.Name {
			t.Logf("Name mismatch: got %q, want %q", retrieved.Name, instance.Name)
			return false
		}
		if retrieved.Host != instance.Host {
			t.Logf("Host mismatch: got %q, want %q", retrieved.Host, instance.Host)
			return false
		}
		if retrieved.Port != instance.Port {
			t.Logf("Port mismatch: got %d, want %d", retrieved.Port, instance.Port)
			return false
		}
		if retrieved.Mode != instance.Mode {
			t.Logf("Mode mismatch: got %q, want %q", retrieved.Mode, instance.Mode)
			return false
		}
		if retrieved.AgentTokenHash != instance.AgentTokenHash {
			t.Logf("AgentTokenHash mismatch: got %q, want %q", retrieved.AgentTokenHash, instance.AgentTokenHash)
			return false
		}
		if string(retrieved.AgentTokenEncrypted) != string(instance.AgentTokenEncrypted) {
			t.Logf("AgentTokenEncrypted mismatch")
			return false
		}
		if retrieved.AgentVersion != instance.AgentVersion {
			t.Logf("AgentVersion mismatch: got %q, want %q", retrieved.AgentVersion, instance.AgentVersion)
			return false
		}
		if retrieved.Status != instance.Status {
			t.Logf("Status mismatch: got %q, want %q", retrieved.Status, instance.Status)
			return false
		}
		if retrieved.DockerVersion != instance.DockerVersion {
			t.Logf("DockerVersion mismatch: got %q, want %q", retrieved.DockerVersion, instance.DockerVersion)
			return false
		}
		if retrieved.OS != instance.OS {
			t.Logf("OS mismatch: got %q, want %q", retrieved.OS, instance.OS)
			return false
		}
		if retrieved.CPUCores != instance.CPUCores {
			t.Logf("CPUCores mismatch: got %d, want %d", retrieved.CPUCores, instance.CPUCores)
			return false
		}
		if retrieved.TotalMemory != instance.TotalMemory {
			t.Logf("TotalMemory mismatch: got %d, want %d", retrieved.TotalMemory, instance.TotalMemory)
			return false
		}
		if retrieved.LastSeen != instance.LastSeen {
			t.Logf("LastSeen mismatch: got %d, want %d", retrieved.LastSeen, instance.LastSeen)
			return false
		}
		if retrieved.CreatedAt != instance.CreatedAt {
			t.Logf("CreatedAt mismatch: got %d, want %d", retrieved.CreatedAt, instance.CreatedAt)
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   instanceGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 1 failed: %v", err)
	}
}

// instanceParams holds generated parameters for an Instance
type instanceParams struct {
	ID                  string
	Name                string
	Host                string
	Port                int
	Mode                string
	AgentTokenHash      string
	AgentTokenEncrypted []byte
	AgentVersion        string
	Status              string
	DockerVersion       string
	OS                  string
	CPUCores            int
	TotalMemory         int64
	LastSeen            int64
	CreatedAt           int64
}

// instanceGenerator generates random valid Instance parameters
func instanceGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate valid ID (non-empty)
	idLen := rng.Intn(20) + 10 // 10-29 chars
	id := make([]byte, idLen)
	idChars := "abcdefghijklmnopqrstuvwxyz0123456789-"
	for i := range id {
		id[i] = idChars[rng.Intn(len(idChars))]
	}
	idStr := string(id)

	// Generate valid Name (1-100 chars)
	nameLen := rng.Intn(99) + 1 // 1-99 chars
	name := make([]byte, nameLen)
	nameChars := "abcdefghijklmnopqrstuvwxyz0123456789 -_"
	for i := range name {
		name[i] = nameChars[rng.Intn(len(nameChars))]
	}
	nameStr := string(name)

	// Generate valid Host (valid hostname - alphanumeric and dots/dashes)
	hostParts := rng.Intn(3) + 2 // 2-4 parts
	host := make([]string, hostParts)
	for i := range host {
		partLen := rng.Intn(10) + 1
		part := make([]byte, partLen)
		for j := range part {
			part[j] = "abcdefghijklmnopqrstuvwxyz0123456789"[rng.Intn(36)]
		}
		host[i] = string(part)
	}
	hostStr := ""

	if rng.Intn(10) < 7 {
		// 70% chance: regular hostname (e.g., "server1.example.com")
		hostStr = host[0]
		for i := 1; i < len(host); i++ {
			hostStr += "." + host[i]
		}
	} else {
		// 30% chance: IP address-like (e.g., "192.168.1.1")
		hostStr = host[0][:min(len(host[0]), 3)]
		for i := 1; i < min(4, len(host)); i++ {
			hostStr += "." + host[i][:min(len(host[i]), 3)]
		}
	}

	// Generate valid Port (1-65535)
	port := rng.Intn(65535) + 1

	// Generate valid Mode (direct or edge)
	mode := "direct"
	if rng.Intn(2) == 1 {
		mode = "edge"
	}

	// Generate AgentTokenHash
	tokenHashLen := rng.Intn(30) + 10
	tokenHash := make([]byte, tokenHashLen)
	// String has 40 characters: a-z (26) + 0-9 (10) + $ # @ ! (4)
	agentTokenHashChars := "abcdefghijklmnopqrstuvwxyz0123456789$#@!"
	for i := range tokenHash {
		tokenHash[i] = agentTokenHashChars[rng.Intn(len(agentTokenHashChars))]
	}
	agentTokenHash := string(tokenHash)

	// Generate AgentTokenEncrypted
	encLen := rng.Intn(40) + 10
	agentTokenEncrypted := make([]byte, encLen)
	for i := range agentTokenEncrypted {
		agentTokenEncrypted[i] = byte(rng.Intn(256))
	}

	// Generate AgentVersion (e.g., "1.2.3")
	agentVersion := "1." + string(rune('0'+rng.Intn(10))) + "." + string(rune('0'+rng.Intn(10)))

	// Generate valid Status (online, offline, or enrolling)
	statuses := []string{"online", "offline", "enrolling"}
	status := statuses[rng.Intn(3)]

	// Generate DockerVersion (e.g., "24.0.5")
	dockerVersion := "24." + string(rune('0'+rng.Intn(10))) + "." + string(rune('0'+rng.Intn(10)))

	// Generate OS (e.g., "linux/amd64", "linux/arm64", "darwin/amd64")
	oses := []string{"linux/amd64", "linux/arm64", "linux/386", "darwin/amd64", "darwin/arm64", "windows/amd64"}
	osStr := oses[rng.Intn(len(oses))]

	// Generate CPUCores (1-64)
	cpuCores := rng.Intn(63) + 1

	// Generate TotalMemory (in bytes - reasonable range 512MB to 128GB)
	totalMemory := int64(rng.Intn(128*1024*1024*1024-512*1024*1024) + 512*1024*1024)

	// Generate valid timestamps (within reasonable range - past 5 years to future 1 year)
	now := time.Now().Unix()
	createdAt := now - int64(rng.Intn(5*365*24*60*60)) // past 5 years
	lastSeen := createdAt + int64(rng.Intn(int(now-createdAt))) // after CreatedAt

	params := instanceParams{
		ID:                  idStr,
		Name:                nameStr,
		Host:                hostStr,
		Port:                port,
		Mode:                mode,
		AgentTokenHash:      agentTokenHash,
		AgentTokenEncrypted: agentTokenEncrypted,
		AgentVersion:        agentVersion,
		Status:              status,
		DockerVersion:       dockerVersion,
		OS:                  osStr,
		CPUCores:            cpuCores,
		TotalMemory:         totalMemory,
		LastSeen:            lastSeen,
		CreatedAt:           createdAt,
	}
	values[0] = reflect.ValueOf(params)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Property 2: Instance-scoped service filtering
// **Validates: Requirements 1.4, 1.10, 1.11**

// TestProperty_InstanceScopedServiceFiltering verifies that ListServicesByInstance
// correctly filters services based on InstanceID - empty for "local", exact match for others.
func TestProperty_InstanceScopedServiceFiltering(t *testing.T) {
	prop := func(params serviceFilterParams) bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		// Save all generated services
		for _, svc := range params.Services {
			if err := database.SaveService(svc); err != nil {
				t.Logf("failed to save service: %v", err)
				return false
			}
		}

		// Test 1: ListServicesByInstance("local") should return only services with empty InstanceID
		localServices, err := database.ListServicesByInstance("local")
		if err != nil {
			t.Logf("failed to list services for local: %v", err)
			return false
		}

		// Count expected local services (empty InstanceID)
		var expectedLocalCount int
		for _, svc := range params.Services {
			if svc.InstanceID == "" {
				expectedLocalCount++
			}
		}

		if len(localServices) != expectedLocalCount {
			t.Logf("local services count mismatch: got %d, want %d", len(localServices), expectedLocalCount)
			return false
		}

		// Verify all returned local services have empty InstanceID
		for _, svc := range localServices {
			if svc.InstanceID != "" {
				t.Logf("local service has non-empty InstanceID: %q", svc.InstanceID)
				return false
			}
		}

		// Test 2: For each unique non-empty InstanceID, verify ListServicesByInstance returns correct services
		uniqueInstanceIDs := params.UniqueInstanceIDs
		for _, instanceID := range uniqueInstanceIDs {
			services, err := database.ListServicesByInstance(instanceID)
			if err != nil {
				t.Logf("failed to list services for instance %q: %v", instanceID, err)
				return false
			}

			// Count expected services for this instanceID
			var expectedCount int
			for _, svc := range params.Services {
				if svc.InstanceID == instanceID {
					expectedCount++
				}
			}

			if len(services) != expectedCount {
				t.Logf("instance %q services count mismatch: got %d, want %d", instanceID, len(services), expectedCount)
				return false
			}

			// Verify all returned services have matching InstanceID
			for _, svc := range services {
				if svc.InstanceID != instanceID {
					t.Logf("instance %q service has wrong InstanceID: %q", instanceID, svc.InstanceID)
					return false
				}
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   serviceFilterGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 2 failed: %v", err)
	}
}

// serviceFilterParams holds generated parameters for service filtering test
type serviceFilterParams struct {
	Services         []Service
	UniqueInstanceIDs []string
}

// serviceFilterGenerator generates random sets of services with varying InstanceIDs
func serviceFilterGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate 3-10 services
	numServices := rng.Intn(8) + 3

	// Generate 1-3 unique instance IDs (plus empty string for local)
	numInstanceIDs := rng.Intn(3) + 1
	instanceIDs := make([]string, numInstanceIDs)
	for i := 0; i < numInstanceIDs; i++ {
		idLen := rng.Intn(15) + 10
		id := make([]byte, idLen)
		idChars := "abcdefghijklmnopqrstuvwxyz0123456789-"
		for j := range id {
			id[j] = idChars[rng.Intn(len(idChars))]
		}
		instanceIDs[i] = string(id)
	}

	services := make([]Service, numServices)
	for i := 0; i < numServices; i++ {
		// Determine InstanceID: some empty (local), some with specific instance IDs
		var instanceID string
		choice := rng.Intn(4)
		if choice == 0 {
			// 25% chance: empty (local)
			instanceID = ""
		} else {
			// 75% chance: one of the generated instance IDs
			instanceID = instanceIDs[rng.Intn(len(instanceIDs))]
		}

		// Generate service ID
		svcIDLen := rng.Intn(15) + 10
		svcID := make([]byte, svcIDLen)
		idChars := "abcdefghijklmnopqrstuvwxyz0123456789-"
		for j := range svcID {
			svcID[j] = idChars[rng.Intn(len(idChars))]
		}

		// Generate service name
		nameLen := rng.Intn(30) + 5
		name := make([]byte, nameLen)
		nameChars := "abcdefghijklmnopqrstuvwxyz0123456789 -_"
		for j := range name {
			name[j] = nameChars[rng.Intn(len(nameChars))]
		}

		// Generate service type
		types := []string{"web", "database", "cache", "queue", "worker", "api", "proxy"}
		serviceType := types[rng.Intn(len(types))]

		// Generate domain (sometimes)
		var domain string
		if rng.Intn(2) == 1 {
			domainLen := rng.Intn(20) + 5
			dom := make([]byte, domainLen)
			domChars := "abcdefghijklmnopqrstuvwxyz."
			for j := range dom {
				dom[j] = domChars[rng.Intn(len(domChars))]
			}
			domain = string(dom)
		}

		// Generate timestamps
		now := time.Now().Unix()
		createdAt := now - int64(rng.Intn(365*24*60*60))

		services[i] = Service{
			ID:         string(svcID),
			InstanceID: instanceID,
			Name:       string(name),
			Type:       serviceType,
			Domain:     domain,
			CreatedAt:  createdAt,
		}
	}

	params := serviceFilterParams{
		Services:          services,
		UniqueInstanceIDs: instanceIDs,
	}
	values[0] = reflect.ValueOf(params)
}
// Property 3: Credential scoping lookup order
// **Validates: Requirements 9.1, 9.2, 9.3, 9.5**

// TestProperty_CredentialScopingLookupOrder verifies that FindRegistryCredentialByDomainAndInstance
// correctly prioritizes instance-specific credentials over global ones, with case-insensitive matching.
func TestProperty_CredentialScopingLookupOrder(t *testing.T) {
	prop := func(params credentialScopingParams) bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		// Save both instance-specific and global credentials for the same domain
		if err := database.SaveRegistryCredential(params.InstanceCredential); err != nil {
			t.Logf("failed to save instance credential: %v", err)
			return false
		}
		if err := database.SaveRegistryCredential(params.GlobalCredential); err != nil {
			t.Logf("failed to save global credential: %v", err)
			return false
		}

		// Test 1: Lookup with matching instanceID should return instance-specific credential
		found, err := database.FindRegistryCredentialByDomainAndInstance(params.Domain, params.InstanceID)
		if err != nil {
			t.Logf("failed to find credential for instance: %v", err)
			return false
		}
		if found == nil {
			t.Logf("expected instance-specific credential, got nil")
			return false
		}
		if found.ID != params.InstanceCredential.ID {
			t.Logf("expected instance-specific credential %q, got %q", params.InstanceCredential.ID, found.ID)
			return false
		}

		// Test 2: Lookup with non-existent instanceID should return global credential
		nonExistentInstanceID := params.InstanceID + "-nonexistent"
		foundGlobal, err := database.FindRegistryCredentialByDomainAndInstance(params.Domain, nonExistentInstanceID)
		if err != nil {
			t.Logf("failed to find credential for non-existent instance: %v", err)
			return false
		}
		if foundGlobal == nil {
			t.Logf("expected global credential fallback, got nil")
			return false
		}
		if foundGlobal.ID != params.GlobalCredential.ID {
			t.Logf("expected global credential %q, got %q", params.GlobalCredential.ID, foundGlobal.ID)
			return false
		}

		// Test 3: Case-insensitive domain matching
		// Convert domain to different cases
		upperDomain := toUpperCase(params.Domain)
		lowerDomain := toLowerCase(params.Domain)
		mixedDomain := toMixedCase(params.Domain)

		// Test with upper case
		foundUpper, err := database.FindRegistryCredentialByDomainAndInstance(upperDomain, params.InstanceID)
		if err != nil {
			t.Logf("failed to find credential with upper case domain: %v", err)
			return false
		}
		if foundUpper == nil {
			t.Logf("expected credential with upper case domain, got nil")
			return false
		}

		// Test with lower case
		foundLower, err := database.FindRegistryCredentialByDomainAndInstance(lowerDomain, params.InstanceID)
		if err != nil {
			t.Logf("failed to find credential with lower case domain: %v", err)
			return false
		}
		if foundLower == nil {
			t.Logf("expected credential with lower case domain, got nil")
			return false
		}

		// Test with mixed case
		foundMixed, err := database.FindRegistryCredentialByDomainAndInstance(mixedDomain, params.InstanceID)
		if err != nil {
			t.Logf("failed to find credential with mixed case domain: %v", err)
			return false
		}
		if foundMixed == nil {
			t.Logf("expected credential with mixed case domain, got nil")
			return false
		}

		// All case variations should return instance-specific credential
		if foundUpper.ID != params.InstanceCredential.ID ||
			foundLower.ID != params.InstanceCredential.ID ||
			foundMixed.ID != params.InstanceCredential.ID {
			t.Logf("case-insensitive matching failed")
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
		Values:   credentialScopingGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 3 failed: %v", err)
	}
}

// credentialScopingParams holds generated parameters for credential scoping test
type credentialScopingParams struct {
	Domain            string
	InstanceID        string
	InstanceCredential RegistryCredential
	GlobalCredential  RegistryCredential
}

// credentialScopingGenerator generates random credentials for testing scoping
func credentialScopingGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate a valid domain
	domainLen := rng.Intn(15) + 5 // 5-19 chars
	domain := make([]byte, domainLen)
	domainChars := "abcdefghijklmnopqrstuvwxyz0123456789."
	for i := range domain {
		domain[i] = domainChars[rng.Intn(len(domainChars))]
	}
	// Ensure it looks like a domain (has at least one dot)
	if rng.Intn(3) > 0 {
		domain[domainLen-1] = '.'
		domain[domainLen-2] = "com"[rng.Intn(3)]
		domain[domainLen-3] = '.'
	}
	domainStr := string(domain)

	// Generate instance ID
	instanceIDLen := rng.Intn(15) + 10
	instanceID := make([]byte, instanceIDLen)
	idChars := "abcdefghijklmnopqrstuvwxyz0123456789-"
	for i := range instanceID {
		instanceID[i] = idChars[rng.Intn(len(idChars))]
	}
	instanceIDStr := string(instanceID)

	// Generate instance-specific credential ID
	instCredIDLen := rng.Intn(10) + 5
	instCredID := make([]byte, instCredIDLen)
	for i := range instCredID {
		instCredID[i] = idChars[rng.Intn(len(idChars))]
	}

	// Generate global credential ID (different from instance)
	globalCredIDLen := rng.Intn(10) + 5
	globalCredID := make([]byte, globalCredIDLen)
	for i := range globalCredID {
		globalCredID[i] = idChars[rng.Intn(len(idChars))]
	}

	// Generate usernames
	usernameLen := rng.Intn(15) + 5
	username := make([]byte, usernameLen)
	usernameChars := "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := range username {
		username[i] = usernameChars[rng.Intn(len(usernameChars))]
	}
	usernameStr := string(username)

	// Different username for global
	globalUsernameLen := rng.Intn(15) + 5
	globalUsername := make([]byte, globalUsernameLen)
	for i := range globalUsername {
		globalUsername[i] = usernameChars[rng.Intn(len(usernameChars))]
	}
	globalUsernameStr := string(globalUsername)

	// Generate timestamps - instance credential is more recent
	now := time.Now().Unix()
	globalUpdatedAt := now - int64(rng.Intn(86400*30)) // up to 30 days ago
	instanceUpdatedAt := globalUpdatedAt + int64(rng.Intn(86400)) // more recent

	instanceCred := RegistryCredential{
		ID:             string(instCredID),
		InstanceID:     instanceIDStr,
		Registry:       domainStr,
		Username:       usernameStr,
		EncryptedToken: []byte("instance-token-" + string(instCredID)),
		CreatedAt:      instanceUpdatedAt - 86400,
		UpdatedAt:      instanceUpdatedAt,
	}

	globalCred := RegistryCredential{
		ID:             string(globalCredID),
		InstanceID:     "", // empty = global
		Registry:       domainStr,
		Username:       globalUsernameStr,
		EncryptedToken: []byte("global-token-" + string(globalCredID)),
		CreatedAt:      globalUpdatedAt - 86400,
		UpdatedAt:      globalUpdatedAt,
	}

	params := credentialScopingParams{
		Domain:            domainStr,
		InstanceID:        instanceIDStr,
		InstanceCredential: instanceCred,
		GlobalCredential:  globalCred,
	}
	values[0] = reflect.ValueOf(params)
}

// Helper functions for case transformations
func toUpperCase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			result[i] = c - 32
		} else if c >= '0' && c <= '9' || c == '.' {
			result[i] = c
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func toLowerCase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func toMixedCase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			if i%2 == 0 {
				result[i] = c - 32 // uppercase
			} else {
				result[i] = c // lowercase
			}
		} else if c >= 'A' && c <= 'Z' {
			if i%2 == 0 {
				result[i] = c + 32 // lowercase
			} else {
				result[i] = c // uppercase
			}
		} else {
			result[i] = c
		}
	}
	return string(result)
}
// Property 12: Local instance deletion protection
// **Validates: Requirements 1.9, 4.9**

// TestProperty_LocalInstanceDeletionProtection verifies that attempting to delete
// the "local" instance always returns an error and leaves the record unchanged.
func TestProperty_LocalInstanceDeletionProtection(t *testing.T) {
	prop := func() bool {
		// Create a temp BBolt database
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		database, err := New(dbPath)
		if err != nil {
			t.Logf("failed to create db: %v", err)
			return false
		}
		defer database.Close()
		defer os.Remove(dbPath)

		// Step 1: Ensure the local instance exists
		if err := database.EnsureLocalInstance(); err != nil {
			t.Logf("failed to ensure local instance: %v", err)
			return false
		}

		// Get the local instance to capture its initial state
		initialInstance, err := database.GetInstance("local")
		if err != nil {
			t.Logf("failed to get local instance before deletion: %v", err)
			return false
		}

		// Step 2: Attempt to delete the local instance - MUST return an error
		deleteErr := database.DeleteInstance("local")
		if deleteErr == nil {
			t.Logf("expected error when deleting local instance, got nil")
			return false
		}

		// Verify the error message indicates local instance cannot be deleted
		expectedErrMsg := "cannot delete the local instance"
		if deleteErr.Error() != expectedErrMsg {
			t.Logf("unexpected error message: got %q, want %q", deleteErr.Error(), expectedErrMsg)
			return false
		}

		// Step 3: Verify the local instance record still exists and is unchanged
		afterInstance, err := database.GetInstance("local")
		if err != nil {
			t.Logf("local instance was deleted when it should not be: %v", err)
			return false
		}

		// Verify all fields are unchanged
		if afterInstance.ID != initialInstance.ID {
			t.Logf("ID changed after failed deletion: got %q, want %q", afterInstance.ID, initialInstance.ID)
			return false
		}
		if afterInstance.Name != initialInstance.Name {
			t.Logf("Name changed after failed deletion: got %q, want %q", afterInstance.Name, initialInstance.Name)
			return false
		}
		if afterInstance.Mode != initialInstance.Mode {
			t.Logf("Mode changed after failed deletion: got %q, want %q", afterInstance.Mode, initialInstance.Mode)
			return false
		}
		if afterInstance.Status != initialInstance.Status {
			t.Logf("Status changed after failed deletion: got %q, want %q", afterInstance.Status, initialInstance.Status)
			return false
		}
		if afterInstance.CreatedAt != initialInstance.CreatedAt {
			t.Logf("CreatedAt changed after failed deletion: got %d, want %d", afterInstance.CreatedAt, initialInstance.CreatedAt)
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 100,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 12 failed: %v", err)
	}
}