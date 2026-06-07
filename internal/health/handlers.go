package health

import (
	"github.com/gin-gonic/gin"
	"github.com/moby/moby/client"
)

// Handlers provides HTTP handlers for health check endpoints
type Handlers struct {
	checker *Checker
}

// NewHandlers creates new health check handlers.
// db is the already-open database instance.
func NewHandlers(db DBPinger, dataDir string, dockerClient *client.Client, version string) *Handlers {
	// Convert docker client to our interface
	var clientInterface DockerClient
	if dockerClient != nil {
		clientInterface = dockerClient
	}

	return &Handlers{
		checker: NewChecker(db, "", dataDir, clientInterface, version),
	}
}

// HandleHealth handles the comprehensive health check endpoint
// GET /health
func (h *Handlers) HandleHealth(c *gin.Context) {
	response := h.checker.CheckHealth(c.Request.Context())

	// Set appropriate HTTP status code
	c.JSON(response.GetHTTPStatus(), response)
}

// HandleLiveness handles the liveness probe endpoint
// GET /health/live
func (h *Handlers) HandleLiveness(c *gin.Context) {
	response := h.checker.CheckLiveness(c.Request.Context())

	// Set appropriate HTTP status code
	c.JSON(response.GetHTTPStatus(), response)
}

// HandleReadiness handles the readiness probe endpoint
// GET /health/ready
func (h *Handlers) HandleReadiness(c *gin.Context) {
	response := h.checker.CheckReadiness(c.Request.Context())

	// Set appropriate HTTP status code
	c.JSON(response.GetHTTPStatus(), response)
}

// RegisterHealthRoutes registers health check routes with the Gin router
func (h *Handlers) RegisterHealthRoutes(router *gin.Engine) {
	// Main health check endpoint
	router.GET("/health", h.HandleHealth)

	// Kubernetes-style probe endpoints
	router.GET("/health/live", h.HandleLiveness)
	router.GET("/health/ready", h.HandleReadiness)

	// Alternative probe endpoints for compatibility
	router.GET("/healthz", h.HandleHealth)   // Kubernetes style
	router.GET("/livez", h.HandleLiveness)   // Kubernetes style
	router.GET("/readyz", h.HandleReadiness) // Kubernetes style
}
