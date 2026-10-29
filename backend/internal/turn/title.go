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

// descriptionMaxRunes bounds the sentence under a path's name. The card that
// shows it clamps to two lines, so a sentence that runs longer is cut
// mid-thought rather than read to the end.
const descriptionMaxRunes = 60

// descriptionPrompt asks the task model what a path is for, in one sentence.
//
// It is a second prompt rather than a second field in the title's reply: a
// title and a description have different shapes and budgets, and one reply
// holding both is a reply that has to be split — which is where a model's
// formatting starts deciding what a path is called.
const descriptionPrompt = `You describe learning paths. Given the user's first question and the assistant's reply, reply with one sentence about what the learner will study on this path.
Rules: at most 60 characters, in the language the user wrote in, no quotes, no trailing punctuation, no explanation. Reply with the sentence alone.`

// Titler names a session after its first exchange — and, in a second call, the
// path that exchange opened: its name is the session's title, and its
// description is a sentence from the same exchange (Claimed.Name).
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
	title, err := t.ask(ctx, titlePrompt, question, answer, titleMaxRunes)
	if err != nil {
		return "", fmt.Errorf("name the conversation: %w", err)
	}
	if title == "" {
		return "", fmt.Errorf("turn: the model named the conversation with nothing")
	}
	return title, nil
}

// Describe writes the sentence a path opened by this exchange is shown under
// its name, from the same question and answer.
//
// It is a second call rather than a second field in the title's reply: a title
// and a description have different shapes and budgets, and asking one reply for
// both makes the reply's formatting decide what a path is called.
func (t *Titler) Describe(ctx context.Context, question, answer string) (string, error) {
	description, err := t.ask(ctx, descriptionPrompt, question, answer, descriptionMaxRunes)
	if err != nil {
		return "", fmt.Errorf("describe the path: %w", err)
	}
	if description == "" {
		return "", fmt.Errorf("turn: the model described the path with nothing")
	}
	return description, nil
}

// ask sends one naming prompt over the exchange and returns the reply as the
// line a label is allowed to be.
func (t *Titler) ask(ctx context.Context, prompt, question, answer string, limit int) (string, error) {
	if t == nil {
		return "", fmt.Errorf("turn: no titler is configured")
	}
	var written strings.Builder
	request := llm.Request{
		Model: t.model,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: prompt},
			{Role: llm.RoleUser, Content: exchange(question, answer)},
		},
		MaxTokens: t.maxTokens,
	}
	if _, err := t.provider.Stream(ctx, request, func(delta llm.Delta) error {
		written.WriteString(delta.Content)
		return nil
	}); err != nil {
		return "", err
	}
	return cleanLine(written.String(), limit), nil
}

// exchange is what the namer is shown: the question whole, and as much of the
// answer as bears on what the conversation is about.
func exchange(question, answer string) string {
	return fmt.Sprintf("User asked:\n%s\n\nAssistant replied:\n%s",
		strings.TrimSpace(question), excerpt(answer, titleExcerptRunes))
}

// cleanTitle reduces a model's reply to a title: one line, no wrapping quotes,
// no trailing full stop, and within the bound.
func cleanTitle(reply string) string {
	return cleanLine(reply, titleMaxRunes)
}

// cleanLine reduces a model's reply to a line a label may be: one line, no
// wrapping quotes, no trailing full stop, and within the bound.
//
// Models answer a request for "the title alone" with an explanation or a quoted
// phrase often enough that leaving it to the caller would put those in a
// session list. Everything here is about making the reply fit the shape a label
// has, and nothing here guesses at what it should say.
func cleanLine(reply string, limit int) string {
	line := strings.TrimSpace(reply)

	// A multi-line reply is a reply that says more than the label, so the label
	// is the first line of it.
	if first, _, found := strings.Cut(line, "\n"); found {
		line = strings.TrimSpace(first)
	}
	// Straight quotes and the bracketing marks a Chinese title is wrapped in
	// are all the same thing here: something the model put around the label.
	line = strings.Trim(line, `"'`+"`"+`「」『』《》`)
	line = strings.TrimRightFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".。!！?？", r)
	})
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > limit {
		line = strings.TrimSpace(string([]rune(line)[:limit]))
	}
	return line
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
