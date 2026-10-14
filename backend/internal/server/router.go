package server

import (
	"log/slog"

	"backend/internal/handler"
	"backend/internal/middleware"
	"backend/internal/ws"
	"github.com/gin-gonic/gin"
)

// Handlers groups the HTTP handlers and verifiers registered by the application router.
type Handlers struct {
	System        *handler.SystemHandler
	LibraryFiles  *handler.LibraryFilesHandler
	LibraryReads  *handler.LibraryReadHandler
	KnowledgeBase *handler.KBHandler
	Auth          *handler.AuthHandler
	Sessions      *handler.SessionHandler
	Questions     *handler.QuestionHandler
	Practice      *handler.PracticeHandler
	Mastery       *handler.MasteryHandler
	Capabilities  *handler.CapabilityHandler
	WebSocket     *ws.Handler
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
	// Gin answers an unmatched trailing slash with a 301 and an HTML body before
	// any handler runs, which would be the one response outside the envelope.
	// Let it fall through to NoRoute so every response stays JSON.
	router.RedirectTrailingSlash = false
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
	// until a later slice adds one. Reading a file back is a different request:
	// what it answers with is the material a turn was grounded on, and the
	// knowledge base endpoints beside it are the collections that material lives
	// in, so both are behind the token.
	apiV1.POST("/library/files", handlers.LibraryFiles.Upload)
	apiV1.POST("/auth/register", handlers.Auth.Register)
	apiV1.POST("/auth/login", handlers.Auth.Login)
	authenticated := apiV1.Group("", middleware.RequireAuth(handlers.TokenVerifier, logger))
	authenticated.GET("/library/files/:fileID", handlers.LibraryReads.Get)
	authenticated.GET("/library/files/:fileID/text", handlers.LibraryReads.Text)
	authenticated.POST("/knowledge-bases", handlers.KnowledgeBase.Create)
	authenticated.GET("/knowledge-bases", handlers.KnowledgeBase.List)
	authenticated.GET("/knowledge-bases/:kbID", handlers.KnowledgeBase.Get)
	authenticated.DELETE("/knowledge-bases/:kbID", handlers.KnowledgeBase.Delete)
	authenticated.POST("/knowledge-bases/:kbID/documents", handlers.KnowledgeBase.AddDocument)
	authenticated.DELETE("/knowledge-bases/:kbID/documents/:documentID", handlers.KnowledgeBase.RemoveDocument)
	// Rebuilding is per document: the path says which collection the request is
	// about, and the body says which document in it to re-read.
	authenticated.POST("/knowledge-bases/:kbID/rebuild", handlers.KnowledgeBase.Rebuild)
	authenticated.POST("/knowledge-bases/:kbID/search", handlers.KnowledgeBase.Search)
	authenticated.POST("/knowledge-bases/:kbID/default", handlers.KnowledgeBase.SetDefault)
	authenticated.GET("/auth/me", handlers.Auth.Me)
	authenticated.GET("/capabilities", handlers.Capabilities.List)
	authenticated.POST("/sessions", handlers.Sessions.Create)
	authenticated.GET("/sessions", handlers.Sessions.List)
	authenticated.GET("/sessions/:sessionID", handlers.Sessions.Get)
	authenticated.PATCH("/sessions/:sessionID", handlers.Sessions.Rename)
	authenticated.DELETE("/sessions/:sessionID", handlers.Sessions.SoftDelete)
	authenticated.POST("/sessions/:sessionID/restore", handlers.Sessions.Restore)
	authenticated.DELETE("/sessions/:sessionID/permanent", handlers.Sessions.DeletePermanently)
	authenticated.GET("/sessions/:sessionID/messages", handlers.Sessions.Messages)
	// The question bank and reviewing are behind the token along with everything
	// else a person's own material goes through: what these answer is what this
	// user has been asked, answered and got wrong.
	authenticated.GET("/questions", handlers.Questions.List)
	authenticated.GET("/questions/:questionID", handlers.Questions.Get)
	authenticated.PATCH("/questions/:questionID", handlers.Questions.Patch)
	authenticated.GET("/question-categories", handlers.Questions.Categories)
	authenticated.GET("/practice/queue", handlers.Practice.Queue)
	authenticated.POST("/practice/reviews", handlers.Practice.Submit)
	authenticated.GET("/practice/reviews/:requestID", handlers.Practice.Read)
	authenticated.GET("/practice/stats", handlers.Practice.Stats)
	// Mastery paths are read and named here, and built through the socket: a
	// path is created inside the turn that was asked to work on it (§2.1), so
	// there is no POST among these. The two lease endpoints are the exception to
	// "a turn's state is a turn's business", and the reason is written where they
	// are handled: a claim left behind by a turn that died is a person's to drop.
	authenticated.GET("/mastery/paths", handlers.Mastery.List)
	authenticated.GET("/mastery/paths/:pathID", handlers.Mastery.Get)
	authenticated.PATCH("/mastery/paths/:pathID", handlers.Mastery.Patch)
	authenticated.GET("/mastery/paths/:pathID/insight", handlers.Mastery.Insight)
	authenticated.GET("/mastery/paths/:pathID/lease", handlers.Mastery.Lease)
	authenticated.DELETE("/mastery/paths/:pathID/lease", handlers.Mastery.ReleaseLease)
	// The socket is authenticated by the middleware above before it is
	// upgraded, so an unauthenticated request is answered as an HTTP request
	// rather than becoming a connection (WS-06).
	authenticated.GET("/ws", handlers.WebSocket.Serve)
	router.NoRoute(handlers.System.NotFound)
	router.NoMethod(handlers.System.MethodNotAllowed)

	return router
}
