package auth

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// HasRole checks if the given user role has sufficient permissions
// compared to the required role.
// Role hierarchy: admin > operator > viewer
func HasRole(userRole, requiredRole string) bool {
	switch requiredRole {
	case RoleViewer:
		return userRole == RoleAdmin || userRole == RoleOperator || userRole == RoleViewer
	case RoleOperator:
		return userRole == RoleAdmin || userRole == RoleOperator
	case RoleAdmin:
		return userRole == RoleAdmin
	default:
		// If no role is specified, default to requiring at least viewer
		return userRole == RoleAdmin || userRole == RoleOperator || userRole == RoleViewer
	}
}
