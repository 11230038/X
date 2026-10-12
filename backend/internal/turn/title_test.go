package turn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/llm"
)

func TestNewTitlerNeedsItsParts(t *testing.T) {
	provider := &scriptedProvider{}
	cases := map[string]struct {
		provider  llm.Provider
		model     string
		maxTokens int
	}{
		"no provider":   {provider: nil, model: "task-model", maxTokens: 64},
		"no model":      {provider: provider, model: "  ", maxTokens: 64},
		"no token room": {provider: provider, model: "task-model", maxTokens: 0},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTitler(test.provider, test.model, test.maxTokens); err == nil {
				t.Error("NewTitler() error = nil, want a rejection")
			}
		})
	}
}

func TestTheTitlerAsksForATitleAndNothingElse(t *testing.T) {
	provider := &scriptedProvider{responses: []llm.Response{{Content: "闭包是什么"}}}
	titler, err := NewTitler(provider, "task-model", 64)
	if err != nil {
		t.Fatalf("NewTitler() error = %v", err)
	}

	title, err := titler.Title(context.Background(), "闭包是什么？", "闭包是……")
	if err != nil {
		t.Fatalf("Title() error = %v", err)
	}
	if title != "闭包是什么" {
		t.Errorf("Title() = %q", title)
	}

	asked := provider.asked()
	if asked[0].Model != "task-model" {
		t.Errorf("Model = %q, want the task model rather than the conversation's", asked[0].Model)
	}
	if asked[0].MaxTokens != 64 {
		t.Errorf("MaxTokens = %d, want the titler's own budget", asked[0].MaxTokens)
	}
	if len(asked[0].Tools) != 0 {
		t.Error("the namer was offered tools")
	}
	// The question and the answer travel as one user message: the namer has no
	// conversation of its own to continue.
	if len(asked[0].Messages) != 2 || asked[0].Messages[0].Role != llm.RoleSystem {
		t.Fatalf("the namer was given %+v", asked[0].Messages)
	}
	if !strings.Contains(asked[0].Messages[1].Content, "闭包是什么？") {
		t.Errorf("the question is missing from %q", asked[0].Messages[1].Content)
	}
}

func TestTheTitlerReportsAModelThatSaysNothing(t *testing.T) {
	provider := &scriptedProvider{responses: []llm.Response{{Content: "   "}}}
	titler, err := NewTitler(provider, "task-model", 64)
	if err != nil {
		t.Fatalf("NewTitler() error = %v", err)
	}

	// An empty title would be stored over the session's own, and a nameless
	// entry in a list is worse than the name it replaced.
	if _, err := titler.Title(context.Background(), "问", "答"); err == nil {
		t.Error("Title() error = nil, want a rejection")
	}
}

func TestTheTitlerReportsAModelThatFails(t *testing.T) {
	provider := &scriptedProvider{err: errors.New("the naming model is unreachable")}
	titler, err := NewTitler(provider, "task-model", 64)
	if err != nil {
		t.Fatalf("NewTitler() error = %v", err)
	}

	if _, err := titler.Title(context.Background(), "问", "答"); err == nil {
		t.Error("Title() error = nil, want the provider's failure")
	}
}

func TestATitleIsReducedToSomethingAListCanShow(t *testing.T) {
	cases := map[string]struct {
		reply string
		want  string
	}{
		"plain":                  {reply: "闭包是什么", want: "闭包是什么"},
		"surrounding space":      {reply: "  闭包是什么\n", want: "闭包是什么"},
		"double quotes":          {reply: `"闭包是什么"`, want: "闭包是什么"},
		"corner brackets":        {reply: "「闭包是什么」", want: "闭包是什么"},
		"a trailing full stop":   {reply: "闭包是什么。", want: "闭包是什么"},
		"a trailing question":    {reply: "闭包是什么？", want: "闭包是什么"},
		"a line then an aside":   {reply: "闭包是什么\n这个标题说明了主题", want: "闭包是什么"},
		"only a full stop":       {reply: "。", want: ""},
		"an empty reply":         {reply: "", want: ""},
		"longer than a list row": {reply: strings.Repeat("长", 40), want: strings.Repeat("长", 24)},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := cleanTitle(test.reply); got != test.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", test.reply, got, test.want)
			}
		})
	}
}

func TestOnlyTheStartOfAnAnswerIsShownToTheNamer(t *testing.T) {
	long := strings.Repeat("甲", titleExcerptRunes+50)

	got := excerpt(long, titleExcerptRunes)
	if !strings.HasSuffix(got, "…") {
		t.Error("a cut answer does not say it was cut")
	}
	if runes := len([]rune(got)); runes != titleExcerptRunes+1 {
		t.Errorf("the excerpt is %d runes, want the limit and the mark", runes)
	}
	if got := excerpt("很短", titleExcerptRunes); got != "很短" {
		t.Errorf("excerpt() = %q, want a short answer whole", got)
	}
}

func TestTheNamerIsNotShownTheWholeAnswer(t *testing.T) {
	provider := &scriptedProvider{responses: []llm.Response{{Content: "标题"}}}
	titler, err := NewTitler(provider, "task-model", 64)
	if err != nil {
		t.Fatalf("NewTitler() error = %v", err)
	}
	if _, err := titler.Title(context.Background(), "问", strings.Repeat("答", 5000)); err != nil {
		t.Fatalf("Title() error = %v", err)
	}

	// A title is decided by what the conversation was about, which the start of
	// the answer already says. The rest would be paid for and change nothing.
	if length := len([]rune(provider.asked()[0].Messages[1].Content)); length > titleExcerptRunes+100 {
		t.Errorf("the namer was given %d runes of the exchange", length)
	}
}
