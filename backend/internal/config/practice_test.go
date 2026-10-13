package config

import (
	"strings"
	"testing"
)

func validPracticeConfig() PracticeConfig {
	return PracticeConfig{
		QuestionDefaultCount: 5,
		QuestionMaxCount:     30,
		QueueSize:            20,
		JudgeMaxTokens:       1024,
	}
}

func TestPracticeConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validPracticeConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPracticeConfigValidateRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*PracticeConfig)
		wantKey string
	}{
		{name: "zero default count", mutate: func(c *PracticeConfig) { c.QuestionDefaultCount = 0 }, wantKey: "QUESTION_DEFAULT_COUNT"},
		{name: "negative default count", mutate: func(c *PracticeConfig) { c.QuestionDefaultCount = -1 }, wantKey: "QUESTION_DEFAULT_COUNT"},
		{name: "zero max count", mutate: func(c *PracticeConfig) { c.QuestionMaxCount = 0 }, wantKey: "QUESTION_MAX_COUNT"},
		{name: "negative max count", mutate: func(c *PracticeConfig) { c.QuestionMaxCount = -1 }, wantKey: "QUESTION_MAX_COUNT"},
		{name: "zero queue size", mutate: func(c *PracticeConfig) { c.QueueSize = 0 }, wantKey: "PRACTICE_QUEUE_SIZE"},
		{name: "negative queue size", mutate: func(c *PracticeConfig) { c.QueueSize = -1 }, wantKey: "PRACTICE_QUEUE_SIZE"},
		{name: "zero judge tokens", mutate: func(c *PracticeConfig) { c.JudgeMaxTokens = 0 }, wantKey: "PRACTICE_JUDGE_MAX_TOKENS"},
		{name: "negative judge tokens", mutate: func(c *PracticeConfig) { c.JudgeMaxTokens = -1 }, wantKey: "PRACTICE_JUDGE_MAX_TOKENS"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validPracticeConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("Validate() error = %v, want one naming %s", err, tt.wantKey)
			}
		})
	}
}

// The ceiling is allowed to sit exactly on the default: that is a deployment
// that hands out one size and refuses to be asked for more, which is a
// decision, not a mistake.
func TestPracticeConfigValidateAcceptsMaxCountEqualToDefault(t *testing.T) {
	cfg := validPracticeConfig()
	cfg.QuestionDefaultCount = 8
	cfg.QuestionMaxCount = 8

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want equal values accepted", err)
	}
}

// The one rule that spans two keys. Neither value is invalid on its own and
// neither key's own check can see the pair, which is why it gets its own test
// rather than a row in the table above.
func TestPracticeConfigValidateRejectsADefaultAboveTheMaximum(t *testing.T) {
	cfg := validPracticeConfig()
	cfg.QuestionDefaultCount = 30
	cfg.QuestionMaxCount = 5

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an error")
	}
	// The message has to name both, because the value that is wrong depends on
	// which of the two the operator meant to change.
	for _, key := range []string{"QUESTION_MAX_COUNT", "QUESTION_DEFAULT_COUNT"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("Validate() error = %v, want one naming %s", err, key)
		}
	}
}

func TestLoadRejectsMissingAndBlankPracticeValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    string
		remove bool
	}{
		{name: "missing default count", key: "QUESTION_DEFAULT_COUNT", remove: true},
		{name: "missing max count", key: "QUESTION_MAX_COUNT", remove: true},
		{name: "missing queue size", key: "PRACTICE_QUEUE_SIZE", remove: true},
		{name: "missing judge tokens", key: "PRACTICE_JUDGE_MAX_TOKENS", remove: true},
		{name: "blank default count", key: "QUESTION_DEFAULT_COUNT"},
		{name: "blank max count", key: "QUESTION_MAX_COUNT"},
		{name: "blank queue size", key: "PRACTICE_QUEUE_SIZE"},
		{name: "blank judge tokens", key: "PRACTICE_JUDGE_MAX_TOKENS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			if tt.remove {
				delete(values, tt.key)
			} else {
				values[tt.key] = " "
			}

			_, err := Load(writeDotenv(t, values))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error = %v, want error naming %s", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsInvalidPracticeValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "default count as zero", key: "QUESTION_DEFAULT_COUNT", value: "0"},
		{name: "default count as a word", key: "QUESTION_DEFAULT_COUNT", value: "five"},
		{name: "default count as a fraction", key: "QUESTION_DEFAULT_COUNT", value: "5.5"},
		{name: "max count as zero", key: "QUESTION_MAX_COUNT", value: "0"},
		{name: "max count as negative", key: "QUESTION_MAX_COUNT", value: "-1"},
		{name: "queue size as zero", key: "PRACTICE_QUEUE_SIZE", value: "0"},
		{name: "queue size as a word", key: "PRACTICE_QUEUE_SIZE", value: "twenty"},
		{name: "judge tokens as zero", key: "PRACTICE_JUDGE_MAX_TOKENS", value: "0"},
		{name: "judge tokens as negative", key: "PRACTICE_JUDGE_MAX_TOKENS", value: "-1"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
		})
	}
}

// The pair is checked after both single values, so this passes every per-key
// rule and is still refused — the case a reader of the four keys one at a time
// never reaches.
func TestLoadRejectsADefaultAboveTheMaximum(t *testing.T) {
	values := validValues()
	values["QUESTION_DEFAULT_COUNT"] = "40"
	values["QUESTION_MAX_COUNT"] = "39"

	_, err := Load(writeDotenv(t, values))
	if err == nil {
		t.Fatal("Load() error = nil, want the pair rejected")
	}
	if !strings.Contains(err.Error(), "QUESTION_MAX_COUNT") {
		t.Fatalf("Load() error = %v, want one naming QUESTION_MAX_COUNT", err)
	}
}

// These four are not secrets, and this is not a secret test. It is the same
// rule for the same reason: a message that carries the value is a message that
// has to be read carefully before it is pasted into an issue, and the operator
// needs the key, not what they wrote for it.
func TestLoadNeverEchoesPracticeValues(t *testing.T) {
	values := validValues()
	values["PRACTICE_JUDGE_MAX_TOKENS"] = "-424242"

	_, err := Load(writeDotenv(t, values))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	if strings.Contains(err.Error(), "424242") {
		t.Fatalf("configuration error echoed the value: %v", err)
	}
}

// Four positive integers in one file, read by four calls that differ only in
// their key. Swapping two of them changes nothing about whether the service
// starts, so nothing but this test notices.
func TestLoadReadsEachPracticeBoundFromItsOwnKey(t *testing.T) {
	values := validValues()
	values["QUESTION_DEFAULT_COUNT"] = "3"
	values["QUESTION_MAX_COUNT"] = "40"
	values["PRACTICE_QUEUE_SIZE"] = "7"
	values["PRACTICE_JUDGE_MAX_TOKENS"] = "256"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := PracticeConfig{QuestionDefaultCount: 3, QuestionMaxCount: 40, QueueSize: 7, JudgeMaxTokens: 256}
	if cfg.Practice != want {
		t.Errorf("Practice = %+v, want %+v", cfg.Practice, want)
	}
}
