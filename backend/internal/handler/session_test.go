package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"backend/internal/session"
	"github.com/gin-gonic/gin"
)

func TestSessionHandlerCreateReturnsTheNewSession(t *testing.T) {
	stub := &sessionStub{record: sampleSession()}
	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions", "")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.SessionID != "sess_1" || envelope.Data.Title != "闭包是什么" {
		t.Errorf("data = %+v, want the session that was created", envelope.Data)
	}
	if envelope.Data.DeletedAt != nil {
		t.Error("a session that was just created is reported as deleted")
	}
	if envelope.Data.Kind != "chat" {
		t.Errorf("kind = %q, want chat", envelope.Data.Kind)
	}
	// A create without a kind is the pre-existing request, and it still means a
	// conversation: only the pages that own a partition name a kind.
	if len(stub.createdKinds) != 1 || stub.createdKinds[0] != session.KindChat {
		t.Errorf("created kinds = %v, want [chat]", stub.createdKinds)
	}
}

func TestSessionHandlerCreatesThePartitionItWasAskedFor(t *testing.T) {
	record := sampleSession()
	record.Kind = session.KindWriting
	stub := &sessionStub{record: record}

	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions?kind=writing", "")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Kind != "writing" {
		t.Errorf("kind = %q, want writing", envelope.Data.Kind)
	}
	if len(stub.createdKinds) != 1 || stub.createdKinds[0] != session.KindWriting {
		t.Errorf("created kinds = %v, want [writing]", stub.createdKinds)
	}
}

func TestSessionHandlerCreatesTheMasteryPartition(t *testing.T) {
	record := sampleSession()
	record.Kind = session.KindMastery
	stub := &sessionStub{record: record}

	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions?kind=mastery", "")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Kind != "mastery" {
		t.Errorf("kind = %q, want mastery", envelope.Data.Kind)
	}
	if len(stub.createdKinds) != 1 || stub.createdKinds[0] != session.KindMastery {
		t.Errorf("created kinds = %v, want [mastery]", stub.createdKinds)
	}
}

func TestSessionHandlerCreatesTheQuestionBankPartition(t *testing.T) {
	record := sampleSession()
	record.Kind = session.KindQuestionBank
	stub := &sessionStub{record: record}

	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions?kind=question_bank", "")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Kind != "question_bank" {
		t.Errorf("kind = %q, want question_bank", envelope.Data.Kind)
	}
	if len(stub.createdKinds) != 1 || stub.createdKinds[0] != session.KindQuestionBank {
		t.Errorf("created kinds = %v, want [question_bank]", stub.createdKinds)
	}
}

func TestSessionHandlerRefusesKindsItCannotCreate(t *testing.T) {
	// `all` is meaningful only for a listing; a create has to pick one side.
	for _, query := range []string{"?kind=all", "?kind=diary"} {
		t.Run(query, func(t *testing.T) {
			stub := &sessionStub{record: sampleSession()}
			recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions"+query, "")

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"invalid_query"`) {
				t.Errorf("body = %s, want invalid_query", recorder.Body.String())
			}
			if len(stub.createdKinds) != 0 {
				t.Errorf("a session was created for %s", query)
			}
		})
	}
}

func TestSessionHandlerListAsksForTheBinItWasAskedFor(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  session.ListOptions
	}{
		{
			name:  "no query lists live chat sessions",
			query: "",
			want:  session.ListOptions{Caller: handlerCaller(), Kind: session.KindChat},
		},
		{
			name:  "deleted lists the bin",
			query: "?deleted=true&limit=20&offset=40",
			want: session.ListOptions{
				Caller: handlerCaller(), Deleted: true, Kind: session.KindChat, Limit: 20, Offset: 40,
			},
		},
		{
			name:  "writing lists the other partition",
			query: "?kind=writing",
			want:  session.ListOptions{Caller: handlerCaller(), Kind: session.KindWriting},
		},
		{
			name:  "mastery lists its own partition",
			query: "?kind=mastery",
			want:  session.ListOptions{Caller: handlerCaller(), Kind: session.KindMastery},
		},
		{
			name:  "question_bank lists its own partition",
			query: "?kind=question_bank",
			want:  session.ListOptions{Caller: handlerCaller(), Kind: session.KindQuestionBank},
		},
		{
			// `all` is explicit and leaves Kind empty: a zero value still means
			// chat, so "every partition" has to be said out loud.
			name:  "all asks for every partition",
			query: "?kind=all",
			want:  session.ListOptions{Caller: handlerCaller(), AllKinds: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{listing: []session.Session{sampleSession()}}
			recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions"+test.query, "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.lists) != 1 || stub.lists[0] != test.want {
				t.Errorf("List() was called with %+v, want %+v", stub.lists, test.want)
			}
			// The paging the client asked for is echoed, so a client that pages
			// can tell which page it is looking at.
			var envelope struct {
				Data sessionListView `json:"data"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Data.Limit != test.want.Limit || envelope.Data.Offset != test.want.Offset {
				t.Errorf("data = %+v, want the paging that was asked for", envelope.Data)
			}
		})
	}
}

func TestSessionHandlerRefusesPagingItCannotRead(t *testing.T) {
	tests := []struct{ name, query string }{
		{name: "a limit that is not a number", query: "?limit=lots"},
		{name: "a negative offset", query: "?offset=-1"},
		{name: "a deleted flag that is not a boolean", query: "?deleted=perhaps"},
		{name: "a kind that does not exist", query: "?kind=diary"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{}
			recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions"+test.query, "")

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.lists) != 0 {
				t.Error("the listing was queried with paging that could not be read")
			}
		})
	}
}

func TestSessionHandlerShowsWhenASessionIsInTheBin(t *testing.T) {
	deleted := sampleSession()
	deleted.DeletedAt = handlerSessionTime
	stub := &sessionStub{record: deleted}

	recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions/sess_1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.DeletedAt == nil {
		t.Fatal("a session in the bin is reported as live")
	}
	if !envelope.Data.DeletedAt.Equal(handlerSessionTime) {
		t.Errorf("deleted_at = %v, want %v", envelope.Data.DeletedAt, handlerSessionTime)
	}
}

func TestSessionHandlerRenameCarriesTheTitle(t *testing.T) {
	stub := &sessionStub{record: sampleSession()}
	recorder := callAs(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.renamed == nil || stub.renamed.sessionID != "sess_1" || stub.renamed.title != "新标题" {
		t.Errorf("Rename() was called with %+v, want the session and its new title", stub.renamed)
	}
}

func TestSessionHandlerRefusesARenameItCannotRead(t *testing.T) {
	tests := []struct{ name, body string }{
		{name: "no body at all", body: ""},
		{name: "a body that is not JSON", body: "title=新标题"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{}
			recorder := callAs(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", test.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.renamed != nil {
				t.Error("a rename ran on a body that could not be read")
			}
		})
	}
}

func TestSessionHandlerRecycleBinRoutesAnswerWithNoContent(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "soft delete", method: http.MethodDelete, path: "/api/v1/sessions/sess_1"},
		{name: "permanent delete", method: http.MethodDelete, path: "/api/v1/sessions/sess_1/permanent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{}
			recorder := callAs(t, newSessionRouter(stub), test.method, test.path, "")

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.touched) != 1 || stub.touched[0] != "sess_1" {
				t.Errorf("the store was asked to touch %v, want the session that was named", stub.touched)
			}
		})
	}
}

func TestSessionHandlerRestoreReturnsTheRestoredSession(t *testing.T) {
	stub := &sessionStub{record: sampleSession()}
	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions/sess_1/restore", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.DeletedAt != nil {
		t.Error("the session is still reported as deleted after being restored")
	}
}

func TestSessionHandlerMessagesReturnsTheHistory(t *testing.T) {
	stub := &sessionStub{history: []session.Message{
		{ID: "msg_1", SessionID: "sess_1", Role: session.RoleUser, Content: "闭包是什么", CreatedAt: handlerSessionTime},
		{ID: "msg_2", SessionID: "sess_1", Role: session.RoleAssistant, Content: "闭包是……", Capability: "chat", CreatedAt: handlerSessionTime},
	}}
	recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions/sess_1/messages", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data messageListView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Messages) != 2 {
		t.Fatalf("messages = %+v, want both", envelope.Data.Messages)
	}
	// The order is the store's and is what a client renders, so it is passed
	// through rather than sorted here.
	if envelope.Data.Messages[0].MessageID != "msg_1" || envelope.Data.Messages[1].MessageID != "msg_2" {
		t.Errorf("messages = %+v, want them in the order they were stored", envelope.Data.Messages)
	}
	if envelope.Data.Messages[1].Capability != "chat" {
		t.Errorf("capability = %q, want what produced the message", envelope.Data.Messages[1].Capability)
	}
}

func TestSessionHandlerMapsWhatTheClientCanActOn(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{name: "a session that is not there", err: session.ErrNotFound, want: http.StatusNotFound, code: "not_found"},
		{name: "a title that cannot be read back", err: session.ErrInvalidTitle, want: http.StatusBadRequest, code: "invalid_title"},
		{
			name: "something only an operator can act on",
			err:  errors.New("private database details"),
			want: http.StatusInternalServerError,
			code: "rename_session_failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{err: test.err}
			recorder := callAs(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Error.Code != test.code {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, test.code)
			}
			// A failure the client cannot fix must not describe us to it.
			if strings.Contains(recorder.Body.String(), "private database details") {
				t.Errorf("the response repeats the internal error: %s", recorder.Body.String())
			}
		})
	}
}

func TestSessionHandlerWithoutAStoreFailsRatherThanPanics(t *testing.T) {
	var handler *SessionHandler
	recorder := callAs(t, func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/v1/sessions", handler.List)
		return router
	}(), http.MethodGet, "/api/v1/sessions", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEverySessionRouteNeedsAnAccount(t *testing.T) {
	// The routes are behind the token, and this is the half of that a handler
	// test can see: a request with no account is not served, and never reaches
	// the domain with an id nobody chose.
	for _, route := range sessionRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			stub := &sessionStub{}
			// No bearer header: what RequireAuth answers with, and whether the
			// domain was reached at all.
			recorder := call(t, newSessionRouter(stub), route.method, route.path, route.body)

			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("status = %d without a token, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.account.Valid() || len(stub.touched)+len(stub.claims) != 0 {
				t.Errorf("the domain was reached without an account: %+v", stub)
			}
		})
	}
}

func TestEverySessionRouteCarriesTheAccountsOwnIdentity(t *testing.T) {
	// The account is the token's and not the request's (AUTH-06): a query
	// parameter or a body field naming another account is not a filter this API
	// has. Each route is walked with a body that tries it.
	for _, route := range sessionRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			stub := &sessionStub{record: sampleSession()}
			body := route.body
			if body == "" {
				body = `{"owner_id":9999}`
			} else {
				body = strings.Replace(body, "{", `{"owner_id":9999,`, 1)
			}
			callAs(t, newSessionRouter(stub), route.method, route.path, body)

			if stub.account != handlerCaller() {
				t.Errorf("the domain was told %+v, want the token's account", stub.account)
			}
		})
	}
}

func TestSessionHandlerClaimHandsOverAnOwnerlessSession(t *testing.T) {
	// §2.5's exit: a session written before V5 belongs to nobody, and this is how
	// one becomes somebody's. The answer is the session rather than a 204,
	// because the row the caller now holds was not in the list they were looking
	// at a moment ago.
	stub := &sessionStub{record: sampleSession()}
	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions/sess_1/claim", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.claims) != 1 || stub.claims[0] != "sess_1" {
		t.Errorf("the domain was asked to claim %v, want the session in the path", stub.claims)
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.SessionID != "sess_1" {
		t.Errorf("data = %+v, want the session that was claimed", envelope.Data)
	}

	// A session that is somebody else's is not there to be claimed, and there is
	// no second answer for it: the same 404 an id that was never issued gets.
	// Saying "it exists and is not yours" would be telling one account about
	// another's row.
	denied := &sessionStub{err: fmt.Errorf("%w: sess_9", session.ErrNotFound)}
	recorder = callAs(t, newSessionRouter(denied), http.MethodPost, "/api/v1/sessions/sess_9/claim", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a session that cannot be claimed, want 404; body=%s",
			recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}
