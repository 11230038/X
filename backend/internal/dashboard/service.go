package dashboard

import (
	"context"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/mastery"
	"backend/internal/practice"
	"backend/internal/session"
)

// What the dashboard reads, declared here rather than imported as the services
// themselves.
//
// Each is one method of one domain, and the interface is written at the place
// that consumes it for the reason every other consumer in this build does it:
// what a reader needs is a fact about the reader. The concrete services satisfy
// these as they stand, and a test about the aggregation can satisfy them without
// a database.
//
// There is no port ceremony here — no assembly-time injection to break a cycle —
// because there is no cycle to break: nothing in this build depends on the
// dashboard (§1).
type Sessions interface {
	List(context.Context, session.ListOptions) ([]session.Session, error)
}

// Paths is the mastery domain as a reader of it. It is two calls and not one
// because the list and the next move answer different questions, and a listing
// that had to assemble every path's insight to draw itself would do the work of
// ten insights to show ten rows.
//
// The two carry the caller differently, and that is the domains' decision rather
// than this package's: the listing already has a request struct that carries what
// it is narrowed by, and the insight is one id and one account, which is a
// parameter. Both are the caller's own — the dashboard is one person's page, and
// there is no version of it that reads somebody else's (§2.12).
type Paths interface {
	List(context.Context, mastery.ListRequest) ([]mastery.Summary, error)
	Insight(context.Context, string, authz.Caller) (mastery.Insight, error)
}

// Reviews is the practice queue as a reader of it: the questions a sitting
// would ask, in the order it would ask them.
type Reviews interface {
	Queue(context.Context, int, authz.Caller) ([]practice.QueueItem, error)
}

// Options is what the dashboard is built from.
type Options struct {
	// RecentLimit is how many entries each category carries, and how far the
	// recommendations look. It is the deployment's number (§5).
	RecentLimit int
	Sessions    Sessions
	Paths       Paths
	Reviews     Reviews
}

// Service reads the dashboard.
type Service struct {
	limit    int
	sessions Sessions
	paths    Paths
	reviews  Reviews
}

// NewService constructs the dashboard over the domains it reads.
//
// Every source is required, and that is a decision: a dashboard built without
// one would answer with a category that is always empty, which is
// indistinguishable from a person who has nothing there. A missing source is a
// wiring mistake and it is refused where it is made.
func NewService(options Options) (*Service, error) {
	if options.RecentLimit <= 0 {
		return nil, fmt.Errorf("dashboard: the recent limit must be positive")
	}
	if options.Sessions == nil {
		return nil, fmt.Errorf("dashboard: no session source")
	}
	if options.Paths == nil {
		return nil, fmt.Errorf("dashboard: no mastery source")
	}
	if options.Reviews == nil {
		return nil, fmt.Errorf("dashboard: no review source")
	}
	return &Service{
		limit:    options.RecentLimit,
		sessions: options.Sessions,
		paths:    options.Paths,
		reviews:  options.Reviews,
	}, nil
}

// Read assembles the dashboard.
//
// The three category reads go in order and each one that fails fails the whole
// request, which is the rule §2.12 states: a read that will not answer is an
// error, and an empty category is not. A dashboard that returned the categories
// it could read and quietly dropped the one that failed would be reporting a
// person's history as shorter than it is, which is the one thing a "where was I"
// view must not do.
func (s *Service) Read(ctx context.Context, caller authz.Caller) (View, error) {
	sessions, err := s.sessions.List(ctx, session.ListOptions{Limit: s.limit, Caller: caller})
	if err != nil {
		return View{}, fmt.Errorf("read recent sessions: %w", err)
	}
	paths, err := s.paths.List(ctx, mastery.ListRequest{Caller: caller})
	if err != nil {
		return View{}, fmt.Errorf("read mastery paths: %w", err)
	}
	reviews, err := s.reviews.Queue(ctx, s.limit, caller)
	if err != nil {
		return View{}, fmt.Errorf("read the review queue: %w", err)
	}

	view := View{
		Sessions: sessionEntries(sessions),
		Paths:    pathEntries(paths, s.limit),
		Reviews:  reviewEntries(reviews),
	}
	next, err := s.recommendations(ctx, paths, caller)
	if err != nil {
		return View{}, err
	}
	view.Next = next
	return view, nil
}

// recommendations turns the paths' next moves into the moves worth showing.
//
// Two rules shape this, and both come from the domains rather than from here:
//
//   - a move whose grounds are empty is not shown. The mastery path's next step
//     cites evidence for a review and for a path that is finished, and cites
//     none for a tree that has not been drawn or a knowledge point nobody has
//     tested — because in those two cases the ground *is* the absence. COURSE-04
//     refuses a recommendation that cannot show what it read, so those two kinds
//     are not recommendations. They are still answers, and the path's own
//     insight endpoint gives them: what a path that has not been started needs
//     is not a suggestion from a dashboard, it is the tree.
//   - a path the list did not show is not recommended on. The categories and the
//     recommendations are cut to the same window, so the page is about the same
//     few things top to bottom.
func (s *Service) recommendations(ctx context.Context, paths []mastery.Summary, caller authz.Caller) ([]Recommendation, error) {
	next := make([]Recommendation, 0, s.limit)
	for _, summary := range paths {
		if len(next) == s.limit {
			break
		}
		insight, err := s.paths.Insight(ctx, summary.PathID, caller)
		if err != nil {
			return nil, fmt.Errorf("read the insight of path %s: %w", summary.PathID, err)
		}
		recommendation, ok, err := toRecommendation(insight)
		if err != nil {
			return nil, err
		}
		if ok {
			next = append(next, recommendation)
		}
	}
	return next, nil
}

// toRecommendation maps one path's next step onto a recommendation, and reports
// whether the step is one to show.
//
// The false is the whole rule of the function and it is not an error: a step
// grounded in nothing is a step this package does not show, and it is the same
// shape as the mastery path's own "the tree is empty" answer rather than a
// defect. What *is* an error is a step this function builds that fails to
// validate, which cannot happen from a step that cited evidence — and is checked
// anyway, at the point of construction, where COURSE-04 asks for it.
func toRecommendation(insight mastery.Insight) (Recommendation, bool, error) {
	step := insight.Next
	if len(step.EvidenceIDs) == 0 {
		return Recommendation{}, false, nil
	}
	kind, ok := kinds[step.Kind]
	if !ok {
		// A step kind this package has no word for is a step kind from a newer
		// build. Showing it under a guess would be worse than not showing it.
		return Recommendation{}, false, nil
	}
	name := step.KnowledgePointID
	for _, point := range insight.Points {
		if point.ID == step.KnowledgePointID {
			name = point.Name
			break
		}
	}
	recommendation, err := build(Recommendation{
		Kind:             kind,
		PathID:           step.PathID,
		KnowledgePointID: step.KnowledgePointID,
		Title:            name,
		Reason:           step.Reason,
		EvidenceIDs:      step.EvidenceIDs,
	})
	if err != nil {
		return Recommendation{}, false, err
	}
	return recommendation, true, nil
}

// kinds is the vocabulary the two packages share. The mastery path names a move
// by what the rule decided (StepKind); the dashboard names it by what a person
// is being asked to do, and the two are the same words — which is why this is a
// table and not a translation.
var kinds = map[mastery.StepKind]Kind{
	mastery.StepLearn:  KindLearn,
	mastery.StepReview: KindReview,
	mastery.StepDone:   KindDone,
}

func sessionEntries(sessions []session.Session) []Entry {
	entries := make([]Entry, 0, len(sessions))
	for _, record := range sessions {
		entry := Entry{ID: record.ID, Title: record.Title, UpdatedAt: record.UpdatedAt}
		// A row with no id cannot be opened, so it is dropped rather than drawn
		// (Entry.Validate). It is not an error: the source is what knows why it
		// had such a row, and the dashboard's answer to a row it cannot show is
		// to show the rest.
		if entry.Validate() == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// pathEntries draws the paths, cut to the same window the recommendations use.
func pathEntries(paths []mastery.Summary, limit int) []Entry {
	if len(paths) > limit {
		paths = paths[:limit]
	}
	entries := make([]Entry, 0, len(paths))
	for _, summary := range paths {
		entry := Entry{
			ID:    summary.PathID,
			Title: summary.Goal,
			// What the tree is made of and how much of it is known, which is what
			// tells two paths with similar goals apart. The same two numbers also
			// travel as a ratio, for a surface that draws a bar rather than a
			// sentence.
			Context:  fmt.Sprintf("已掌握 %d/%d 个知识点", summary.Mastered, summary.KnowledgePoints),
			Progress: &Progress{Done: summary.Mastered, Total: summary.KnowledgePoints},
			// When it was last worked on, falling back to when it was written for
			// a path no conversation has touched: the two are one question, and a
			// zero time would sort it before everything.
			UpdatedAt: seenOrWritten(summary.LastSeenAt, summary.UpdatedAt),
		}
		if entry.Validate() == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

func reviewEntries(items []practice.QueueItem) []Entry {
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		entry := Entry{
			ID:      item.Question.QuestionID,
			Title:   item.Question.Text,
			Context: reviewContext(item.Reason),
		}
		if entry.Validate() == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// reviewContext is why a question is in the queue, in the words a person reads.
//
// It is written here rather than taken from the domain's value because the
// domain's value is a key (`due`, `new`) that selects behaviour, and a page that
// showed it raw would be showing a client the name of a variable. A reason this
// package does not know renders as nothing: a question whose *why* cannot be
// said is still a question to answer.
func reviewContext(reason practice.QueueReason) string {
	switch reason {
	case practice.ReasonDue:
		return "到复习时间了"
	case practice.ReasonNew:
		return "还没做过"
	default:
		return ""
	}
}

// seenOrWritten is when a path was last worked on, or when it was written if it
// never was.
func seenOrWritten(seen, written time.Time) time.Time {
	if seen.IsZero() {
		return written
	}
	return seen
}
