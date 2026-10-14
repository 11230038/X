package server

import (
	"log/slog"

	"backend/internal/authz"
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
	Reading       *handler.ReadingHandler
	Dashboard     *handler.DashboardHandler
	Memory        *handler.MemoryHandler
	Capabilities  *handler.CapabilityHandler
	Permissions   *handler.PermissionsHandler
	Admin         *handler.AdminHandler
	Guardians     *handler.GuardianHandler
	WebSocket     *ws.Handler
	TokenVerifier middleware.TokenVerifier
	// Authorizer answers what an account may reach. It is what the routes below
	// are narrowed by, and it is required: a router built without one refuses
	// every authenticated request rather than serving them unchecked.
	Authorizer middleware.Authorizer
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
	apiV1.POST("/auth/register", handlers.Auth.Register)
	apiV1.POST("/auth/login", handlers.Auth.Login)
	authenticated := apiV1.Group("", middleware.RequireAuth(handlers.TokenVerifier, logger))
	// reach is the second gate: everything below is grouped by the area of the
	// product it belongs to, and every request to one has to reach that area
	// before its handler runs (§2.1). The grouping is what makes the check
	// total — a route added inside one of these groups is covered by
	// construction, and the only way to leave a route unchecked is to register
	// it on `authenticated` instead, which is a visible thing to do in review.
	reach := func(surface string) *gin.RouterGroup {
		return authenticated.Group("",
			middleware.Authorize(handlers.Authorizer, middleware.Surface(surface), logger))
	}
	// What this account is, and what it may reach, before anything else: it is
	// mounted without a surface so that an account restricted out of every area
	// can still ask why, and it is what a client reads to draw the refusals it
	// has not been given yet (§2.16).
	authenticated.GET("/me/permissions", handlers.Permissions.Get)
	system := reach(authz.SurfaceSystem)
	system.GET("/auth/me", handlers.Auth.Me)
	system.GET("/capabilities", handlers.Capabilities.List)
	// The file library is behind the token from the upload onwards. It used to be
	// the one public write in this API, because a file had no owner: what you
	// uploaded was stored for whoever came back for the id. That made every stored
	// file everybody's, which is a state that reads as working until there is a
	// second account. A file now belongs to the account that uploaded it, and
	// every read of one is narrowed to that account, so both of these are here.
	library := reach(authz.SurfaceLibrary)
	library.POST("/library/files", handlers.LibraryFiles.Upload)
	library.GET("/library/files/:fileID", handlers.LibraryReads.Get)
	library.GET("/library/files/:fileID/text", handlers.LibraryReads.Text)
	knowledgeBase := reach(authz.SurfaceKnowledgeBase)
	knowledgeBase.POST("/knowledge-bases", handlers.KnowledgeBase.Create)
	knowledgeBase.GET("/knowledge-bases", handlers.KnowledgeBase.List)
	knowledgeBase.GET("/knowledge-bases/:kbID", handlers.KnowledgeBase.Get)
	knowledgeBase.DELETE("/knowledge-bases/:kbID", handlers.KnowledgeBase.Delete)
	knowledgeBase.POST("/knowledge-bases/:kbID/documents", handlers.KnowledgeBase.AddDocument)
	knowledgeBase.DELETE("/knowledge-bases/:kbID/documents/:documentID", handlers.KnowledgeBase.RemoveDocument)
	// Rebuilding is per document: the path says which collection the request is
	// about, and the body says which document in it to re-read.
	knowledgeBase.POST("/knowledge-bases/:kbID/rebuild", handlers.KnowledgeBase.Rebuild)
	knowledgeBase.POST("/knowledge-bases/:kbID/search", handlers.KnowledgeBase.Search)
	knowledgeBase.POST("/knowledge-bases/:kbID/default", handlers.KnowledgeBase.SetDefault)
	chat := reach(authz.SurfaceChat)
	chat.POST("/sessions", handlers.Sessions.Create)
	chat.GET("/sessions", handlers.Sessions.List)
	chat.GET("/sessions/:sessionID", handlers.Sessions.Get)
	chat.PATCH("/sessions/:sessionID", handlers.Sessions.Rename)
	chat.DELETE("/sessions/:sessionID", handlers.Sessions.SoftDelete)
	chat.POST("/sessions/:sessionID/restore", handlers.Sessions.Restore)
	// Claiming is the exit from the state every session written before V5 is in
	// (§2.5). Without it, an upgraded deployment would hold a set of conversations
	// that only the administrator can see and nobody can act on. It is a POST
	// because it changes the row, and it takes no body because the session and the
	// account are both already named — the id in the path and the account in the
	// token.
	chat.POST("/sessions/:sessionID/claim", handlers.Sessions.Claim)
	chat.DELETE("/sessions/:sessionID/permanent", handlers.Sessions.DeletePermanently)
	chat.GET("/sessions/:sessionID/messages", handlers.Sessions.Messages)
	// The question bank and reviewing are behind the token along with everything
	// else a person's own material goes through: what these answer is what this
	// user has been asked, answered and got wrong.
	notebook := reach(authz.SurfaceNotebook)
	notebook.GET("/questions", handlers.Questions.List)
	// The batch import is one route in two modes: `dry_run=true` reports what
	// writing the file would do and writes nothing, and the same route without it
	// does what the report said (§2.13).
	notebook.POST("/questions/import", handlers.Questions.Import)
	notebook.GET("/questions/:questionID", handlers.Questions.Get)
	notebook.PATCH("/questions/:questionID", handlers.Questions.Patch)
	notebook.GET("/question-categories", handlers.Questions.Categories)
	practice := reach(authz.SurfacePractice)
	practice.GET("/practice/queue", handlers.Practice.Queue)
	practice.POST("/practice/reviews", handlers.Practice.Submit)
	practice.GET("/practice/reviews/:requestID", handlers.Practice.Read)
	practice.GET("/practice/stats", handlers.Practice.Stats)
	// Mastery paths are read and named here, and built through the socket: a
	// path is created inside the turn that was asked to work on it (§2.1), so
	// there is no POST among these. The two lease endpoints are the exception to
	// "a turn's state is a turn's business", and the reason is written where they
	// are handled: a claim left behind by a turn that died is a person's to drop.
	mastery := reach(authz.SurfaceMastery)
	mastery.GET("/mastery/paths", handlers.Mastery.List)
	mastery.GET("/mastery/paths/:pathID", handlers.Mastery.Get)
	mastery.PATCH("/mastery/paths/:pathID", handlers.Mastery.Patch)
	mastery.GET("/mastery/paths/:pathID/insight", handlers.Mastery.Insight)
	mastery.GET("/mastery/paths/:pathID/lease", handlers.Mastery.Lease)
	mastery.DELETE("/mastery/paths/:pathID/lease", handlers.Mastery.ReleaseLease)
	// Reading is where the reader is, and every route here is one reader's own
	// material: what they have opened, where they stopped in it, and what they
	// wrote. A material is a row pointing at a document the knowledge base
	// parsed, so the words come back through these routes and never as a file.
	reading := reach(authz.SurfaceReading)
	reading.GET("/reading/materials", handlers.Reading.List)
	reading.POST("/reading/materials", handlers.Reading.Create)
	reading.GET("/reading/materials/:materialID", handlers.Reading.Get)
	reading.GET("/reading/materials/:materialID/content", handlers.Reading.Content)
	reading.GET("/reading/materials/:materialID/marks", handlers.Reading.Marks)
	reading.POST("/reading/materials/:materialID/marks", handlers.Reading.AddMark)
	reading.POST("/reading/materials/:materialID/position", handlers.Reading.SavePosition)
	// The two extension routes are the closed set of reading aids this build has
	// and the run of one of them over one passage. The list is a read of the
	// build; the run is the reader asking a question about the text in front of
	// them, and neither is routed through a turn (§2.9).
	// A paired book is the reader's own confirmation that two materials are one
	// book in two languages. The list is a recommendation computed per request and
	// writes nothing; the write is the confirmation itself; and the cancel is the
	// same row removed (§2.10). Nothing downstream behaves bilingually until that
	// row is there, which is why the list carries the enabled state and its reason.
	reading.GET("/reading/materials/:materialID/pairings", handlers.Reading.Pairings)
	reading.POST("/reading/materials/:materialID/pairings", handlers.Reading.ConfirmPairing)
	reading.DELETE("/reading/pairings/:pairingID", handlers.Reading.CancelPairing)
	reading.GET("/reading/materials/:materialID/extensions", handlers.Reading.Extensions)
	reading.POST("/reading/materials/:materialID/extensions/:name", handlers.Reading.RunExtension)
	// The dashboard is the one read that spans the domains, and it sits beside
	// the resources it reads rather than under any of them (COURSE-03).
	dashboard := reach(authz.SurfaceDashboard)
	dashboard.GET("/dashboard", handlers.Dashboard.Get)
	// Memory is what the system has been told about an account and what it
	// admitted from it (§2.14). Every route here is the caller's own memory: the
	// account comes from the token and there is no owner in any body or query.
	//
	// The reads come first — the observations, the facts, one fact with the
	// evidence chain it was admitted on — and then the writes. `POST /memory/facts`
	// is the user's own way in (§2.7), and it is the only write here that takes a
	// statement without naming a fact: the two routes beside it are the two batches,
	// each of which is run as a preview and then as itself.
	//
	// There is no DELETE, and that is §2.14's sentence rather than an omission:
	// MEM-02's three verbs are edit, retract and cleanup, so an id in a URL is never
	// something this API removes. `POST /memory/facts/dedupe` and
	// `/memory/facts/:factID/retract` are different routes at different depths, so
	// the static `dedupe` does not shadow the parameter.
	memory := reach(authz.SurfaceMemory)
	memory.GET("/memory/traces", handlers.Memory.Traces)
	memory.GET("/memory/facts", handlers.Memory.Facts)
	memory.GET("/memory/facts/:factID", handlers.Memory.Fact)
	memory.PATCH("/memory/facts/:factID", handlers.Memory.EditFact)
	memory.POST("/memory/facts", handlers.Memory.StateFact)
	memory.POST("/memory/facts/:factID/retract", handlers.Memory.RetractFact)
	memory.POST("/memory/facts/:factID/restore", handlers.Memory.RestoreFact)
	memory.POST("/memory/facts/dedupe", handlers.Memory.Dedupe)
	memory.POST("/memory/cleanup", handlers.Memory.Cleanup)
	memory.GET("/memory/synthesis", handlers.Memory.Synthesis)
	memory.GET("/memory/audit", handlers.Memory.Audit)
	memory.GET("/memory/export", handlers.Memory.Export)
	// Governance is the one area no preset opens: what may reach it is decided by
	// the role layer and nothing else (§2.1). It is written after every surface
	// group because it is the only one whose reach depends on *who* is asking
	// rather than on what they are doing, and reading this function top to bottom
	// is meant to be the product's own summary of itself.
	//
	// These routes are also the only way a grant or a relationship is ever
	// written at runtime. Without them the four-layer model would be correct and
	// unusable — a preset settable only by hand in the database and a grant only
	// by a migration (§2.16).
	admin := reach(authz.SurfaceAdmin)
	admin.GET("/admin/users", handlers.Admin.ListUsers)
	admin.PATCH("/admin/users/:userID", handlers.Admin.UpdateUser)
	// A grant is one row per (account, item) and the item is two values — a kind
	// and an id, half of which are the empty id that means "every item of this
	// kind" — so the read is under the account and the write names the item in
	// its body rather than in a path segment that cannot hold the empty one.
	admin.GET("/admin/users/:userID/grants", handlers.Admin.ListGrants)
	admin.POST("/admin/users/:userID/grants", handlers.Admin.SetGrant)
	admin.DELETE("/admin/users/:userID/grants", handlers.Admin.WithdrawGrant)
	admin.GET("/admin/guardians", handlers.Guardians.ListRelationships)
	admin.POST("/admin/guardians", handlers.Guardians.CreateRelationship)
	admin.DELETE("/admin/guardians", handlers.Guardians.RevokeRelationship)
	admin.GET("/admin/guardians/:linkID/permissions", handlers.Guardians.ListPermissions)
	admin.POST("/admin/guardians/:linkID/permissions", handlers.Guardians.GrantPermission)
	admin.DELETE("/admin/guardians/:linkID/permissions", handlers.Guardians.RevokePermission)
	// A guardian's own view, and a different surface from the administrator's:
	// the two are different accounts with different reasons for being here, and
	// the standard baseline reaches both while a learner reaches neither. It is
	// the one route whose answer is narrowed after the fact — the list is the
	// relationships this caller holds, so an account with none is told it has
	// none rather than refused.
	guardian := reach(authz.SurfaceGuardian)
	guardian.GET("/guardian/learners", handlers.Guardians.ListLearners)
	// The socket is authenticated by the middleware above before it is
	// upgraded, so an unauthenticated request is answered as an HTTP request
	// rather than becoming a connection (WS-06).
	chat.GET("/ws", handlers.WebSocket.Serve)
	router.NoRoute(handlers.System.NotFound)
	router.NoMethod(handlers.System.MethodNotAllowed)

	return router
}
