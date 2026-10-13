package deepquestion

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/notebook"
)

// Plan is the set of questions one turn set out to write.
//
// It is the whole of what ideation produces, and it is deliberately a value and
// not a behaviour: the turn reads it out of the request, shows it, and hands it
// to the model's stage as an instruction. Nothing about it is a promise the
// service enforces afterwards — the count is what the progress line is counted
// against, not a quota that stops a model from writing eleven.
type Plan struct {
	// Count is how many questions this turn will write, already clamped to the
	// deployment's ceiling.
	Count int
	// Types are the kinds the user asked for, in the closure's own order. An
	// empty list is not "no kinds": it is "the request named none", which leaves
	// the choice to the model.
	Types []notebook.QuestionType
	// Difficulty is one of the three the bank stores, empty when the request did
	// not say.
	Difficulty string
	// Categories are the filing labels the request named.
	Categories []string
	// MaterialID and MaterialTitle are the material the questions are to come
	// from. They are what the turn's survey found rather than what the turn was
	// given, because a capability is never told what it was given.
	MaterialID    string
	MaterialTitle string
}

// ideate works out what this turn is going to write, and no model is called to
// do it.
//
// Three steps: ask the runtime to survey this turn's material, read the set of
// questions out of the user's own words by rule, and lay both out where the user
// can see them. It is the third time this codebase has refused to spend a model
// call on working out what to do before doing it — the general assistant's
// exploring stage, the context pre-survey, and here — and the reasoning is the
// same each time: what this step produces is a specification that can be
// computed, and its value is that it is on screen, not that it is well phrased.
//
// It has a second effect worth naming. The specification lands before the first
// question does, so the progress a question reports ("第 3 道，共 10 道") is true
// from the first call rather than once the model has finished and can be asked
// how many it wrote.
func (c *Capability) ideate(ctx context.Context, rc capability.RunContext) (Plan, capability.Grounding, error) {
	if err := capability.EmitStage(ctx, rc, event.TypeStageStart, StageIdeation); err != nil {
		return Plan{}, capability.Grounding{}, err
	}
	asked := question(rc.Messages())
	grounding, err := rc.Ground(ctx, asked)
	if err != nil {
		return Plan{}, capability.Grounding{}, err
	}
	plan := readPlan(asked, c.limits)
	plan.MaterialID, plan.MaterialTitle = materialOf(grounding.Sources)
	if err := publishPlan(ctx, rc, plan, grounding); err != nil {
		return Plan{}, capability.Grounding{}, err
	}
	if err := capability.EmitStage(ctx, rc, event.TypeStageEnd, StageIdeation); err != nil {
		return Plan{}, capability.Grounding{}, err
	}
	return plan, grounding, nil
}

// publishPlan tells whoever is watching what this turn is about to write.
//
// The sources are published only when there were some, which is the rule the
// general assistant's exploring stage already follows: a turn carrying nothing
// gets no sources event, because an empty citation list is a list to cite from.
// The observation is published either way, and that is the difference between
// the two steps — the plan exists whether or not there was material to ground
// it, and a turn that found nothing still owes the user the sentence saying what
// it is about to do.
//
// The observation carries both the survey's summary, in the same shape the
// general assistant publishes it in, and the plan itself: a client that only
// knows how to draw "what was looked at" reads the first, and one drawing the
// question set reads the second.
func publishPlan(ctx context.Context, rc capability.RunContext, plan Plan, grounding capability.Grounding) error {
	if len(grounding.Sources) > 0 {
		if err := rc.Emitter().Emit(ctx, event.Event{
			Type:     event.TypeSources,
			Stage:    StageIdeation,
			Metadata: map[string]any{event.MetaSources: grounding.Sources},
		}); err != nil {
			return err
		}
	}
	return rc.Emitter().Emit(ctx, event.Event{
		Type:    event.TypeObservation,
		Stage:   StageIdeation,
		Content: plan.sentence(),
		Metadata: map[string]any{
			event.MetaGrounding: map[string]any{
				"summary":      groundingSummary(grounding),
				"source_count": len(grounding.Sources),
				"unavailable":  orEmpty(grounding.Unavailable),
			},
			event.MetaQuestionPlan: plan.metadata(),
		},
	})
}

// sentence is the plan in one line, for a client that shows what a turn is about
// to do without reading the structured form.
func (p Plan) sentence() string {
	var text strings.Builder
	fmt.Fprintf(&text, "我准备出 %d 道题", p.Count)
	if len(p.Types) > 0 {
		names := make([]string, 0, len(p.Types))
		for _, kind := range p.Types {
			names = append(names, kindName(kind))
		}
		fmt.Fprintf(&text, "，题型是%s", strings.Join(names, "、"))
	}
	if p.Difficulty != "" {
		fmt.Fprintf(&text, "，难度%s", difficultyName(p.Difficulty))
	}
	if len(p.Categories) > 0 {
		fmt.Fprintf(&text, "，归到「%s」", strings.Join(p.Categories, "、"))
	}
	if p.MaterialTitle != "" {
		fmt.Fprintf(&text, "，取材于《%s》", p.MaterialTitle)
	}
	return text.String() + "。"
}

// metadata is the plan as the observation payload carries it.
//
// The two lists are lists even when they are empty, and the two strings are
// strings even when they are empty: a payload that serialised an empty list as
// null would make every reader tell "none named" apart from "the field is not
// there", and the count is the only number that is always a decision.
func (p Plan) metadata() map[string]any {
	types := make([]string, 0, len(p.Types))
	for _, kind := range p.Types {
		types = append(types, string(kind))
	}
	return map[string]any{
		"count":          p.Count,
		"question_types": types,
		"difficulty":     p.Difficulty,
		"material_id":    p.MaterialID,
		"material_title": p.MaterialTitle,
		"categories":     orEmpty(p.Categories),
	}
}

// nameOf the three kinds, for the sentence a person reads.
func kindName(kind notebook.QuestionType) string {
	switch kind {
	case notebook.QuestionChoice:
		return "选择题"
	case notebook.QuestionFillBlank:
		return "填空题"
	default:
		return "主观题"
	}
}

func difficultyName(difficulty string) string {
	switch difficulty {
	case "easy":
		return "偏简单"
	case "medium":
		return "中等"
	default:
		return "偏难"
	}
}

// groundingSummary is the survey in one line, in the shape and the words the
// general assistant's exploring stage publishes it with. The two capabilities
// are read by the same client, and a summary that said the same thing
// differently in each would be two things to keep in step.
func groundingSummary(grounding capability.Grounding) string {
	summary := fmt.Sprintf("查看了本轮携带的材料，找到 %d 条相关片段", len(grounding.Sources))
	if len(grounding.Sources) == 0 {
		summary = "查看了本轮携带的材料，没有找到相关片段"
	}
	if len(grounding.Unavailable) > 0 {
		summary += fmt.Sprintf("；有 %d 项材料没有读到", len(grounding.Unavailable))
	}
	return summary
}

// orEmpty keeps an empty list a list.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// question is what this turn is answering: the newest thing the user said.
//
// It is read from the history rather than handed in, the same way the general
// assistant's exploring stage reads it, because the history is what the runtime
// already gives a capability and a second way to say the same thing would be a
// second thing to keep in step.
func question(history []llm.Message) string {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == llm.RoleUser {
			return history[index].Content
		}
	}
	return ""
}

// materialOf names the material a plan is to be written from.
//
// It is the best-ranked passage's document, and that is a deliberate reading of
// what "针对这一章出题" means: the chapter is whatever the survey found most
// about, and the capability has no other handle on material at all — the
// attachments a turn carries are the runtime's, and a capability is never told
// what it was given (KB-07). A turn that found nothing has no material, and the
// two fields stay empty rather than naming a document nobody read.
func materialOf(sources []event.Source) (string, string) {
	if len(sources) == 0 {
		return "", ""
	}
	return sources[0].FileID, sources[0].Title
}

// readPlan reads the set of questions out of what the user asked for.
//
// It is rules and defaults, and the reason it is not a model call is written up
// on ideate. Everything here is deliberately narrow: a rule that reads more than
// the sentence meant would write a set nobody asked for, which is worse than
// writing the default one, because the default is at least the deployment's own
// number and the wrong reading is a surprise.
func readPlan(content string, limits Limits) Plan {
	return Plan{
		Count:      readCount(content, limits),
		Types:      readTypes(content),
		Difficulty: readDifficulty(content),
		Categories: readCategories(content),
	}
}

// The words the rules read. Each is a closed set of the spellings people
// actually use, written out here rather than built by pattern, so that what the
// turn can read is a list somebody can look at.
var (
	// countPattern finds a number the user attached to questions: "出 10 道",
	// "给我 5 题", "三道". The counting word is what makes it a count — without
	// it "第一章出题" would read as a request for one question.
	countPattern = regexp.MustCompile(`([0-9]+|[一二两三四五六七八九十]+)\s*(?:道|个|题)`)
	// choiceWords, fillBlankWords and shortAnswerWords are the three kinds'
	// spellings. A kind nobody can judge is not one of them: 判断题 is a way of
	// writing a two-option choice question, and reading it as one would be this
	// service deciding how somebody else's question is answered.
	choiceWords      = regexp.MustCompile("选择|单选|多选")
	fillBlankWords   = regexp.MustCompile("填空")
	shortAnswerWords = regexp.MustCompile("主观|简答|问答|论述")
	// The three difficulties. The earliest spelling mentioned wins, so a sentence
	// that names two of them ("从简单到困难") is read as the first one it names.
	//
	// None of them is the bare character 难, and that is the point of writing the
	// list out: 难度 is how the field itself is named, so "难度中等" contains 难
	// earlier than it contains 中等 and a bare 难 would read every such request as
	// the hardest one — including "难度简单". The words below are the ones people
	// write when they mean the difficulty, and a request that says it some other
	// way is read as having said nothing, which leaves the choice to the model.
	easyWords   = regexp.MustCompile("简单|容易|基础|入门")
	mediumWords = regexp.MustCompile("中等|一般|普通")
	hardWords   = regexp.MustCompile("困难|较难|偏难|难题|难点|进阶|拔高|挑战")
	// chapterPattern finds the filing label a request names, which in Chinese is
	// how a chapter is written. It is the only category rule there is: anything
	// looser would invent a label the user never used, and the bank's labels are
	// what the question list is filed and filtered by.
	chapterPattern = regexp.MustCompile(`第[0-9一二两三四五六七八九十百]+[章节讲部课]`)
	// chineseNumerals are the digits a count can be written with. 十 is handled
	// in chineseNumber rather than here, because it is the only one that is a
	// position rather than a digit.
	chineseNumerals = map[rune]int{
		'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5,
		'六': 6, '七': 7, '八': 8, '九': 9,
	}
)

// readCount reads how many questions were asked for, or the deployment's own
// number.
//
// A count larger than the ceiling is clamped rather than refused: it is the same
// judgement the listing endpoints make about an oversized page, and for a
// stronger reason — "给我 200 道题" is a request that would be two hundred model
// calls, and answering it with the maximum is a service that did as much as it
// is willing to do rather than one that did nothing and explained why.
func readCount(content string, limits Limits) int {
	match := countPattern.FindStringSubmatch(content)
	if match == nil {
		return limits.Default
	}
	count := 0
	if digits := match[1]; digits[0] >= '0' && digits[0] <= '9' {
		count, _ = strconv.Atoi(digits)
	} else {
		count = chineseNumber(digits)
	}
	if count <= 0 {
		// A number the rules cannot make sense of is no number: the default is a
		// better answer than a guess at what "几道" written as digits might mean.
		return limits.Default
	}
	if count > limits.Max {
		return limits.Max
	}
	return count
}

// readTypes reads which kinds were asked for, in the closure's own order.
//
// The order is fixed rather than the order they appeared in, so that the same
// request always produces the same list: the plan is compared against itself in
// tests and read by a client, and a list whose order depended on the phrasing
// would be a list with no meaning.
func readTypes(content string) []notebook.QuestionType {
	kinds := make([]notebook.QuestionType, 0, 3)
	for _, asked := range []struct {
		kind  notebook.QuestionType
		words *regexp.Regexp
	}{
		{notebook.QuestionChoice, choiceWords},
		{notebook.QuestionFillBlank, fillBlankWords},
		{notebook.QuestionShortAnswer, shortAnswerWords},
	} {
		if asked.words.MatchString(content) {
			kinds = append(kinds, asked.kind)
		}
	}
	return kinds
}

// readDifficulty reads the difficulty the request named.
//
// The earliest spelling wins, because a sentence that names two of them is
// describing a range — "从简单到困难" starts where it starts — and the first one
// mentioned is the only reading that does not require deciding what the user
// meant. Nothing mentioned is an empty string and not a middle value: this
// version has no default difficulty, and "medium" in the plan would be a claim
// about the questions that nobody made.
func readDifficulty(content string) string {
	best, found := len(content), ""
	for _, named := range []struct {
		value string
		words *regexp.Regexp
	}{
		{"easy", easyWords},
		{"medium", mediumWords},
		{"hard", hardWords},
	} {
		at := named.words.FindStringIndex(content)
		if at == nil || at[0] >= best {
			continue
		}
		best, found = at[0], named.value
	}
	return found
}

// readCategories reads the filing labels the request named, in the order they
// were named and each of them once.
func readCategories(content string) []string {
	found := chapterPattern.FindAllString(content, -1)
	categories := make([]string, 0, len(found))
	seen := make(map[string]bool, len(found))
	for _, name := range found {
		if seen[name] {
			continue
		}
		seen[name] = true
		categories = append(categories, name)
	}
	return categories
}

// chineseNumber reads a number written in Chinese digits.
//
// It handles the forms a count is written in — 三, 十, 十二, 二十三 — and
// returns nothing for anything else, which readCount reads as "no number". A
// parser that guessed at the rest would turn a typo into a request for a
// different number of questions than the one that was written.
func chineseNumber(digits string) int {
	total, sawPosition := 0, false
	for _, character := range digits {
		if character == '十' {
			if total == 0 {
				total = 1
			}
			total *= 10
			sawPosition = true
			continue
		}
		value, ok := chineseNumerals[character]
		if !ok {
			return 0
		}
		if sawPosition {
			total += value
		} else {
			total = value
		}
	}
	return total
}
