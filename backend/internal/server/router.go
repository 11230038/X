package server

import (
	"log/slog"

	"backend/internal/handler"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// NewRouter builds the application router without binding a network port.
func NewRouter(logger *slog.Logger, appEnv string) *gin.Engine {
	if appEnv != "development" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	// Do not trust forwarded headers unless a trusted proxy is configured.
	_ = router.SetTrustedProxies(nil)
	router.HandleMethodNotAllowed = true
	router.Use(
		middleware.RequestID(),
		middleware.Recovery(logger),
		middleware.AccessLog(logger),
	)

	system := handler.NewSystemHandler()
	router.GET("/", system.Root)
	router.GET("/health", system.Health)
	router.GET("/healthz", system.Liveness)
	router.GET("/readyz", system.Readiness)
	router.NoRoute(system.NotFound)
	router.NoMethod(system.MethodNotAllowed)

	return router
}
