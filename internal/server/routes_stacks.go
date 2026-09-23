package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sdldev/dockpal/internal/composecli"
	"github.com/sdldev/dockpal/internal/docker"

	"github.com/gin-gonic/gin"
)

// Dockge-style compose stack routes (local host).
//
// These run through the `docker compose` CLI on the host that serves Dockpal.
// The remote mirror lives in instance_stacks_routes.go under
// /api/instances/:instance_id/stacks..., backed by the AgentClient stack RPCs
// (Local/Direct/Edge implementations).

// registerStackRoutes wires /api/stacks endpoints. viewerGroup handles reads,
// operatorGroup mutations (both already carry auth + rate-limit middleware).
func registerStackRoutes(viewerGroup, operatorGroup *gin.RouterGroup) {
	viewerGroup.GET("/stacks", handleListStacks)
	viewerGroup.GET("/stacks/meta/networks", handleListDockerNetworks)
	viewerGroup.GET("/stacks/meta/globalenv", handleGetGlobalEnv)
	viewerGroup.GET("/stacks/:name", handleGetStack)

	operatorGroup.POST("/stacks", handleCreateStack)
	operatorGroup.PUT("/stacks/:name", handleUpdateStack)
	operatorGroup.DELETE("/stacks/:name", handleDeleteStack)
	operatorGroup.PUT("/stacks/meta/globalenv", handleSetGlobalEnv)

	operatorGroup.POST("/stacks/:name/deploy", handleDeployStack)
	for _, action := range []string{"up", "start", "stop", "restart", "down", "update"} {
		action := action
		operatorGroup.POST("/stacks/:name/"+action, func(c *gin.Context) {
			handleStackAction(c, action)
		})
	}

	operatorGroup.POST("/stacks/:name/services/:service/up", func(c *gin.Context) { handleStackServiceAction(c, "up") })
	operatorGroup.POST("/stacks/:name/services/:service/stop", func(c *gin.Context) { handleStackServiceAction(c, "stop") })
	operatorGroup.POST("/stacks/:name/services/:service/restart", func(c *gin.Context) { handleStackServiceAction(c, "restart") })
}

// stackCLIUnavailable responds 501 when the docker compose plugin is missing.
func stackCLIUnavailable(c *gin.Context) bool {
	if !composecli.Available() {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "docker compose CLI not available on this host"})
		return true
	}
	return false
}

func stackError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case composecli.IsBusy(err), strings.Contains(msg, "another operation is already running"):
		c.JSON(http.StatusConflict, gin.H{"error": msg})
	case strings.Contains(msg, "not found"):
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
	case strings.Contains(msg, "already exists"):
		c.JSON(http.StatusConflict, gin.H{"error": msg})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
	}
}

func handleListStacks(c *gin.Context) {
	if stackCLIUnavailable(c) {
		return
	}
	stacks, err := docker.ListStacks(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"stacks": stacks})
}

// handleListDockerNetworks powers the network editor dropdown.
func handleListDockerNetworks(c *gin.Context) {
	if stackCLIUnavailable(c) {
		return
	}
	names, err := docker.ListDockerNetworks(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"networks": names})
}

// handleGetGlobalEnv returns the shared global.env content ("" when absent).
func handleGetGlobalEnv(c *gin.Context) {
	content, err := docker.GetGlobalEnv()
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"content": content})
}

// handleSetGlobalEnv validates and writes the shared global.env.
func handleSetGlobalEnv(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := docker.SetGlobalEnv(req.Content); err != nil {
		stackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func handleGetStack(c *gin.Context) {
	if stackCLIUnavailable(c) {
		return
	}
	name := c.Param("name")
	stack, err := docker.GetStackFull(c.Request.Context(), name)
	if err != nil {
		stackError(c, err)
		return
	}
	c.JSON(http.StatusOK, stack)
}

type stackPayload struct {
	Name    string `json:"name" binding:"required"`
	Compose string `json:"compose" binding:"required"`
	Env     string `json:"env"`
}

func handleCreateStack(c *gin.Context) {
	var req stackPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and compose are required"})
		return
	}
	if err := docker.SaveStack(req.Name, req.Compose, req.Env, true); err != nil {
		stackError(c, err)
		return
	}
	stack, err := docker.GetStack(req.Name)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, stack)
}

func handleUpdateStack(c *gin.Context) {
	var req struct {
		Compose string `json:"compose" binding:"required"`
		Env     string `json:"env"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "compose is required"})
		return
	}
	name := c.Param("name")
	if err := docker.SaveStack(name, req.Compose, req.Env, false); err != nil {
		stackError(c, err)
		return
	}
	stack, err := docker.GetStack(name)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, stack)
}

func handleDeleteStack(c *gin.Context) {
	if stackCLIUnavailable(c) {
		return
	}
	name := c.Param("name")
	if err := docker.StackDelete(c.Request.Context(), name); err != nil {
		stackError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// handleDeployStack saves the stack (create-or-update) then runs
// `docker compose up -d --remove-orphans` in the background; progress is
// streamed over the existing deploy WebSocket (/api/deploy/stream/:id).
func handleDeployStack(c *gin.Context) {
	if stackCLIUnavailable(c) {
		return
	}
	name := c.Param("name")
	var req struct {
		Compose string `json:"compose"`
		Env     string `json:"env"`
		IsAdd   bool   `json:"is_add"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// empty body is fine — deploy whatever is on disk
		req.Compose = ""
		req.Env = ""
		req.IsAdd = false
	}

	if req.Compose != "" {
		if err := docker.SaveStack(name, req.Compose, req.Env, req.IsAdd); err != nil {
			stackError(c, err)
			return
		}
	} else if _, err := docker.GetStack(name); err != nil {
		stackError(c, err)
		return
	}

	session := globalDeployManager.CreateSession()
	go func() {
		err := composecli.StackUpStreamed(context.Background(), name, session)
		if err == nil {
			session.Emit("done", "Deployed", "done")
		}
		time.AfterFunc(30*time.Second, func() {
			globalDeployManager.RemoveSession(session.ID)
		})
	}()

	c.JSON(http.StatusOK, gin.H{"deploy_id": session.ID})
}

func handleStackAction(c *gin.Context, action string) {
	if stackCLIUnavailable(c) {
		return
	}
	name := c.Param("name")
	ctx := c.Request.Context()

	var err error
	switch action {
	case "up", "start":
		err = docker.StackUp(ctx, name)
	case "stop":
		err = docker.StackStop(ctx, name)
	case "restart":
		err = docker.StackRestart(ctx, name)
	case "down":
		err = docker.StackDown(ctx, name)
	case "update":
		err = docker.StackUpdate(ctx, name)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action"})
		return
	}
	if err != nil {
		stackError(c, err)
		return
	}

	stack, getErr := docker.GetStackFull(ctx, name)
	if getErr != nil {
		// Action succeeded; report without status details.
		c.JSON(http.StatusOK, gin.H{"message": action + " ok"})
		return
	}
	c.JSON(http.StatusOK, stack)
}

func handleStackServiceAction(c *gin.Context, action string) {
	if stackCLIUnavailable(c) {
		return
	}
	name := c.Param("name")
	service := c.Param("service")
	if service == "" || strings.ContainsAny(service, "/\\ ") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid service name"})
		return
	}
	ctx := c.Request.Context()

	var err error
	switch action {
	case "up":
		err = docker.StackServiceUp(ctx, name, service)
	case "stop":
		err = docker.StackServiceStop(ctx, name, service)
	case "restart":
		err = docker.StackServiceRestart(ctx, name, service)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action"})
		return
	}
	if err != nil {
		// compose exits non-zero for unknown service names
		if errors.Is(err, context.Canceled) {
			c.JSON(http.StatusRequestTimeout, gin.H{"error": err.Error()})
			return
		}
		stackError(c, err)
		return
	}
	stack, getErr := docker.GetStackFull(ctx, name)
	if getErr != nil {
		c.JSON(http.StatusOK, gin.H{"message": action + " ok"})
		return
	}
	c.JSON(http.StatusOK, stack)
}
