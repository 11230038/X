package video

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"backend/internal/outbound"
)

// testUpstream is the site as these cases need it: it answers the two paths the
// client calls, counts each, and lets a case decide what a search says. It is a
// test server on the loopback address, which is why the client under test dials
// through the LAN policy rather than the external one.
type testUpstream struct {
	body   string
	status int

	navCalls    int
	searchCalls int
	searchQuery url.Values
	searchPath  string
	searchVerb  string
}

func (u *testUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case navPath:
		u.navCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-101,"data":{"wbi_img":{` +
			`"img_url":"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",` +
			`"sub_url":"https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"}}}`))
	case searchPath:
		u.searchCalls++
		u.searchQuery = r.URL.Query()
		u.searchPath = r.URL.Path
		u.searchVerb = r.Method
		status := u.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(u.body))
	default:
		http.NotFound(w, r)
	}
}

func newTestClient(t *testing.T) (*Client, *testUpstream) {
	t.Helper()
	upstream := &testUpstream{}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	dial, err := outbound.DialContext(outbound.LANServicePolicy{})
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	client, err := NewClient(Options{
		BaseURL:  server.URL,
		Timeout:  2 * time.Second,
		CacheTTL: time.Minute,
		Dial:     dial,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client, upstream
}

const aSearchPage = `{"code":0,"data":{"numPages":3,"result":[
{"type":"video","bvid":"BV1xx411c7mD","title":"线性代数 <em class=\"keyword\">矩阵</em> &amp; 行列式",
 "author":"某老师","pic":"http://i0.hdslb.com/a.jpg","duration":"1:02:05","play":12345,"pubdate":1700000000},
{"type":"video","bvid":"BV2","title":"第二讲","author":"另一位",
 "pic":"//i0.hdslb.com/b.jpg","duration":"29:36","play":7,"pubdate":1700000001},
{"type":"live","title":"直播"},
{"type":"video","bvid":"","title":"没有视频 id"}
]}}`

func TestSearchNormalisesOnePageOfTheSitesResults(t *testing.T) {
	client, upstream := newTestClient(t)
	upstream.body = aSearchPage

	result, err := client.Search(context.Background(), Query{Text: "  线性代数 "})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if upstream.searchVerb != http.MethodGet || upstream.searchPath != searchPath {
		t.Errorf("upstream saw %s %s, want GET %s", upstream.searchVerb, upstream.searchPath, searchPath)
	}
	for key, want := range map[string]string{
		"search_type": "video",
		"keyword":     "线性代数",
		"page":        "1",
	} {
		if got := upstream.searchQuery.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if upstream.searchQuery.Get("wts") == "" || upstream.searchQuery.Get("w_rid") == "" {
		t.Errorf("query = %v, want a timestamp and a signature", upstream.searchQuery)
	}

	if result.Query != "线性代数" || result.Page != 1 || !result.HasMore {
		t.Errorf("result = %+v, want the trimmed query, page 1 and another page", result)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v, want the two videos and nothing else", result.Items)
	}

	first := result.Items[0]
	if first.VideoID != "BV1xx411c7mD" || first.Title != "线性代数 矩阵 & 行列式" {
		t.Errorf("first = %+v, want the id and the highlighted title cleaned", first)
	}
	if first.CoverURL != "https://i0.hdslb.com/a.jpg" {
		t.Errorf("cover = %q, want it upgraded to https", first.CoverURL)
	}
	if first.DurationSeconds != 3725 || first.PlayCount != 12345 || first.PublishedAt != 1700000000 {
		t.Errorf("first = %+v, want the duration and the numbers as they were sent", first)
	}
	// The two URLs are built from the id and from nothing else: a client is
	// never handed an address assembled out of a field it cannot see.
	if first.WatchURL != "https://www.bilibili.com/video/BV1xx411c7mD" {
		t.Errorf("watch URL = %q", first.WatchURL)
	}
	if first.EmbedURL != "https://player.bilibili.com/player.html?bvid=BV1xx411c7mD&page=1&autoplay=0" {
		t.Errorf("embed URL = %q", first.EmbedURL)
	}
	if second := result.Items[1]; second.CoverURL != "https://i0.hdslb.com/b.jpg" || second.DurationSeconds != 1776 {
		t.Errorf("second = %+v, want the protocol-relative cover upgraded and 29:36 read", second)
	}
	// The key is fetched once and reused; a page does not cost two round trips.
	if upstream.navCalls != 1 {
		t.Errorf("nav was called %d times, want once", upstream.navCalls)
	}
}

func TestSearchReusesAPageUntilTheCacheExpires(t *testing.T) {
	client, upstream := newTestClient(t)
	upstream.body = aSearchPage

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := client.Search(context.Background(), Query{Text: "linear algebra"}); err != nil {
			t.Fatalf("Search() error = %v", err)
		}
	}
	if upstream.searchCalls != 1 {
		t.Fatalf("the upstream was asked %d times, want the cached page on the second", upstream.searchCalls)
	}

	// A different page is a different request.
	if _, err := client.Search(context.Background(), Query{Text: "linear algebra", Page: 2}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if upstream.searchCalls != 2 {
		t.Errorf("the upstream was asked %d times, want page two fetched", upstream.searchCalls)
	}
}

func TestSearchReportsWhatTheUpstreamRefusedAsAnError(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		body    string
		wantErr bool
		code    int
	}{
		{name: "a non-zero envelope", body: `{"code":-412,"message":"risk control"}`, wantErr: true, code: -412},
		{name: "an HTTP refusal", status: http.StatusPreconditionFailed,
			body: "<html>risk control</html>", wantErr: true},
		{name: "a body that is not JSON", body: "not json", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, upstream := newTestClient(t)
			upstream.body, upstream.status = test.body, test.status

			_, err := client.Search(context.Background(), Query{Text: "algebra"})
			if !test.wantErr {
				if err != nil {
					t.Fatalf("Search() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Search() accepted a refused answer")
			}
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) {
				return // a body that would not decode is its own failure
			}
			if test.code != 0 && httpErr.Code != test.code {
				t.Errorf("HTTPError.Code = %d, want %d", httpErr.Code, test.code)
			}
			if test.status != 0 && httpErr.StatusCode != test.status {
				t.Errorf("HTTPError.StatusCode = %d, want %d", httpErr.StatusCode, test.status)
			}
		})
	}
}

func TestSearchRefusesAQueryItCannotServeWithoutAskingAnyone(t *testing.T) {
	client, upstream := newTestClient(t)
	long := strings.Repeat("词", MaxQueryRunes+1)

	for _, query := range []Query{
		{Text: ""},
		{Text: "   "},
		{Text: long},
		{Text: "algebra", Page: -1},
		{Text: "algebra", Page: MaxPage + 1},
	} {
		if _, err := client.Search(context.Background(), query); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("Search(%+v) error = %v, want ErrInvalidQuery", query, err)
		}
	}
	if upstream.searchCalls != 0 || upstream.navCalls != 0 {
		t.Errorf("the upstream was called %d times, want none", upstream.searchCalls+upstream.navCalls)
	}
}

func TestSearchRefusesAnAnswerLargerThanTheBound(t *testing.T) {
	client, upstream := newTestClient(t)
	upstream.body = strings.Repeat("x", maxResponseBodyBytes+1)

	if _, err := client.Search(context.Background(), Query{Text: "algebra"}); err == nil {
		t.Fatal("Search() accepted a response past the bound")
	}
}

func TestSearchReportsAKeyItCannotDerive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":-101,"data":{}}`))
	}))
	t.Cleanup(server.Close)
	dial, err := outbound.DialContext(outbound.LANServicePolicy{})
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	keyless, err := NewClient(Options{
		BaseURL: server.URL, Timeout: 2 * time.Second, CacheTTL: time.Minute, Dial: dial,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := keyless.Search(context.Background(), Query{Text: "algebra"}); err == nil {
		t.Fatal("Search() signed with a key the upstream did not give")
	}
}
