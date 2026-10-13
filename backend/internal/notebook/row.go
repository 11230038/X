package notebook

import (
	"encoding/json"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/datatypes"
)

// The JSON columns are decoded strictly: a row this package wrote and cannot
// read back is a broken row, and reporting the questions around it as if
// nothing were wrong would leave the broken one to be discovered later, by
// something that has less idea what it was looking at.
func toEntry(record models.NotebookEntry) (Entry, error) {
	question := Question{
		QuestionID: record.QuestionID,
		Text:       record.Question,
		Type:       QuestionType(record.QuestionType),
		SessionID:  stringOf(record.SessionID),
		TurnID:     stringOf(record.TurnID),
		Source:     stringOf(record.Source),
		MaterialID: stringOf(record.MaterialID),
	}
	question.CorrectAnswer = stringOf(record.CorrectAnswer)
	question.Explanation = stringOf(record.Explanation)
	question.Difficulty = stringOf(record.Difficulty)
	if len(record.Options) > 0 {
		if err := json.Unmarshal(record.Options, &question.Options); err != nil {
			return Entry{}, fmt.Errorf("read options of question %s: %w", record.QuestionID, err)
		}
	}
	if len(record.Extra) > 0 {
		var extra struct {
			KnowledgePoints []string `json:"knowledge_points"`
		}
		if err := json.Unmarshal(record.Extra, &extra); err != nil {
			return Entry{}, fmt.Errorf("read extra of question %s: %w", record.QuestionID, err)
		}
		question.KnowledgePoints = extra.KnowledgePoints
	}

	entry := Entry{
		ID:        record.NotebookEntriesID,
		Question:  question,
		CreatedAt: record.CreatedAt,
		Answer: Answer{
			UserAnswer: stringOf(record.UserAnswer),
			IsCorrect:  record.IsCorrect,
			Resolved:   record.Resolved,
			Bookmarked: record.Bookmarked != nil && *record.Bookmarked,
		},
	}
	entry.Answer.Result = stringOf(record.Result)
	if record.AttemptCount != nil {
		entry.Answer.AttemptCount = int(*record.AttemptCount)
	}
	if record.UpdatedAt != nil {
		entry.Answer.UpdatedAt = *record.UpdatedAt
	}
	return entry, nil
}

// jsonColumn encodes a JSON column, leaving it NULL when there is nothing to
// store: an empty array and "no answer" are not the same thing, and only one of
// them is true for a question with no options.
func jsonColumn[T any](value []T) (datatypes.JSON, error) {
	if len(value) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(encoded), nil
}

func extraColumn(knowledgePoints []string) (datatypes.JSON, error) {
	if len(knowledgePoints) == 0 {
		return nil, nil
	}
	return json.Marshal(map[string]any{"knowledge_points": knowledgePoints})
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
