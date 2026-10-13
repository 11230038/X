package practice

// The objective half of judging: comparing an answer against the answer the
// question stores.
//
// This file takes no context and no provider, and that is the point of it. The
// two branches of judging are separated by what they are, not by how they are
// called — one is a comparison and one is a model call — so the comparison is
// written where it cannot accidentally acquire a model call, and the folding
// rules it is defined by are written next to it rather than next to a prompt.

import (
	"fmt"
	"slices"
	"strings"

	"backend/internal/notebook"
)

// judgeObjective compares an answer against what the question stores.
//
// Comparing "B" with "B" through a model costs a call, can be wrong, and can
// answer differently the second time; a certain answer has to stay certain. The
// verdict it returns is the comparison's, and the question is refused rather
// than judged when there is nothing sound to compare against.
func judgeObjective(question notebook.Question, userAnswer string) (Judgment, error) {
	var (
		correct bool
		shown   string
	)
	switch question.Type {
	case notebook.QuestionChoice:
		want, err := acceptedChoiceKeys(question)
		if err != nil {
			return Judgment{}, err
		}
		correct = slices.Equal(parseKeys(userAnswer), want.folded)
		shown = want.shown
	case notebook.QuestionFillBlank:
		want, err := acceptedFillBlankAnswers(question)
		if err != nil {
			return Judgment{}, err
		}
		correct = slices.Contains(want.folded, foldAnswer(userAnswer))
		shown = want.shown
	}
	return Judgment{
		Kind:      KindObjective,
		IsCorrect: correct,
		Reason:    objectiveReason(userAnswer, shown, correct),
		Rubric:    []string{},
		Boundary:  objectiveBoundary,
	}, nil
}

// objectiveBoundary is what an objective judgement says about itself, and it is
// the same sentence the design writes for it: the answer came out of the bank,
// so there is nothing here to weigh.
const objectiveBoundary = "答案由题库给出，不是模型判断"

// objectiveReason says what was compared.
//
// It names both sides even when they agree, and it names them as they are
// stored rather than as they were folded: a fill-in answer that is judged wrong
// because of a character the user cannot see is a verdict they will want to
// check, and the two answers side by side is what lets them.
func objectiveReason(userAnswer, stored string, correct bool) string {
	if strings.TrimSpace(userAnswer) == "" {
		return fmt.Sprintf("你没有作答，题库的答案是「%s」。", stored)
	}
	verdict := "不一致"
	if correct {
		verdict = "一致"
	}
	return fmt.Sprintf("你答的是「%s」，题库的答案是「%s」，两者%s。", userAnswer, stored, verdict)
}

// acceptedAnswers is what a question accepts: the folded forms the comparison
// runs on, and the answer as a person should see it.
//
// The two are not the same string, and keeping them apart is what makes the
// reason readable: the comparison needs a form with no width, no capitals and
// no stray spaces in it, and a reader needs the form the bank actually stores.
// Showing the folded one would answer a question the user did not ask — "why
// does the answer say a when I wrote A".
type acceptedAnswers struct {
	folded []string
	shown  string
}

// acceptedChoiceKeys reads a choice question's stored answer as the set of
// option keys it names, refusing the question when that set does not fit.
//
// It is checked here rather than at save time because this is where a wrong
// answer does harm: a question whose stored answer names an option it does not
// offer is unanswerable, and judging it would mark every answer to it wrong.
// The fault is the bank's, so it is reported as an unusable question rather
// than as a wrong answer.
func acceptedChoiceKeys(question notebook.Question) (acceptedAnswers, error) {
	if len(question.Options) == 0 {
		return acceptedAnswers{}, fmt.Errorf(
			"%w: the choice question offers no options", ErrUnusableQuestion)
	}
	offered := make(map[string]bool, len(question.Options))
	for _, option := range question.Options {
		offered[foldKey(option.Key)] = true
	}

	parts := keyParts(question.CorrectAnswer)
	if len(parts) == 0 {
		return acceptedAnswers{}, fmt.Errorf(
			"%w: the choice question stores no answer", ErrUnusableQuestion)
	}
	accepted := acceptedAnswers{folded: make([]string, 0, len(parts))}
	for _, part := range parts {
		if !offered[part.folded] {
			return acceptedAnswers{}, fmt.Errorf(
				"%w: the stored answer names option %q, which the question does not offer",
				ErrUnusableQuestion, part.folded)
		}
		accepted.folded = append(accepted.folded, part.folded)
	}
	slices.Sort(accepted.folded)

	// Shown in the canonical order and the stored spelling: a question whose
	// answer was written "C,A" has already been read as the set it is, and
	// echoing the order it happened to be written in would read as a claim about
	// the order.
	spelling := make([]string, 0, len(parts))
	for _, key := range accepted.folded {
		written, _ := writtenOf(parts, key)
		spelling = append(spelling, written)
	}
	accepted.shown = strings.Join(spelling, ",")
	return accepted, nil
}

// acceptedFillBlankAnswers reads a fill-in question's answer as the answers it
// accepts, refusing a question that stores none.
//
// More than one is data rather than a guess. A question whose answer has more
// than one accepted spelling says so in its own answer, with "|" between them,
// which keeps the comparison exact and keeps the acceptance where a person can
// read and edit it — as opposed to asking a model to decide what "close enough"
// means, which is the thing this whole branch exists to avoid.
func acceptedFillBlankAnswers(question notebook.Question) (acceptedAnswers, error) {
	var accepted acceptedAnswers
	showing := make([]string, 0, 2)
	for _, alternative := range strings.Split(question.CorrectAnswer, "|") {
		folded := foldAnswer(alternative)
		if folded == "" || slices.Contains(accepted.folded, folded) {
			continue
		}
		accepted.folded = append(accepted.folded, folded)
		showing = append(showing, strings.TrimSpace(alternative))
	}
	if len(accepted.folded) == 0 {
		return acceptedAnswers{}, fmt.Errorf(
			"%w: the fill-in question stores no answer", ErrUnusableQuestion)
	}
	accepted.shown = strings.Join(showing, " 或 ")
	return accepted, nil
}

// keyPart is one option key as it was written: the folded form the comparison
// uses, and the spelling the question stored.
type keyPart struct {
	folded  string
	written string
}

// keyParts reads a comma-separated list of option keys, keeping the first
// spelling of each and dropping the ones that say nothing.
//
// A key written twice is one selection, which is what makes "A,A" the same
// answer as "A": the storage convention is a sorted list of distinct keys, so
// the two sides are sets rather than sequences, and comparing them as sequences
// would compare the order in which a client happened to send them.
func keyParts(answer string) []keyPart {
	parts := make([]keyPart, 0, 2)
	for _, chunk := range strings.Split(answer, ",") {
		folded := foldKey(chunk)
		if folded == "" {
			continue
		}
		if _, seen := writtenOf(parts, folded); seen {
			continue
		}
		parts = append(parts, keyPart{folded: folded, written: strings.TrimSpace(chunk)})
	}
	return parts
}

// parseKeys reads a comma-separated list of option keys as a sorted set.
func parseKeys(answer string) []string {
	parts := keyParts(answer)
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		keys = append(keys, part.folded)
	}
	slices.Sort(keys)
	return keys
}

// writtenOf returns the spelling recorded for a folded key, and whether any
// part carries it.
func writtenOf(parts []keyPart, folded string) (string, bool) {
	for _, part := range parts {
		if part.folded == folded {
			return part.written, true
		}
	}
	return "", false
}

// foldAnswer folds an answer the way the fill-in comparison is defined to.
//
// The rule is fixed and narrow: trim, collapse runs of whitespace to one space,
// turn full-width characters into their half-width forms, lower-case ASCII, and
// then require the two sides to be equal. It is not semantic — a synonym is
// wrong — and the reason it is not is that a judgement nobody can reproduce is
// not worth having. The escape hatch for synonyms is a question that lists
// them.
//
// Note that it never inserts a space that was not written: "什么是闭包" and
// "什么是 闭包" stay different answers, because folding whitespace runs cannot
// decide where a space belongs.
func foldAnswer(answer string) string {
	var (
		builder strings.Builder
		pending bool
	)
	for _, character := range toHalfWidth(answer) {
		if isSpace(character) {
			pending = true
			continue
		}
		if pending && builder.Len() > 0 {
			builder.WriteRune(' ')
		}
		pending = false
		builder.WriteRune(lowerASCII(character))
	}
	return builder.String()
}

// toHalfWidth turns full-width forms into the ASCII they stand for.
//
// The range is the one that matters: the full-width forms of the printable
// ASCII characters sit at a fixed offset, and the ideographic space is not in
// that range. Everything else — Chinese punctuation, accented letters, any
// other script — is left alone, because turning those into something else would
// be deciding that two answers are the same when nobody said they were.
func toHalfWidth(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, character := range text {
		switch {
		case character == ideographicSpace:
			builder.WriteRune(' ')
		case character >= fullWidthFirst && character <= fullWidthLast:
			builder.WriteRune(character - fullWidthOffset)
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// The full-width block, written as numbers because the characters themselves
// are indistinguishable from their half-width forms at a glance — a reader
// comparing two answers that differ only in width is exactly how this gets
// wrong.
//
// U+FF01 to U+FF5E are the full-width forms of the printable ASCII characters
// from "!" to "~", each exactly U+FF00 above the character it stands for. The
// ideographic space U+3000 sits outside that block and is its own case.
const (
	fullWidthFirst  = 0xff01
	fullWidthLast   = 0xff5e
	fullWidthOffset = 0xfee0
	// ideographicSpace is the full-width space. It folds to a space like the
	// others, which matters more than it sounds: it is what a Chinese input
	// method produces when someone means to type a space.
	ideographicSpace = 0x3000
)

// isSpace reports whether a character is whitespace the comparison folds.
func isSpace(character rune) bool {
	switch character {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

// lowerASCII folds the twenty-six letters an option key can be written in.
//
// Not unicode.ToLower: the rule is ASCII case folding, and the Unicode version
// of it answers questions this comparison has no business answering — "İ"
// lowering to two characters, for one.
func lowerASCII(character rune) rune {
	if character >= 'A' && character <= 'Z' {
		return character + ('a' - 'A')
	}
	return character
}

// foldKey folds an option key. It is the same folding as an answer's, minus the
// whitespace handling: an option key is one token by definition, so a key that
// arrives with a space in it is a key that is wrong.
func foldKey(key string) string {
	folded := toHalfWidth(strings.TrimSpace(key))
	var builder strings.Builder
	builder.Grow(len(folded))
	for _, character := range folded {
		if isSpace(character) {
			continue
		}
		builder.WriteRune(lowerASCII(character))
	}
	return builder.String()
}
