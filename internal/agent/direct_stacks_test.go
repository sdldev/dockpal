package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/sdldev/dockpal/internal/docker"
)

// newDirectTestClient spins up a TLS test server (DirectClient skips cert
// verification) and returns a client pointed at it plus the mux for routes.
func newDirectTestClient(t *testing.T) (*DirectClient, *http.ServeMux, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	client := NewDirectClient("test", u.Hostname(), port, "secret-token")
	return client, mux, srv
}

func TestDirectClientStackCRUD(t *testing.T) {
	client, mux, _ := newDirectTestClient(t)
	ctx := context.Background()

	mux.HandleFunc("/agent/docker/stacks", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{
				"stacks": []docker.Stack{{Name: "web", Status: "draft"}},
			})
		case http.MethodPost:
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			if req["name"] == "" || req["compose"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "name and compose required"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(docker.Stack{Name: req["name"], Managed: true, ComposeYAML: req["compose"]})
		}
	})

	stacks, err := client.ListStacks(ctx)
	if err != nil || len(stacks) != 1 || stacks[0].Name != "web" {
		t.Fatalf("ListStacks: %v %+v", err, stacks)
	}

	saved, err := client.SaveStack(ctx, "web", "services: {}\n", "A=1\n", true)
	if err != nil || saved.Name != "web" || !saved.Managed {
		t.Fatalf("SaveStack: %v %+v", err, saved)
	}

	// Missing name → 400 with message preserved
	_, err = client.SaveStack(ctx, "", "", "", true)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 error, got %v", err)
	}

	// GetStack
	mux.HandleFunc("/agent/docker/stacks/web", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(docker.Stack{Name: "web", Status: "running", Managed: true})
	})
	got, err := client.GetStack(ctx, "web")
	if err != nil || got.Status != "running" {
		t.Fatalf("GetStack: %v %+v", err, got)
	}

	// DeleteStack
	mux.HandleFunc("/agent/docker/stacks/old", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			json.NewEncoder(w).Encode(map[string]string{"message": "deleted"})
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	if err := client.DeleteStack(ctx, "old"); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}
}

func TestDirectClientStackActions(t *testing.T) {
	client, mux, _ := newDirectTestClient(t)
	ctx := context.Background()

	var lastPath string
	handler := func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		// Busy conflict: message must survive for server-side stackError mapping
		if strings.HasSuffix(r.URL.Path, "/stop") {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "another operation is already running for this stack"})
			return
		}
		json.NewEncoder(w).Encode(docker.Stack{Name: "web", Status: "running"})
	}
	mux.HandleFunc("/agent/docker/stacks/web/up", handler)
	mux.HandleFunc("/agent/docker/stacks/web/stop", handler)
	mux.HandleFunc("/agent/docker/stacks/web/services/db/restart", handler)

	stack, err := client.StackAction(ctx, "web", "up")
	if err != nil || stack.Status != "running" || lastPath != "/agent/docker/stacks/web/up" {
		t.Fatalf("StackAction up: %v %+v path=%s", err, stack, lastPath)
	}

	_, err = client.StackAction(ctx, "web", "stop")
	if err == nil || !strings.Contains(err.Error(), "another operation is already running") {
		t.Fatalf("expected busy error with message, got %v", err)
	}

	_, err = client.StackServiceAction(ctx, "web", "db", "restart")
	if err != nil || lastPath != "/agent/docker/stacks/web/services/db/restart" {
		t.Fatalf("StackServiceAction: %v path=%s", err, lastPath)
	}
}

func TestDirectClientStackMeta(t *testing.T) {
	client, mux, _ := newDirectTestClient(t)
	ctx := context.Background()

	mux.HandleFunc("/agent/docker/stacks/meta/networks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"networks": []string{"net1", "net2"}})
	})
	mux.HandleFunc("/agent/docker/stacks/meta/globalenv", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			if req["content"] != "G=1\n" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"message": "saved"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"content": "G=1\n"})
	})

	nets, err := client.ListDockerNetworks(ctx)
	if err != nil || len(nets) != 2 || nets[0] != "net1" {
		t.Fatalf("ListDockerNetworks: %v %+v", err, nets)
	}

	env, err := client.GetGlobalEnv(ctx)
	if err != nil || env != "G=1\n" {
		t.Fatalf("GetGlobalEnv: %v %q", err, env)
	}

	if err := client.SetGlobalEnv(ctx, "G=1\n"); err != nil {
		t.Fatalf("SetGlobalEnv: %v", err)
	}
	if err := client.SetGlobalEnv(ctx, "bad"); err == nil {
		t.Fatalf("expected SetGlobalEnv error")
	}
}