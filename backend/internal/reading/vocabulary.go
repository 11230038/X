package reading

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/internal/llm"
)

// vocabularyName is what the extension is called, in the route that runs it and
// in the catalogue.
const vocabularyName = "vocabulary"

// vocabularyPrompt is what the vocabulary extension tells its model it is.
//
// Two of its instructions carry weight beyond politeness:
//
//   - the gloss covers the word in this passage and nothing else. A model
//     answering from its own knowledge gives the dictionary's first sense, which
//     is regularly not the sense the passage is using — and a wrong gloss is
//     worse than no gloss, because the reader has no way to tell.
//   - the headword is copied out of the passage unchanged. A gloss list is
//     matched back against the text it came from, and a headword the model
//     rewrote has no place in the text to be matched to.
const vocabularyPrompt = `你从一段原文里挑出会影响理解的词或短语，逐个给出简短解释。

只依据我给的那段原文。不要补充原文之外的背景，不要解释原文里没有出现的词。挑的是"读到它会卡住"的那些，不是所有词——不清楚这个水平的读者是否需要，就不要挑。最多 20 条，宁少勿滥。

只输出一个 JSON 数组，不要代码块围栏，不要在它前后写任何别的文字。数组每一项是一个对象：

- title：原文里的那个词或短语，原样照抄，不要改写、不要换成别的词形。
- body：它在这段原文里是什么意思，一到两句。用读者读得懂的话说，不要拿同义词绕圈解释。

如果这段原文里没有值得解释的词，输出空数组 []。`

// VocabularyExtension is the reading aid that glosses a passage's words.
type vocabularyExtension struct {
	provider  llm.Provider
	model     string
	maxTokens int
}

// NewVocabulary builds the extension that explains the words in a passage.
//
// The model is named at assembly time rather than read from configuration here,
// because an extension is a thing the process has and not a thing the process
// looks up: which model serves it is decided where every other dependency is
// decided.
func NewVocabulary(provider llm.Provider, model string, maxTokens int) Extension {
	return &vocabularyExtension{
		provider:  provider,
		model:     strings.TrimSpace(model),
		maxTokens: maxTokens,
	}
}

func (v *vocabularyExtension) Name() string { return vocabularyName }

func (v *vocabularyExtension) ResultSchema() ResultSchema { return ResultCards }

func (v *vocabularyExtension) Available() (bool, string) {
	return modelConfigured(v.provider, v.model)
}

// Run glosses the passage it was given.
//
// The model's answer is decoded and re-encoded, and the round trip is the point:
// what reaches the registry is this package's own JSON rather than the model's
// text, so a model that added a field or wrapped the array in an object is
// caught here rather than drawn wrong.
func (v *vocabularyExtension) Run(ctx context.Context, reading Context) (json.RawMessage, error) {
	answer, err := modelAnswer(ctx, v.provider, llm.Request{
		Model:       v.model,
		Temperature: extensionTemperature,
		MaxTokens:   v.maxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: vocabularyPrompt},
			{Role: llm.RoleUser, Content: passageOf(reading)},
		},
	})
	if err != nil {
		return nil, err
	}
	var cards []Card
	if err := decodeModelJSON(answer, &cards); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(cards)
	if err != nil {
		return nil, fmt.Errorf("encode the vocabulary result: %w", err)
	}
	return payload, nil
}

// passageOf is what an extension shows its model: the passage, labelled.
//
// The label is what makes the passage data rather than prose. A passage that
// reads like an instruction is read as one otherwise, and this is the same
// defence the judge prompt in the practice domain uses. Nothing else is passed
// in — not the material, not the conversation — because an extension has no use
// for either and every field it is given is a field it can leak.
func passageOf(reading Context) string {
	return "摘录：\n" + reading.Text
}
