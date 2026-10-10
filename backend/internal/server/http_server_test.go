package server

import (
	"net/http"
	"testing"
	"time"

	"backend/internal/config"
)

func TestNewHTTPServerUsesConfiguredTimeouts(t *testing.T) {
	cfg := config.Config{
		HTTPAddr: "127.0.0.1:9090",
		HTTPTimeouts: config.HTTPTimeoutConfig{
			ReadHeader: 3 * time.Second,
			Read:       2 * time.Minute,
			Write:      3 * time.Minute,
			Idle:       45 * time.Second,
		},
	}
	server := NewHTTPServer(cfg, http.NewServeMux())
	if server.ReadHeaderTimeout != cfg.HTTPTimeouts.ReadHeader || server.ReadTimeout != cfg.HTTPTimeouts.Read {
		t.Errorf("read timeouts = %s/%s", server.ReadHeaderTimeout, server.ReadTimeout)
	}
	if server.WriteTimeout != cfg.HTTPTimeouts.Write || server.IdleTimeout != cfg.HTTPTimeouts.Idle {
		t.Errorf("write/idle timeouts = %s/%s", server.WriteTimeout, server.IdleTimeout)
	}
}
