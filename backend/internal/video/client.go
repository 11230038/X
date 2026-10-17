package video

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	searchPath = "/x/web-interface/wbi/search/type"
	navPath    = "/x/web-interface/nav"

	// The upstream serves a JSON API to anything that asks like a browser and
	// turns away some requests that ask like a script. These two headers are the
	// smallest part of that difference; the signature is the rest.
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	referer   = "https://www.bilibili.com/"
)

// Client is the search client. It holds no per-request state: the cache and the
// signing key are shared and both are safe for concurrent use.
type Client struct {
	baseURL string
	http    *http.Client
	search  *searchCache
	keys    *wbiKeyCache
}

// NewClient builds a client from validated options.
func NewClient(opts Options) (*Client, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The one hook every connection this client makes has to pass through; see
	// Options.Dial and §2.5.
	transport.DialContext = opts.Dial
	return &Client{
		baseURL: strings.TrimSuffix(strings.TrimSpace(opts.BaseURL), "/"),
		http:    &http.Client{Timeout: opts.Timeout, Transport: transport},
		search:  newSearchCache(opts.CacheTTL, time.Now),
		keys:    &wbiKeyCache{now: time.Now},
	}, nil
}

// Search answers one page for one keyword.
//
// A cache hit is a complete answer: the stored page is what the upstream said
// moments ago, and returning it without touching the network is the point of
// the cache. Failures are not stored.
func (c *Client) Search(ctx context.Context, query Query) (SearchResult, error) {
	text := strings.TrimSpace(query.Text)
	if text == "" || utf8.RuneCountInString(text) > MaxQueryRunes {
		return SearchResult{}, ErrInvalidQuery
	}
	page := query.Page
	if page == 0 {
		page = 1
	}
	if page < 1 || page > MaxPage {
		return SearchResult{}, ErrInvalidQuery
	}

	key := cacheKey(text, page)
	if hit, ok := c.search.get(key); ok {
		hit.Query = text
		return hit, nil
	}

	signingKey, err := c.signingKey(ctx)
	if err != nil {
		return SearchResult{}, err
	}

	params := url.Values{}
	params.Set("search_type", "video")
	params.Set("keyword", text)
	params.Set("page", strconv.Itoa(page))
	params.Set("wts", strconv.FormatInt(time.Now().Unix(), 10))

	endpoint := c.baseURL + searchPath + "?" + signQuery(params, signingKey)
	var envelope searchEnvelope
	if err := c.getJSON(ctx, endpoint, &envelope); err != nil {
		return SearchResult{}, err
	}
	if envelope.Code != 0 {
		return SearchResult{}, &HTTPError{
			StatusCode: http.StatusOK,
			Code:       envelope.Code,
			Message:    envelope.Message,
		}
	}

	result := SearchResult{
		Query: text,
		Page:  page,
		Items: make([]Result, 0, len(envelope.Data.Result)),
	}
	for _, entry := range envelope.Data.Result {
		// Search answers mix videos with live rooms, columns and users; this
		// endpoint serves videos, and an entry without a video id cannot be
		// embedded at all.
		if entry.Type != "video" || entry.BVID == "" {
			continue
		}
		result.Items = append(result.Items, normalise(entry))
	}
	result.HasMore = page < envelope.Data.NumPages && page < MaxPage

	c.search.put(key, result)
	return result, nil
}

// signingKey answers the pair-derived key, fetching the pair when the cached one
// has expired. Concurrent callers may each fetch once on a cold start; the
// duplicate is cheaper to write than a single-flight map.
func (c *Client) signingKey(ctx context.Context) (string, error) {
	if key, ok := c.keys.get(); ok {
		return key, nil
	}
	var envelope navEnvelope
	if err := c.getJSON(ctx, c.baseURL+navPath, &envelope); err != nil {
		return "", err
	}
	// The nav endpoint reports an unauthenticated caller with a non-zero code
	// and still returns the signing key. That is the documented shape of this
	// call, so the code is not consulted: what this client needs is the key.
	key, err := mixinKey(envelope.Data.WbiImg.ImgURL, envelope.Data.WbiImg.SubURL)
	if err != nil {
		return "", err
	}
	c.keys.put(key)
	return key, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("video: build request: %w", err)
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", referer)
	request.Header.Set("Accept", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return wrapContext(ctx, fmt.Errorf("video: call upstream: %w", err))
	}
	defer func() {
		_ = response.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil {
		return wrapContext(ctx, fmt.Errorf("video: read response: %w", err))
	}
	if len(raw) > maxResponseBodyBytes {
		return fmt.Errorf("video: response is larger than %d bytes", maxResponseBodyBytes)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return &HTTPError{StatusCode: response.StatusCode, Message: brief(raw)}
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("video: decode response: %w", err)
	}
	return nil
}

// brief keeps a failed response's first line, for the log. Risk control answers
// with a whole HTML page; the first two hundred characters say whether it was
// risk control without putting the page in a log line.
func brief(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	runes := []rune(text)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return text
}

// wrapContext prefers the context's own error so that callers can reliably tell
// cancellation apart from a transport failure.
func wrapContext(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

var highlightTags = regexp.MustCompile(`<[^>]*>`)

// cleanTitle removes the `<em class="keyword">` marks the site puts around the
// matched words, then decodes entities. Both are presentation, not title: a
// client that showed the raw string would show markup around the keyword the
// person just typed.
func cleanTitle(raw string) string {
	return strings.TrimSpace(html.UnescapeString(highlightTags.ReplaceAllString(raw, "")))
}

// secureURL makes a cover address one a page served over https may load.
func secureURL(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "//"):
		return "https:" + raw
	case strings.HasPrefix(raw, "http://"):
		return "https://" + strings.TrimPrefix(raw, "http://")
	default:
		return raw
	}
}

// parseDuration reads the site's `mm:ss` / `h:mm:ss` form into seconds. A value
// it cannot read becomes zero rather than dropping the result: the video is
// still playable without a duration, and one odd field should not empty a page.
func parseDuration(raw string) int64 {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return 0
	}
	var total int64
	for _, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || value < 0 {
			return 0
		}
		total = total*60 + value
	}
	return total
}

func normalise(entry searchEntry) Result {
	return Result{
		VideoID:         entry.BVID,
		Title:           cleanTitle(entry.Title),
		Author:          entry.Author,
		CoverURL:        secureURL(entry.Pic),
		DurationSeconds: parseDuration(entry.Duration),
		PlayCount:       entry.Play,
		PublishedAt:     entry.PubDate,
		WatchURL:        "https://www.bilibili.com/video/" + url.PathEscape(entry.BVID),
		EmbedURL:        "https://player.bilibili.com/player.html?bvid=" + url.QueryEscape(entry.BVID) + "&page=1&autoplay=0",
	}
}

// The wire shapes, kept private: what leaves this package is Result, not the
// site's spelling.

type searchEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		NumPages int           `json:"numPages"`
		Result   []searchEntry `json:"result"`
	} `json:"data"`
}

type searchEntry struct {
	Type     string `json:"type"`
	BVID     string `json:"bvid"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Pic      string `json:"pic"`
	Duration string `json:"duration"`
	Play     int64  `json:"play"`
	PubDate  int64  `json:"pubdate"`
}

type navEnvelope struct {
	Code int `json:"code"`
	Data struct {
		WbiImg struct {
			ImgURL string `json:"img_url"`
			SubURL string `json:"sub_url"`
		} `json:"wbi_img"`
	} `json:"data"`
}
