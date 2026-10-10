package server

import (
	"net/http"

	"backend/internal/config"
)

// NewHTTPServer creates an HTTP server with configured production timeouts.
func NewHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPTimeouts.ReadHeader,
		ReadTimeout:       cfg.HTTPTimeouts.Read,
		WriteTimeout:      cfg.HTTPTimeouts.Write,
		IdleTimeout:       cfg.HTTPTimeouts.Idle,
	}
}
