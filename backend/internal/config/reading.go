package config

import (
	"fmt"
	"time"
)

// ReadingConfig holds the three knobs the reading layer has, and there are three
// because everything else about reading is a shape rather than a setting: the
// locator is V2's citation rescaled, the extension result schemas are a closed
// set each extension declares, and where a reader stopped is data. What is left
// is how big a unit of text is, how long a side answer may be, and how long one
// extension may take.
type ReadingConfig struct {
	// UnitMaxChars is the largest a unit of a material's text may be. A unit is
	// a run of the document's own chunks, so this is what turns a chunk stream
	// into what a reader scrolls through and what one extension is handed: too
	// small and every unit is a paragraph, too large and the side panel is
	// summarizing something nobody is looking at.
	UnitMaxChars int
	// TutorMaxTokens is the output limit for one side-panel answer — the reading
	// aids an extension produces. It is not the reading conversation's own
	// limit: that one belongs to the capability's `responding` stage, where the
	// rest of the conversation's tuning lives (§5).
	TutorMaxTokens int
	// ExtensionTimeout is the wall clock one extension gets before it is skipped
	// with a reason (READ-03). It exists so that "a broken extension is skipped
	// and the reader goes on" is true of a hung one and not only of a panicking
	// one: a Go function that ignores its context cannot be stopped, so the
	// deadline is what the registry waits for and then lets go.
	ExtensionTimeout time.Duration
}

// Validate checks the reading knobs.
//
// All three are positive, and each has a failure that is worth naming rather
// than reading off the arithmetic: a zero unit bound would cut a material into
// units of nothing, a zero token limit would ask every side answer for no
// output, and a zero timeout would skip every extension before it could answer.
func (c ReadingConfig) Validate() error {
	if c.UnitMaxChars <= 0 {
		return fmt.Errorf("READING_UNIT_MAX_CHARS must be a positive integer")
	}
	if c.TutorMaxTokens <= 0 {
		return fmt.Errorf("READING_TUTOR_MAX_TOKENS must be a positive integer")
	}
	if c.ExtensionTimeout <= 0 {
		return fmt.Errorf("READING_EXTENSION_TIMEOUT must be a positive duration")
	}
	return nil
}

func loadReadingConfig(values map[string]string) (ReadingConfig, error) {
	unitMaxChars, err := parsePositiveInt(values, "READING_UNIT_MAX_CHARS")
	if err != nil {
		return ReadingConfig{}, err
	}
	tutorMaxTokens, err := parsePositiveInt(values, "READING_TUTOR_MAX_TOKENS")
	if err != nil {
		return ReadingConfig{}, err
	}
	extensionTimeout, err := parsePositiveDuration(values, "READING_EXTENSION_TIMEOUT")
	if err != nil {
		return ReadingConfig{}, err
	}
	return ReadingConfig{
		UnitMaxChars:     unitMaxChars,
		TutorMaxTokens:   tutorMaxTokens,
		ExtensionTimeout: extensionTimeout,
	}, nil
}
