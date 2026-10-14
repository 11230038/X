package mastery

// A question put, an answer judged, or a learner moving past one — against a real
// seekdb.
//
// Two rules live here and both are storage claims. "At most one question is open
// on a path" is kept by the service in one transaction, because the reference
// implementation states it as a partial unique index and this engine has none, so
// what it is worth depends on whether the read and the write really happen
// together. And "a skip is not a wrong answer" is a claim about a table that must
// stay empty: the case counts the evidence rows rather than reading the verdict,
// because the verdict is what would be right and the rows are what the gate
// counts.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
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
	// attach to and says so rather than inventing one.
	if _, err := h.service.Grade(ctx, GradeRequest{
		PathID: "mp_1", Actor: actor, QuestionID: "qn_1", Result: ResultCorrect,
	}, h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("grading with nothing open: %v, want %v", err, ErrNotFound)
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
