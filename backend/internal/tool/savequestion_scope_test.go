package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSaveQuestionRefusesAnAttachmentTheTurnIsNotCarrying(t *testing.T) {
	bank := &stubQuestionBank{carried: []Carried{
		{Label: "1", MaterialID: "file_1"},
		{Label: "2", MaterialID: "file_2"},
	}}
	// The scope is the turn's and never the argument's (KB-07): a label is looked
	// up in the list the turn gave, so a question can only be filed under
	// material the user actually put in front of this conversation.
	result := save(t, bank, `{"question": "这个合同的有效期是多久？", "question_type": "fill_blank", "correct_answer": "两年", "explanation": "合同写明有效期为两年。", "source_label": "7"}`)
	if result.Success {
		t.Fatalf("a question citing an attachment that is not here was stored: %+v", result)
	}
	if !strings.Contains(result.Content, "7") || !strings.Contains(result.Content, "1、2") {
		t.Errorf("the refusal = %q, want it to name what was asked for and what is here", result.Content)
	}
	if len(bank.saved) != 0 {
		t.Errorf("the runtime was reached anyway: %+v", bank.saved)
	}
}

func TestSaveQuestionFilesAQuestionUnderTheMaterialItCameFrom(t *testing.T) {
	question := `{"question": "这个合同的有效期是多久？", "question_type": "fill_blank", "correct_answer": "两年", "explanation": "合同写明有效期为两年。"}`
	labeled := question[:len(question)-1] + `, "source_label": "1"}`

	for _, test := range []struct {
		name     string
		carried  []Carried
		args     string
		wantID   string
		wantSour string
	}{
		{
			name:     "the only attachment a turn carries needs no naming",
			carried:  []Carried{{Label: "1", MaterialID: "file_1"}},
			args:     question,
			wantSour: "attachment",
			wantID:   "file_1",
		},
		{
			name:     "a named attachment is recorded against it",
			carried:  []Carried{{Label: "1", MaterialID: "file_1"}, {Label: "2", MaterialID: "file_2"}},
			args:     labeled,
			wantSour: "attachment",
			wantID:   "file_1",
		},
		{
			// Several attachments and no label is a question with no material:
			// picking one would cite material the model never claimed.
			name:    "one of several attachments is not a guess worth making",
			carried: []Carried{{Label: "1", MaterialID: "file_1"}, {Label: "2", MaterialID: "file_2"}},
			args:    question,
		},
		{
			name: "a question from general knowledge has no material",
			args: question,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bank := &stubQuestionBank{carried: test.carried}
			if result := save(t, bank, test.args); !result.Success {
				t.Fatalf("the question was refused: %q", result.Content)
			}
			stored := only(t, bank)
			if stored.Source != test.wantSour || stored.MaterialID != test.wantID {
				t.Errorf("stored source = (%q, %q), want (%q, %q)",
					stored.Source, stored.MaterialID, test.wantSour, test.wantID)
			}
		})
	}
}

func TestSaveQuestionArgumentsThatAreNotAQuestionAreMalformedCalls(t *testing.T) {
	// A call that never said what to write is not a question the model could fix
	// by phrasing it differently, and it is answered the way the source tools
	// answer a missing label: an error, which the loop reports as a failed call.
	for _, arguments := range []string{
		`[]`,
		`{"question": 1, "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。"}`,
		`{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": {"text": "不减。"}, "explanation": "按定义。"}`,
		`{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。", "options": ["A", "B"]}`,
	} {
		if _, err := NewSaveQuestionTool(&stubQuestionBank{}).Invoke(context.Background(), call(t, arguments)); err == nil {
			t.Errorf("Invoke(%s) error = nil, want a malformed-call error", arguments)
		}
	}
}

func TestASaveFailureThatIsNotARefusalStaysAFailure(t *testing.T) {
	broken := &stubQuestionBank{err: errors.New("the database stopped answering")}
	result, err := NewSaveQuestionTool(broken).Invoke(context.Background(), call(t, `{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。"}`))
	if err == nil {
		t.Fatalf("Invoke() = %+v, want the failure to stay one", result)
	}
	if result.Content != "" {
		t.Errorf("a failure was given content: %q", result.Content)
	}
}

func TestTheDefinitionIsTheOnlyPlaceTheStorageConventionsAreTold(t *testing.T) {
	// The judge reads a choice answer as comma-separated option keys and a blank
	// answer as spellings separated by "|", and nothing else in this service ever
	// says so to the model: the question is written here or not at all. So the
	// schema's own words are part of the contract, not documentation.
	var schema struct {
		Properties map[string]struct {
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	definition := NewSaveQuestionTool(&stubQuestionBank{}).Definition()
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatalf("the schema does not decode: %v", err)
	}
	if definition.Name != SaveQuestionName {
		t.Errorf("Definition().Name = %q, want %q", definition.Name, SaveQuestionName)
	}
	if schema.AdditionalProperties {
		t.Error("the schema accepts properties it does not declare, so a typo is stored as a missing field")
	}
	for _, field := range []string{"question", "question_type", "correct_answer", "explanation"} {
		if !contains(schema.Required, field) {
			t.Errorf("the schema does not require %q: %v", field, schema.Required)
		}
	}
	// Every field the tool reads is declared, and only those: a name that drifted
	// from the parser on one side would silently stop arriving.
	for _, field := range []string{"question", "question_type", "options", "correct_answer",
		"explanation", "difficulty", "source_label", "knowledge_points", "categories"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("the schema does not declare %q", field)
		}
	}
	if len(schema.Properties) != 9 {
		t.Errorf("the schema declares %d properties, want the 9 the tool reads", len(schema.Properties))
	}
	for _, want := range []string{"choice", "fill_blank", "short_answer"} {
		if !contains(schema.Properties["question_type"].Enum, want) {
			t.Errorf("question_type does not offer %q: %v", want, schema.Properties["question_type"].Enum)
		}
	}
	for _, want := range []string{"easy", "medium", "hard"} {
		if !contains(schema.Properties["difficulty"].Enum, want) {
			t.Errorf("difficulty does not offer %q: %v", want, schema.Properties["difficulty"].Enum)
		}
	}
	answer := schema.Properties["correct_answer"].Description
	if !strings.Contains(answer, "A,C") || !strings.Contains(answer, "|") {
		t.Errorf("correct_answer = %q, want the judge's two conventions spelled out for the model", answer)
	}
}

// contains reports whether a list holds a value, for the two closed sets a JSON
// Schema states as an array.
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
