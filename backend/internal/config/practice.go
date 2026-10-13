package config

import "fmt"

// PracticeConfig holds the scale of asking questions and reviewing them. These
// four keys all answer "how much" and none of them answers "how it is computed":
// the intervals, the ease factor and the judging prompt are algorithms, and an
// algorithm put in a settings file is a second implementation of it waiting to
// disagree with the first.
type PracticeConfig struct {
	// QuestionDefaultCount is how many questions are written when the user does
	// not say. It is what most requests get, so it is the one number that
	// decides how long a normal "出题" takes and how much it costs.
	QuestionDefaultCount int
	// QuestionMaxCount is the ceiling on one request, and it exists because the
	// count is a multiplication: one request for 200 questions is 200 model
	// calls deep, and the request that asked for them looks exactly like the one
	// that asked for five. The reason it must not be smaller than the default is
	// not arithmetic tidiness — with the two written the other way round the
	// service refuses the default it hands out itself, and the user who asked
	// for nothing in particular is the one who gets the error.
	QuestionMaxCount int
	// QueueSize is how many entries one review queue returns. The queue is a
	// query, not a job: it is computed from what is due, what is new and what
	// was wrong, and this number only says where the list is cut.
	QueueSize int
	// JudgeMaxTokens caps the output of one short-answer judging call. Judging
	// is deliberately not a capability (it is a fixed question with a written
	// answer, not a role), so its output limit has nowhere else to live.
	JudgeMaxTokens int
}

// Validate checks the asking and reviewing bounds.
func (c PracticeConfig) Validate() error {
	for _, field := range []struct {
		key   string
		value int
	}{
		{"QUESTION_DEFAULT_COUNT", c.QuestionDefaultCount},
		{"QUESTION_MAX_COUNT", c.QuestionMaxCount},
		{"PRACTICE_QUEUE_SIZE", c.QueueSize},
		{"PRACTICE_JUDGE_MAX_TOKENS", c.JudgeMaxTokens},
	} {
		if field.value <= 0 {
			return fmt.Errorf("%s must be a positive integer", field.key)
		}
	}
	// The one rule that is about two keys at once. Neither value is wrong on its
	// own, so neither key's own check can see it, and the failure it causes is
	// far from the setting that caused it.
	if c.QuestionMaxCount < c.QuestionDefaultCount {
		return fmt.Errorf("QUESTION_MAX_COUNT must not be smaller than QUESTION_DEFAULT_COUNT")
	}
	return nil
}

func loadPracticeConfig(values map[string]string) (PracticeConfig, error) {
	questionDefaultCount, err := parsePositiveInt(values, "QUESTION_DEFAULT_COUNT")
	if err != nil {
		return PracticeConfig{}, err
	}
	questionMaxCount, err := parsePositiveInt(values, "QUESTION_MAX_COUNT")
	if err != nil {
		return PracticeConfig{}, err
	}
	queueSize, err := parsePositiveInt(values, "PRACTICE_QUEUE_SIZE")
	if err != nil {
		return PracticeConfig{}, err
	}
	judgeMaxTokens, err := parsePositiveInt(values, "PRACTICE_JUDGE_MAX_TOKENS")
	if err != nil {
		return PracticeConfig{}, err
	}
	return PracticeConfig{
		QuestionDefaultCount: questionDefaultCount,
		QuestionMaxCount:     questionMaxCount,
		QueueSize:            queueSize,
		JudgeMaxTokens:       judgeMaxTokens,
	}, nil
}
