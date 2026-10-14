package dashboard

// The aggregation, over the three ports.
//
// This is the one place in the build where a test is written against doubles and
// that is right, because there is nothing to prove about storage here: the
// dashboard owns no table (§2.12), writes nothing, and every question its cases
// ask — which categories an empty history produces, what makes a recommendation
// one that may be handed out, whether a source that fails fails the read — is
// answered by this package's own code. The ports were declared as interfaces for
// exactly this, and the domains behind them are pinned against a real seekdb in
// their own packages.

import (
	"backend/internal/authz"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/mastery"
	"backend/internal/notebook"
	"backend/internal/practice"
	"backend/internal/session"
)

var atNine = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// The three sources, each holding what the next case wants it to answer and
// remembering what it was asked.
type sessionsStub struct {
	rows []session.Session
	err  error
	opts session.ListOptions
}

func (s *sessionsStub) List(_ context.Context, options session.ListOptions) ([]session.Session, error) {
	s.opts = options
	return s.rows, s.err
}

type pathsStub struct {
	rows     []mastery.Summary
	insights map[string]mastery.Insight
	listErr  error
	readErr  map[string]error
	asked    []string
}

func (p *pathsStub) List(context.Context, mastery.ListRequest) ([]mastery.Summary, error) {
	return p.rows, p.listErr
}

func (p *pathsStub) Insight(_ context.Context, pathID string, _ authz.Caller) (mastery.Insight, error) {
	p.asked = append(p.asked, pathID)
	if err := p.readErr[pathID]; err != nil {
		return mastery.Insight{}, err
	}
	return p.insights[pathID], nil
}

type reviewsStub struct {
	rows  []practice.QueueItem
	err   error
	limit int
}

func (r *reviewsStub) Queue(_ context.Context, limit int, _ authz.Caller) ([]practice.QueueItem, error) {
	r.limit = limit
	return r.rows, r.err
}

// testCaller is the account every case reads for. The dashboard is one
// person's page, so there is exactly one of these and no case varies it.
var testCaller = authz.Caller{UserID: 1, Role: authz.RoleUser}

// dashboard builds a service over the three, with the limit a case chooses.
func dashboard(t *testing.T, limit int, s *sessionsStub, p *pathsStub, r *reviewsStub) *Service {
	t.Helper()
	service, err := NewService(Options{RecentLimit: limit, Sessions: s, Paths: p, Reviews: r})
	if err != nil {
		t.Fatalf("build the dashboard: %v", err)
	}
	return service
}

// empty is the three sources with nothing in them.
func empty() (*sessionsStub, *pathsStub, *reviewsStub) {
	return &sessionsStub{},
		&pathsStub{insights: map[string]mastery.Insight{}, readErr: map[string]error{}},
		&reviewsStub{}
}

// summary is one path as the list reports it.
func summary(pathID, goal string, mastered, points int, seen, written time.Time) mastery.Summary {
	return mastery.Summary{
		PathID: pathID, Goal: goal, Emoji: "🧭", Status: "active", Version: 1,
		Modules: 1, KnowledgePoints: points, Mastered: mastered,
		LastSeenAt: seen, UpdatedAt: written,
	}
}

// studying is one path's insight whose next move is a review of a knowledge
// point with three observations behind it.
func studying(pathID, pointID, name string) mastery.Insight {
	return mastery.Insight{
		Path:   mastery.Path{ID: pathID},
		Points: []mastery.PointInsight{{ID: pointID, Name: name, Level: mastery.LevelWeak}},
		Next: mastery.Step{
			Kind: mastery.StepReview, PathID: pathID, KnowledgePointID: pointID,
			Reason:      "「" + name + "」该复习了。",
			EvidenceIDs: []string{"mle_a", "mle_b", "mle_c"},
		},
	}
}

func TestReadingAPersonWithNothingGivesFourEmptyLists(t *testing.T) {
	sessions, paths, reviews := empty()
	view, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller)
	if err != nil {
		t.Fatalf("read the dashboard: %v", err)
	}
	// Empty and missing are different answers and the API makes only one of them
	// (§2.12 第 2 条): every list is a list, so a client never has to tell "nothing
	// here" from "not sent".
	if view.Sessions == nil || view.Paths == nil || view.Reviews == nil || view.Next == nil {
		t.Errorf("the empty view is %+v, want four empty lists and no nulls", view)
	}
	if len(view.Sessions)+len(view.Paths)+len(view.Reviews)+len(view.Next) != 0 {
		t.Errorf("the empty view carries %+v", view)
	}
}

func TestTheLimitIsTheSameWindowForEveryCategory(t *testing.T) {
	sessions, paths, reviews := empty()
	sessions.rows = []session.Session{
		{ID: "s_1", Title: "第一个", UpdatedAt: atNine},
		{ID: "s_2", Title: "第二个", UpdatedAt: atNine},
		{ID: "s_3", Title: "第三个", UpdatedAt: atNine},
	}
	for _, id := range []string{"mp_a", "mp_b", "mp_c"} {
		paths.rows = append(paths.rows, summary(id, "目标 "+id, 1, 3, atNine, time.Time{}))
		paths.insights[id] = studying(id, "kp_1", "闭包")
	}

	view, err := dashboard(t, 2, sessions, paths, reviews).Read(context.Background(), testCaller)
	if err != nil {
		t.Fatalf("read the dashboard: %v", err)
	}
	// The two reads that take a limit are handed the deployment's number rather
	// than a number this package chose.
	if sessions.opts.Limit != 2 || reviews.limit != 2 {
		t.Errorf("the sources were asked for %d and %d, want the limit of 2",
			sessions.opts.Limit, reviews.limit)
	}
	// The two the domains answer whole are cut here, to the same window: the page
	// is about the same few things top to bottom.
	if len(view.Paths) != 2 || len(view.Next) != 2 {
		t.Errorf("the listing carries %d paths and %d moves, want 2 of each",
			len(view.Paths), len(view.Next))
	}
	// A path the list cut is not recommended on, because the dashboard would then
	// be suggesting something it is not showing.
	for _, pathID := range paths.asked {
		if pathID == "mp_c" {
			t.Error("the third path was read for a recommendation the page does not show")
		}
	}
	// The sessions arrive already cut by the source, and this package passes them
	// through: it is not its limit to apply twice.
	if len(view.Sessions) != 3 {
		t.Errorf("the listing carries %d conversations, want the 3 the source returned", len(view.Sessions))
	}
}

func TestAnEntryThatCannotBeOpenedIsDroppedAndTheRestAreKept(t *testing.T) {
	sessions, paths, reviews := empty()
	sessions.rows = []session.Session{
		{ID: "", Title: "没有 id", UpdatedAt: atNine},
		{ID: "s_1", Title: "", UpdatedAt: atNine},
	}
	// A path nobody has touched has no last-seen time, and falls back to when it
	// was written: the two are one question, and a zero time would sort it before
	// everything.
	paths.rows = []mastery.Summary{
		{PathID: "", Goal: "没有 id"},
		summary("mp_a", "学会闭包", 2, 4, time.Time{}, atNine),
	}
	reviews.rows = []practice.QueueItem{
		{Question: notebook.Question{QuestionID: "", Text: "没有 id"}, Reason: practice.ReasonDue},
		{Question: notebook.Question{QuestionID: "qn_1", Text: "什么是闭包"}, Reason: practice.ReasonDue},
		{Question: notebook.Question{QuestionID: "qn_2", Text: "循环里的闭包"}, Reason: practice.ReasonNew},
		{Question: notebook.Question{QuestionID: "qn_3", Text: "第三种"}, Reason: practice.QueueReason("later")},
	}

	view, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller)
	if err != nil {
		t.Fatalf("read the dashboard: %v", err)
	}
	if len(view.Sessions) != 1 || view.Sessions[0].ID != "s_1" {
		t.Errorf("the conversations are %+v, want the one that can be opened", view.Sessions)
	}
	// A row with no title is still a row: what this package must not do is invent
	// one, so the entry is shown with the title the domain gave it — nothing.
	if view.Sessions[0].Title != "" {
		t.Errorf("the conversation was given the title %q", view.Sessions[0].Title)
	}
	if len(view.Paths) != 1 || view.Paths[0].ID != "mp_a" {
		t.Fatalf("the paths are %+v, want the one that can be opened", view.Paths)
	}
	if view.Paths[0].Context != "已掌握 2/4 个知识点" {
		t.Errorf("the path's context is %q", view.Paths[0].Context)
	}
	if !view.Paths[0].UpdatedAt.Equal(atNine) {
		t.Errorf("the path reads as updated %s, want the time it was written", view.Paths[0].UpdatedAt)
	}
	if len(view.Reviews) != 3 {
		t.Fatalf("the reviews are %+v, want the three that can be opened", view.Reviews)
	}
	if view.Reviews[0].Context != "到复习时间了" || view.Reviews[1].Context != "还没做过" {
		t.Errorf("the review contexts are %q and %q", view.Reviews[0].Context, view.Reviews[1].Context)
	}
	// A reason this package does not know renders as nothing rather than as the
	// domain's key: showing `later` raw would be showing a client the name of a
	// variable.
	if view.Reviews[2].Context != "" {
		t.Errorf("an unknown reason rendered as %q", view.Reviews[2].Context)
	}
}

func TestARecommendationWithoutGroundsIsNotShown(t *testing.T) {
	sessions, paths, reviews := empty()
	// Three paths whose next moves are three different things: one that cites what
	// it read, one that needs the tree drawn, and one about a knowledge point nobody
	// has tested. The last two are answers and not recommendations — their ground is
	// the absence of evidence, which COURSE-04 will not accept as grounds.
	paths.rows = []mastery.Summary{
		summary("mp_a", "学会闭包", 1, 3, atNine, time.Time{}),
		summary("mp_b", "还没画", 0, 0, atNine, time.Time{}),
		summary("mp_c", "没考过", 0, 3, atNine, time.Time{}),
	}
	paths.insights["mp_a"] = studying("mp_a", "kp_1", "闭包捕获的是变量")
	paths.insights["mp_b"] = mastery.Insight{
		Path: mastery.Path{ID: "mp_b"},
		Next: mastery.Step{Kind: mastery.StepMap, PathID: "mp_b", Reason: "这条路径还是空的。"},
	}
	paths.insights["mp_c"] = mastery.Insight{
		Path:   mastery.Path{ID: "mp_c"},
		Points: []mastery.PointInsight{{ID: "kp_2", Name: "循环里的闭包"}},
		Next: mastery.Step{
			Kind: mastery.StepLearn, PathID: "mp_c", KnowledgePointID: "kp_2",
			Reason: "「循环里的闭包」还没考过。",
		},
	}

	view, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller)
	if err != nil {
		t.Fatalf("read the dashboard: %v", err)
	}
	if len(view.Next) != 1 {
		t.Fatalf("the moves are %+v, want only the one with grounds", view.Next)
	}
	next := view.Next[0]
	if next.Kind != KindReview || next.PathID != "mp_a" || next.KnowledgePointID != "kp_1" {
		t.Errorf("the move is %+v", next)
	}
	// The title is the knowledge point's *name*, read off the insight the step came
	// with: an id is not something to draw.
	if next.Title != "闭包捕获的是变量" {
		t.Errorf("the move is titled %q, want the knowledge point's name", next.Title)
	}
	if len(next.EvidenceIDs) != 3 || next.Reason == "" {
		t.Errorf("the move does not carry its grounds: %+v", next)
	}
}

func TestAMoveInAWordThisPackageDoesNotKnowIsNotShown(t *testing.T) {
	sessions, paths, reviews := empty()
	paths.rows = []mastery.Summary{summary("mp_a", "学会闭包", 1, 3, atNine, time.Time{})}
	// A step kind from a newer build, with grounds: showing it under a guess would
	// be worse than not showing it, because the surface would draw the wrong verb.
	paths.insights["mp_a"] = mastery.Insight{
		Path:   mastery.Path{ID: "mp_a"},
		Points: []mastery.PointInsight{{ID: "kp_1", Name: "闭包"}},
		Next: mastery.Step{
			Kind: mastery.StepKind("watch_a_video"), PathID: "mp_a", KnowledgePointID: "kp_1",
			Reason: "看一段视频。", EvidenceIDs: []string{"mle_a"},
		},
	}

	view, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller)
	if err != nil {
		t.Fatalf("a step kind this build does not know failed the whole read: %v", err)
	}
	if len(view.Next) != 0 {
		t.Errorf("the moves are %+v, want none", view.Next)
	}
}

func TestAMoveThatCannotBeDrawnIsAnErrorRatherThanASilentDrop(t *testing.T) {
	sessions, paths, reviews := empty()
	paths.rows = []mastery.Summary{summary("mp_a", "学会闭包", 1, 3, atNine, time.Time{})}
	// Grounds and no sentence: a recommendation that cannot say what it stands on
	// is refused at the point it is built, which is where COURSE-04 asks for it.
	// Silently dropping it would be answering a malformed step with an empty page.
	paths.insights["mp_a"] = mastery.Insight{
		Path:   mastery.Path{ID: "mp_a"},
		Points: []mastery.PointInsight{{ID: "kp_1", Name: "闭包"}},
		Next: mastery.Step{
			Kind: mastery.StepReview, PathID: "mp_a", KnowledgePointID: "kp_1",
			EvidenceIDs: []string{"mle_a"},
		},
	}

	_, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller)
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("a move with no reason: %v, want %v", err, ErrInvalidEntry)
	}
	if !strings.Contains(err.Error(), "mp_a") {
		t.Errorf("the refusal reads %q, want it to name the path", err)
	}
}

func TestASourceThatWillNotAnswerFailsTheWholeRead(t *testing.T) {
	// Each of the three in turn. A dashboard that returned the categories it could
	// read would report somebody's history as shorter than it is, which is the one
	// thing a "where was I" view must not do (§2.12).
	broken := errors.New("the storage is not answering")
	cases := []struct {
		name      string
		breakIt   func(*sessionsStub, *pathsStub, *reviewsStub)
		wantInMsg string
	}{
		{"conversations", func(s *sessionsStub, _ *pathsStub, _ *reviewsStub) { s.err = broken }, "sessions"},
		{"paths", func(_ *sessionsStub, p *pathsStub, _ *reviewsStub) { p.listErr = broken }, "paths"},
		{"the queue", func(_ *sessionsStub, _ *pathsStub, r *reviewsStub) { r.err = broken }, "queue"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sessions, paths, reviews := empty()
			testCase.breakIt(sessions, paths, reviews)
			_, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller)
			if !errors.Is(err, broken) {
				t.Fatalf("reading with %s broken: %v, want the source's own error", testCase.name, err)
			}
			if !strings.Contains(err.Error(), testCase.wantInMsg) {
				t.Errorf("the refusal reads %q, want it to say which read failed", err)
			}
		})
	}

	// And a path whose insight will not answer fails it too: the recommendation is
	// part of the answer, and half a dashboard is not a shorter one.
	sessions, paths, reviews := empty()
	paths.rows = []mastery.Summary{summary("mp_a", "学会闭包", 1, 3, atNine, time.Time{})}
	paths.readErr["mp_a"] = broken
	if _, err := dashboard(t, 10, sessions, paths, reviews).Read(context.Background(), testCaller); !errors.Is(err, broken) {
		t.Fatalf("reading with a path's insight broken: %v, want the source's own error", err)
	}
}

func TestTheDashboardIsRefusedWithoutASourceOrALimit(t *testing.T) {
	sessions, paths, reviews := empty()
	cases := []struct {
		name    string
		options Options
	}{
		{"no limit", Options{Sessions: sessions, Paths: paths, Reviews: reviews}},
		{"a negative limit", Options{RecentLimit: -1, Sessions: sessions, Paths: paths, Reviews: reviews}},
		{"no conversation source", Options{RecentLimit: 10, Paths: paths, Reviews: reviews}},
		{"no mastery source", Options{RecentLimit: 10, Sessions: sessions, Reviews: reviews}},
		{"no queue source", Options{RecentLimit: 10, Sessions: sessions, Paths: paths}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewService(testCase.options); err == nil {
				t.Error("a dashboard was built without something it reads")
			}
		})
	}
}

func TestTheViewsOwnRules(t *testing.T) {
	// The rules the three types carry, pinned where they live: a kind this build
	// does not define, an entry with nothing to open, and a recommendation that
	// cannot show its grounds.
	if Kind("reading").Valid() || !KindLearn.Valid() || !KindReview.Valid() || !KindDone.Valid() {
		t.Error("the recommendation kinds are not the closed set they are meant to be")
	}
	if err := (Entry{ID: "  "}).Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("an entry that names nothing: %v, want %v", err, ErrInvalidEntry)
	}
	// A blank title is not a refusal: a conversation nobody has named yet carries
	// none, and drawing it empty is the truth.
	if err := (Entry{ID: "s_1"}).Validate(); err != nil {
		t.Errorf("an entry with no title: %v, want it shown", err)
	}
	whole := Recommendation{
		Kind: KindReview, PathID: "mp_a", KnowledgePointID: "kp_1", Title: "闭包",
		Reason: "该复习了。", EvidenceIDs: []string{"mle_a"},
	}
	if err := whole.Validate(); err != nil {
		t.Errorf("a recommendation with everything it needs: %v", err)
	}
	// A recommendation is about a path, and what it names inside the path is not
	// always a knowledge point: "finish this module" is a move about a path, so the
	// coordinate is allowed to be empty and only the four fields above are not.
	if err := (Recommendation{
		Kind: KindDone, PathID: "mp_a", Title: "这一章学完了", Reason: "只剩复盘。",
		EvidenceIDs: []string{"mle_a"},
	}).Validate(); err != nil {
		t.Errorf("a recommendation about a path rather than a knowledge point: %v", err)
	}
	for _, missing := range []Recommendation{
		{Kind: KindReview, PathID: "mp_a", Title: "闭包", Reason: "该复习了。"},
		{Kind: KindReview, PathID: "mp_a", Title: "闭包", EvidenceIDs: []string{"mle_a"}},
		{Kind: KindReview, PathID: "mp_a", Reason: "该复习了。", EvidenceIDs: []string{"mle_a"}},
		{Kind: KindReview, Title: "闭包", Reason: "该复习了。", EvidenceIDs: []string{"mle_a"}},
		{Kind: Kind("watch"), PathID: "mp_a", Title: "闭包", Reason: "该复习了。", EvidenceIDs: []string{"mle_a"}},
	} {
		if err := missing.Validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("a recommendation missing a field validated: %+v", missing)
		}
	}
}
