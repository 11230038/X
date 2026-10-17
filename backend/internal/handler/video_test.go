package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"backend/internal/video"
	"github.com/gin-gonic/gin"
)

// videoSearchStub is the search as the handler reaches it: it records what it
// was asked and answers what the case decided.
type videoSearchStub struct {
	result  video.SearchResult
	err     error
	queries []video.Query
}

func (s *videoSearchStub) Search(_ context.Context, query video.Query) (video.SearchResult, error) {
	s.queries = append(s.queries, query)
	return s.result, s.err
}

func newVideoRouter(search VideoSearch) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewVideoHandler(search, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/video/search", guard(), handler.Search)
	return router
}

type aVideoFailure struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestVideoHandlerAnswersAPageWithSnakeCaseFields(t *testing.T) {
	stub := &videoSearchStub{result: video.SearchResult{
		Query: "线性代数", Page: 2, HasMore: true,
		Items: []video.Result{{
			VideoID: "BV1xx411c7mD", Title: "线性代数 第一讲", Author: "某老师",
			CoverURL: "https://i0.hdslb.com/a.jpg", DurationSeconds: 3725,
			PlayCount: 12345, PublishedAt: 1700000000,
			WatchURL: "https://www.bilibili.com/video/BV1xx411c7mD",
			EmbedURL: "https://player.bilibili.com/player.html?bvid=BV1xx411c7mD&page=1&autoplay=0",
		}},
	}}
	recorder := callAs(t, newVideoRouter(stub), http.MethodGet,
		"/api/v1/video/search?q=%E7%BA%BF%E6%80%A7%E4%BB%A3%E6%95%B0&page=2", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.queries) != 1 || stub.queries[0] != (video.Query{Text: "线性代数", Page: 2}) {
		t.Fatalf("Search() was given %+v, want the trimmed query and the page", stub.queries)
	}
	var envelope struct {
		Data videoSearchView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	view := envelope.Data
	if view.Query != "线性代数" || view.Page != 2 || !view.HasMore || len(view.Items) != 1 {
		t.Fatalf("data = %+v, want the page as the domain answered it", view)
	}
	item := view.Items[0]
	if item.Source != "bilibili" || item.VideoID != "BV1xx411c7mD" ||
		item.DurationSeconds != 3725 || item.EmbedURL == "" {
		t.Errorf("item = %+v, want the result with its source spelled out", item)
	}
	// The wire says snake_case; the domain's spelling stays on this side.
	for _, want := range []string{`"video_id"`, `"cover_url"`, `"duration_seconds"`,
		`"play_count"`, `"published_at"`, `"watch_url"`, `"embed_url"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
		}
	}
}

func TestVideoHandlerDefaultsToTheFirstPage(t *testing.T) {
	stub := &videoSearchStub{result: video.SearchResult{Query: "algebra", Page: 1}}
	recorder := callAs(t, newVideoRouter(stub), http.MethodGet, "/api/v1/video/search?q=+algebra+", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := stub.queries[0]; got != (video.Query{Text: "algebra", Page: 1}) {
		t.Errorf("Search() was given %+v, want the trimmed query on page one", got)
	}
}

func TestVideoHandlerAnswersAnEmptyPageAsAnEmptyList(t *testing.T) {
	stub := &videoSearchStub{result: video.SearchResult{Query: "nothing", Page: 1}}
	recorder := callAs(t, newVideoRouter(stub), http.MethodGet, "/api/v1/video/search?q=nothing", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// An empty list rather than null, for the reason every other list here does:
	// the client should not have to tell the two apart.
	if body := recorder.Body.String(); !strings.Contains(body, `"items":[]`) {
		t.Errorf("body = %s, want an empty list", body)
	}
}

func TestVideoHandlerRefusesAQueryItCannotSearch(t *testing.T) {
	long := strings.Repeat("词", video.MaxQueryRunes+1)
	for _, test := range []struct{ name, query string }{
		{"no query", "?"},
		{"a blank query", "?q=+"},
		{"a query past the bound", "?q=" + long},
		{"a page of zero", "?q=algebra&page=0"},
		{"a page past the bound", "?q=algebra&page=51"},
		{"a page that is not a number", "?q=algebra&page=first"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &videoSearchStub{}
			recorder := callAs(t, newVideoRouter(stub), http.MethodGet, "/api/v1/video/search"+test.query, "")

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			var envelope aVideoFailure
			decodeBody(t, recorder, &envelope)
			if envelope.Error.Code != "invalid_query" {
				t.Errorf("code = %q, want invalid_query", envelope.Error.Code)
			}
			if len(stub.queries) != 0 {
				t.Errorf("Search() was called with %+v, want a bad query stopped here", stub.queries)
			}
		})
	}
}

func TestVideoHandlerReportsAnUpstreamFailureAsUnavailable(t *testing.T) {
	stub := &videoSearchStub{err: &video.HTTPError{StatusCode: 412, Message: "risk control"}}
	recorder := callAs(t, newVideoRouter(stub), http.MethodGet, "/api/v1/video/search?q=algebra", "")

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope aVideoFailure
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "video_search_unavailable" {
		t.Errorf("code = %q, want video_search_unavailable", envelope.Error.Code)
	}
	// Which failure it was is for the log: the person cannot act on the
	// difference, and the upstream's words are not this service's to repeat.
	if strings.Contains(recorder.Body.String(), "risk control") {
		t.Errorf("the response repeats the upstream: %s", recorder.Body.String())
	}
}

func TestVideoHandlerStopsAQueryTheDomainRefuses(t *testing.T) {
	stub := &videoSearchStub{err: video.ErrInvalidQuery}
	recorder := callAs(t, newVideoRouter(stub), http.MethodGet, "/api/v1/video/search?q=algebra", "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope aVideoFailure
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "invalid_query" {
		t.Errorf("code = %q, want invalid_query", envelope.Error.Code)
	}
}

func TestVideoHandlerWithoutASearchFailsRatherThanPanics(t *testing.T) {
	var handler *VideoHandler
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/video/search", guard(), handler.Search)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/video/search?q=algebra", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope aVideoFailure
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "video_unavailable" {
		t.Errorf("code = %q, want video_unavailable", envelope.Error.Code)
	}
}

func TestVideoHandlerKeepsTheDomainFailureOutOfTheAnswer(t *testing.T) {
	stub := &videoSearchStub{err: errors.New("the site answered with something odd")}
	recorder := callAs(t, newVideoRouter(stub), http.MethodGet, "/api/v1/video/search?q=algebra", "")

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "something odd") {
		t.Errorf("the response carries the failure: %s", recorder.Body.String())
	}
}
