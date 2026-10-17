package server

import (
	"backend/internal/handler"
	"backend/internal/middleware"
	"backend/internal/ws"
)

// Handlers groups the HTTP handlers and verifiers registered by the application router.
type Handlers struct {
	System           *handler.SystemHandler
	Ops              *handler.OpsHandler
	LibraryFiles     *handler.LibraryFilesHandler
	LibraryReads     *handler.LibraryReadHandler
	LibraryLifecycle *handler.LibraryLifecycleHandler
	KnowledgeBase    *handler.KBHandler
	Auth             *handler.AuthHandler
	Sessions         *handler.SessionHandler
	Personas         *handler.PersonaHandler
	Questions        *handler.QuestionHandler
	Practice         *handler.PracticeHandler
	Mastery          *handler.MasteryHandler
	Reading          *handler.ReadingHandler
	Book             *handler.BookHandler
	Video            *handler.VideoHandler
	Dashboard        *handler.DashboardHandler
	Memory           *handler.MemoryHandler
	Capabilities     *handler.CapabilityHandler
	Permissions      *handler.PermissionsHandler
	Admin            *handler.AdminHandler
	Guardians        *handler.GuardianHandler
	Audit            *handler.AuditHandler
	Reports          *handler.ReportsHandler
	Usage            *handler.UsageHandler
	Workspace        *handler.WorkspaceHandler
	WebSocket        *ws.Handler
	TokenVerifier    middleware.TokenVerifier
	// TicketVerifier redeems the short-lived credential a browser connects to the
	// websocket with, since a browser has nowhere to put an Authorization header
	// on a handshake. It is required by the websocket route and by nothing else:
	// a router built without one refuses a browser's connection attempt with a
	// 500 that names the missing wiring, rather than with a 401 that would blame
	// the caller's ticket.
	TicketVerifier middleware.TicketVerifier
	// Authorizer answers what an account may reach. It is what the routes below
	// are narrowed by, and it is required: a router built without one refuses
	// every authenticated request rather than serving them unchecked.
	Authorizer middleware.Authorizer
	// Refusals is where a refusal is written down. It is optional, and the only
	// place that passes one is main.go, next to the audit service it comes from:
	// a router built without one still refuses, it just does not leave a record
	// — which is what every test in this repository wants, since what they are
	// about is whether a request is refused (§2.16).
	Refusals middleware.Recorder
	// Limiter bounds how fast one account may do the kinds of work that cost
	// something outside this process (§2.10). It is mounted on routes
	// rather than on a group, so a router built without one answers those
	// routes' callers with a 500 rather than serving them unmeasured — the same
	// direction the authorizer's absence takes.
	Limiter middleware.Limiter
}
