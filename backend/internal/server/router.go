package server

import (
	"log/slog"

	"backend/internal/handler"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// Handlers groups the HTTP handlers and verifiers registered by the application router.
type Handlers struct {
	System        *handler.SystemHandler
	LibraryFiles  *handler.LibraryFilesHandler
	Auth          *handler.AuthHandler
	TokenVerifier middleware.TokenVerifier
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
	// The file library has no ownership model yet, so its upload route stays public
	// until a later slice adds one.
	apiV1.POST("/library/files", handlers.LibraryFiles.Upload)
	apiV1.POST("/auth/register", handlers.Auth.Register)
	apiV1.POST("/auth/login", handlers.Auth.Login)
	authenticated := apiV1.Group("", middleware.RequireAuth(handlers.TokenVerifier, logger))
	authenticated.GET("/auth/me", handlers.Auth.Me)
	router.NoRoute(handlers.System.NotFound)
	router.NoMethod(handlers.System.MethodNotAllowed)

	return router
}
