// Package practice owns what happens after a question has been answered:
// judging the answer, deciding when the question is due again, and choosing
// what to put in front of the user next.
//
// It reads the question bank rather than owning it. A question is addressed by
// its content and stored by internal/notebook; this package asks for one and
// judges an answer to it, and the two are separate jobs because the bank is
// written by the conversation that generated the question and this is written
// by the review that answered it.
package practice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"backend/internal/llm"
	"backend/internal/notebook"
)

var (
	// ErrJudgeUnavailable reports a judgement that could not be produced: the
	// provider was unreachable, or what it replied cannot be read as a
	// judgement. It is retryable, and it is deliberately not a wrong answer —
	// "this could not be judged" and "this is wrong" are different things to
	// tell someone who is studying, and a retry is only the right advice for one
	// of them.
	ErrJudgeUnavailable = errors.New("practice: the judgement is unavailable")
	// ErrUnusableQuestion reports a stored question that cannot be judged as it
	// stands: a choice question whose answer names an option it does not offer,
	// a fill-in question with nothing to compare against. The fault is in the
	// bank rather than in the answer, so retrying is pointless and the caller
	// should say what is wrong with the question instead of blaming the answer.
	ErrUnusableQuestion = errors.New("practice: the question cannot be judged")
)

// judgeTemperature is the temperature every judging call is made at.
//
// It is not tuned for accuracy; it is tuned against variance. Someone who
// submits the same answer twice and is told two different things stops
// believing either verdict, so the judge is asked to be reproducible rather
// than creative — which is also why it is written here rather than configured:
// it is a property of judging, not a deployment's preference.
//
// It is a constant in code and not a key in the capability configuration JSON,
// because that JSON is validated against the names of registered capabilities.
// Judging is not one — it is a synchronous call inside a REST request, not a
// conversation turn — and a key naming an unregistered capability stops the
// service from starting.
const judgeTemperature = 0.2

// Kind says who judged an answer.
//
// It is the machine-readable half of CAP-07. The surface reads it to decide
// whether to mark the verdict as a model's opinion, which is a thing the design
// asks for in words; and it is a field rather than a convention about the
// reason text, because the surface must not have to read prose to find out
// whether the prose is an opinion.
//
// There are three authors and not two: the bank's stored answer, a model, and
// the person answering. The third is the one that looks like it could be folded
// into either and cannot — nobody compared anything and no model was asked.
type Kind string

const (
	// KindObjective is a comparison against the answer the question stores. No
	// model was involved and the verdict is not an opinion — which is the side
	// of CAP-07 that is easy to miss: a certain answer must not be dressed up
	// as a model judgement either.
	KindObjective Kind = "objective"
	// KindModelJudgment is a model reading an answer against the question's
	// explanation. It is a judgement, and the surface says so.
	KindModelJudgment Kind = "model_judgment"
	// KindSelfReport is the person answering declaring that they do not know.
	// Nothing was read and nothing was compared: the marker already schedules
	// the question as wrong, and asking either branch to look at an answer that
	// was never given would spend a call to learn what was just said out loud.
	KindSelfReport Kind = "self_report"
)

// Judgment is one answer judged once.
//
// The names of these fields are a wire shape and not an implementation detail:
// the value is stored verbatim in the review event's outcome_json and handed
// back unchanged when the same submission arrives again, so renaming a field is
// a data migration.
//
// The two branches fill it differently, and the empty half is the honest value
// rather than an omission. An objective judgement has no rubric — a rubric is
// the list of points a judge looks for, and a comparison has no partial credit
// to look for — and no feedback, because the question's own explanation is what
// the reader wants next and the caller already holds it. Its Confidence is nil
// and its Model is nil; a caller that needs to know who judged reads the second
// of those rather than parsing the reason.
type Judgment struct {
	Kind      Kind `json:"kind"`
	IsCorrect bool `json:"is_correct"`
	// Reason says why. A model judgement points at the fragments of the question
	// and the answer it turned on; an objective one names both answers, because
	// the comparison is the whole of its reason.
	Reason string `json:"reason"`
	// Rubric is the list of points the judgement was made against, taken from
	// the question's explanation. It is never nil — an empty list is a judgement
	// made against nothing in particular, and a reader should not have to tell
	// that apart from a field the writer forgot.
	Rubric     []string `json:"rubric"`
	Feedback   string   `json:"feedback"`
	Suggestion string   `json:"suggestion"`
	// Confidence is the model's own estimate from 0 to 1. It is nil when no
	// model judged: nobody's certainty is not a certainty of zero.
	Confidence *float64 `json:"confidence"`
	// Boundary says what the judgement cannot decide. AIQ-05 asks the model for
	// it, and the objective branch states it too — "the answer comes from the
	// bank, not from a model" is exactly the boundary a reader needs in order to
	// know how much to trust the verdict.
	Boundary string `json:"boundary"`
	// Model names the model that judged, or is nil when none did.
	Model *string `json:"model"`
}

// Judge judges answers. It holds one provider and one model, because a
// judgement is short, self-contained and has no conversation of its own.
type Judge struct {
	provider  llm.Provider
	model     string
	maxTokens int
}

// NewJudge creates a judge over a provider.
//
// The model is the conversation model and not the task model. The task channel
// exists for calls the user did not ask for — naming a session is the example
// the configuration gives — and judging is a user pressing submit whose output
// is text the user then reads.
func NewJudge(provider llm.Provider, model string, maxTokens int) (*Judge, error) {
	if provider == nil {
		return nil, fmt.Errorf("practice: a judge needs a provider")
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("practice: a judge needs a model")
	}
	if maxTokens <= 0 {
		return nil, fmt.Errorf("practice: a judge needs a positive token budget, got %d", maxTokens)
	}
	return &Judge{provider: provider, model: model, maxTokens: maxTokens}, nil
}

// Judge judges one answer to one question.
//
// Which of the two paths runs is decided by the question's type and by nothing
// else. There is no configuration that sends an objective question to a model
// and no flag that makes a comparison out of a short answer: the type is what
// the question is, and what the question is decides how it can be judged.
func (j *Judge) Judge(ctx context.Context, question notebook.Question, userAnswer string) (Judgment, error) {
	switch question.Type {
	case notebook.QuestionChoice, notebook.QuestionFillBlank:
		return judgeObjective(question, userAnswer)
	case notebook.QuestionShortAnswer:
		return j.judgeWithModel(ctx, question, userAnswer)
	default:
		return Judgment{}, fmt.Errorf("%w: %q is not a question type",
			ErrUnusableQuestion, question.Type)
	}
}

// judgeWithModel asks the model to judge a short answer against the question's
// explanation.
func (j *Judge) judgeWithModel(ctx context.Context, question notebook.Question, userAnswer string) (Judgment, error) {
	request := llm.Request{
		Model: j.model,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: judgeSystemPrompt},
			{Role: llm.RoleUser, Content: judgeQuestion(question, userAnswer)},
		},
		Temperature: judgeTemperature,
		MaxTokens:   j.maxTokens,
	}

	// The reply is assembled from the stream and parsed once at the end. A
	// judgement is read whole or not at all: half a JSON object is not a
	// judgement, so there is nothing a partial parse could do with the
	// fragments as they arrive.
	var reply strings.Builder
	if _, err := j.provider.Stream(ctx, request, func(delta llm.Delta) error {
		reply.WriteString(delta.Content)
		return nil
	}); err != nil {
		// A call that ended because the caller's context did is the caller's own
		// deadline coming back, not a judgement that failed. The two have to stay
		// apart: only one of them is worth retrying, and the caller is the one
		// that knows which.
		if ctx.Err() != nil {
			return Judgment{}, err
		}
		// Both errors stay in the chain: the wrap says the judging is what could
		// not happen, and the cause is still the caller's to recognise — a call
		// refused because the account is out of quota is not a judge that was
		// unavailable, and a caller told only the second cannot tell them apart.
		return Judgment{}, fmt.Errorf("%w: %w", ErrJudgeUnavailable, err)
	}
	return parseJudgment(reply.String(), j.model)
}

// judgeReply is the shape the judge is asked to reply in.
//
// Every field is a pointer so that a missing one can be told apart from a zero
// one. A model that answers with an empty list of points has judged against
// nothing; a model that does not mention the points at all has not produced the
// shape that was asked for, and the two are different failures.
type judgeReply struct {
	IsCorrect  *bool     `json:"is_correct"`
	Reason     *string   `json:"reason"`
	Rubric     *[]string `json:"rubric"`
	Feedback   *string   `json:"feedback"`
	Suggestion *string   `json:"suggestion"`
	Confidence *float64  `json:"confidence"`
	Boundary   *string   `json:"boundary"`
}

// parseJudgment reads a model's reply as a judgement.
//
// Strict on purpose, and the strictness is the design rather than a shortcut:
// the alternative to refusing an unreadable reply is guessing what it meant,
// and a guess at "was this answer right" is the one guess that must never be
// made. So a reply that is not one JSON object carrying all seven fields is not
// a judgement however sensible it reads, and it becomes an error the caller can
// retry rather than a verdict the user will act on.
//
// The text of the reply never enters the error. It is model output, it can be
// long, and a caller or a log has no use for it that the field names do not
// already serve.
func parseJudgment(reply, model string) (Judgment, error) {
	var parsed judgeReply
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply)), &parsed); err != nil {
		return Judgment{}, fmt.Errorf("%w: the model's reply is not a JSON object", ErrJudgeUnavailable)
	}
	fields := []struct {
		name    string
		present bool
	}{
		{"is_correct", parsed.IsCorrect != nil},
		{"reason", parsed.Reason != nil},
		{"rubric", parsed.Rubric != nil},
		{"feedback", parsed.Feedback != nil},
		{"suggestion", parsed.Suggestion != nil},
		{"confidence", parsed.Confidence != nil},
		{"boundary", parsed.Boundary != nil},
	}
	for _, field := range fields {
		if !field.present {
			return Judgment{}, fmt.Errorf("%w: the model's reply has no %s",
				ErrJudgeUnavailable, field.name)
		}
	}
	if strings.TrimSpace(*parsed.Reason) == "" {
		return Judgment{}, fmt.Errorf("%w: the model's reply gives no reason",
			ErrJudgeUnavailable)
	}
	if strings.TrimSpace(*parsed.Boundary) == "" {
		return Judgment{}, fmt.Errorf("%w: the model's reply states no boundary",
			ErrJudgeUnavailable)
	}
	if *parsed.Confidence < 0 || *parsed.Confidence > 1 {
		return Judgment{}, fmt.Errorf("%w: the model's confidence is %v, which is not 0 to 1",
			ErrJudgeUnavailable, *parsed.Confidence)
	}

	judged := model
	return Judgment{
		Kind:       KindModelJudgment,
		IsCorrect:  *parsed.IsCorrect,
		Reason:     strings.TrimSpace(*parsed.Reason),
		Rubric:     cleanRubric(*parsed.Rubric),
		Feedback:   strings.TrimSpace(*parsed.Feedback),
		Suggestion: strings.TrimSpace(*parsed.Suggestion),
		Confidence: parsed.Confidence,
		Boundary:   strings.TrimSpace(*parsed.Boundary),
		Model:      &judged,
	}, nil
}

// cleanRubric drops the points that say nothing.
//
// An empty entry is a blank line in a list, not a judgement made against
// nothing, so dropping it is not the same as filling in what the model left
// out — and the result is never nil, so that a reader iterating it does not
// have to handle a list that is absent and a list that is empty as two cases.
func cleanRubric(points []string) []string {
	cleaned := make([]string, 0, len(points))
	for _, point := range points {
		if trimmed := strings.TrimSpace(point); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}
