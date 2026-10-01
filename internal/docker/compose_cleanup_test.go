package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"
)

// newFakeDockerClient builds a Client backed by an in-process HTTP server so
// the create/start/inspect/remove flow can be exercised without a Docker daemon.
func newFakeDockerClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	cli, err := client.NewClientWithOpts(
		client.WithHost(server.URL),
		client.WithVersion("1.54"),
		client.WithHTTPClient(server.Client()),
	)
	if err != nil {
		server.Close()
		t.Fatalf("failed to build moby client: %v", err)
	}
	return &Client{cli: cli}, server
}

// fakeAPIServer is a minimal Docker API stand-in that records the calls made
// against it.
type fakeAPIServer struct {
	mu           sync.Mutex
	startFail    bool   // when set, /start returns a daemon failure
	nameConflict string // when set, the first create fails with this message
	inspectBody  string // body returned by /containers/{id}/json
	removed      []string
	createCalls  int
}

func (s *fakeAPIServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/start") && r.Method == http.MethodPost:
			s.mu.Lock()
			fail := s.startFail
			s.mu.Unlock()
			if fail {
				writeDaemonError(w, http.StatusInternalServerError,
					"failed to set up container networking: failed to bind host port 0.0.0.0:3306/tcp: address already in use")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/json") && r.Method == http.MethodGet:
			s.mu.Lock()
			body := s.inspectBody
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		case path == "/v1.54/containers/create" && r.Method == http.MethodPost:
			s.mu.Lock()
			s.createCalls++
			conflict := s.nameConflict
			s.mu.Unlock()
			if conflict != "" && s.createCalls == 1 {
				writeDaemonError(w, http.StatusConflict, conflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"Id":"c1","Warnings":[]}`))
		case strings.HasPrefix(path, "/v1.54/containers/") && r.Method == http.MethodDelete:
			id := strings.Split(strings.TrimPrefix(path, "/v1.54/containers/"), "?")[0]
			s.mu.Lock()
			s.removed = append(s.removed, id)
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (s *fakeAPIServer) removedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.removed...)
}

func writeDaemonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func testService() ComposeService {
	return ComposeService{
		Image:       "mariadb:11.4",
		Ports:       []string{"33106:3306"},
		Environment: map[string]string{"MARIADB_ROOT_PASSWORD": "secret"},
		Restart:     "unless-stopped",
	}
}

// TestCreateAndStartService_RemovesContainerOnStartFailure covers the exact
// scenario seen in production: create succeeds, start fails on a port conflict,
// and the half-created container must not be left behind.
func TestCreateAndStartService_RemovesContainerOnStartFailure(t *testing.T) {
	srv := &fakeAPIServer{startFail: true}
	c, _ := newFakeDockerClient(t, srv.handler())

	_, err := c.createAndStartService(context.Background(), "proj", "svc", testService(), &ComposeFile{})
	if err == nil || !strings.Contains(err.Error(), "failed to start container svc") {
		t.Fatalf("expected start failure, got: %v", err)
	}
	if got := srv.removedIDs(); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("expected created container c1 to be removed, got: %v", got)
	}
}

// TestCreateAndStartService_RetriesAfterStaleNameConflict verifies a stale
// non-running container from a previous failed deploy is removed so the retry
// can reuse the name.
func TestCreateAndStartService_RetriesAfterStaleNameConflict(t *testing.T) {
	srv := &fakeAPIServer{
		nameConflict: `Conflict. The container name "/proj_svc" is already in use by container "stale1"`,
		inspectBody:  `{"Id":"stale1","State":{"Status":"created","Running":false}}`,
	}
	c, _ := newFakeDockerClient(t, srv.handler())

	createdID, err := c.createAndStartService(context.Background(), "proj", "svc", testService(), &ComposeFile{})
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if createdID != "c1" {
		t.Fatalf("expected created container ID c1, got %q", createdID)
	}
	if got := srv.removedIDs(); len(got) != 1 || got[0] != "stale1" {
		t.Fatalf("expected stale container stale1 to be removed, got: %v", got)
	}
	if srv.createCalls != 2 {
		t.Fatalf("expected create to be retried once, got %d calls", srv.createCalls)
	}
}

// TestCreateAndStartService_PreservesRunningContainerOnNameConflict makes sure
// a live container holding the name is never auto-removed.
func TestCreateAndStartService_PreservesRunningContainerOnNameConflict(t *testing.T) {
	srv := &fakeAPIServer{
		nameConflict: `Conflict. The container name "/proj_svc" is already in use by container "live1"`,
		inspectBody:  `{"Id":"live1","State":{"Status":"running","Running":true}}`,
	}
	c, _ := newFakeDockerClient(t, srv.handler())

	_, err := c.createAndStartService(context.Background(), "proj", "svc", testService(), &ComposeFile{})
	if err == nil {
		t.Fatal("expected create to fail when a running container holds the name")
	}
	if got := srv.removedIDs(); len(got) != 0 {
		t.Fatalf("expected running container to be left alone, removed: %v", got)
	}
	if srv.createCalls != 1 {
		t.Fatalf("expected no retry against a running container, got %d calls", srv.createCalls)
	}
}

func TestIsContainerNameConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"name conflict", errors.New(`Conflict. The container name "/proj_svc" is already in use by container "abc"`), true},
		{"unrelated error", errors.New("failed to bind host port: address already in use"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isContainerNameConflict(tc.err); got != tc.want {
				t.Fatalf("isContainerNameConflict(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
