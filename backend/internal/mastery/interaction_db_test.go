package mastery

// A question put, an answer judged, or a learner moving past one — against a real
// seekdb.
//
// Two rules live here and both are storage claims. "At most one question is open
// on a path" is held by migration 00019's unique key over a generated column, so
// what the cases below watch is the loser of a race: it meets that key, and the
// insert is what turns the collision back into the refusal the service's own read
// gives. The read is the fast path for a second caller that is not racing, and it
// is not what makes the rule true. And "a skip is not a wrong answer" is a claim
// about a table that must stay empty: the case counts the evidence rows rather
// than reading the verdict, because the verdict is what would be right and the
// rows are what the gate counts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestDBAPathCarriesOneOpenQuestionAtATime(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	asked, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller)
	if err != nil {
		t.Fatalf("ask a question: %v", err)
	}
	if asked.KnowledgePointID != "kp_capture" || asked.QuestionID != "qn_1" {
		t.Errorf("the open interaction is %+v", asked)
	}
	if got := h.countRows(t, "mastery_interactions"); got != 1 {
		t.Fatalf("%d interactions are stored, want one", got)
	}
	// Open is the result column being NULL, which is the only way this table says
	// "still waiting" — and the only thing a second ask can be refused against.
	var open struct {
		Result *string `gorm:"column:result_json"`
	}
	if err := h.resource.GORM().Raw("SELECT result_json FROM mastery_interactions "+
		"WHERE interaction_id = ?", asked.ID).Scan(&open).Error; err != nil {
		t.Fatalf("read the stored interaction: %v", err)
	}
	if open.Result != nil {
		t.Errorf("a question that has been asked and not answered holds %q", *open.Result)
	}

	// A second question on the same path is refused, and the refusal carries the
	// question that is waiting: a model told "there is an open question (min_…)"
	// can pick it back up, while one told "conflict" can only start over.
	_, err = h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_loop", QuestionID: "qn_2",
	}, h.caller)
	if !errors.Is(err, ErrOpenInteraction) {
		t.Fatalf("asking twice: %v, want %v", err, ErrOpenInteraction)
	}
	var waiting *OpenInteractionError
	if !errors.As(err, &waiting) {
		t.Fatalf("the refusal is %T, want the open question carried with it", err)
	}
	if waiting.Interaction.ID != asked.ID || waiting.Interaction.KnowledgePointID != "kp_capture" {
		t.Errorf("the refusal names %+v, want the question that is waiting", waiting.Interaction)
	}
	if got := h.countRows(t, "mastery_interactions"); got != 1 {
		t.Errorf("%d interactions are stored, want the refusal to have written nothing", got)
	}

	// An interaction names a coordinate, and the coordinate has to be in the tree:
	// knowledge point ids are not a table and no foreign key can reach them, so this
	// is the only place it can be checked at all.
	if _, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_nobody", QuestionID: "qn_3",
	}, h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("asking about a knowledge point the path does not have: %v, want %v", err, ErrNotFound)
	}
}

func TestDBClosingAQuestionRecordsTheVerdictAgainstItsCoordinate(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	asked, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	graded, err := h.service.Grade(ctx, GradeRequest{
		PathID: "mp_1", Actor: actor, QuestionID: "qn_1", UserAnswer: "捕获的是变量",
		Result: ResultCorrect, AssessmentType: "comparison", Detail: []byte(`{"matched":"变量"}`),
	}, h.caller)
	if err != nil {
		t.Fatalf("grade: %v", err)
	}
	if graded.ID != asked.ID {
		t.Errorf("the grade reports interaction %s, want %s", graded.ID, asked.ID)
	}

	// The observation is filed against the coordinate the interaction carried, which
	// is what the coordinate is stored for: without it a verdict would be an
	// observation about nothing in particular.
	evidences, err := h.repository.Evidences(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the evidence: %v", err)
	}
	if len(evidences) != 1 {
		t.Fatalf("%d observations are stored, want one", len(evidences))
	}
	if evidences[0].KnowledgePoint != "kp_capture" || evidences[0].Result != ResultCorrect {
		t.Errorf("the observation is %+v", evidences[0])
	}
	if evidences[0].Source != SourceGradedAnswer || evidences[0].AssessmentType != "comparison" {
		t.Errorf("the observation says it came from %q/%q", evidences[0].Source, evidences[0].AssessmentType)
	}
	// The judge's own account is kept verbatim, and "verbatim" is judged by the
	// fields rather than by the bytes: it is stored inside a JSON column, and this
	// engine rewrites the bytes of a JSON document on the way in (whitespace comes
	// back as the engine's spacing, not the caller's). Comparing strings here would
	// be a case about that rewriting.
	var detail map[string]any
	if err := json.Unmarshal(evidences[0].Detail, &detail); err != nil {
		t.Fatalf("the verdict's own account came back as %s: %v", evidences[0].Detail, err)
	}
	if detail["matched"] != "变量" {
		t.Errorf("the verdict's own account came back as %v", detail)
	}

	// The answer is stored as it came and the row is closed, both of which are one
	// write: a question answered with nothing said about the learner is the case
	// this table exists to prevent.
	var closed struct {
		Answer  *string `gorm:"column:user_answer"`
		Outcome *string `gorm:"column:result_json"`
	}
	if err := h.resource.GORM().Raw("SELECT user_answer, result_json FROM mastery_interactions "+
		"WHERE interaction_id = ?", asked.ID).Scan(&closed).Error; err != nil {
		t.Fatalf("read the stored interaction: %v", err)
	}
	if closed.Answer == nil || *closed.Answer != "捕获的是变量" {
		t.Errorf("the stored answer is %v", closed.Answer)
	}
	if closed.Outcome == nil {
		t.Fatal("the interaction was not closed")
	}
	var outcome map[string]any
	if err := json.Unmarshal([]byte(*closed.Outcome), &outcome); err != nil {
		t.Fatalf("the outcome is not JSON: %v", err)
	}
	if outcome["result"] != string(ResultCorrect) || outcome["skipped"] != nil {
		t.Errorf("the outcome says %v", outcome)
	}

	// And the path is free: the next question can be asked.
	if _, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_loop", QuestionID: "qn_2",
	}, h.caller); err != nil {
		t.Errorf("asking after the first question was answered: %v", err)
	}

	// A verdict on a question this path did not ask is refused rather than recorded
	// against whatever is open: the answer would otherwise land on the wrong
	// coordinate with nothing to show it had.
	if _, err := h.service.Grade(ctx, GradeRequest{
		PathID: "mp_1", Actor: actor, QuestionID: "qn_1", Result: ResultCorrect,
	}, h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("grading a question that is not the open one: %v, want %v", err, ErrNotFound)
	}
	if got := h.countRows(t, "mastery_learning_evidences"); got != 1 {
		t.Errorf("%d observations are stored, want the refused verdict to have written none", got)
	}
}

func TestDBAVerdictThatLosesTheCloseRaceIsNotASecondObservation(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	asked, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	// The close is a compare-and-swap on the one thing that distinguishes an open
	// interaction from a closed one: of two calls that both decided to close the
	// same row, exactly one writes and the other is told it was not the one. The
	// loser gets a false rather than an error, because the row it wanted is in the
	// state it wanted it in.
	first, err := h.repository.CloseInteraction(ctx, asked.ID, "捕获的是变量",
		[]byte(`{"result":"correct"}`), atTen, h.caller)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !first {
		t.Fatal("the first close reported closing nothing")
	}
	second, err := h.repository.CloseInteraction(ctx, asked.ID, "另一个答案",
		[]byte(`{"result":"incorrect"}`), atEleven, h.caller)
	if err != nil {
		t.Fatalf("close again: %v", err)
	}
	if second {
		t.Error("the same interaction was closed twice")
	}
	// The winner's answer is the one that stands.
	var closed struct {
		Answer *string `gorm:"column:user_answer"`
	}
	if err := h.resource.GORM().Raw("SELECT user_answer FROM mastery_interactions "+
		"WHERE interaction_id = ?", asked.ID).Scan(&closed).Error; err != nil {
		t.Fatalf("read the stored interaction: %v", err)
	}
	if closed.Answer == nil || *closed.Answer != "捕获的是变量" {
		t.Errorf("the stored answer is %v, want the winner's", closed.Answer)
	}

	// The same refusal one level up: with nothing open, a verdict has no question to
	// attach to and says so rather than inventing one. It is the "no question
	// waiting" sentence rather than "not found", because the ordinary cause is a
	// question that closed when its time ran out, and the caller's next move is to
	// ask again.
	if _, err := h.service.Grade(ctx, GradeRequest{
		PathID: "mp_1", Actor: actor, QuestionID: "qn_1", Result: ResultCorrect,
	}, h.caller); !errors.Is(err, ErrNoOpenQuestion) {
		t.Errorf("grading with nothing open: %v, want %v", err, ErrNoOpenQuestion)
	}
}

func TestDBASkipEndsTheQuestionAndWritesNoObservation(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	asked, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if err := h.service.Skip(ctx, SkipRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", Reason: "先放着",
	}, h.caller); err != nil {
		t.Fatalf("skip: %v", err)
	}

	// The whole of the rule: a skip recorded as an observation would move the gate,
	// and the learner who said "先放着" would come back to find themselves marked bad
	// at something they had not been asked about.
	if got := h.countRows(t, "mastery_learning_evidences"); got != 0 {
		t.Errorf("%d observations are stored, want a skip to write none", got)
	}
	// And the knowledge point is still unknown rather than weak, which is the same
	// rule seen from the reading end.
	insight, err := h.service.Insight(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the insight: %v", err)
	}
	for _, point := range insight.Points {
		if point.ID == "kp_capture" && point.Level != LevelUnknown {
			t.Errorf("a skipped knowledge point reads as %q, want unknown", point.Level)
		}
	}
	if len(insight.Weak) != 0 {
		t.Errorf("a skip made %v weak", insight.Weak)
	}

	// The question it ended is closed, and closed as a skip: leaving the row open
	// would block the next question with a refusal about a question nobody is going
	// to answer.
	var closed struct {
		Outcome *string `gorm:"column:result_json"`
	}
	if err := h.resource.GORM().Raw("SELECT result_json FROM mastery_interactions "+
		"WHERE interaction_id = ?", asked.ID).Scan(&closed).Error; err != nil {
		t.Fatalf("read the stored interaction: %v", err)
	}
	if closed.Outcome == nil {
		t.Fatal("the skipped question was left open")
	}
	var outcome map[string]any
	if err := json.Unmarshal([]byte(*closed.Outcome), &outcome); err != nil {
		t.Fatalf("the outcome is not JSON: %v", err)
	}
	if outcome["skipped"] != true || outcome["reason"] != "先放着" {
		t.Errorf("the outcome says %v, want a skip with its reason", outcome)
	}
	// The history names the interaction this skip ended, so that "this moved past a
	// knowledge point" and "this ended the question that was open" are not two
	// readings of the same line.
	events, err := h.repository.Events(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	var skipped bool
	for _, event := range events {
		if event.Type != EventInteractionSkipped {
			continue
		}
		skipped = true
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("the skip's payload is not JSON: %v", err)
		}
		if payload["interaction_id"] != asked.ID {
			t.Errorf("the skip names %v, want the question it ended", payload["interaction_id"])
		}
	}
	if !skipped {
		t.Error("nothing in the history records the skip")
	}

	// A skip with nothing open is a plain move past a knowledge point: an event and
	// no interaction, and the payload says so by naming none.
	if err := h.service.Skip(ctx, SkipRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_loop",
	}, h.caller); err != nil {
		t.Fatalf("skip with nothing open: %v", err)
	}
	if got := h.countRows(t, "mastery_learning_evidences"); got != 0 {
		t.Errorf("%d observations are stored, want still none", got)
	}
}

// The rule above, under the one condition the service's read cannot survive:
// several asks that all read "nothing is open" before any of them has written.
//
// What refuses the second writer is not that read — under READ COMMITTED a read
// matching no row locks nothing, which is the hole 00009 measured on turns — but
// migration 00019's unique key. So what this case watches is the loser: it must
// be told the same thing the slow second caller is told, with the question that
// is waiting inside the error, rather than a duplicate-key failure arriving at
// the caller as a 500.
func TestDBOnlyOneOpenInteractionPerPathSurvivesConcurrency(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	const askers = 8
	type outcome struct {
		asked Interaction
		err   error
	}
	results := make([]outcome, askers)
	// The asks are held at the gate until every goroutine is running, so that they
	// overlap rather than queue behind each other's start-up.
	gate := make(chan struct{})
	var raised sync.WaitGroup
	for i := range askers {
		raised.Add(1)
		go func() {
			defer raised.Done()
			<-gate
			asked, err := h.service.Ask(ctx, AskRequest{
				PathID: "mp_1", Actor: actor,
				KnowledgePointID: "kp_capture", QuestionID: fmt.Sprintf("qn_%d", i),
			}, h.caller)
			results[i] = outcome{asked: asked, err: err}
		}()
	}
	close(gate)
	raised.Wait()

	var opened Interaction
	accepted := 0
	refused := 0
	for i, result := range results {
		if result.err == nil {
			accepted++
			opened = result.asked
			continue
		}
		if !errors.Is(result.err, ErrOpenInteraction) {
			t.Errorf("ask %d: %v, want the refusal about the question that is already open", i, result.err)
			continue
		}
		var waiting *OpenInteractionError
		if !errors.As(result.err, &waiting) {
			t.Errorf("ask %d: %v, want the open question carried with the refusal", i, result.err)
			continue
		}
		// The refusal is only worth carrying if it names the question that is
		// there: a model handed the id can pick that question back up.
		if waiting.Interaction.ID == "" || waiting.Interaction.QuestionID == "" {
			t.Errorf("ask %d: the refusal names %+v", i, waiting.Interaction)
		}
		refused++
	}
	if accepted != 1 {
		t.Fatalf("%d asks were accepted, want exactly one", accepted)
	}
	if refused != askers-1 {
		t.Fatalf("%d asks were refused as a race, want %d", refused, askers-1)
	}
	// Every refusal names the same interaction, and it is the one that opened:
	// there is one open question, not one per loser.
	for i, result := range results {
		if result.err == nil {
			continue
		}
		var waiting *OpenInteractionError
		if errors.As(result.err, &waiting) && waiting.Interaction.ID != opened.ID {
			t.Errorf("ask %d: the refusal names %s, want the question that opened: %s",
				i, waiting.Interaction.ID, opened.ID)
		}
	}
	// And the table holds one open row, which is the constraint seen from the
	// storage end rather than from the callers'.
	if got := h.countRows(t, "mastery_interactions"); got != 1 {
		t.Errorf("%d interactions are stored, want one", got)
	}
}

func TestDBAQuestionRowThatSaysNothingAboutWhatWasAskedIsRefused(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)

	// A row written by something other than this package: the coordinate lives in
	// the question column, and a grade that does not know which knowledge point it
	// is about would record evidence against nothing. Refusing it is better than
	// reading it as an interaction with no question.
	if err := h.resource.GORM().Exec("INSERT INTO mastery_interactions "+
		"(interaction_id, path_id, turn_id, session_id, question_json, creat_time) "+
		"VALUES ('min_blank', 'mp_1', 't_1', 's_1', NULL, ?)", atNine).Error; err != nil {
		t.Fatalf("write an interaction with no question: %v", err)
	}
	if _, err := h.repository.ActiveInteraction(context.Background(), "mp_1", h.caller); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("reading an interaction that records no question: %v, want %v", err, ErrInvalidRequest)
	}

	// And a path with no open question says so with the absence sentinel, which is
	// the ordinary state of a path nobody is being asked about.
	if err := h.resource.GORM().Exec("DELETE FROM mastery_interactions").Error; err != nil {
		t.Fatalf("clear the interactions: %v", err)
	}
	if _, err := h.repository.ActiveInteraction(context.Background(), "mp_1", h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading the open question of a path that has none: %v, want %v", err, ErrNotFound)
	}
}

// TestDBTheStateReadCarriesTheQuestionThatIsWaiting is the read a model makes
// before it does anything else on a path.
//
// While a question is open, every new one is refused and mastery_grade is the
// only way forward — keyed by the question id, which is nowhere in the tree. So
// the state has to carry it: without it the model is told a question waits and
// given no way to answer it, and the path stays blocked until the learner
// abandons the conversation.
func TestDBTheStateReadCarriesTheQuestionThatIsWaiting(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	view, err := h.service.Load(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("load a path with nothing waiting: %v", err)
	}
	if view.Open != nil {
		t.Errorf("a path nobody is being asked about reports %+v open", view.Open)
	}

	asked, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	view, err = h.service.Load(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("load a path with a question waiting: %v", err)
	}
	if view.Open == nil {
		t.Fatal("the state does not carry the open question")
	}
	if view.Open.ID != asked.ID || view.Open.QuestionID != "qn_1" ||
		view.Open.KnowledgePointID != "kp_capture" {
		t.Errorf("the state carries %+v, want the question that was asked", view.Open)
	}

	// Closed by a verdict, and the state stops reporting it. A read that kept
	// showing it would send the next turn to grade a question that is answered.
	if _, err := h.service.Grade(ctx, GradeRequest{
		PathID: "mp_1", Actor: actor, QuestionID: "qn_1", UserAnswer: "变量",
		Result: ResultCorrect, AssessmentType: "comparison", Detail: []byte(`{"matched":"变量"}`),
	}, h.caller); err != nil {
		t.Fatalf("grade: %v", err)
	}
	view, err = h.service.Load(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("load after the answer: %v", err)
	}
	if view.Open != nil {
		t.Errorf("an answered question is still reported as waiting: %+v", view.Open)
	}
}

// TestDBAnAbandonedQuestionClosesWhenItsTimeIsUp is the bound on "one open
// question per path". Without it a learner who walks away leaves a question that
// refuses every question after it, for as long as the path exists — the state
// the product actually reached, where the only way out was to answer a question
// the learner was no longer looking at.
//
// The close is lazy, like the lease's expiry: nothing runs on a timer, and the
// question ends the next time something needs the path. It is a real close and
// not a row a reader skips, because "one open question" is a unique key over the
// open rows — an expired row that was merely ignored would still refuse the
// next insert.
func TestDBAnAbandonedQuestionClosesWhenItsTimeIsUp(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	abandoned, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	// The harness's question ttl is one hour, and the clock is the only thing
	// this case has to move.
	h.clock.advance(time.Hour + time.Second)

	// The state stops reporting it, because no verdict will be accepted on it.
	view, err := h.service.Load(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Open != nil {
		t.Errorf("a question past its ttl is still reported as waiting: %+v", view.Open)
	}

	// And the next question is accepted, which is the point of the change: the
	// path must not be blocked by a question nobody is going to answer.
	next, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_loop", QuestionID: "qn_2",
	}, h.caller)
	if err != nil {
		t.Fatalf("the path is still blocked by the abandoned question: %v", err)
	}
	if next.QuestionID != "qn_2" {
		t.Errorf("the open question is %+v, want the one just asked", next)
	}

	// How the abandoned one ended is written where the endings go, and it is not
	// a verdict: the learner never answered, and the row says so instead of
	// carrying an empty result that would read as a wrong answer.
	var closed struct {
		Result     *string `gorm:"column:result_json"`
		UserAnswer *string `gorm:"column:user_answer"`
	}
	if err := h.resource.GORM().Raw("SELECT result_json, user_answer FROM mastery_interactions "+
		"WHERE interaction_id = ?", abandoned.ID).Scan(&closed).Error; err != nil {
		t.Fatalf("read the closed interaction: %v", err)
	}
	var outcome struct {
		Expired bool `json:"expired"`
	}
	if closed.Result == nil {
		t.Fatal("the abandoned question is still open")
	}
	if err := json.Unmarshal([]byte(*closed.Result), &outcome); err != nil || !outcome.Expired {
		t.Errorf("the abandoned question closed as %s, want an expiry", *closed.Result)
	}
	if closed.UserAnswer != nil {
		t.Errorf("the abandoned question holds the answer %q", *closed.UserAnswer)
	}

	// The history has its own kind of ending for it, so that "closed unanswered"
	// is a fact in the log and not only in the outcome column.
	var expired int64
	if err := h.resource.GORM().Raw("SELECT COUNT(*) FROM mastery_events WHERE event_type = ?",
		string(EventInteractionExpired)).Scan(&expired).Error; err != nil {
		t.Fatalf("count the expiry events: %v", err)
	}
	if expired != 1 {
		t.Errorf("%d expiry events are stored, want one", expired)
	}
}

// TestDBAnAnswerAfterTheQuestionClosedIsRefusedAsClosed is the late answer: the
// learner comes back after the ttl and writes what they would have written.
//
// It closes the question and records nothing. Evidence is about what the learner
// knows, and an answer nobody asked for and nobody waited for is not an
// observation of that — marking it right or wrong would move a level on the
// strength of a question that was already over. What the caller gets back is a
// sentence saying the question closed, because the model's next move is to ask
// again.
func TestDBAnAnswerAfterTheQuestionClosedIsRefusedAsClosed(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	if _, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_capture", QuestionID: "qn_1",
	}, h.caller); err != nil {
		t.Fatalf("ask: %v", err)
	}
	h.clock.advance(time.Hour + time.Second)

	_, err := h.service.Grade(ctx, GradeRequest{
		PathID: "mp_1", Actor: actor, QuestionID: "qn_1", UserAnswer: "变量",
		Result: ResultCorrect, AssessmentType: "comparison", Detail: []byte(`{"matched":"变量"}`),
	}, h.caller)
	if !errors.Is(err, ErrNoOpenQuestion) {
		t.Fatalf("grading a closed question: %v, want %v", err, ErrNoOpenQuestion)
	}
	if got := h.countRows(t, "mastery_learning_evidences"); got != 0 {
		t.Errorf("%d evidence rows were written for an answer nobody waited for", got)
	}

	// The refusal still settles the path: the close happened before the verdict
	// was refused, so the next question is allowed rather than refused by a row
	// that had already timed out.
	if _, err := h.service.Ask(ctx, AskRequest{
		PathID: "mp_1", Actor: actor, KnowledgePointID: "kp_loop", QuestionID: "qn_2",
	}, h.caller); err != nil {
		t.Errorf("the path is still blocked after a late answer: %v", err)
	}
}
