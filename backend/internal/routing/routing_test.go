package routing

import (
	"strings"
	"testing"
)

// The two tables below are the product's own words (PRD §19.3, §23.5 第 21 条),
// and they are the reason this package exists as a table rather than as a
// condition: the negative side is the one that matters, because every phrasing
// on it is a sentence somebody will really write in a conversation about
// studying, and routing it into the question writer is the mistake this version
// is most likely to make.

func TestWhatIsReadAsARequestForQuestions(t *testing.T) {
	tests := []struct {
		name string
		said string
	}{
		{"the plain request", "出 10 道题"},
		{"with the verb in front", "帮我出 10 道题"},
		{"about a chapter", "针对这一章出题"},
		{"a kind and a count", "给我出 5 道选择题"},
		{"a wish", "给我 5 道题"},
		{"a count with 来 instead of a verb", "来三道题"},
		{"a kind with no count", "出几道填空题"},
		{"the machine register", "生成 20 道题"},
		{"one question", "出个题"},
		{"both words", "给我出题"},
		{"some questions", "来点题"},
		{"naming the bank it should come from", "从第三章题库里出 10 道题"},
		{"english", "generate 5 questions"},
		{"english, capitalised", "Generate 5 questions about chapter 3"},
		{"english, as a wish", "give me 5 questions"},
		{"english, no count", "create me some questions"},
		{"english, examined with a named set", "quiz me with 5 questions about chapter 3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, matched := Route(test.said)
			if !matched {
				t.Fatalf("Route(%q) matched nothing, want a route to %s", test.said, Question)
			}
			if decision.Capability != Question {
				t.Errorf("Route(%q) went to %q, want %q", test.said, decision.Capability, Question)
			}
			if decision.By != By {
				t.Errorf("Route(%q).By = %q, want %q", test.said, decision.By, By)
			}
			// The reason is what a person reads afterwards to see why a turn went
			// where it went, so an empty one would be a decision nobody can audit.
			if strings.TrimSpace(decision.Reason) == "" {
				t.Errorf("Route(%q) matched with no reason given", test.said)
			}
			if decision.Confidence <= 0 || decision.Confidence > 1 {
				t.Errorf("Route(%q).Confidence = %v, want a weight between 0 and 1",
					test.said, decision.Confidence)
			}
		})
	}
}

func TestWhatIsNotReadAsARequestForQuestions(t *testing.T) {
	tests := []struct {
		name string
		said string
	}{
		// The words that are about questions without being a request for any.
		{"the bare noun", "习题"},
		{"practising", "练习"},
		{"a test", "测验"},
		{"an exam", "考试"},
		{"the bank", "题库"},
		{"asking about the bank", "什么是题库"},
		{"handing over the bank", "给我题库"},
		{"practising, as a wish", "我想练习一下"},
		{"asking what is covered", "考试范围是什么"},
		// The interactive intent, which the question writer is not.
		{"being examined", "考考我第三章"},
		{"being examined in English", "quiz me"},
		{"being examined on a chapter", "Quiz me on chapter three"},
		// A sentence that asks for both is a wish to be examined: the questions
		// are how, not what.
		{"both at once", "出 10 道题考考我"},
		{"both at once, as a wish", "给我出几道题考考我"},
		// Questions that are being talked about rather than asked for.
		{"the noun and nothing else", "这道题我不会"},
		{"asking for help with one", "帮我看看这道题怎么做"},
		{"asking for an explanation", "给我讲一下这一章的习题"},
		{"asking for a summary", "帮我总结这一章"},
		{"asking about a chapter", "第三章讲的是什么"},
		{"asking how much", "一道题多少钱"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, matched := Route(test.said)
			if matched {
				t.Errorf("Route(%q) went to %q (%s), want the turn left to the default",
					test.said, decision.Capability, decision.Reason)
			}
		})
	}
}

func TestNothingSaidIsNothingToRoute(t *testing.T) {
	for _, said := range []string{"", "   ", "\n\t"} {
		if decision, matched := Route(said); matched {
			t.Errorf("Route(%q) = %+v, want nothing", said, decision)
		}
	}
}

func TestARequestThatNamesACountIsReadMoreStronglyThanOneThatDoesNot(t *testing.T) {
	// The weight is the rule's own, written where the rule is, and this is what
	// keeps it that: the same intent said with a number is a stronger reading
	// than the same intent said without one.
	counted, _ := Route("出 10 道题")
	bare, _ := Route("出题")
	if counted.Confidence <= bare.Confidence {
		t.Errorf("a counted request reads as %v and a bare one as %v, want the counted one stronger",
			counted.Confidence, bare.Confidence)
	}
	if counted.Reason == bare.Reason {
		t.Errorf("both requests report the reason %q, want each rule to say what it read", counted.Reason)
	}
}

func TestEveryRuleSaysWhereWhyAndHowStrongly(t *testing.T) {
	// A rule with no capability would route a turn to nowhere, one with no reason
	// would make a decision nobody can audit, and a weight outside (0, 1] would
	// be a number the client cannot read as a strength.
	for index, candidate := range table {
		if candidate.pattern == nil {
			t.Errorf("rule %d reads no pattern", index)
			continue
		}
		if candidate.capability != Question {
			t.Errorf("rule %d goes to %q, want %q", index, candidate.capability, Question)
		}
		if strings.TrimSpace(candidate.reason) == "" {
			t.Errorf("rule %d (%s) gives no reason", index, candidate.pattern)
		}
		if candidate.confidence <= 0 || candidate.confidence > 1 {
			t.Errorf("rule %d (%s) carries the weight %v, want between 0 and 1",
				index, candidate.pattern, candidate.confidence)
		}
	}
}

func TestTheBankIsNotTheOnlyWordThatIsReadOut(t *testing.T) {
	// Reading the bank's name out is not a general licence to ignore words: a
	// sentence that only names the bank is still not a request for questions,
	// which is the same test as the one above said a second way.
	if _, matched := Route("题库"); matched {
		t.Error("naming the bank alone was read as a request for questions")
	}
	if _, matched := Route("第三章题库"); matched {
		t.Error("a chapter's bank alone was read as a request for questions")
	}
}
