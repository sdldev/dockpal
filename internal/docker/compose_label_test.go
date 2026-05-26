package docker

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// labelsOf parses the round-tripped YAML and returns the labels map for
// a given service, regardless of mapping or sequence form. Returns nil
// when the service has no labels block at all.
func labelsOf(t *testing.T, composeYAML, service string) map[string]string {
	t.Helper()
	cf, err := ParseComposeFile(composeYAML)
	if err != nil {
		t.Fatalf("output YAML is not parseable: %v\n---\n%s", err, composeYAML)
	}
	svc, ok := cf.Services[service]
	if !ok {
		t.Fatalf("service %q not found in output\n---\n%s", service, composeYAML)
	}
	if svc.Labels == nil {
		return nil
	}
	return parseLabels(svc.Labels)
}

// hasLabelsKey returns true if the given service node in the YAML has a
// `labels:` key (regardless of value). Used to verify the labels block
// was dropped when the last entry was removed.
func hasLabelsKey(t *testing.T, composeYAML, service string) bool {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &root); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if len(root.Content) == 0 {
		return false
	}
	rootMap := root.Content[0]
	services := findYAMLMapValue(rootMap, "services")
	if services == nil {
		return false
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		if services.Content[i].Value != service {
			continue
		}
		svc := services.Content[i+1]
		return findYAMLMapValue(svc, "labels") != nil
	}
	return false
}

func TestSetServiceLabel_TableDriven(t *testing.T) {
	type want struct {
		labels      map[string]string // expected labels for the service after round-trip; nil means no labels block
		hasLabelsKV bool              // whether `labels:` key should still exist (for sequence drop case)
	}

	tests := []struct {
		name    string
		input   string
		key     string
		value   string
		service string // which service to inspect
		wantErr bool
		want    want
	}{
		{
			name: "add new label to mapping form with existing labels",
			input: `services:
  web:
    image: nginx:latest
    labels:
      existing: "kept"
      another: "also-kept"
`,
			key:     "dockpal.auto-update",
			value:   "true",
			service: "web",
			want: want{
				labels: map[string]string{
					"existing":            "kept",
					"another":             "also-kept",
					"dockpal.auto-update": "true",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "replace existing label in mapping form preserving others",
			input: `services:
  web:
    image: nginx:latest
    labels:
      dockpal.auto-update: "false"
      existing: "kept"
`,
			key:     "dockpal.auto-update",
			value:   "true",
			service: "web",
			want: want{
				labels: map[string]string{
					"dockpal.auto-update": "true",
					"existing":            "kept",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "remove label from mapping form preserving others",
			input: `services:
  web:
    image: nginx:latest
    labels:
      dockpal.auto-update: "true"
      keep-me: "yes"
`,
			key:     "dockpal.auto-update",
			value:   "",
			service: "web",
			want: want{
				labels: map[string]string{
					"keep-me": "yes",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "remove only label from mapping form drops labels block",
			input: `services:
  web:
    image: nginx:latest
    labels:
      dockpal.auto-update: "true"
`,
			key:     "dockpal.auto-update",
			value:   "",
			service: "web",
			want: want{
				labels:      nil,
				hasLabelsKV: false,
			},
		},
		{
			name: "add label to service with no labels block creates mapping",
			input: `services:
  web:
    image: nginx:latest
`,
			key:     "dockpal.auto-update",
			value:   "true",
			service: "web",
			want: want{
				labels: map[string]string{
					"dockpal.auto-update": "true",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "remove label on service with no labels block is a no-op",
			input: `services:
  web:
    image: nginx:latest
`,
			key:     "dockpal.auto-update",
			value:   "",
			service: "web",
			want: want{
				labels:      nil,
				hasLabelsKV: false,
			},
		},
		{
			name: "sequence form: add new label",
			input: `services:
  web:
    image: nginx:latest
    labels:
      - "existing=kept"
      - "another=also-kept"
`,
			key:     "dockpal.auto-update",
			value:   "true",
			service: "web",
			want: want{
				labels: map[string]string{
					"existing":            "kept",
					"another":             "also-kept",
					"dockpal.auto-update": "true",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "sequence form: replace existing label",
			input: `services:
  web:
    image: nginx:latest
    labels:
      - "dockpal.auto-update=false"
      - "existing=kept"
`,
			key:     "dockpal.auto-update",
			value:   "true",
			service: "web",
			want: want{
				labels: map[string]string{
					"dockpal.auto-update": "true",
					"existing":            "kept",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "sequence form: remove label preserving others",
			input: `services:
  web:
    image: nginx:latest
    labels:
      - "dockpal.auto-update=true"
      - "keep-me=yes"
`,
			key:     "dockpal.auto-update",
			value:   "",
			service: "web",
			want: want{
				labels: map[string]string{
					"keep-me": "yes",
				},
				hasLabelsKV: true,
			},
		},
		{
			name: "sequence form: removing last entry drops labels block",
			input: `services:
  web:
    image: nginx:latest
    labels:
      - "dockpal.auto-update=true"
`,
			key:     "dockpal.auto-update",
			value:   "",
			service: "web",
			want: want{
				labels:      nil,
				hasLabelsKV: false,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := SetServiceLabel(tc.input, tc.key, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil. output:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := labelsOf(t, out, tc.service)
			if len(got) != len(tc.want.labels) {
				t.Errorf("label count mismatch: got %v, want %v\n---\n%s", got, tc.want.labels, out)
			}
			for k, v := range tc.want.labels {
				gv, ok := got[k]
				if !ok {
					t.Errorf("missing label %q in output. got=%v\n---\n%s", k, got, out)
					continue
				}
				if gv != v {
					t.Errorf("label %q: got %q, want %q\n---\n%s", k, gv, v, out)
				}
			}

			if hk := hasLabelsKey(t, out, tc.service); hk != tc.want.hasLabelsKV {
				t.Errorf("hasLabelsKey mismatch: got %v, want %v\n---\n%s", hk, tc.want.hasLabelsKV, out)
			}
		})
	}
}

// TestSetServiceLabel_AppliesToAllServices verifies the function rewrites
// the label on every service in the compose, not just the first one.
func TestSetServiceLabel_AppliesToAllServices(t *testing.T) {
	in := `services:
  web:
    image: nginx:latest
    labels:
      existing: "kept"
  api:
    image: api:1.0
  db:
    image: postgres:15
    labels:
      - "existing=kept"
`
	out, err := SetServiceLabel(in, "dockpal.auto-update", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, svc := range []string{"web", "api", "db"} {
		labels := labelsOf(t, out, svc)
		if got := labels["dockpal.auto-update"]; got != "true" {
			t.Errorf("service %s: expected dockpal.auto-update=true, got %q (all=%v)\n---\n%s",
				svc, got, labels, out)
		}
	}

	// Existing labels should be preserved on services that had them.
	webLabels := labelsOf(t, out, "web")
	if webLabels["existing"] != "kept" {
		t.Errorf("web: expected existing=kept, got %q", webLabels["existing"])
	}
	dbLabels := labelsOf(t, out, "db")
	if dbLabels["existing"] != "kept" {
		t.Errorf("db: expected existing=kept, got %q", dbLabels["existing"])
	}
}

// TestSetServiceLabel_RemoveAppliesToAllServices verifies the function
// removes the label on every service that had it.
func TestSetServiceLabel_RemoveAppliesToAllServices(t *testing.T) {
	in := `services:
  web:
    image: nginx:latest
    labels:
      dockpal.auto-update: "true"
      keep: "yes"
  api:
    image: api:1.0
    labels:
      - "dockpal.auto-update=true"
      - "keep=yes"
`
	out, err := SetServiceLabel(in, "dockpal.auto-update", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, svc := range []string{"web", "api"} {
		labels := labelsOf(t, out, svc)
		if _, has := labels["dockpal.auto-update"]; has {
			t.Errorf("service %s: dockpal.auto-update was not removed (labels=%v)\n---\n%s",
				svc, labels, out)
		}
		if labels["keep"] != "yes" {
			t.Errorf("service %s: keep=yes was not preserved (labels=%v)", svc, labels)
		}
	}
}

// TestSetServiceLabel_PreservesCommentsLenient performs a round-trip
// through SetServiceLabel and asserts the YAML round-trips through the
// *yaml.Node API while keeping the structural payload intact. Comments
// inside mapping values that yaml.v3 places on adjacent nodes should
// survive; the assertion is lenient because yaml.v3 may relocate or
// strip head/foot comments outside mappings.
func TestSetServiceLabel_PreservesCommentsLenient(t *testing.T) {
	in := `# top-level head comment
services:
  web:
    image: nginx:latest # inline image comment
    # comment above labels
    labels:
      existing: "kept" # inline label comment
`
	out, err := SetServiceLabel(in, "dockpal.auto-update", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The label edit should have happened and the existing label kept.
	labels := labelsOf(t, out, "web")
	if labels["existing"] != "kept" {
		t.Errorf("existing label was lost: %v\n---\n%s", labels, out)
	}
	if labels["dockpal.auto-update"] != "true" {
		t.Errorf("new label not added: %v\n---\n%s", labels, out)
	}

	// At least one of the comments inside the services map should
	// survive the round-trip. yaml.v3 attaches comments to the node
	// they precede or are inline with, so at least the inline image
	// comment or the inline label comment is expected to remain.
	hasInlineImage := strings.Contains(out, "inline image comment")
	hasInlineLabel := strings.Contains(out, "inline label comment")
	hasAboveLabels := strings.Contains(out, "comment above labels")

	if !hasInlineImage && !hasInlineLabel && !hasAboveLabels {
		t.Errorf("expected at least one comment inside the services map to survive the round-trip; output was:\n%s", out)
	}
}

func TestSetServiceLabel_InvalidYAMLReturnsError(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "broken indentation and unclosed brace",
			input: "services: {\n  web: { image: nginx, ports: [\n",
		},
		{
			name:  "tab-indented mapping with stray colons",
			input: "services:\n\tweb:\n\t\timage: : nginx\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SetServiceLabel(tc.input, "dockpal.auto-update", "true")
			if err == nil {
				t.Fatalf("expected error for invalid YAML, got nil")
			}
		})
	}
}

func TestSetServiceLabel_NoServicesReturnsError(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "version only, no services key",
			input: `version: "3.8"`,
		},
		{
			name:  "services key present but empty mapping",
			input: "services: {}\n",
		},
		{
			name:  "services key present but null",
			input: "services:\n",
		},
		{
			name:  "completely empty document",
			input: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SetServiceLabel(tc.input, "dockpal.auto-update", "true")
			if err == nil {
				t.Fatalf("expected error when services missing, got nil")
			}
		})
	}
}

func TestSetServiceLabel_EmptyKeyReturnsError(t *testing.T) {
	in := `services:
  web:
    image: nginx:latest
`
	if _, err := SetServiceLabel(in, "", "true"); err == nil {
		t.Fatalf("expected error for empty key, got nil")
	}
}
