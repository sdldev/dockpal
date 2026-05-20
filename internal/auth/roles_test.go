package auth

import "testing"

func TestHasRole(t *testing.T) {
	tests := []struct {
		assigned string
		required string
		expected bool
	}{
		// Admin assigned
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleOperator, true},
		{RoleAdmin, RoleViewer, true},
		{RoleAdmin, "", true},

		// Operator assigned
		{RoleOperator, RoleAdmin, false},
		{RoleOperator, RoleOperator, true},
		{RoleOperator, RoleViewer, true},
		{RoleOperator, "", true},

		// Viewer assigned
		{RoleViewer, RoleAdmin, false},
		{RoleViewer, RoleOperator, false},
		{RoleViewer, RoleViewer, true},
		{RoleViewer, "", true},

		// Invalid roles/empty
		{"invalid", RoleViewer, false},
		{"", RoleViewer, false},
		{RoleViewer, "invalid", true}, // default required role check
	}

	for _, tc := range tests {
		result := HasRole(tc.assigned, tc.required)
		if result != tc.expected {
			t.Errorf("HasRole(%q, %q) = %v; expected %v", tc.assigned, tc.required, result, tc.expected)
		}
	}
}
