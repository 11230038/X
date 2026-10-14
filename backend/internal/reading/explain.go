package reading

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/internal/llm"
)

// explainName is what the extension is called, in the route that runs it and in
// the catalogue.
const explainName = "explain"

// explainPrompt is what the explain extension tells its model it is.
//
// Two of its instructions carry weight beyond politeness:
//
//   - it explains the passage and does not extend it. An explanation that adds
//     background the passage does not have is a second author speaking, and the
//     reader cannot tell where the text stopped.
//   - the answer is prose and not JSON, because this extension's schema is text.
//     The wrapping is done here, and telling the model so keeps it from spending
//     its answer escaping quotes.
const explainPrompt = `你把一段原文讲清楚：它在说什么、为什么这么说、有没有没说出口的前提。

只依据我给的那段原文。不要补充原文之外的背景，不要替作者下他本人没下的结论。原文是个问句就回答它，是个结论就把它的依据讲明白。

直接写给读者的解释，用中文，控制在 400 字以内。不要写"这段话说的是……"这样的开头，不要复述原文，不要代码块围栏，也不要输出 JSON——我要的是这段话本身。`

// explainExtension is the reading aid that reads a passage out loud, as it were.
type explainExtension struct {
	provider  llm.Provider
	model     string
	maxTokens int
}

// NewExplain builds the extension that explains a passage in plainer words.
func NewExplain(provider llm.Provider, model string, maxTokens int) Extension {
	return &explainExtension{
		provider:  provider,
		model:     strings.TrimSpace(model),
		maxTokens: maxTokens,
	}
}

func (e *explainExtension) Name() string { return explainName }

func (e *explainExtension) ResultSchema() ResultSchema { return ResultText }

func (e *explainExtension) Available() (bool, string) {
	return modelConfigured(e.provider, e.model)
}

// Run explains the passage it was given.
//
// The model answers in prose and the JSON string is built here, which is the
// one place this extension differs from the ones whose schema is a structure:
// asking a model for a JSON string gets an answer full of escaped quotes and
// stray backslashes, and the escaping is this side's job, not the model's.
func (e *explainExtension) Run(ctx context.Context, reading Context) (json.RawMessage, error) {
	answer, err := modelAnswer(ctx, e.provider, llm.Request{
		Model:       e.model,
		Temperature: extensionTemperature,
		MaxTokens:   e.maxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: explainPrompt},
			{Role: llm.RoleUser, Content: passageOf(reading)},
		},
	})
	if err != nil {
		return nil, err
	}
	explained := stripFences(answer)
	if explained == "" {
		return nil, fmt.Errorf("the model explained nothing")
	}
	payload, err := json.Marshal(explained)
	if err != nil {
		return nil, fmt.Errorf("encode the explain result: %w", err)
	}
	return payload, nil
}
