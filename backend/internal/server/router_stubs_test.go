package server

// The router these cases drive, and the stubs it is wired to.
//
// Every stub here answers something recognisable as its own, because the cases
// in the files beside this one are about *which* handler a path reached — a body
// that could have come from two handlers makes a mis-registered route a passing
// assertion. What any of these answers *say* is the business of the handler
// package's own tests; here they are a fingerprint.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/book"
	"backend/internal/dashboard"
	"backend/internal/handler"
	"backend/internal/kb"
	"backend/internal/library"
	"backend/internal/middleware"
	"backend/internal/usage"
	"backend/internal/video"
	"github.com/gin-gonic/gin"
)

type readyChecker struct {
	err error
}

func (c readyChecker) Ping(context.Context) error {
	return c.err
}

func newTestRouter(t *testing.T, checker readyChecker) *gin.Engine {
	t.Helper()
	return newTestRouterWith(t, checker, nil)
}

// newTestRouterWith is the same router with a reviewing failure to hand back.
// The bank and the review path are wired to stubs whose answers are recognisable
// one from another, so that a route that was registered against the wrong
// handler is a failing assertion rather than a plausible body.
func newTestRouterWith(t *testing.T, checker readyChecker, practiceErr error) *gin.Engine {
	t.Helper()
	return newTestRouterWithAuthorizer(t, checker, practiceErr, routerAuthorizerStub{})
}

// newTestRouterWithAuthorizer is the same router over a chosen authorizer, so
// that a case about a refusal can hand one in without every other case in this
// file having to describe a policy it is not about.
func newTestRouterWithAuthorizer(
	t *testing.T, checker readyChecker, practiceErr error, authorizer middleware.Authorizer,
) *gin.Engine {
	t.Helper()
	return newTestRouterWithGates(t, checker, practiceErr, authorizer, &routerLimiterStub{})
}

// newTestRouterWithGates is the builder the wrappers above and the rate limit
// cases share: the router with both of its narrowings chosen. A case about one of
// them hands in the other as the permissive one it would have got anyway, which
// is what keeps these cases from describing a policy or a limit they are not
// about.
func newTestRouterWithGates(
	t *testing.T, checker readyChecker, practiceErr error,
	authorizer middleware.Authorizer, limiter middleware.Limiter,
) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	system := handler.NewSystemHandler(checker, 50*time.Millisecond, logger)
	libraryFiles := handler.NewLibraryFilesHandler(routerUploaderStub{}, 1024, logger)
	authenticator := routerAuthStub{}
	// The operations handler is the one handler here that refuses to be built
	// without its sources, so it is built rather than written inline: a router
	// whose version route is not wired at all would answer 200 with an empty
	// version, which is a shape no assertion about a route being mounted catches.
	ops, err := handler.NewOpsHandler(handler.OpsConfig{
		Env:           "development",
		SchemaVersion: routerSchemaVersion,
		Readiness:     checker,
		Runtime:       routerRuntimeStub{},
	}, logger)
	if err != nil {
		t.Fatalf("NewOpsHandler() error = %v", err)
	}
	return NewRouter(logger, "development", Handlers{
		System:       system,
		Ops:          ops,
		LibraryFiles: libraryFiles,
		LibraryReads: handler.NewLibraryReadHandler(routerFileStub{}, routerMaterialStub{}, logger),
		// One stub answering both halves, because in the server it is one service:
		// the bytes a download hands over are the bytes the parsers read (§2.14).
		LibraryLifecycle: handler.NewLibraryLifecycleHandler(
			routerLibraryLifecycleStub{}, routerOpenerStub{}, logger),
		Auth:        handler.NewAuthHandler(authenticator, logger),
		Book:        handler.NewBookHandler(routerBookStub{}, logger),
		Questions:   handler.NewQuestionHandler(routerQuestionsStub{}, logger),
		Practice:    handler.NewPracticeHandler(routerPracticeStub{err: practiceErr}, routerQueueSize, logger),
		Permissions: handler.NewPermissionsHandler(routerPermissionsStub{}, logger),
		Admin: handler.NewAdminHandler(
			routerAdminStub{}, routerAdminStub{}, routerAdminStub{},
			routerAccountLifecycleStub{}, routerAuditWriterStub{}, logger, nil),
		Guardians: handler.NewGuardianHandler(routerGuardianStub{}, logger),
		Audit:     handler.NewAuditHandler(routerAuditStub{}, logger),
		Reports:   handler.NewReportsHandler(routerDashboardStub{}, routerAuditWriterStub{}, logger),
		Usage:     handler.NewUsageHandler(routerUsageStub{}, logger),
		Video:     handler.NewVideoHandler(routerVideoStub{}, logger),
		// The package routes are the one group here mounted with no surface, and
		// the stub beside them is what lets router_workspace_test.go say so: a
		// policy that refuses everything refuses the library and not these.
		Workspace:     handler.NewWorkspaceHandler(routerWorkspaceStub{}, logger),
		TokenVerifier: authenticator,
		// The same stub answers both, because in the server it is one service: the
		// ticket a client is handed a moment ago is the ticket the socket accepts.
		TicketVerifier: authenticator,
		Authorizer:     authorizer,
		Limiter:        limiter,
		// KnowledgeBase is deliberately left out. This file is about which
		// routes exist and which group they sit in; what a knowledge base
		// endpoint answers is handler/kb_test.go's business, and wiring a stub
		// here would say nothing the other file does not already say.
	})
}

type routerFileStub struct{}

// routerAuthorizerStub is the policy as most cases in this file need it: every
// request allowed. A case about a route being registered is not also a case
// about what the policy says about it, and the two questions have their own
// files — internal/authz for the second, this one for the first.
type routerAuthorizerStub struct {
	// deny makes every decision a refusal, which is what the case about the 403
	// boundary hands in.
	deny bool
}

func (s routerAuthorizerStub) Decide(
	context.Context, authz.Caller, authz.Action, int64,
) (authz.Decision, error) {
	if s.deny {
		return authz.Decision{Layer: authz.LayerPreset, Reason: authz.ReasonBaselineClosed}, nil
	}
	return authz.Decision{Allowed: true, Layer: authz.LayerSelf, Reason: authz.ReasonOwnMaterial}, nil
}

func (routerFileStub) Find(context.Context, string, authz.Caller) (library.UploadResult, error) {
	return library.UploadResult{ID: "lib_1", SHA256: strings.Repeat("a", 64), Filename: "手册.pdf"}, nil
}

type routerMaterialStub struct{}

func (routerMaterialStub) DocumentOfFile(context.Context, string, authz.Caller) (kb.Document, error) {
	return kb.Document{ID: "doc_1", KBID: "kb_1", FileID: "lib_1", TextChars: 12}, nil
}

func (routerMaterialStub) ReadWindow(context.Context, string, int64, int64, authz.Caller) (kb.Window, error) {
	return kb.Window{Document: kb.Document{ID: "doc_1", TextChars: 12}, Text: "保修期是两年。", CharEnd: 7, TotalChars: 12}, nil
}

func (routerMaterialStub) ChunkSpans(context.Context, string, authz.Caller) ([]kb.ChunkSpan, error) {
	return nil, nil
}

// routerBookStub answers the book routes with shapes a client would recognise.
// The values carry the book surface's own names, so a route mounted against
// another handler's stub is a failing assertion rather than a plausible body.
type routerBookStub struct{}

func (routerBookStub) List(context.Context, authz.Caller) ([]book.Summary, error) {
	return []book.Summary{{BookID: "bk_1", Title: "保修手册"}}, nil
}

func (routerBookStub) Get(context.Context, authz.Caller, string) (book.Detail, error) {
	return book.Detail{Book: book.Summary{BookID: "bk_1", Title: "保修手册"}}, nil
}

func (routerBookStub) Create(context.Context, authz.Caller, book.CreateInput) (book.Detail, error) {
	return book.Detail{Book: book.Summary{BookID: "bk_1", Title: "保修手册"}}, nil
}

func (routerBookStub) Delete(context.Context, authz.Caller, string) error { return nil }

func (routerBookStub) GenerateOutline(context.Context, authz.Caller, string) (book.Detail, error) {
	return book.Detail{Book: book.Summary{BookID: "bk_1", Title: "保修手册"}}, nil
}

func (routerBookStub) PutOutline(context.Context, authz.Caller, string, book.OutlineInput) (book.Detail, error) {
	return book.Detail{Book: book.Summary{BookID: "bk_1", Title: "保修手册"}}, nil
}

func (routerBookStub) GenerateChapter(context.Context, authz.Caller, string, int) (book.Chapter, error) {
	return book.Chapter{ChapterID: "bc_1", BookID: "bk_1", Ordinal: 1, Title: "第一章", Status: book.StatusReady}, nil
}

func (routerBookStub) Chapter(context.Context, authz.Caller, string, int) (book.Chapter, error) {
	return book.Chapter{ChapterID: "bc_1", BookID: "bk_1", Ordinal: 1, Title: "第一章", Status: book.StatusReady}, nil
}

func (routerBookStub) SaveProgress(context.Context, authz.Caller, string, int) (book.Summary, error) {
	return book.Summary{BookID: "bk_1", Title: "保修手册"}, nil
}

func (routerBookStub) Export(context.Context, authz.Caller, string) (book.Export, error) {
	return book.Export{Filename: "保修手册.md", Content: "# 保修手册"}, nil
}

type routerUploaderStub struct{}

func (routerUploaderStub) Upload(context.Context, library.UploadInput) (library.UploadResult, error) {
	return library.UploadResult{}, nil
}

// routerLibraryLifecycleStub answers the listing and the recycle bin. As with
// the knowledge base stub above, what these routes *say* is
// handler/library_recycle_test.go's business — this file is about which routes
// exist, in which group, and behind which gate.
type routerLibraryLifecycleStub struct{}

func (routerLibraryLifecycleStub) Search(
	context.Context, library.SearchQuery,
) ([]library.FileSummary, error) {
	return nil, nil
}

func (routerLibraryLifecycleStub) SoftDelete(
	context.Context, string, library.Actor,
) (library.FileSummary, error) {
	return library.FileSummary{ID: "lib_1", Filename: "手册.pdf"}, nil
}

func (routerLibraryLifecycleStub) Restore(
	context.Context, string, library.Actor,
) (library.FileSummary, error) {
	return library.FileSummary{ID: "lib_1", Filename: "手册.pdf"}, nil
}

func (routerLibraryLifecycleStub) Purge(context.Context, string, library.Actor) error {
	return nil
}

// routerOpenerStub hands back a reader over nothing, which is all the download
// route needs to answer: the bytes it streams are handler/library_recycle_test.go's
// business.
type routerOpenerStub struct{}

func (routerOpenerStub) Open(context.Context, string, authz.Caller) (library.StoredFile, error) {
	return library.StoredFile{
		ID: "lib_1", Filename: "手册.pdf", MIMEType: "application/pdf",
		SizeBytes: 4, Content: io.NopCloser(strings.NewReader("data")),
	}, nil
}

// routerPermissionsStub answers a picture of the caller's reach. This file is
// about the route being reachable at all, so what the picture *says* is
// handler/permissions_test.go's business; all that matters here is that the
// route is mounted and answers behind a token.
type routerPermissionsStub struct{}

func (routerPermissionsStub) Self(context.Context, authz.Caller) (authz.Self, error) {
	return authz.Self{
		Preset: authz.PresetStandard,
		Items: []authz.Decision{{
			Item: authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceChat}, Allowed: true,
		}},
	}, nil
}

// routerAdminStub serves both halves of the governance handler — the accounts
// and the grants — with answers that are recognisable as this stub's, so that a
// governance route registered against the wrong handler is a failing assertion
// rather than a plausible body. What the answers *say* is
// handler/admin_test.go's business.
type routerAdminStub struct{}

func (routerAdminStub) Accounts(context.Context) ([]authz.Account, error) {
	return []authz.Account{{UserID: 1, Username: "root", Role: authz.RoleAdmin}}, nil
}

func (routerAdminStub) SetPreset(context.Context, authz.Caller, int64, string) error { return nil }

func (routerAdminStub) Assignments(context.Context, int64) ([]authz.Assignment, error) {
	return []authz.Assignment{{
		Item: authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceReading}, Allowed: true,
	}}, nil
}

func (routerAdminStub) Assign(context.Context, authz.Caller, int64, authz.Item, bool) error {
	return nil
}

func (routerAdminStub) Withdraw(context.Context, int64, authz.Item) error { return nil }

// The two sign-in writes are served by the same stub as the rest of governance
// here, because what this file is about is which routes exist and which group
// each sits in. What they do is handler/admin_test.go's business.

func (routerAdminStub) RevokeTokens(context.Context, int64) error { return nil }

func (routerAdminStub) Unlock(context.Context, int64) error { return nil }

// routerGuardianStub answers the relationship routes.
type routerGuardianStub struct{}

func (routerGuardianStub) AllRelationships(context.Context) ([]authz.Link, error) {
	return []authz.Link{{ID: "lnk_1", GuardianID: 7, LearnerID: 42}}, nil
}

func (routerGuardianStub) Relationships(context.Context, int64) ([]authz.Link, error) {
	return []authz.Link{{ID: "lnk_1", GuardianID: 7, LearnerID: 42,
		Permissions: []authz.Permission{authz.PermViewReports}}}, nil
}

func (routerGuardianStub) Link(context.Context, authz.Caller, int64, int64, []authz.Permission) (authz.Link, error) {
	return authz.Link{ID: "lnk_1", GuardianID: 7, LearnerID: 42}, nil
}

func (routerGuardianStub) Revoke(context.Context, string) error { return nil }

func (routerGuardianStub) LinkByID(_ context.Context, linkID string) (authz.Link, error) {
	return authz.Link{ID: linkID, GuardianID: 7, LearnerID: 42}, nil
}

func (routerGuardianStub) GrantPermission(context.Context, authz.Caller, string, authz.Permission) error {
	return nil
}

func (routerGuardianStub) RevokePermission(context.Context, string, authz.Permission) error {
	return nil
}

// routerAuditStub answers the audit read. It holds nothing, which is also the
// interesting answer: an instance with no entries is what a fresh deployment
// looks like, and the route has to be reachable in that state.
type routerAuditStub struct{}

func (routerAuditStub) List(context.Context, audit.Filter) ([]audit.Event, error) {
	return nil, nil
}

func (routerAuditStub) MaxPageSize() int { return 50 }

// routerUsageStub answers the usage report with a window that has nothing in it.
// What a report says is handler/usage_test.go's business; what matters here is
// that the route is mounted, behind a token, in the group it belongs to — which
// is what the surface in a refusal is the only way to see.
type routerUsageStub struct{}

func (routerUsageStub) Read(context.Context, usage.Query) (usage.Report, error) {
	return usage.Report{Groups: []usage.Group{}}, nil
}

// routerVideoStub answers the video search with one recognisable result, so that
// a route wired to the wrong handler is a failing assertion rather than a
// plausible empty page. What a result *says* is handler/video_test.go's business.
type routerVideoStub struct{}

func (routerVideoStub) Search(_ context.Context, query video.Query) (video.SearchResult, error) {
	return video.SearchResult{
		Query: query.Text,
		Page:  query.Page,
		Items: []video.Result{{
			VideoID: "BV1xx411c7mD", Title: "线性代数 第一讲", Author: "教师",
			EmbedURL: "https://player.bilibili.com/player.html?bvid=BV1xx411c7mD&page=1&autoplay=0",
		}},
	}, nil
}

// routerSchemaVersion is the schema the test router's binary claims to require.
// The number itself is not what a routing case checks — ops_test.go owns that —
// only that the route answers.
const routerSchemaVersion = int64(17)

// routerRuntimeStub is the process as the status route reads it: a quiet one,
// with nothing in flight. What a status says is handler/ops_test.go's business.
type routerRuntimeStub struct{}

func (routerRuntimeStub) ActiveTurns() int                               { return 0 }
func (routerRuntimeStub) Indexing() bool                                 { return false }
func (routerRuntimeStub) UnfinishedCalls(context.Context) (int64, error) { return 0, nil }

// routerDashboardStub answers the reports route with a view that is
// recognisable as this stub's, so that the one route which answers with another
// account's material cannot be quietly registered against the wrong handler.
// What the view *says* is handler/dashboard_test.go's business.
type routerDashboardStub struct{}

func (routerDashboardStub) Read(
	_ context.Context, actor authz.Caller,
) (dashboard.View, error) {
	return dashboard.View{
		Sessions: []dashboard.Entry{{ID: "ses_1", Title: "algebra", Context: "session"}},
	}, nil
}

// routerAuditWriterStub accepts the entry the reports route writes. A router
// test is about which routes exist and which group they sit in; what an entry
// says is internal/audit's and handler/'s business.
type routerAuditWriterStub struct{}

func (routerAuditWriterStub) Record(context.Context, audit.Event) error { return nil }

// routerAuthStub accepts exactly one token and rejects everything else.
type routerAuthStub struct{}

const routerAcceptedToken = "accepted-token"

func (routerAuthStub) Register(_ context.Context, input auth.RegisterInput) (auth.AuthResult, error) {
	return auth.AuthResult{
		User:  auth.User{UserID: 1, Username: input.Username, Role: "user"},
		Token: routerAcceptedToken,
	}, nil
}

func (routerAuthStub) Login(context.Context, auth.LoginInput) (auth.AuthResult, error) {
	return auth.AuthResult{
		User:  auth.User{UserID: 1, Username: "alice", Role: "user"},
		Token: routerAcceptedToken,
	}, nil
}

// RevokeTokens takes nothing back, and that is the point of it being here: what
// a stub has to satisfy is the route mounting, and a case about a token really
// stopping is auth's — where the account row and the moment are both real.
func (routerAuthStub) RevokeTokens(context.Context, int64) error { return nil }

// ChangePassword takes nothing either, for the reason above: whether the old
// password was really checked and whether the account really ends up unable to
// use the old one are questions about a stored hash, and they are asked in
// internal/auth against a real database.
func (routerAuthStub) ChangePassword(context.Context, int64, string, string) error { return nil }

// The allowlist flag is true here so that the four lifecycle routes' second gate
// passes on the "an allowed account reaches it" half of the surface cases; which
// account is on the allowlist is middleware/superadmin_test.go's business.
func (routerAuthStub) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != routerAcceptedToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{UserID: 1, Username: "alice", Role: "user", SuperAdmin: true}, nil
}

// IssueWebSocketTicket hands back a ticket and the moment it stops working. As
// with the two writes above, what is being satisfied here is the route mounting:
// whether the ticket really verifies, really expires, and really stops working
// when the account's tokens are taken back is asked in internal/auth, where the
// secret and the clock are real.
func (routerAuthStub) IssueWebSocketTicket(context.Context, int64) (string, time.Time, error) {
	return routerWsTicket, routerTicketExpiry, nil
}

const routerWsTicket = "signed-ticket"

// routerTicketExpiry is the moment the stub's ticket stops working. It is a
// fixed instant rather than a call to time.Now, so that a case about the route
// can assert what came back.
var routerTicketExpiry = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Redeem accepts exactly the ticket IssueWebSocketTicket handed out. It is
// deliberately as narrow as Authenticate beside it: the point of the stub is
// that a request arriving with the wrong credential is a refusal rather than a
// plausible account, which is what lets a routing case tell the ticket path
// apart from a route that simply has no gate on it.
func (routerAuthStub) Redeem(_ context.Context, ticket string) (auth.User, error) {
	if ticket != routerWsTicket {
		return auth.User{}, auth.ErrInvalidTicket
	}
	return auth.User{UserID: 1, Username: "alice", Role: "user"}, nil
}

// routerRequest builds one request, optionally bearing the stub's token.
func routerRequest(method, path, body string, authenticated bool) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
	}
	return request
}
