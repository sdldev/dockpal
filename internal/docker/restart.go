package docker

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultRestartPolicy is applied to a service whose restart policy is empty,
// unknown, or (when auto-start is enforced) set to a policy that does not
// survive a host reboot.
const DefaultRestartPolicy = "unless-stopped"

// validRestartPolicies is the set of Docker restart policy modes DockPal accepts.
// "on-failure" may carry a ":N" max-retry suffix.
var validRestartPolicies = map[string]bool{
	"no":             true,
	"always":         true,
	"unless-stopped": true,
	"on-failure":     true,
}

// isRebootSafePolicy reports whether a container with this policy comes back up
// after the Docker daemon restarts (e.g. host reboot).
func isRebootSafePolicy(policy string) bool {
	return policy == "always" || policy == "unless-stopped"
}

// NormalizeRestartPolicy resolves the effective restart policy for a service.
//
// Empty or unknown values become DefaultRestartPolicy. When forceAutoStart is
// true, the reboot-unsafe policies "no" and "on-failure" are upgraded to
// DefaultRestartPolicy so the app starts again after a host reboot. When
// forceAutoStart is false, an explicit "no"/"on-failure" is preserved.
func NormalizeRestartPolicy(raw string, forceAutoStart bool) string {
	policy := strings.ToLower(strings.TrimSpace(raw))
	if policy == "" {
		return DefaultRestartPolicy
	}

	// Treat "on-failure:3" and friends as "on-failure" for classification.
	base := policy
	if idx := strings.IndexByte(base, ':'); idx >= 0 {
		base = base[:idx]
	}
	if !validRestartPolicies[base] {
		return DefaultRestartPolicy
	}

	if forceAutoStart && !isRebootSafePolicy(base) {
		return DefaultRestartPolicy
	}
	return policy
}

// EnsureComposeAutoStart rewrites a docker-compose YAML so every service carries
// a restart policy that survives a host reboot.
//
// If override is a non-empty valid policy it is applied verbatim to every
// service (the user's explicit choice). Otherwise each service's existing
// restart value is passed through NormalizeRestartPolicy with forceAutoStart.
//
// The rewrite walks the YAML node tree so unrelated keys, ordering, and
// comments are preserved; only the `restart` key per service is set or inserted.
func EnsureComposeAutoStart(composeYAML, override string, forceAutoStart bool) (string, error) {
	desiredOverride := ""
	if trimmed := strings.ToLower(strings.TrimSpace(override)); trimmed != "" {
		base := trimmed
		if idx := strings.IndexByte(base, ':'); idx >= 0 {
			base = base[:idx]
		}
		if validRestartPolicies[base] {
			desiredOverride = trimmed
		}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return "", fmt.Errorf("invalid compose YAML: %w", err)
	}
	if len(doc.Content) == 0 {
		return "", fmt.Errorf("empty compose YAML")
	}

	root := doc.Content[0]
	servicesNode := mappingValue(root, "services")
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
		return "", fmt.Errorf("no services defined in compose file")
	}

	// services is a mapping of name -> service definition (every odd index).
	for i := 1; i < len(servicesNode.Content); i += 2 {
		svc := servicesNode.Content[i]
		if svc.Kind != yaml.MappingNode {
			continue
		}

		existing := mappingValue(svc, "restart")
		current := ""
		if existing != nil {
			current = existing.Value
		}

		desired := desiredOverride
		if desired == "" {
			desired = NormalizeRestartPolicy(current, forceAutoStart)
		}

		if existing != nil {
			existing.Value = desired
			existing.Tag = "!!str"
			existing.Style = 0
			continue
		}
		svc.Content = append(svc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "restart"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: desired},
		)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("failed to re-marshal compose YAML: %w", err)
	}
	return string(out), nil
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
