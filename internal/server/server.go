package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

type Server struct {
	router *gin.Engine
	port   string
	srv    *http.Server
}

func New() *Server {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3012"
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	return &Server{
		router: router,
		port:   port,
	}
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

func (s *Server) Run() error {
	s.srv = &http.Server{
		Addr:    fmt.Sprintf(":%s", s.port),
		Handler: s.router,
	}

	log.Printf("Dockpal server starting on port %s", s.port)
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
