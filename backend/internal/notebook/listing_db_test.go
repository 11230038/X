package notebook

// The listing half of the storage contract, checked against a real seekdb: the
// order a page comes back in, what paging does at its edges, and each of the
// eight filters — which is where the claims that cannot be made about a double
// are. "Case-insensitive" is a property of the comparison the SQL asks for,
// "unresolved" is a query over three stored values, and a category is a join.

import (
	"context"
	"slices"
	"testing"
)

// seed is one question to put in the bank before a listing test runs.
type seed struct {
	text   string
	second int
	adjust func(*Question)
	names  []string
	marks  Change
}

// seedBank writes the given questions at the given seconds and returns the row
// each one got, which is what a test needs to file or mark it afterwards.
func seedBank(t *testing.T, repository *GORMRepository, seeds []seed) map[string]Upserted {
	t.Helper()
	rows := make(map[string]Upserted, len(seeds))
	for _, item := range seeds {
		question := sampleQuestion(item.text)
		if item.adjust != nil {
			item.adjust(&question)
		}
		result := saveAt(t, repository, question, atSecond(item.second))
		if len(item.names) > 0 {
			if err := repository.AddCategories(context.Background(), result.ID, item.names); err != nil {
				t.Fatalf("file %q under %q: %v", item.text, item.names, err)
			}
		}
		if !item.marks.Empty() {
			if err := repository.Mark(context.Background(), result.QuestionID, item.marks); err != nil {
				t.Fatalf("mark %q: %v", item.text, err)
			}
		}
		rows[item.text] = result
	}
	return rows
}

// listed returns a page of the bank as the texts of the questions on it, sorted,
// so that a test about which questions a filter selects is not also a test about
// their order.
func listed(t *testing.T, repository *GORMRepository, filter Filter) []string {
	t.Helper()
	entries, err := repository.List(context.Background(), filter)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	texts := make([]string, 0, len(entries))
	for _, entry := range entries {
		texts = append(texts, entry.Question.Text)
	}
	slices.Sort(texts)
	return texts
}

func wantTexts(texts ...string) []string {
	slices.Sort(texts)
	return texts
}

// TestListReturnsTheNewestFirst pins the order, tiebreaker and all.
//
// Two questions written in the same second are ordinary — a turn generates
// several in a row — and an order that is not decided between them makes paging
// skip one row and repeat another.
func TestListReturnsTheNewestFirst(t *testing.T) {
	repository, _ := newDBRepository(t)
	rows := seedBank(t, repository, []seed{
		{text: "第一题", second: 0},
		{text: "第二题", second: 0},
		{text: "第三题", second: 1},
	})

	entries, err := repository.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Question.Text)
	}

	// The newest is first; the two written in the same second come back by row
	// key, which is the half of the order that makes it a total one.
	first, second := "第一题", "第二题"
	if rows[first].ID > rows[second].ID {
		first, second = second, first
	}
	want := []string{"第三题", first, second}
	if !slices.Equal(got, want) {
		t.Errorf("the listing is %q, want %q", got, want)
	}
}

// TestListPagesThroughTheBank checks that a page is a window rather than a
// prefix: the second page of two is the next two, in the same order.
func TestListPagesThroughTheBank(t *testing.T) {
	repository, _ := newDBRepository(t)
	seedBank(t, repository, []seed{
		{text: "第一题", second: 0},
		{text: "第二题", second: 1},
		{text: "第三题", second: 2},
		{text: "第四题", second: 3},
	})

	first := listed(t, repository, Filter{Limit: 2})
	if !slices.Equal(first, wantTexts("第四题", "第三题")) {
		t.Errorf("the first page is %q, want the two newest", first)
	}
	second := listed(t, repository, Filter{Limit: 2, Offset: 2})
	if !slices.Equal(second, wantTexts("第二题", "第一题")) {
		t.Errorf("the second page is %q, want the two oldest", second)
	}
	// Past the end is an empty page, not an error.
	if beyond := listed(t, repository, Filter{Limit: 2, Offset: 4}); len(beyond) != 0 {
		t.Errorf("a page past the end holds %q, want nothing", beyond)
	}
	// A bound that is a typo is taken as the default rather than refused.
	if unstated := listed(t, repository, Filter{Limit: -1}); len(unstated) != 4 {
		t.Errorf("a listing with a negative limit holds %d questions, want all four", len(unstated))
	}
	if oversized := listed(t, repository, Filter{Limit: MaxListLimit + 1}); len(oversized) != 4 {
		t.Errorf("a listing over the maximum holds %d questions, want all four", len(oversized))
	}
}

// TestListNarrowsToWhatTheFilterSelects runs every filter over one bank.
//
// The bank is built so that each case is decided by exactly one field: the two
// questions that share a value differ in everything a case does not name, so a
// filter that ignored its field would return the whole bank and fail.
func TestListNarrowsToWhatTheFilterSelects(t *testing.T) {
	repository, _ := newDBRepository(t)
	yes, no := true, false
	seedBank(t, repository, []seed{
		{
			text:   "闭包是什么",
			second: 0,
			adjust: func(question *Question) { question.Difficulty = "medium" },
			names:  []string{"数学"},
			marks:  Change{Bookmarked: &yes, Resolved: &no},
		},
		{
			text:   "What is a closure",
			second: 1,
			adjust: func(question *Question) {
				question.Type = QuestionShortAnswer
				question.Difficulty = "hard"
				question.Source = "attachment"
				question.MaterialID = "file_1"
			},
			names: []string{"英语"},
			marks: Change{Resolved: &yes},
		},
		{
			text:   "50% 的人会答错",
			second: 2,
			adjust: func(question *Question) {
				question.Type = QuestionFillBlank
				question.Difficulty = "easy"
				question.MaterialID = "file_2"
			},
		},
	})

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"no filter", Filter{}, wantTexts("闭包是什么", "What is a closure", "50% 的人会答错")},
		{"one source", Filter{Source: "attachment"}, wantTexts("What is a closure")},
		{"one material", Filter{MaterialID: "file_1"}, wantTexts("What is a closure")},
		{"one difficulty", Filter{Difficulty: "easy"}, wantTexts("50% 的人会答错")},
		{"one type", Filter{Type: QuestionFillBlank}, wantTexts("50% 的人会答错")},
		{"one category", Filter{Category: "数学"}, wantTexts("闭包是什么")},
		{"a category nobody is filed under", Filter{Category: "物理"}, nil},
		// The three states of resolved, which is why the column is not a flag.
		{"untouched", Filter{Resolution: ResolutionUntouched}, wantTexts("50% 的人会答错")},
		{"open", Filter{Resolution: ResolutionOpen}, wantTexts("闭包是什么")},
		{"done", Filter{Resolution: ResolutionDone}, wantTexts("What is a closure")},
		{"any resolution", Filter{Resolution: ResolutionAny},
			wantTexts("闭包是什么", "What is a closure", "50% 的人会答错")},
		// The two halves of bookmarked partition the bank: one question carries
		// the mark, the other two have never been marked either way.
		{"bookmarked", Filter{Bookmarked: &yes}, wantTexts("闭包是什么")},
		{"not bookmarked", Filter{Bookmarked: &no}, wantTexts("What is a closure", "50% 的人会答错")},
		// The text search, over a pattern the user typed.
		{"a word", Filter{Query: "closure"}, wantTexts("What is a closure")},
		{"the same word in capitals", Filter{Query: "CLOSURE"}, wantTexts("What is a closure")},
		{"a pattern with a wildcard in it", Filter{Query: "50%"}, wantTexts("50% 的人会答错")},
		{"text nobody wrote", Filter{Query: "递归"}, nil},
		{"two filters at once", Filter{Source: "knowledge_base", Difficulty: "easy"},
			wantTexts("50% 的人会答错")},
		{"two filters that cannot both hold", Filter{Source: "attachment", Difficulty: "easy"}, nil},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if got := listed(t, repository, scenario.filter); !slices.Equal(got, scenario.want) {
				t.Errorf("the filter selected %q, want %q", got, scenario.want)
			}
		})
	}
}

// TestListOfAnEmptyBankIsAnEmptyPage keeps an empty bank from being an error or
// a page of nothing-but-a-nil: a caller ranges over what it gets back.
func TestListOfAnEmptyBankIsAnEmptyPage(t *testing.T) {
	repository, _ := newDBRepository(t)
	entries, err := repository.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("an empty bank listed %d questions", len(entries))
	}
}

// TestListCarriesTheCategoriesOfEveryEntry covers the one query the page makes
// for categories instead of one per row — and with it that the names are attached
// to the right questions rather than to the page.
func TestListCarriesTheCategoriesOfEveryEntry(t *testing.T) {
	repository, _ := newDBRepository(t)
	seedBank(t, repository, []seed{
		{text: "闭包是什么", second: 0, names: []string{"数学", "编程"}},
		{text: "什么是作用域", second: 1, names: []string{"编程"}},
		{text: "没人归档的题", second: 2},
	})

	entries, err := repository.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	filed := map[string][]string{}
	for _, entry := range entries {
		filed[entry.Question.Text] = entry.Categories
	}
	if want := []string{"数学", "编程"}; !slices.Equal(filed["闭包是什么"], want) {
		t.Errorf("the first question is filed under %q, want %q", filed["闭包是什么"], want)
	}
	if want := []string{"编程"}; !slices.Equal(filed["什么是作用域"], want) {
		t.Errorf("the second question is filed under %q, want %q", filed["什么是作用域"], want)
	}
	if got := filed["没人归档的题"]; len(got) != 0 {
		t.Errorf("a question filed under nothing came back filed under %q", got)
	}
}

// TestListPagesQuestionsWrittenInTheSameSecond is the case the tiebreaker exists
// for, taken all the way through: four questions in one second, read two at a
// time, must be read exactly once each.
func TestListPagesQuestionsWrittenInTheSameSecond(t *testing.T) {
	repository, _ := newDBRepository(t)
	seedBank(t, repository, []seed{
		{text: "第一题", second: 0},
		{text: "第二题", second: 0},
		{text: "第三题", second: 0},
		{text: "第四题", second: 0},
	})

	seen := map[string]bool{}
	for offset := 0; offset < 4; offset += 2 {
		for _, text := range listed(t, repository, Filter{Limit: 2, Offset: offset}) {
			if seen[text] {
				t.Errorf("paging returned %q twice", text)
			}
			seen[text] = true
		}
	}
	for _, text := range []string{"第一题", "第二题", "第三题", "第四题"} {
		if !seen[text] {
			t.Errorf("paging never returned %q", text)
		}
	}
}
