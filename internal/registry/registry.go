package registry

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/sdldev/dockpal/internal/db"
)

// ErrCredentialNotFound is returned when a registry credential cannot be found.
var ErrCredentialNotFound = errors.New("credential not found")

// Manager handles registry credential operations.
type Manager struct {
	db        *db.DB
	cryptoKey []byte
}

// CreateRequest represents the input for creating a new registry credential.
type CreateRequest struct {
	Registry string `json:"registry" binding:"required,max=253"`
	Username string `json:"username" binding:"required,max=100"`
	Token    string `json:"token" binding:"required,max=255"`
}

// UpdateRequest represents the input for updating an existing credential.
type UpdateRequest struct {
	Username string `json:"username,omitempty"`
	Token    string `json:"token,omitempty"`
}

// CredentialSummary is returned in list/get responses with the token masked.
type CredentialSummary struct {
	ID              string `json:"id"`
	Registry        string `json:"registry"`
	Username        string `json:"username"`
	MaskedToken     string `json:"masked_token"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	LastValidatedAt int64  `json:"last_validated_at"`
}

// TestResult represents the outcome of a connection test.
type TestResult struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	ValidatedAt int64  `json:"validated_at,omitempty"`
}

// DockerAuthConfig is the JSON structure for Docker registry auth headers.
type DockerAuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var registryHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
}

// NewManager creates a registry manager with an encryption key derived from the JWT secret.
func NewManager(database *db.DB, jwtSecret string) *Manager {
	key, err := DeriveKey(jwtSecret)
	if err != nil {
		// Key derivation failed; manager will reject all crypto operations.
		return &Manager{db: database, cryptoKey: nil}
	}
	return &Manager{db: database, cryptoKey: key}
}

// ValidatePAT validates a GitHub Personal Access Token format.
// Supports classic tokens (ghp_ + 36 alphanumeric = 40 total) and
// fine-grained tokens (github_pat_ prefix, 20+ chars total).
func ValidatePAT(token string) error {
	if strings.HasPrefix(token, "ghp_") {
		if len(token) != 40 {
			return fmt.Errorf("classic PAT must be 40 characters (ghp_ + 36)")
		}
		if !isAlphanumeric(token[4:]) {
			return fmt.Errorf("PAT must contain only alphanumeric characters after prefix")
		}
		return nil
	}
	if strings.HasPrefix(token, "github_pat_") {
		if len(token) < 20 {
			return fmt.Errorf("fine-grained PAT too short")
		}
		return nil
	}
	return fmt.Errorf("token must start with 'ghp_' or 'github_pat_'")
}

// ExtractDomain returns the registry domain from an image reference.
// Returns empty string for Docker Hub images (no domain or domain without a dot).
func ExtractDomain(imageRef string) string {
	parts := strings.SplitN(imageRef, "/", 2)
	if len(parts) < 2 {
		return "" // no domain, e.g., "nginx:latest"
	}
	if strings.Contains(parts[0], ".") {
		return strings.ToLower(parts[0])
	}
	return "" // e.g., "library/nginx" — Docker Hub
}

// MaskToken returns a masked version of the token showing only the last 4 characters.
func MaskToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}

// Create stores a new registry credential after validation.
// If a credential with the same registry (case-insensitive) already exists, it updates it.
func (m *Manager) Create(req CreateRequest) (*CredentialSummary, error) {
	if m.cryptoKey == nil {
		return nil, fmt.Errorf("encryption configuration error")
	}

	// Field validation
	if strings.TrimSpace(req.Registry) == "" {
		return nil, fmt.Errorf("registry is required")
	}
	if len(req.Registry) > 253 {
		return nil, fmt.Errorf("registry must be at most 253 characters")
	}
	if strings.TrimSpace(req.Username) == "" {
		return nil, fmt.Errorf("username is required")
	}
	if len(req.Username) > 100 {
		return nil, fmt.Errorf("username must be at most 100 characters")
	}
	if strings.TrimSpace(req.Token) == "" {
		return nil, fmt.Errorf("token is required")
	}
	if len(req.Token) > 255 {
		return nil, fmt.Errorf("token must be at most 255 characters")
	}

	// PAT format validation
	if err := ValidatePAT(req.Token); err != nil {
		return nil, fmt.Errorf("invalid token format: %s", err.Error())
	}

	// Encrypt token
	encrypted, err := Encrypt([]byte(req.Token), m.cryptoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt token")
	}

	now := time.Now().Unix()

	// Check for duplicate (case-insensitive registry name)
	existing, _ := m.db.FindRegistryCredentialByDomain(req.Registry)
	if existing != nil {
		// Update existing credential
		existing.Username = req.Username
		existing.EncryptedToken = encrypted
		existing.UpdatedAt = now
		if err := m.db.SaveRegistryCredential(*existing); err != nil {
			return nil, fmt.Errorf("failed to update credential")
		}
		return &CredentialSummary{
			ID:              existing.ID,
			Registry:        existing.Registry,
			Username:        existing.Username,
			MaskedToken:     MaskToken(req.Token),
			Status:          credentialStatus(existing.LastValidatedAt),
			CreatedAt:       existing.CreatedAt,
			UpdatedAt:       existing.UpdatedAt,
			LastValidatedAt: existing.LastValidatedAt,
		}, nil
	}

	// Create new credential
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	id := fmt.Sprintf("reg-%x", b)
	cred := db.RegistryCredential{
		ID:             id,
		Registry:       req.Registry,
		Username:       req.Username,
		EncryptedToken: encrypted,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := m.db.SaveRegistryCredential(cred); err != nil {
		return nil, fmt.Errorf("failed to store credential")
	}

	return &CredentialSummary{
		ID:          id,
		Registry:    req.Registry,
		Username:    req.Username,
		MaskedToken: MaskToken(req.Token),
		Status:      "unknown",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// List returns all credentials with masked tokens and status.
func (m *Manager) List() ([]CredentialSummary, error) {
	if m.cryptoKey == nil {
		return nil, fmt.Errorf("encryption configuration error")
	}

	creds, err := m.db.ListRegistryCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to list credentials")
	}

	summaries := make([]CredentialSummary, 0, len(creds))
	for _, cred := range creds {
		// Decrypt to get masked token
		token, err := Decrypt(cred.EncryptedToken, m.cryptoKey)
		if err != nil {
			// If decryption fails, show placeholder
			summaries = append(summaries, CredentialSummary{
				ID:              cred.ID,
				Registry:        cred.Registry,
				Username:        cred.Username,
				MaskedToken:     "****",
				Status:          credentialStatus(cred.LastValidatedAt),
				CreatedAt:       cred.CreatedAt,
				UpdatedAt:       cred.UpdatedAt,
				LastValidatedAt: cred.LastValidatedAt,
			})
			continue
		}
		maskedToken := MaskToken(string(token))
		zeroBytes(token)

		summaries = append(summaries, CredentialSummary{
			ID:              cred.ID,
			Registry:        cred.Registry,
			Username:        cred.Username,
			MaskedToken:     maskedToken,
			Status:          credentialStatus(cred.LastValidatedAt),
			CreatedAt:       cred.CreatedAt,
			UpdatedAt:       cred.UpdatedAt,
			LastValidatedAt: cred.LastValidatedAt,
		})
	}

	return summaries, nil
}

// Get returns a single credential by ID with masked token.
func (m *Manager) Get(id string) (*CredentialSummary, error) {
	if m.cryptoKey == nil {
		return nil, fmt.Errorf("encryption configuration error")
	}

	cred, err := m.db.GetRegistryCredential(id)
	if err != nil {
		return nil, ErrCredentialNotFound
	}

	token, err := Decrypt(cred.EncryptedToken, m.cryptoKey)
	if err != nil {
		return nil, fmt.Errorf("stored credential cannot be decrypted, please re-save")
	}
	defer zeroBytes(token)

	return &CredentialSummary{
		ID:              cred.ID,
		Registry:        cred.Registry,
		Username:        cred.Username,
		MaskedToken:     MaskToken(string(token)),
		Status:          credentialStatus(cred.LastValidatedAt),
		CreatedAt:       cred.CreatedAt,
		UpdatedAt:       cred.UpdatedAt,
		LastValidatedAt: cred.LastValidatedAt,
	}, nil
}

// Update modifies the token and/or username for an existing credential.
func (m *Manager) Update(id string, req UpdateRequest) error {
	if m.cryptoKey == nil {
		return fmt.Errorf("encryption configuration error")
	}

	cred, err := m.db.GetRegistryCredential(id)
	if err != nil {
		return ErrCredentialNotFound
	}

	if req.Username != "" {
		if len(req.Username) > 100 {
			return fmt.Errorf("username must be at most 100 characters")
		}
		cred.Username = req.Username
	}

	if req.Token != "" {
		if len(req.Token) > 255 {
			return fmt.Errorf("token must be at most 255 characters")
		}
		if err := ValidatePAT(req.Token); err != nil {
			return fmt.Errorf("invalid token format: %s", err.Error())
		}
		encrypted, err := Encrypt([]byte(req.Token), m.cryptoKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt token")
		}
		cred.EncryptedToken = encrypted
	}

	cred.UpdatedAt = time.Now().Unix()

	if err := m.db.SaveRegistryCredential(*cred); err != nil {
		return fmt.Errorf("failed to update credential")
	}

	return nil
}

// Delete permanently removes a credential from the store.
func (m *Manager) Delete(id string) error {
	// Verify it exists first
	_, err := m.db.GetRegistryCredential(id)
	if err != nil {
		return ErrCredentialNotFound
	}

	if err := m.db.DeleteRegistryCredential(id); err != nil {
		return fmt.Errorf("failed to delete credential")
	}

	return nil
}

// TestConnection validates credentials against the registry's /v2/ endpoint.
func (m *Manager) TestConnection(id string) (*TestResult, error) {
	if m.cryptoKey == nil {
		return nil, fmt.Errorf("encryption configuration error")
	}

	cred, err := m.db.GetRegistryCredential(id)
	if err != nil {
		return nil, ErrCredentialNotFound
	}

	token, err := Decrypt(cred.EncryptedToken, m.cryptoKey)
	if err != nil {
		return &TestResult{Status: "error", Message: "failed to decrypt token"}, nil
	}
	defer zeroBytes(token)

	// Test against registry v2 API
	url := fmt.Sprintf("https://%s/v2/", cred.Registry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &TestResult{Status: "error", Message: "failed to create request"}, nil
	}
	req.SetBasicAuth(cred.Username, string(token))

	resp, err := registryHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return &TestResult{Status: "error", Message: "connection timeout"}, nil
		}
		return &TestResult{Status: "error", Message: "network error: " + err.Error()}, nil
	}
	defer resp.Body.Close()

	now := time.Now().Unix()
	switch {
	case resp.StatusCode == 200:
		// Update last validated timestamp
		cred.LastValidatedAt = now
		m.db.SaveRegistryCredential(*cred)
		return &TestResult{Status: "valid", Message: "connection successful", ValidatedAt: now}, nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return &TestResult{Status: "invalid", Message: "credentials rejected by registry"}, nil
	default:
		return &TestResult{Status: "error", Message: fmt.Sprintf("registry returned %d", resp.StatusCode)}, nil
	}
}

// registryAliases maps registry domains to their credential fallback domains.
// ghcr.io and github.com share the same GitHub PAT authentication.
var registryAliases = map[string]string{
	"ghcr.io": "github.com",
}

// GetAuthHeader returns the base64-encoded Docker auth header for a given image reference.
// Returns empty string if no matching credentials found.
func (m *Manager) GetAuthHeader(imageRef string) (string, error) {
	if m.cryptoKey == nil {
		return "", fmt.Errorf("encryption configuration error")
	}

	domain := ExtractDomain(imageRef)
	if domain == "" {
		return "", nil // no auth needed for Docker Hub public
	}

	cred, err := m.db.FindRegistryCredentialByDomain(domain)
	if err != nil || cred == nil {
		// Try alias fallback (e.g., ghcr.io → github.com)
		if alias, ok := registryAliases[domain]; ok {
			cred, err = m.db.FindRegistryCredentialByDomain(alias)
		}
	}
	if err != nil || cred == nil {
		return "", nil // no credentials stored, pull without auth
	}

	// Decrypt token
	token, err := Decrypt(cred.EncryptedToken, m.cryptoKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credentials for %s", domain)
	}
	defer zeroBytes(token)

	// Build Docker auth config
	authConfig := DockerAuthConfig{
		Username: cred.Username,
		Password: string(token),
	}
	jsonBytes, err := json.Marshal(authConfig)
	if err != nil {
		return "", fmt.Errorf("failed to build auth header")
	}
	return base64.URLEncoding.EncodeToString(jsonBytes), nil
}

// GetTokenForDomain retrieves the decrypted plaintext token for a given registry domain.
// Used for Git clone authentication when a github.com credential is stored.
func (m *Manager) GetTokenForDomain(domain string) (string, error) {
	if m.cryptoKey == nil {
		return "", fmt.Errorf("encryption configuration error")
	}

	cred, err := m.db.FindRegistryCredentialByDomain(domain)
	if err != nil || cred == nil {
		return "", nil
	}

	token, err := Decrypt(cred.EncryptedToken, m.cryptoKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credentials for %s", domain)
	}
	defer zeroBytes(token)

	return string(token), nil
}

// isAlphanumeric checks if a string contains only alphanumeric characters.
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// credentialStatus returns the status string based on the last validated timestamp.
func credentialStatus(lastValidatedAt int64) string {
	if lastValidatedAt == 0 {
		return "unknown"
	}
	// Consider valid if validated within the last 7 days
	if time.Now().Unix()-lastValidatedAt < 7*24*60*60 {
		return "valid"
	}
	return "unknown"
}
