package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"backend/internal/httpx"
	"backend/internal/video"
	"github.com/gin-gonic/gin"
)

// VideoSearch is the video search as HTTP reaches it. It has one method because
// the feature has one: a keyword goes out, a page of results comes back, and the
// player itself belongs to the other site.
type VideoSearch interface {
	Search(ctx context.Context, query video.Query) (video.SearchResult, error)
}

// VideoHandler serves the search box behind the video learning page.
type VideoHandler struct {
	search VideoSearch
	logger *slog.Logger
}

// NewVideoHandler constructs the video HTTP handler.
func NewVideoHandler(search VideoSearch, logger *slog.Logger) *VideoHandler {
	return &VideoHandler{search: search, logger: logger}
}

func (h *VideoHandler) ready(c *gin.Context) bool {
	if h != nil && h.search != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "video_unavailable", "video search is not configured")
	return false
}

// Search answers one page for one keyword.
//
// A query that is empty, too long or outside the page range is the caller's to
// fix and is a 400. Everything the upstream did — a timeout, a refusal, a body
// this service could not read — is one answer, 503: the person cannot act on the
// difference, and telling them which failure it was would only describe somebody
// else's service.
func (h *VideoHandler) Search(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	text := strings.TrimSpace(c.Query("q"))
	switch {
	case text == "":
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", "q is required")
		return
	case utf8.RuneCountInString(text) > video.MaxQueryRunes:
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", "q must be at most 100 characters")
		return
	}
	page, err := requestedPage(c.Query("page"))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}

	result, err := h.search.Search(c.Request.Context(), video.Query{Text: text, Page: page})
	if err != nil {
		if errors.Is(err, video.ErrInvalidQuery) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_query", "the search query is not valid")
			return
		}
		if h.logger != nil {
			h.logger.Error("video search failed", "error", err, "query", text, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusServiceUnavailable, "video_search_unavailable", "the video search is unavailable")
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, videoSearchViewOf(result))
}

// requestedPage reads the one-based page, defaulting to the first. An absent
// parameter and an empty one are the same request; anything else has to be a
// number in the range both sides will ask for.
func requestedPage(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("page must be an integer")
	}
	if page < 1 || page > video.MaxPage {
		return 0, errors.New("page must be between 1 and 50")
	}
	return page, nil
}

type videoItemView struct {
	// Source is spelled out rather than implied: the page is built around one
	// site today, and a client that shows where a result came from should not
	// have to know that from the route.
	Source          string `json:"source"`
	VideoID         string `json:"video_id"`
	Title           string `json:"title"`
	Author          string `json:"author"`
	CoverURL        string `json:"cover_url"`
	DurationSeconds int64  `json:"duration_seconds"`
	PlayCount       int64  `json:"play_count"`
	PublishedAt     int64  `json:"published_at"`
	WatchURL        string `json:"watch_url"`
	EmbedURL        string `json:"embed_url"`
}

type videoSearchView struct {
	Query   string          `json:"query"`
	Page    int             `json:"page"`
	HasMore bool            `json:"has_more"`
	Items   []videoItemView `json:"items"`
}

func videoSearchViewOf(result video.SearchResult) videoSearchView {
	items := make([]videoItemView, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, videoItemView{
			Source:          "bilibili",
			VideoID:         item.VideoID,
			Title:           item.Title,
			Author:          item.Author,
			CoverURL:        item.CoverURL,
			DurationSeconds: item.DurationSeconds,
			PlayCount:       item.PlayCount,
			PublishedAt:     item.PublishedAt,
			WatchURL:        item.WatchURL,
			EmbedURL:        item.EmbedURL,
		})
	}
	return videoSearchView{
		Query:   result.Query,
		Page:    result.Page,
		HasMore: result.HasMore,
		Items:   items,
	}
}
