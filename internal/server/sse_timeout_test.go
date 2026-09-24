package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestSSEStreamSurvivesWriteTimeout proves the SSE handlers are not capped by
// http.Server.WriteTimeout. WriteTimeout is an *absolute* deadline net/http
// installs per connection; a non-hijacking streaming handler that writes past
// it gets "i/o timeout" and the client sees a truncated feed. clearWriteDeadline
// removes it so the stream lives as long as the request context.
//
// The handler below mirrors the real SSE loop: write a frame, flush, repeat —
// well past the server's timeout window.
func TestSSEStreamSurvivesWriteTimeout(t *testing.T) {
	const writeTimeout = 200 * time.Millisecond
	const frames = 6
	const frameEvery = 100 * time.Millisecond

	h := func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		clearWriteDeadline(c)

		ctx := c.Request.Context()
		for i := 0; i < frames; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(frameEvery):
			}
			if _, err := c.Writer.Write([]byte("data: frame\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stream", h)

	srv := &http.Server{Handler: r, WriteTimeout: writeTimeout}
	ts := httptest.NewUnstartedServer(r)
	ts.Config = srv
	ts.Start()
	defer ts.Close()
	defer srv.Shutdown(context.Background())

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(ts.URL + "/stream")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading stream failed (likely i/o timeout from WriteTimeout): %v", err)
	}

	// Total streaming time is frames*frameEvery = 600ms, 3x the 200ms WriteTimeout.
	if got := strings.Count(string(body), "data: frame"); got != frames {
		t.Fatalf("expected %d frames past the %s WriteTimeout, got %d", frames, writeTimeout, got)
	}
}
