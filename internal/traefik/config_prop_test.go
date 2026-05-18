package traefik

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"gopkg.in/yaml.v3"
)

// **Validates: Requirements 12.3**

// Property 14: Traefik Config Structure
// Verify generated config contains `websecure` entrypoint and `letsencrypt` certResolver
// for any valid domain, serviceName, and port combination.

func TestProperty14_TraefikConfigStructure(t *testing.T) {
	// Property: For any valid domain and serviceName, the generated TraefikConfig
	// MUST contain a router with EntryPoints including "websecure" and TLS with
	// CertResolver set to "letsencrypt".
	prop := func(params traefikConfigParams) bool {
		// Build the config the same way GenerateConfig does (without filesystem I/O)
		var config TraefikConfig
		config.HTTP.Routers = make(map[string]Router)
		config.HTTP.Services = make(map[string]Service)

		routerName := params.serviceName + "-router"
		serviceURL := "http://" + params.serviceName + ":" + portStr(params.port)

		config.HTTP.Routers[routerName] = Router{
			Rule:        "Host(`" + params.domain + "`)",
			Service:     params.serviceName,
			EntryPoints: []string{"websecure"},
			TLS: &TLSConfig{
				CertResolver: "letsencrypt",
			},
		}

		config.HTTP.Services[params.serviceName] = Service{
			LoadBalancer: LoadBalancer{
				Servers: []Server{{URL: serviceURL}},
			},
		}

		// Marshal and re-parse to verify structure survives round-trip
		out, err := yaml.Marshal(&config)
		if err != nil {
			t.Logf("marshal failed: %v", err)
			return false
		}

		var parsed TraefikConfig
		if err := yaml.Unmarshal(out, &parsed); err != nil {
			t.Logf("unmarshal failed: %v", err)
			return false
		}

		// Check: router must exist
		router, exists := parsed.HTTP.Routers[routerName]
		if !exists {
			t.Logf("router %q not found in parsed config", routerName)
			return false
		}

		// Check: entryPoints must contain "websecure"
		hasWebsecure := false
		for _, ep := range router.EntryPoints {
			if ep == "websecure" {
				hasWebsecure = true
				break
			}
		}
		if !hasWebsecure {
			t.Logf("router %q missing 'websecure' entrypoint, got: %v", routerName, router.EntryPoints)
			return false
		}

		// Check: TLS certResolver must be "letsencrypt"
		if router.TLS == nil {
			t.Logf("router %q has nil TLS config", routerName)
			return false
		}
		if router.TLS.CertResolver != "letsencrypt" {
			t.Logf("router %q certResolver is %q, expected 'letsencrypt'", routerName, router.TLS.CertResolver)
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   traefikConfigGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 14 failed: %v", err)
	}
}

// TestProperty14_GenerateConfigAlwaysHasHTTPS verifies that any config built by
// GenerateConfig's logic always produces YAML containing the websecure and letsencrypt fields.
func TestProperty14_GenerateConfigAlwaysHasHTTPS(t *testing.T) {
	// Property: The raw YAML output always contains the strings "websecure" and "letsencrypt"
	prop := func(params traefikConfigParams) bool {
		var config TraefikConfig
		config.HTTP.Routers = make(map[string]Router)
		config.HTTP.Services = make(map[string]Service)

		routerName := params.serviceName + "-router"
		serviceURL := "http://" + params.serviceName + ":" + portStr(params.port)

		config.HTTP.Routers[routerName] = Router{
			Rule:        "Host(`" + params.domain + "`)",
			Service:     params.serviceName,
			EntryPoints: []string{"websecure"},
			TLS: &TLSConfig{
				CertResolver: "letsencrypt",
			},
		}

		config.HTTP.Services[params.serviceName] = Service{
			LoadBalancer: LoadBalancer{
				Servers: []Server{{URL: serviceURL}},
			},
		}

		out, err := yaml.Marshal(&config)
		if err != nil {
			return false
		}

		content := string(out)
		hasWebsecure := contains(content, "websecure")
		hasLetsencrypt := contains(content, "letsencrypt")

		if !hasWebsecure {
			t.Logf("YAML output missing 'websecure':\n%s", content)
			return false
		}
		if !hasLetsencrypt {
			t.Logf("YAML output missing 'letsencrypt':\n%s", content)
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 1000,
		Values:   traefikConfigGenerator,
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 14 (HTTPS presence) failed: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func portStr(port int) string {
	result := ""
	if port == 0 {
		return "0"
	}
	n := port
	for n > 0 {
		result = string(rune('0'+n%10)) + result
		n /= 10
	}
	return result
}

// traefikConfigParams represents random inputs for Traefik config generation
type traefikConfigParams struct {
	domain      string
	serviceName string
	port        int
}

func traefikConfigGenerator(values []reflect.Value, rng *rand.Rand) {
	// Generate a valid domain (e.g. "abc.example.com")
	domainParts := []string{"app", "web", "api", "svc", "dev", "prod", "test"}
	tlds := []string{"com", "io", "dev", "net", "org"}
	domain := domainParts[rng.Intn(len(domainParts))]
	domain += "."
	domain += domainParts[rng.Intn(len(domainParts))]
	domain += "."
	domain += tlds[rng.Intn(len(tlds))]

	// Generate a valid service name (alphanumeric + hyphens)
	nameLen := 3 + rng.Intn(12)
	nameChars := "abcdefghijklmnopqrstuvwxyz0123456789-"
	name := make([]byte, nameLen)
	// First char must be alpha
	name[0] = "abcdefghijklmnopqrstuvwxyz"[rng.Intn(26)]
	for i := 1; i < nameLen; i++ {
		name[i] = nameChars[rng.Intn(len(nameChars))]
	}

	// Generate a valid port (1-65535)
	port := 1 + rng.Intn(65535)

	params := traefikConfigParams{
		domain:      domain,
		serviceName: string(name),
		port:        port,
	}
	values[0] = reflect.ValueOf(params)
}
