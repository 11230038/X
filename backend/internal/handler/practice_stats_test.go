package handler

import (
	"net/http"
	"testing"
	"time"

	"backend/internal/practice"
)

// TestPracticeHandlerStatsReadsTheWindow covers the two spellings of an instant
// and the bucket, and the fact that all three travel to the domain unexamined:
// widening a window to whole buckets is the domain's decision, and a handler
// that rounded here would round differently from the series it is answered with.
func TestPracticeHandlerStatsReadsTheWindow(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  practice.StatsQuery
	}{
		{name: "nothing named", query: "", want: practice.StatsQuery{}},
		{
			name:  "timestamps",
			query: "?from=2026-10-06T00:00:00Z&to=2026-10-13T00:00:00Z&bucket=day",
			want: practice.StatsQuery{
				From: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
				To:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), Bucket: practice.BucketDay,
			},
		},
		{
			name:  "bare dates",
			query: "?from=2026-09-01&to=2026-10-01&bucket=week",
			want: practice.StatsQuery{
				From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
				To:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Bucket: practice.BucketWeek,
			},
		},
		{
			name:  "one end only",
			query: "?from=2026-10-06",
			want:  practice.StatsQuery{From: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &practiceStub{}
			recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet,
				"/api/v1/practice/stats"+test.query, "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.statsAsked) != 1 {
				t.Fatalf("the history was asked for %d times, want once", len(stub.statsAsked))
			}
			got := stub.statsAsked[0]
			if !got.From.Equal(test.want.From) || !got.To.Equal(test.want.To) ||
				got.Bucket != test.want.Bucket {
				t.Errorf("the window reads %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestPracticeHandlerStatsRefusesAnInstantItCannotRead(t *testing.T) {
	stub := &practiceStub{}
	for _, query := range []string{"?from=yesterday", "?to=13/10/2026", "?from=2026-13-45"} {
		recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/stats"+query, "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q gave status %d, want 400; body=%s",
				query, recorder.Code, recorder.Body.String())
		}
		if !containsJSON(recorder.Body.String(), `"invalid_query"`) {
			t.Errorf("query %q reads %s", query, recorder.Body.String())
		}
	}
	if len(stub.statsAsked) != 0 {
		t.Error("a window that was refused reached the domain anyway")
	}
}

// TestPracticeHandlerStatsReportsTheWindowItCovered pins that the answer carries
// the window back. A window is widened to whole buckets, so the one that was
// asked for is not the one that was served, and a chart that trusted its own
// request would draw a half day as a whole one.
func TestPracticeHandlerStatsReportsTheWindowItCovered(t *testing.T) {
	stub := &practiceStub{stats: practice.Stats{
		From:   time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		To:     time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		Bucket: practice.BucketDay,
		Series: []practice.BucketStat{
			{Label: "2026-10-05", Reviews: 3, Correct: 2, Wrong: 1, Added: 1, Accuracy: 2.0 / 3.0},
		},
		Sources:    []practice.Distribution{{Name: "knowledge_base", Count: 2}, {Name: "", Count: 1}},
		Categories: []practice.Distribution{{Name: "数学", Count: 3}},
	}}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet,
		"/api/v1/practice/stats?from=2026-10-06&to=2026-10-12", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data statsView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if want := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC); !envelope.Data.From.Equal(want) {
		t.Errorf("the window starts at %v, want the bucket the request fell in, %v", envelope.Data.From, want)
	}
	if envelope.Data.Bucket != "day" {
		t.Errorf("the bucket reads %q", envelope.Data.Bucket)
	}
	if len(envelope.Data.Series) != 1 || envelope.Data.Series[0].Label != "2026-10-05" {
		t.Fatalf("the series reads %+v", envelope.Data.Series)
	}
	if got := envelope.Data.Series[0]; got.Reviews != 3 || got.Correct != 2 ||
		got.Wrong != 1 || got.Added != 1 {
		t.Errorf("the point reads %+v", got)
	}
	// The empty source name is a value and not a hole: it is every question that
	// was not written by a conversation.
	if len(envelope.Data.Sources) != 2 || envelope.Data.Sources[1].Name != "" {
		t.Errorf("the sources read %+v", envelope.Data.Sources)
	}
	if len(envelope.Data.Categories) != 1 || envelope.Data.Categories[0].Count != 3 {
		t.Errorf("the labels read %+v", envelope.Data.Categories)
	}
}

func TestPracticeHandlerStatsShowsAnEmptyHistoryAsEmptyLists(t *testing.T) {
	stub := &practiceStub{stats: practice.Stats{Bucket: practice.BucketDay}}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/stats", "")
	body := recorder.Body.String()
	for _, want := range []string{`"series":[]`, `"sources":[]`, `"categories":[]`} {
		if !containsJSON(body, want) {
			t.Errorf("a window with nothing in it is missing %s: %s", want, body)
		}
	}
}
