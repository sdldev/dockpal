package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/acme/autocert"
)

// DefaultShutdownTimeout is used for ReadTimeout, WriteTimeout, IdleTimeout
// and graceful Shutdown when DOCKPAL_SHUTDOWN_TIMEOUT is not set.
const DefaultShutdownTimeout = 30 * time.Second

const defaultMaxRequestBodyBytes int64 = 10 << 20

type Server struct {
	router          *gin.Engine
	port            string
	srv             *http.Server
	tls             bool
	tlsCert         string
	tlsKey          string
	tlsDomain       string
	dataDir         string
	shutdownTimeout time.Duration
}

func New(tls bool, tlsCert, tlsKey, tlsDomain, dataDir string) *Server {
	port := os.Getenv("PORT")
	if port == "" {
		if tls {
			port = "3443"
		} else {
			port = "3012"
		}
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(maxRequestBodyMiddleware(resolveMaxRequestBodyBytes()))
	router.Use(gin.Recovery())

	return &Server{
		router:          router,
		port:            port,
		tls:             tls,
		tlsCert:         tlsCert,
		tlsKey:          tlsKey,
		tlsDomain:       tlsDomain,
		dataDir:         dataDir,
		shutdownTimeout: resolveShutdownTimeout(),
	}
}

// resolveShutdownTimeout reads DOCKPAL_SHUTDOWN_TIMEOUT (e.g. "30s", "2m") and
// falls back to DefaultShutdownTimeout when unset or invalid.
func resolveShutdownTimeout() time.Duration {
	raw := os.Getenv("DOCKPAL_SHUTDOWN_TIMEOUT")
	if raw == "" {
		return DefaultShutdownTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("Invalid DOCKPAL_SHUTDOWN_TIMEOUT %q, falling back to %s", raw, DefaultShutdownTimeout)
		return DefaultShutdownTimeout
	}
	return d
}

func resolveMaxRequestBodyBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("DOCKPAL_MAX_REQUEST_BODY_BYTES"))
	if raw == "" {
		return defaultMaxRequestBodyBytes
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		log.Printf("Invalid DOCKPAL_MAX_REQUEST_BODY_BYTES %q, falling back to %d", raw, defaultMaxRequestBodyBytes)
		return defaultMaxRequestBodyBytes
	}
	return n
}

func maxRequestBodyMiddleware(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
		if len(c.Errors) == 0 {
			return
		}
		for _, err := range c.Errors {
			if errors.Is(err.Err, http.ErrBodyReadAfterClose) || strings.Contains(err.Err.Error(), "http: request body too large") {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				return
			}
		}
	}
}

// ShutdownTimeout returns the configured shutdown timeout.
func (s *Server) ShutdownTimeout() time.Duration {
	return s.shutdownTimeout
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) newHTTPServer() *http.Server {
	// Use the configured shutdown timeout for read/write/idle to keep behavior
	// consistent during long-running production operations.
	rwTimeout := s.shutdownTimeout
	idleTimeout := s.shutdownTimeout * 4
	return &http.Server{
		Addr:              fmt.Sprintf(":%s", s.port),
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       rwTimeout,
		WriteTimeout:      rwTimeout * 2,
		IdleTimeout:       idleTimeout,
	}
}

func (s *Server) Run() error {
	if s.tls {
		if s.tlsDomain != "" {
			certDir := filepath.Join(s.dataDir, "certs")
			if err := os.MkdirAll(certDir, 0700); err != nil {
				return fmt.Errorf("failed to create autocert cache directory: %w", err)
			}
			m := &autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(s.tlsDomain),
				Cache:      autocert.DirCache(certDir),
			}
			s.srv = s.newHTTPServer()
			s.srv.TLSConfig = m.TLSConfig()

			// Start HTTP-01 challenge responder on port 80 (Let's Encrypt requirement)
			go func() {
				log.Println("Starting Let's Encrypt HTTP challenge responder on port 80")
				if err := http.ListenAndServe(":80", m.HTTPHandler(nil)); err != nil {
					log.Printf("Warning: Let's Encrypt HTTP challenge server on port 80 failed: %v", err)
				}
			}()

			log.Printf("Dockpal server starting with Let's Encrypt TLS on port %s for domain %s", s.port, s.tlsDomain)
			return s.srv.ListenAndServeTLS("", "")
		}

		certFile := s.tlsCert
		keyFile := s.tlsKey

		if certFile == "" || keyFile == "" {
			certFile = filepath.Join(s.dataDir, "certs", "selfsigned.crt")
			keyFile = filepath.Join(s.dataDir, "certs", "selfsigned.key")

			if _, err := os.Stat(certFile); os.IsNotExist(err) {
				log.Println("Generating self-signed certificate for TLS...")
				if err := generateSelfSignedCert(certFile, keyFile); err != nil {
					return fmt.Errorf("failed to generate self-signed certificate: %w", err)
				}
			}
		}

		s.srv = s.newHTTPServer()
		log.Printf("Dockpal server starting with TLS on port %s (cert: %s, key: %s)", s.port, certFile, keyFile)
		return s.srv.ListenAndServeTLS(certFile, keyFile)
	}

	s.srv = s.newHTTPServer()

	log.Printf("Dockpal server starting on port %s (HTTP)", s.port)
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}
