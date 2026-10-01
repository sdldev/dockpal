package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sdldev/dockpal/internal/auth"
	"github.com/sdldev/dockpal/internal/docker"
)

// Instance-scoped Dockge-style stack routes — the remote mirror of
// routes_stacks.go. Handlers depend on the narrow stackClient interface
// (same pattern as the apps handlers' triggerWorker) so tests can inject a
// fake without implementing the whole AgentClient surface.

// stackClient is the narrow agent surface the stack handlers need.
type stackClient interface {
	ListStacks(ctx context.Context) ([]docker.Stack, error)
	GetStack(ctx context.Context, name string) (*docker.Stack, error)
	SaveStack(ctx context.Context, name, composeYAML, composeENV string, isAdd bool) (*docker.Stack, error)
	DeleteStack(ctx context.Context, name string) error
	StackAction(ctx context.Context, name, action string) (*docker.Stack, error)
	StackServiceAction(ctx context.Context, name, service, action string) (*docker.Stack, error)
	DeployStackStreamed(ctx context.Context, name, composeYAML, composeENV string, isAdd bool, session *docker.DeploySession) error
	ListDockerNetworks(ctx context.Context) ([]string, error)
	GetGlobalEnv(ctx context.Context) (string, error)
	SetGlobalEnv(ctx context.Context, content string) error
}

// stackClientFromContext resolves the narrow interface from the gin context.
// Works for Local/Direct/Edge clients; returns false when a test injected a
// non-stack client.
func stackClientFromContext(c *gin.Context) (stackClient, bool) {
	v, ok := c.Get("agent_client")
	if !ok {
		return nil, false
	}
	sc, ok := v.(stackClient)
	return sc, ok
}

func registerInstanceStackRoutes(g *gin.RouterGroup) {
	g.GET("/stacks", RequireRole(auth.RoleViewer), handleInstanceListStacks)
	g.GET("/stacks/meta/networks", RequireRole(auth.RoleViewer), handleInstanceListDockerNetworks)
	g.GET("/stacks/meta/globalenv", RequireRole(auth.RoleViewer), handleInstanceGetGlobalEnv)
	g.PUT("/stacks/meta/globalenv", RequireRole(auth.RoleOperator), handleInstanceSetGlobalEnv)
	g.GET("/stacks/:name", RequireRole(auth.RoleViewer), handleInstanceGetStack)

	g.POST("/stacks", RequireRole(auth.RoleOperator), handleInstanceCreateStack)
	g.PUT("/stacks/:name", RequireRole(auth.RoleOperator), handleInstanceUpdateStack)
	g.DELETE("/stacks/:name", RequireRole(auth.RoleOperator), handleInstanceDeleteStack)

	g.POST("/stacks/:name/deploy", RequireRole(auth.RoleOperator), handleInstanceDeployStack)
	for _, action := range []string{"up", "start", "stop", "restart", "down", "update"} {
		action := action
		g.POST("/stacks/:name/"+action, RequireRole(auth.RoleOperator), func(c *gin.Context) {
			handleInstanceStackAction(c, action)
		})
	}

	g.POST("/stacks/:name/services/:service/up", RequireRole(auth.RoleOperator), func(c *gin.Context) {
		handleInstanceStackServiceAction(c, "up")
	})
	g.POST("/stacks/:name/services/:service/stop", RequireRole(auth.RoleOperator), func(c *gin.Context) {
		handleInstanceStackServiceAction(c, "stop")
	})
	g.POST("/stacks/:name/services/:service/restart", RequireRole(auth.RoleOperator), func(c *gin.Context) {
		handleInstanceStackServiceAction(c, "restart")
	})
}

func handleInstanceListStacks(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	stacks, err := client.ListStacks(c.Request.Context())
	if err != nil {
		stackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"stacks": stacks})
}

func handleInstanceListDockerNetworks(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	names, err := client.ListDockerNetworks(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"networks": names})
}

func handleInstanceGetGlobalEnv(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	content, err := client.GetGlobalEnv(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"content": content})
}

func handleInstanceSetGlobalEnv(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := client.SetGlobalEnv(c.Request.Context(), req.Content); err != nil {
		stackError(c, err)
		return
	}
	auditInstanceStack(c, "stack.globalenv", "global.env", "")
	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func handleInstanceGetStack(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	stack, err := client.GetStack(c.Request.Context(), c.Param("name"))
	if err != nil {
		stackError(c, err)
		return
	}
	c.JSON(http.StatusOK, stack)
}

func handleInstanceCreateStack(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	var req struct {
		Name    string `json:"name" binding:"required"`
		Compose string `json:"compose" binding:"required"`
		Env     string `json:"env"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and compose are required"})
		return
	}
	stack, err := client.SaveStack(c.Request.Context(), req.Name, req.Compose, req.Env, true)
	if err != nil {
		stackError(c, err)
		return
	}
	auditInstanceStack(c, "stack.create", req.Name, "")
	c.JSON(http.StatusCreated, stack)
}

func handleInstanceUpdateStack(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	var req struct {
		Compose string `json:"compose" binding:"required"`
		Env     string `json:"env"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "compose is required"})
		return
	}
	stack, err := client.SaveStack(c.Request.Context(), c.Param("name"), req.Compose, req.Env, false)
	if err != nil {
		stackError(c, err)
		return
	}
	auditInstanceStack(c, "stack.update", c.Param("name"), "")
	c.JSON(http.StatusOK, stack)
}

func handleInstanceDeleteStack(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	if err := client.DeleteStack(c.Request.Context(), c.Param("name")); err != nil {
		stackError(c, err)
		return
	}
	auditInstanceStack(c, "stack.delete", c.Param("name"), "")
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// handleInstanceDeployStack saves (when compose present) then runs
// `docker compose up -d --remove-orphans` on the instance host; progress is
// streamed over the existing deploy WebSocket
// (/api/instances/:instance_id/deploy/stream/:id).
func handleInstanceDeployStack(c *gin.Context) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	name := c.Param("name")
	var req struct {
		Compose string `json:"compose"`
		Env     string `json:"env"`
		IsAdd   bool   `json:"is_add"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Compose = ""
		req.Env = ""
		req.IsAdd = false
	}

	session := globalDeployManager.CreateSession()
	go func() {
		// Always schedule session removal — even on panic — so a stuck or
		// crashed deploy never leaks the session (audit M4). Bounded context
		// so a hung remote call can't leak forever.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("instance deploy stack goroutine panicked", "stack", name, "panic", r)
			}
			time.AfterFunc(30*time.Second, func() {
				globalDeployManager.RemoveSession(session.ID)
			})
		}()
		err := client.DeployStackStreamed(ctx, name, req.Compose, req.Env, req.IsAdd, session)
		if err == nil {
			session.Emit("done", "Deployed", "done")
		} else {
			session.Emit("error", err.Error(), "error")
		}
	}()

	auditInstanceStack(c, "stack.deploy", name, "deploy_id "+session.ID)
	c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
}

func handleInstanceStackAction(c *gin.Context, action string) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	stack, err := client.StackAction(c.Request.Context(), c.Param("name"), action)
	if err != nil {
		stackError(c, err)
		return
	}
	auditInstanceStack(c, "stack."+action, c.Param("name"), "")
	if stack == nil {
		c.JSON(http.StatusOK, gin.H{"message": action + " ok"})
		return
	}
	c.JSON(http.StatusOK, stack)
}

func handleInstanceStackServiceAction(c *gin.Context, action string) {
	client, ok := stackClientFromContext(c)
	if !ok {
		internalError(c, errNotStackClient)
		return
	}
	name := c.Param("name")
	service := c.Param("service")
	if !validateServiceName(service) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid service name"})
		return
	}
	stack, err := client.StackServiceAction(c.Request.Context(), name, service, action)
	if err != nil {
		stackError(c, err)
		return
	}
	if stack == nil {
		c.JSON(http.StatusOK, gin.H{"message": action + " ok"})
		return
	}
	c.JSON(http.StatusOK, stack)
}

var errNotStackClient = errors.New("agent client does not support stack operations")

// auditInstanceStack records a stack mutation for the instance in the URL.
// Never pass compose/env content in details — those hold secrets (audit H4).
func auditInstanceStack(c *gin.Context, action, name, details string) {
	database := getDatabase(c)
	if database == nil {
		return
	}
	resource := "instances/" + c.Param("instance_id") + "/stacks/" + name
	LogAudit(c, database, action, resource, "success", details)
}
