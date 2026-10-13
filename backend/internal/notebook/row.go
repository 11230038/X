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
	question, err := QuestionOf(record)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		ID:        record.NotebookEntriesID,
		Question:  question,
		Answer:    AnswerOf(record),
		CreatedAt: record.CreatedAt,
	}, nil
}

// QuestionOf reads the question face of a stored row.
//
// It is exported because the review path reads the row itself — it needs the
// row's own key and the answer face beside the question, neither of which the
// bank's read path hands out — and it needs the question in the bank's own
// terms to have anything to judge an answer against. Two mappings of the same
// columns would be two places to keep in step, and the second one would be the
// one that goes stale.
func QuestionOf(record models.NotebookEntry) (Question, error) {
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
			return Question{}, fmt.Errorf("read options of question %s: %w", record.QuestionID, err)
		}
	}
	if len(record.Extra) > 0 {
		var extra struct {
			KnowledgePoints []string `json:"knowledge_points"`
		}
		if err := json.Unmarshal(record.Extra, &extra); err != nil {
			return Question{}, fmt.Errorf("read extra of question %s: %w", record.QuestionID, err)
		}
		question.KnowledgePoints = extra.KnowledgePoints
	}
	return question, nil
}

// AnswerOf reads the answer face of a stored row.
//
// Reading it is not the same as writing it: the review path writes this face,
// and it reads the row for the question beside it. The snapshot is handed over
// as it stands so that the one path that reports the face without changing it —
// a skipped question, which is asked and answered with nothing — reports what
// is stored rather than an empty answer it invented.
func AnswerOf(record models.NotebookEntry) Answer {
	answer := Answer{
		UserAnswer: stringOf(record.UserAnswer),
		IsCorrect:  record.IsCorrect,
		Resolved:   record.Resolved,
		Bookmarked: record.Bookmarked != nil && *record.Bookmarked,
		Result:     stringOf(record.Result),
	}
	if record.AttemptCount != nil {
		answer.AttemptCount = int(*record.AttemptCount)
	}
	if record.UpdatedAt != nil {
		answer.UpdatedAt = *record.UpdatedAt
	}
	return answer
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
