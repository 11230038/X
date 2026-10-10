package server

import (
	"log/slog"

	"backend/internal/handler"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// Handlers groups the HTTP handlers registered by the application router.
type Handlers struct {
	System       *handler.SystemHandler
	LibraryFiles *handler.LibraryFilesHandler
}

// NewRouter builds the application router without binding a network port.
func NewRouter(logger *slog.Logger, appEnv string, handlers Handlers) *gin.Engine {
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

	router.GET("/", handlers.System.Root)
	router.GET("/health", handlers.System.Health)
	router.GET("/healthz", handlers.System.Liveness)
	router.GET("/readyz", handlers.System.Readiness)
	apiV1 := router.Group("/api/v1")
	apiV1.POST("/library/files", handlers.LibraryFiles.Upload)
	router.NoRoute(handlers.System.NotFound)
	router.NoMethod(handlers.System.MethodNotAllowed)

	return router
}
