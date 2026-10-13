package deepquestion

import (
	"strings"
	"testing"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/notebook"
)

// rules are the limits the table below is read against: five questions when the
// request says nothing, thirty as the ceiling.
var rules = Limits{Default: 5, Max: 30}

func TestTheCountIsReadOutOfTheRequest(t *testing.T) {
	// The counting word is what makes a number a count, and a request that has
	// none of them is read as having asked for nothing in particular — which is
	// the deployment's number, not zero and not a guess.
	tests := []struct {
		name    string
		request string
		want    int
	}{
		{"digits", "出 10 道题", 10},
		{"digits without a space", "出10道题", 10},
		{"a Chinese count", "给我来三道", 3},
		{"a Chinese count with a position", "出十二道", 12},
		{"a Chinese count over a position", "出二十道", 20},
		{"the ceiling itself", "出 30 道题", 30},
		{"more than the ceiling is clamped", "给我 200 道题", 30},
		{"a chapter number is not a count", "第 3 章出 5 道题", 5},
		{"nothing said", "出题", 5},
		{"a number nobody wrote", "出几道题", 5},
		{"zero is not a request for none", "出 0 道题", 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readCount(test.request, rules); got != test.want {
				t.Errorf("readCount(%q) = %d, want %d", test.request, got, test.want)
			}
		})
	}
}

func TestTheKindsAreAClosedSetReadInAFixedOrder(t *testing.T) {
	// The list is in the closure's own order rather than the order the words
	// appeared in: the plan is what the model is instructed with and what a
	// client draws, and a list whose order depended on the phrasing would be a
	// list with nothing to read into it.
	tests := []struct {
		name    string
		request string
		want    []notebook.QuestionType
	}{
		{"one kind", "出 5 道选择题", []notebook.QuestionType{notebook.QuestionChoice}},
		{"its other spellings", "出 5 道单选题", []notebook.QuestionType{notebook.QuestionChoice}},
		{"fill in the blank", "出 5 道填空题", []notebook.QuestionType{notebook.QuestionFillBlank}},
		{"a written answer", "出 3 道简答题", []notebook.QuestionType{notebook.QuestionShortAnswer}},
		{"two kinds, one order", "出 5 道填空题和选择题", []notebook.QuestionType{notebook.QuestionChoice, notebook.QuestionFillBlank}},
		{"two kinds, the other phrasing", "出 5 道选择题和填空题", []notebook.QuestionType{notebook.QuestionChoice, notebook.QuestionFillBlank}},
		{"a kind with no answer key is not one", "出 5 道判断题", []notebook.QuestionType{}},
		{"nothing said", "出题", []notebook.QuestionType{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readTypes(test.request); !equalTypes(got, test.want) {
				t.Errorf("readTypes(%q) = %v, want %v", test.request, got, test.want)
			}
		})
	}
}

func TestTheDifficultyIsTheEarliestOneNamed(t *testing.T) {
	// A sentence that names two of them is describing a range, and where it
	// starts is the only reading that does not require deciding what the user
	// meant. Nothing named is nothing said: the empty string rather than a middle
	// value, because "medium" in the plan would be a claim nobody made.
	tests := []struct {
		name    string
		request string
		want    string
	}{
		{"easy", "出 10 道简单的题", "easy"},
		{"its other spellings", "出 10 道基础题", "easy"},
		{"medium", "出 10 道难度中等的题", "medium"},
		{"medium, plainly", "出 10 道一般的题", "medium"},
		{"hard", "出 3 道难题", "hard"},
		{"a range starts where it starts", "出 10 道从简单到困难的题", "easy"},
		// The regression: 难度中等 and 难度简单 both contain 难 before they contain
		// the word that says which one they mean, so a rule that looked for the
		// bare character read every request named through the field's own name as
		// the hardest one.
		{"the field's own name is not the difficulty", "难度简单，出 10 道题", "easy"},
		{"nothing said", "出 10 道题", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readDifficulty(test.request); got != test.want {
				t.Errorf("readDifficulty(%q) = %q, want %q", test.request, got, test.want)
			}
		})
	}
}

func TestTheCategoryIsTheChapterTheRequestNames(t *testing.T) {
	// A chapter is the only filing label a request names in Chinese, and it is
	// taken as written: a looser rule would invent a label the user never used,
	// and the bank's labels are what the question list is filtered by.
	tests := []struct {
		name    string
		request string
		want    []string
	}{
		{"one chapter", "针对第三章出 10 道题", []string{"第三章"}},
		{"a lecture rather than a chapter", "第二讲的题", []string{"第二讲"}},
		{"two of them, as written", "第三章和第二章都出", []string{"第三章", "第二章"}},
		{"each of them once", "第三章出完再出第三章", []string{"第三章"}},
		{"nothing named", "出 10 道题", []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readCategories(test.request); !equalStrings(got, test.want) {
				t.Errorf("readCategories(%q) = %v, want %v", test.request, got, test.want)
			}
		})
	}
}

func TestTheWholePlanComesOutOfOneSentence(t *testing.T) {
	plan := readPlan("针对第三章出 10 道填空题，难度中等", rules)
	if plan.Count != 10 {
		t.Errorf("Count = %d, want 10", plan.Count)
	}
	if !equalTypes(plan.Types, []notebook.QuestionType{notebook.QuestionFillBlank}) {
		t.Errorf("Types = %v, want the kind that was asked for", plan.Types)
	}
	if plan.Difficulty != "medium" {
		t.Errorf("Difficulty = %q, want medium", plan.Difficulty)
	}
	if !equalStrings(plan.Categories, []string{"第三章"}) {
		t.Errorf("Categories = %v, want the chapter", plan.Categories)
	}
	// The material is not read out of the sentence: it is what the survey found,
	// and a capability has no other handle on what the turn carries (KB-07).
	if plan.MaterialID != "" || plan.MaterialTitle != "" {
		t.Errorf("the plan names material before anything was read: %+v", plan)
	}
}

func TestTheMaterialIsTheBestRankedPassage(t *testing.T) {
	// "针对这一章出题" means the chapter the survey found most about, because that
	// is the only handle a capability has. A turn that found nothing names
	// nothing, rather than naming the document it was never given.
	fileID, title := materialOf([]event.Source{
		{Index: 1, FileID: "file_1", DocumentID: "doc_1", Title: "第三章讲义.md"},
		{Index: 2, FileID: "file_2", DocumentID: "doc_2", Title: "习题集.md"},
	})
	if fileID != "file_1" || title != "第三章讲义.md" {
		t.Errorf("materialOf() = (%q, %q), want the first-ranked passage's document", fileID, title)
	}
	if fileID, title := materialOf(nil); fileID != "" || title != "" {
		t.Errorf("materialOf(nil) = (%q, %q), want no material", fileID, title)
	}
}

func TestThePlanIsRenderedForAPerson(t *testing.T) {
	// One line, and it is the observation's own content: a client that shows what
	// a turn is about to do reads this rather than the structured form.
	tests := []struct {
		name string
		plan Plan
		want string
	}{
		{
			name: "everything was named",
			plan: Plan{
				Count: 3, Types: []notebook.QuestionType{notebook.QuestionFillBlank},
				Difficulty: "easy", Categories: []string{"第二章"}, MaterialTitle: "第二章讲义.md",
			},
			want: "我准备出 3 道题，题型是填空题，难度偏简单，归到「第二章」，取材于《第二章讲义.md》。",
		},
		{
			name: "only the count was",
			plan: Plan{Count: 5},
			want: "我准备出 5 道题。",
		},
		{
			name: "two kinds read as one list",
			plan: Plan{Count: 4, Types: []notebook.QuestionType{notebook.QuestionChoice, notebook.QuestionShortAnswer}},
			want: "我准备出 4 道题，题型是选择题、主观题。",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.plan.sentence(); got != test.want {
				t.Errorf("sentence() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTheMetadataKeepsEmptyListsAsLists(t *testing.T) {
	// A payload that serialised an empty list as null would make every reader
	// tell "none named" apart from "the field is not there", which is a
	// distinction the plan does not make.
	metadata := Plan{Count: 5}.metadata()
	if got, ok := metadata["question_types"].([]string); !ok || got == nil || len(got) != 0 {
		t.Errorf("question_types = %#v, want an empty list rather than nothing", metadata["question_types"])
	}
	if got, ok := metadata["categories"].([]string); !ok || got == nil || len(got) != 0 {
		t.Errorf("categories = %#v, want an empty list rather than nothing", metadata["categories"])
	}
	if metadata["difficulty"] != "" || metadata["material_id"] != "" || metadata["material_title"] != "" {
		t.Errorf("metadata = %v, want empty strings where nothing was said", metadata)
	}
	// The count is the one number that is always a decision, and it is a number
	// rather than a string: it is what the progress a question reports is read
	// against.
	if count, ok := metadata["count"].(int); !ok || count != 5 {
		t.Errorf("count = %#v, want the number 5", metadata["count"])
	}
}

func TestTheSurveyIsSummarisedTheWayTheAssistantSummarisesIt(t *testing.T) {
	// The same client reads both capabilities' observations, and a summary that
	// said the same thing in different words would be two things to keep in step.
	// The second sentence is the one thing this summary adds: a turn that could
	// not read part of what it carries has to say so (ATT-04, KB-07).
	tests := []struct {
		name      string
		grounding capability.Grounding
		want      string
	}{
		{
			name:      "material was found",
			grounding: capability.Grounding{Sources: make([]event.Source, 3)},
			want:      "查看了本轮携带的材料，找到 3 条相关片段",
		},
		{
			name:      "nothing was found",
			grounding: capability.Grounding{},
			want:      "查看了本轮携带的材料，没有找到相关片段",
		},
		{
			name: "something could not be read",
			grounding: capability.Grounding{
				Sources:     make([]event.Source, 1),
				Unavailable: []string{"一份扫描件"},
			},
			want: "找到 1 条相关片段；有 1 项材料没有读到",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := groundingSummary(test.grounding)
			if !strings.Contains(summary, test.want) {
				t.Errorf("groundingSummary() = %q, want it to say %q", summary, test.want)
			}
		})
	}
}

// equalTypes compares two kind lists, which travel as a closed set rather than
// as strings.
func equalTypes(got, want []notebook.QuestionType) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
