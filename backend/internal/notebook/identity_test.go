package notebook

import (
	"regexp"
	"testing"
)

// The identity rules, which are the whole reason "the same question" is a
// decidable thing. They are pure functions over strings, so there is nothing
// here a database could add: the storage contract lives in the tests beside
// this file.

func TestNormalizeFoldsWhitespaceRunsAndTrims(t *testing.T) {
	cases := []struct {
		name  string
		given string
		want  string
	}{
		{"already clean", "什么是闭包", "什么是闭包"},
		{"one run inside", "什么是  闭包", "什么是 闭包"},
		{"a run on both ends", "  什么是闭包  ", "什么是闭包"},
		{"every kind of space", "\t什么是\n闭包\v\f\r", "什么是 闭包"},
		{"a run of mixed kinds", "什么是 \t\n 闭包", "什么是 闭包"},
		{"only whitespace", " \t\n ", ""},
		{"empty", "", ""},
		// Nothing is folded that is not ASCII whitespace: see the test below.
		{"a full-width space", "什么是　闭包", "什么是　闭包"},
		{"case is kept", "What Is A Closure", "What Is A Closure"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if got := normalize(scenario.given); got != scenario.want {
				t.Errorf("normalize(%q) = %q, want %q", scenario.given, got, scenario.want)
			}
		})
	}
}

// TestNormalizeKeepsTheIdeographicSpace is the one character the rule turns on.
//
// strings.Fields would treat U+3000 as a separator and fold it, which is the
// 全角半角转换 this package refuses to do: two questions differing only in an
// ideographic space are refused an identity that says they are the same one,
// because a wrong merge costs more than a duplicate row.
func TestNormalizeKeepsTheIdeographicSpace(t *testing.T) {
	if normalize("A　B") == normalize("A B") {
		t.Fatal("a full-width space was folded to a half-width one")
	}
	if normalize("A　B") != "A　B" {
		t.Fatalf("normalize(\"A　B\") = %q, want it unchanged", normalize("A　B"))
	}
}

// TestQuestionIDIsStableAcrossWhitespace fixes what folding whitespace runs
// does and, just as importantly, what it does not do.
//
// The variants below differ only in how much whitespace there is; the second
// group differs in whether there is any. A space where the base text has none is
// content, not layout — folding it away would make "What is a closure" and
// "Whatis a closure" one question, and the rule settles for a duplicate row in
// exchange for never making that merge.
func TestQuestionIDIsStableAcrossWhitespace(t *testing.T) {
	given := QuestionID("什么是 闭包", QuestionShortAnswer)
	for _, text := range []string{" 什么是 闭包", "什么是 闭包 ", "什么是  闭包", "什么是\t闭包", "什么是\n\n闭包"} {
		if got := QuestionID(text, QuestionShortAnswer); got != given {
			t.Errorf("QuestionID(%q) = %s, want the address of the same question %s", text, got, given)
		}
	}
	for _, text := range []string{"什么是闭包", "什么是 闭包？", "What is a closure"} {
		if got := QuestionID(text, QuestionShortAnswer); got == given {
			t.Errorf("QuestionID(%q) = %s, which is a different question's address", text, got)
		}
	}
}

// TestQuestionIDSeparatesTheTextFromTheType pins that the type takes part. The
// same sentence asked two ways is two questions: they are answered differently,
// judged differently, and one of them has options.
func TestQuestionIDSeparatesTheTextFromTheType(t *testing.T) {
	text := "下列哪个是闭包"
	if QuestionID(text, QuestionChoice) == QuestionID(text, QuestionShortAnswer) {
		t.Fatal("the same text under two types got one address")
	}
}

// TestQuestionIDHasASeparatorNoQuestionCanContain is the reason the digest takes
// a NUL byte rather than the four characters that spell one.
//
// Without it the address is of the concatenation, and the concatenation cannot
// tell ("ab", "c") from ("a", "bc") — a collision that no test with ordinary
// questions would ever produce, and that the one question someone writes
// afterwards would hit.
func TestQuestionIDHasASeparatorNoQuestionCanContain(t *testing.T) {
	if QuestionID("ab", "c") == QuestionID("a", "bc") {
		t.Fatal("the text and the type are concatenated without a separator")
	}
}

func TestQuestionIDIsReadable(t *testing.T) {
	pattern := regexp.MustCompile(`^qn_[0-9a-f]{24}$`)
	id := QuestionID("什么是闭包", QuestionShortAnswer)
	if !pattern.MatchString(id) {
		t.Fatalf("QuestionID = %q, want %s", id, pattern)
	}
}

// TestCategoryIDComesFromTheName is what makes two writers creating the same
// category at the same moment collide instead of making two rows. It is also
// the reason the id is not random, which is the opposite of the row key's rule.
func TestCategoryIDComesFromTheName(t *testing.T) {
	if categoryID("数学") != categoryID("数学") {
		t.Fatal("the same category name got two ids")
	}
	// Named-matched under a binary collation, so case is part of the name.
	if categoryID("Math") == categoryID("math") {
		t.Fatal("two categories the schema treats as different got one id")
	}
	pattern := regexp.MustCompile(`^nbc_[0-9a-f]{24}$`)
	if id := categoryID("数学"); !pattern.MatchString(id) {
		t.Fatalf("categoryID = %q, want %s", id, pattern)
	}
}

func TestQuestionValidate(t *testing.T) {
	cases := []struct {
		name     string
		question Question
		refused  bool
	}{
		{"a choice question", Question{Text: "选一个", Type: QuestionChoice}, false},
		{"a blank text", Question{Text: "", Type: QuestionChoice}, true},
		{"a text of spaces", Question{Text: " \t\n ", Type: QuestionChoice}, true},
		{"an unknown type", Question{Text: "选一个", Type: QuestionType("essay")}, true},
		{"no type", Question{Text: "选一个"}, true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			err := scenario.question.Validate()
			if refused := err != nil; refused != scenario.refused {
				t.Fatalf("Validate() = %v, want refused = %v", err, scenario.refused)
			}
		})
	}
}

func TestFilterValidate(t *testing.T) {
	cases := []struct {
		name    string
		filter  Filter
		refused bool
	}{
		{"the zero filter", Filter{}, false},
		{"one resolution", Filter{Resolution: ResolutionDone}, false},
		{"an unknown resolution", Filter{Resolution: Resolution("maybe")}, true},
		{"one type", Filter{Type: QuestionChoice}, false},
		{"an unknown type", Filter{Type: QuestionType("essay")}, true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			err := scenario.filter.Validate()
			if refused := err != nil; refused != scenario.refused {
				t.Fatalf("Validate() = %v, want refused = %v", err, scenario.refused)
			}
		})
	}
}

func TestCleanCategoryNames(t *testing.T) {
	cases := []struct {
		name  string
		given []string
		want  []string
	}{
		{"none", nil, []string{}},
		{"one", []string{"数学"}, []string{"数学"}},
		{"trimmed", []string{"  数学  "}, []string{"数学"}},
		{"blanks dropped", []string{"", "   ", "数学"}, []string{"数学"}},
		{"repeats dropped", []string{"数学", "数学", " 数学 "}, []string{"数学"}},
		{"order kept", []string{"数学", "英语"}, []string{"数学", "英语"}},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			got := cleanCategoryNames(scenario.given)
			if len(got) != len(scenario.want) {
				t.Fatalf("cleanCategoryNames(%q) = %q, want %q", scenario.given, got, scenario.want)
			}
			for index := range got {
				if got[index] != scenario.want[index] {
					t.Fatalf("cleanCategoryNames(%q) = %q, want %q", scenario.given, got, scenario.want)
				}
			}
		})
	}
}
