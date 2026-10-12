package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// AskUserName is the name ask_user mounts and is called under.
const AskUserName = "ask_user"

// ErrMissingQuestion reports an ask_user call with no question in it.
var ErrMissingQuestion = errors.New("tool: ask_user needs a question")

// Asker is the turn runtime's side of ask_user: it publishes the question,
// pauses the turn until the user answers, and fails if no answer arrives before
// the turn's input deadline.
//
// It is an interface because the tool must not know how a turn is paused; the
// runtime owns that, and a test can stand in for it.
type Asker interface {
	AskUser(ctx context.Context, question string) (string, error)
}

// AskUserTool pauses the turn to ask the user one question. It is always on:
// every capability needs a way to stop and ask rather than guess.
type AskUserTool struct {
	asker Asker
}

var _ Tool = (*AskUserTool)(nil)

// NewAskUserTool returns the tool bound to an asker.
func NewAskUserTool(asker Asker) *AskUserTool {
	return &AskUserTool{asker: asker}
}

// Definition declares the question as required. The description is written for
// the model and so follows the product's language, unlike the errors here.
func (t *AskUserTool) Definition() Definition {
	return Definition{
		Name: AskUserName,
		Description: "向用户提出一个问题，并暂停等待用户回答。" +
			"当缺少必要信息、存在多个都合理的方向、或需要用户确认后再继续时使用。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"question": {
					"type": "string",
					"description": "要问用户的问题，应当具体、一次只问一件事"
				}
			},
			"required": ["question"],
			"additionalProperties": false
		}`),
	}
}

// Invoke asks the question and returns the answer as the tool's content.
//
// Extra arguments are tolerated rather than rejected: the schema asks the model
// for exactly one field, but a model that adds another has still asked a usable
// question, and failing the call over it would cost the user a turn.
func (t *AskUserTool) Invoke(ctx context.Context, call Call) (Result, error) {
	var args struct {
		Question string `json:"question"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("tool: ask_user arguments are not a JSON object: %w", err)
	}
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return Result{}, ErrMissingQuestion
	}

	answer, err := t.asker.AskUser(ctx, question)
	if err != nil {
		// A question that could not be asked ends the turn rather than becoming
		// a result: the runtime only fails here when the turn is over — the
		// deadline for an answer passed, or the turn was cancelled — and
		// answering on without the answer would be inventing one.
		return Result{}, fmt.Errorf("%w: %w", ErrFatal, err)
	}
	return Result{
		Content:  answer,
		Success:  true,
		Metadata: map[string]any{"question": question},
	}, nil
}
