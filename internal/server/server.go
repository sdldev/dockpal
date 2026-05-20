package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/acme/autocert"
)

type Server struct {
	router    *gin.Engine
	port      string
	srv       *http.Server
	tls       bool
	tlsCert   string
	tlsKey    string
	tlsDomain string
	dataDir   string
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
	router.Use(gin.Recovery())

	return &Server{
		router:    router,
		port:      port,
		tls:       tls,
		tlsCert:   tlsCert,
		tlsKey:    tlsKey,
		tlsDomain: tlsDomain,
		dataDir:   dataDir,
	}
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) newHTTPServer() *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%s", s.port),
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
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
