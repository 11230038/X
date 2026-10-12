package turn

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"backend/internal/llm"
)

// titleMaxRunes bounds a generated title. It is well inside what the session
// accepts, because a title is read in a list beside dozens of others: one that
// fills the column is one nobody reads to the end. The prompt asks for it and
// this enforces it, so a model that ignores the instruction costs a cut title
// rather than an unreadable list.
const titleMaxRunes = 24

// titleExcerptRunes is how much of the answer the namer is shown. A title is
// decided by the first thing that was said, and sending the whole answer would
// pay for tokens that cannot change the outcome.
const titleExcerptRunes = 400

// titlePrompt asks the task model for a name, and asks for nothing else.
//
// It is a system message rather than part of the instruction, so that the
// question and the answer arrive as data the model was told about rather than
// as text it might continue.
const titlePrompt = `You name conversations. Given the user's first question and the assistant's reply, reply with a title for the conversation.
Rules: at most 24 characters, in the language the user wrote in, no quotes, no trailing punctuation, no explanation. Reply with the title alone.`

// Titler names a session after its first exchange.
//
// It calls the task model rather than the conversation's model, because naming
// is a short call with a small budget and no tools: falling back to the bigger
// model would pay conversation prices for a label.
type Titler struct {
	provider  llm.Provider
	model     string
	maxTokens int
}

// NewTitler creates a namer over a provider.
func NewTitler(provider llm.Provider, model string, maxTokens int) (*Titler, error) {
	if provider == nil {
		return nil, fmt.Errorf("turn: a titler needs a provider")
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("turn: a titler needs a model")
	}
	if maxTokens <= 0 {
		return nil, fmt.Errorf("turn: a titler needs a positive token budget, got %d", maxTokens)
	}
	return &Titler{provider: provider, model: model, maxTokens: maxTokens}, nil
}

// Title names the conversation from its first exchange.
//
// Everything it returns has already been trimmed to something the session
// accepts, so a caller does not have to clean it again before storing it.
func (t *Titler) Title(ctx context.Context, question, answer string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("turn: no titler is configured")
	}
	var named strings.Builder
	request := llm.Request{
		Model: t.model,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: titlePrompt},
			{Role: llm.RoleUser, Content: exchange(question, answer)},
		},
		MaxTokens: t.maxTokens,
	}
	if _, err := t.provider.Stream(ctx, request, func(delta llm.Delta) error {
		named.WriteString(delta.Content)
		return nil
	}); err != nil {
		return "", fmt.Errorf("name the conversation: %w", err)
	}

	title := cleanTitle(named.String())
	if title == "" {
		return "", fmt.Errorf("turn: the model named the conversation with nothing")
	}
	return title, nil
}

// exchange is what the namer is shown: the question whole, and as much of the
// answer as bears on what the conversation is about.
func exchange(question, answer string) string {
	return fmt.Sprintf("User asked:\n%s\n\nAssistant replied:\n%s",
		strings.TrimSpace(question), excerpt(answer, titleExcerptRunes))
}

// cleanTitle reduces a model's reply to a title: one line, no wrapping quotes,
// no trailing full stop, and within the bound.
//
// Models answer a request for "the title alone" with an explanation or a quoted
// phrase often enough that leaving it to the caller would put those in a
// session list. Everything here is about making the reply fit the shape a title
// has, and nothing here guesses at what the title should say.
func cleanTitle(reply string) string {
	title := strings.TrimSpace(reply)

	// A multi-line reply is a reply that says more than the title, so the title
	// is the first line of it.
	if line, _, found := strings.Cut(title, "\n"); found {
		title = strings.TrimSpace(line)
	}
	// Straight quotes and the bracketing marks a Chinese title is wrapped in
	// are all the same thing here: something the model put around the title.
	title = strings.Trim(title, `"'`+"`"+`「」『』《》`)
	title = strings.TrimRightFunc(title, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".。!！?？", r)
	})
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > titleMaxRunes {
		title = strings.TrimSpace(string([]rune(title)[:titleMaxRunes]))
	}
	return title
}

// excerpt returns the first limit runes of a text, marking it when it was cut.
func excerpt(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
