// Package routing decides which capability a turn should run when the client did
// not ask for one, by reading what the user wrote (TOOL-08).
//
// It is a leaf: a table of patterns, a closed set of words, and one pure
// function. It imports nothing from this service — not the capability it names,
// not the runtime that asks it — so that what a sentence is read as can be
// checked row by row without a turn, a session or a model.
//
// The table is deliberately narrow, and narrowness is the point. Routing is a
// guess about what somebody meant, made before they have said anything else, and
// a wrong guess costs more than a missed one: a user put into the question writer
// by accident has to notice, cancel and rephrase, while a user left in the
// general assistant has lost nothing. So only an asking verb and a question noun
// together are a request for questions, and a request to be examined question by
// question is not one at all — the question writer produces a set and stops.
// Reading "题" anywhere in a sentence as a request for questions is the mistake
// this table is shaped to avoid.
package routing

import (
	"regexp"
	"strings"
)

// By is what a decision says when a rule made it.
//
// It is a constant because the payload has the field and a client reads it: a
// decision made some other way — the client naming a capability, say — would be
// told apart by this value, and this version only ever makes them one way.
const By = "rule"

// Question is the capability that writes question sets. The name is spelled here
// rather than imported from the package that implements it: that package belongs
// to the service's runtime, and a table that knew it would not be a leaf.
const Question = "deep_question"

// Decision is where a turn that did not ask for a capability should be sent.
type Decision struct {
	// Capability is the capability to run.
	Capability string
	// By is how the decision was made.
	By string
	// Reason is which rule matched, in one sentence, for whoever reads the turn
	// later: the user watching the mode change, or an operator asking why a
	// sentence went where it went.
	Reason string
	// Confidence is that rule's own weight. It is not a measurement and not a
	// model's estimate: a request that names how many questions it wants is a
	// stronger reading of the same intent than one that only names the noun, and
	// the table says so where the rule is written.
	Confidence float64
}

// rule is one line of the table: what it reads, where it sends the turn, and how
// strongly it reads it.
type rule struct {
	pattern    *regexp.Regexp
	capability string
	reason     string
	confidence float64
}

// The words the rules read. Each is a closed set written out rather than built
// by pattern, so that what this table can recognise is a list somebody can look
// at — the same reasoning the question writer's own reading of a request uses.
const (
	// kind is the question kinds a request can name. A kind nobody can judge is
	// not one of them.
	kind = `选择|单选|多选|填空|主观|简答|问答|论述|判断`
	// noun is what the thing being asked for is called.
	noun = `题(?:目|组)?|习题|练习题?|卷子|试卷|考卷`
	// count is a number attached to questions, written either way.
	count = `[0-9一二两三四五六七八九十几]+`
	// verb is the asking verb: 出 is what a person says, 生成 is what a person
	// writing to a machine says.
	verb = `出|生成`
	// give is how a set is asked for with no verb at all. 来 is the one to watch:
	// it is a common word, and the rules below keep it safe by requiring the noun
	// to arrive almost immediately after it.
	give = `给我|来|帮我|帮忙`
)

var (
	// numberedOut reads a request that says how many, with the verb in front:
	// 出 10 道题, 出 5 道选择题.
	numberedOut = regexp.MustCompile(`(?:` + verb + `)\s*` + count + `\s*[道个]\s*(?:` + kind + `)?\s*(?:` + noun + `)`)
	// numberedGive is the same request phrased as a wish rather than an
	// instruction: 给我 5 道题, 给我出 5 道选择题, 来三道. 来 is allowed here as
	// well as standing on its own, because 给我来三道 is how both are said at once.
	numberedGive = regexp.MustCompile(`(?:` + give + `)\s*(?:` + verb + `|来)?\s*` + count + `\s*[道个]\s*(?:` + kind + `)?\s*(?:` + noun + `)`)
	// bareOut reads a request that names no count: 出题, 出个题, 出几道选择题,
	// 生成题目.
	bareOut = regexp.MustCompile(`(?:` + verb + `)\s*(?:个|几|一些|点|几道)?\s*(?:` + kind + `)?\s*(?:` + noun + `)`)
	// bareGive reads the same with no verb: 给我几道题, 来点题, 给我出题.
	bareGive = regexp.MustCompile(`(?:` + give + `)\s*(?:` + verb + `|来)?\s*(?:个|几|一些|点|几道|道)?\s*(?:` + kind + `)?\s*(?:` + noun + `)`)
	// english reads the same request written in English, which is a set of its
	// own because the words are: generate 5 questions, create me some questions.
	// The verb and the noun are held close together, so "make sure the questions
	// are clear" is not read as a request to make any.
	english = regexp.MustCompile(`(?i)\b(?:generate|create|make|produce)\s+(?:me\s+)?(?:[0-9]+\s+)?(?:some\s+|more\s+|new\s+)*(?:quiz\s+)?questions?\b`)
	// englishGive is "give me 5 questions", which the verb above does not cover.
	englishGive = regexp.MustCompile(`(?i)\bgive me\s+(?:[0-9]+\s+)?(?:some\s+|more\s+|new\s+)*(?:quiz\s+)?questions?\b`)
	// englishWith is "quiz me with 5 questions" — the one phrasing that holds
	// being examined and asking for a set in the same sentence, and names the set
	// it wants, which is why the guard below lets it through.
	englishWith = regexp.MustCompile(`(?i)\bquiz me with\b[^.!?\n]{0,24}\bquestions?\b`)
)

// table is the rules, in the order they are tried; the first match decides.
//
// The numbered phrasings come before the bare ones so that "给我出 5 道题" is
// reported as what it is — a request that named a count — rather than as the
// weaker reading the same sentence also supports.
var table = []rule{
	{
		pattern:    numberedOut,
		capability: Question,
		reason:     "话里用「出 + 数量 + 道/个 + 题」说了要几道题",
		confidence: 0.95,
	},
	{
		pattern:    numberedGive,
		capability: Question,
		reason:     "话里用「给我/来 + 数量 + 道/个 + 题」说了要几道题",
		confidence: 0.95,
	},
	{
		pattern:    english,
		capability: Question,
		reason:     "话里是要 generate / create 一批 questions",
		confidence: 0.9,
	},
	{
		pattern:    englishGive,
		capability: Question,
		reason:     "话里是要 give me … questions",
		confidence: 0.9,
	},
	{
		pattern:    bareOut,
		capability: Question,
		reason:     "话里是要出题（出题 / 出几道 / 生成题目 这类说法）",
		confidence: 0.8,
	},
	{
		pattern:    bareGive,
		capability: Question,
		reason:     "话里是要题（给我几道题 / 来点题 这类说法）",
		confidence: 0.75,
	},
	{
		pattern:    englishWith,
		capability: Question,
		reason:     "话里是要 quiz me with … questions，点明了要的是一组题",
		confidence: 0.85,
	},
}

// interactive is the intent that keeps a turn in the general assistant: a request
// to be examined question by question.
//
// The question writer produces a set and stops. It does not ask the questions
// back one at a time, so a sentence that asks to be quizzed is not a request for
// a set even when the same sentence also asks for one — "出 10 道题考考我" is
// somebody asking to be examined, and the ten questions are how.
//
// unless is a phrase that makes such a sentence an order for a set after all,
// and it is a second pattern rather than a lookahead because Go's regular
// expressions have no lookahead: "quiz me with 5 questions" says which set it
// wants.
var interactive = []struct {
	pattern *regexp.Regexp
	unless  *regexp.Regexp
}{
	{pattern: regexp.MustCompile(`考考我|考我|考一下我|测一测我|测试我|小测我|面试我`)},
	{
		pattern: regexp.MustCompile(`(?i)\bquiz me\b`),
		unless:  regexp.MustCompile(`(?i)\bquiz me with\b`),
	},
}

// bankWords are the words that name the bank rather than its contents, and they
// are read off the text before the rules are matched.
//
// "给我题库" is a request for the bank; "从第三章题库里出 10 道题" is a request
// for questions that happens to say where from. Reading the word out tells the
// two apart, without every rule having to know about it.
var bankWords = []string{"题库"}

// Route reads what the user wrote and decides where the turn should go.
//
// A returned false is not "no idea": it is "this table has nothing to say about
// that sentence", and the caller runs the default capability. The three
// conditions are checked in the order they cost: nothing to read, no wish to be
// examined, then the rules in order.
func Route(content string) (Decision, bool) {
	text := strings.TrimSpace(content)
	if text == "" {
		return Decision{}, false
	}
	if asksToBeExamined(text) {
		return Decision{}, false
	}
	readable := withoutBankWords(text)
	for _, candidate := range table {
		if !candidate.pattern.MatchString(readable) {
			continue
		}
		return Decision{
			Capability: candidate.capability,
			By:         By,
			Reason:     candidate.reason,
			Confidence: candidate.confidence,
		}, true
	}
	return Decision{}, false
}

// asksToBeExamined reports whether the sentence asks for questions to be put to
// the user one at a time.
func asksToBeExamined(text string) bool {
	for _, guard := range interactive {
		if !guard.pattern.MatchString(text) {
			continue
		}
		if guard.unless != nil && guard.unless.MatchString(text) {
			continue
		}
		return true
	}
	return false
}

// withoutBankWords is the sentence with the bank's own name taken out of it.
func withoutBankWords(text string) string {
	for _, word := range bankWords {
		text = strings.ReplaceAll(text, word, "")
	}
	return text
}
