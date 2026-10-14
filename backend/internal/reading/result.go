package reading

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResultSchema is the shape of what an extension returns, and the five shapes
// are a closed set.
//
// It is closed for the same reason a question's type is (notebook): the reader
// knows how to draw exactly these, and an extension returning a sixth shape
// would reach the interface as something it has no renderer for. An extension
// declares its schema before it runs, so a client can decide how to draw a
// result before being handed one.
type ResultSchema string

// The five result shapes this version knows.
const (
	ResultText     ResultSchema = "text"
	ResultCards    ResultSchema = "cards"
	ResultTimeline ResultSchema = "timeline"
	ResultQuiz     ResultSchema = "quiz"
	ResultAudioRef ResultSchema = "audio_ref"
)

// Valid reports whether the schema is one of the five.
func (s ResultSchema) Valid() bool {
	switch s {
	case ResultText, ResultCards, ResultTimeline, ResultQuiz, ResultAudioRef:
		return true
	default:
		return false
	}
}

const (
	// maxResultBytes bounds one extension's answer. An extension runs inside a
	// request that a person is waiting on, and an answer this size is already
	// more than a side panel can show: past it the result is refused rather than
	// rendered, because "the panel is enormous" is not a state the reader has.
	maxResultBytes = 64_000
	// maxResultItems bounds a list-shaped result. A hundred cards is not a panel
	// either.
	maxResultItems = 20
	// maxResultTextChars bounds a text-shaped result, in characters rather than
	// bytes because that is the unit the bound is chosen in.
	maxResultTextChars = 20_000
)

// Card is one item of a cards result: a heading and the text under it.
type Card struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Moment is one item of a timeline result: a point and what happened there.
type Moment struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// QuizQuestion is one item of a quiz result.
type QuizQuestion struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

// AudioRef is an audio_ref result: where the audio for this passage is.
type AudioRef struct {
	URL string `json:"url"`
}

// validateResult checks an extension's answer against the shape it declared.
//
// The check is here and not in each extension for the same reason the timeout
// is: an extension that answers in the wrong shape has failed, and the failure
// belongs to the thing that knows all the shapes. What this stops is a result
// reaching the reader as something it will try to draw and cannot.
func validateResult(schema ResultSchema, payload json.RawMessage) error {
	if len(payload) == 0 {
		return fmt.Errorf("the extension returned nothing")
	}
	if len(payload) > maxResultBytes {
		return fmt.Errorf("the result is larger than %d bytes", maxResultBytes)
	}
	switch schema {
	case ResultText:
		var text string
		if err := json.Unmarshal(payload, &text); err != nil {
			return fmt.Errorf("a %s result must be a JSON string", schema)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("a %s result must not be blank", schema)
		}
		if len([]rune(text)) > maxResultTextChars {
			return fmt.Errorf("a %s result is longer than %d characters", schema, maxResultTextChars)
		}
	case ResultCards:
		// A cards result may be empty, and the other list shapes may not. "There
		// is no word in this passage worth glossing" is an answer a reader can
		// use, while an empty timeline or an empty quiz is a thing nobody asked
		// for — the difference is whether the emptiness is a fact about the
		// passage or a failure to produce anything.
		var cards []Card
		if err := json.Unmarshal(payload, &cards); err != nil {
			return fmt.Errorf("a %s result must be a JSON array of cards", schema)
		}
		if len(cards) > maxResultItems {
			return fmt.Errorf("a %s result carries at most %d cards", schema, maxResultItems)
		}
		for _, card := range cards {
			if strings.TrimSpace(card.Title) == "" {
				return fmt.Errorf("every %s card needs a title", schema)
			}
		}
	case ResultTimeline:
		var moments []Moment
		if err := json.Unmarshal(payload, &moments); err != nil {
			return fmt.Errorf("a %s result must be a JSON array of moments", schema)
		}
		if len(moments) == 0 || len(moments) > maxResultItems {
			return fmt.Errorf("a %s result carries between 1 and %d moments", schema, maxResultItems)
		}
		for _, moment := range moments {
			if strings.TrimSpace(moment.Label) == "" {
				return fmt.Errorf("every %s moment needs a label", schema)
			}
		}
	case ResultQuiz:
		var questions []QuizQuestion
		if err := json.Unmarshal(payload, &questions); err != nil {
			return fmt.Errorf("a %s result must be a JSON array of questions", schema)
		}
		if len(questions) == 0 || len(questions) > maxResultItems {
			return fmt.Errorf("a %s result carries between 1 and %d questions", schema, maxResultItems)
		}
		for _, question := range questions {
			if strings.TrimSpace(question.Question) == "" {
				return fmt.Errorf("every %s question needs a prompt", schema)
			}
			if len(question.Choices) < 2 || len(question.Choices) > 8 {
				return fmt.Errorf("every %s question needs between 2 and 8 choices", schema)
			}
		}
	case ResultAudioRef:
		var ref AudioRef
		if err := json.Unmarshal(payload, &ref); err != nil {
			return fmt.Errorf("a %s result must be a JSON object", schema)
		}
		if strings.TrimSpace(ref.URL) == "" {
			return fmt.Errorf("a %s result needs a url", schema)
		}
	default:
		return fmt.Errorf("%q is not a result schema", schema)
	}
	return nil
}
