package kb

import (
	"strings"
	"testing"
	"unicode/utf8"

	"backend/internal/event"
)

func TestParseCitationsReadsTheNumbersAnAnswerUsed(t *testing.T) {
	cases := []struct {
		name        string
		answer      string
		sourceCount int
		cited       []int
		unresolved  []int
	}{
		{
			name: "in order", answer: "先看 [1]，再看 [2]。", sourceCount: 2,
			cited: []int{1, 2},
		},
		{
			name: "repeated references are one citation", answer: "[1] 说了这件事，[1] 又说了一次。",
			sourceCount: 1, cited: []int{1},
		},
		{
			name: "the order is the order they appear", answer: "[3] then [1] then [2]",
			sourceCount: 3, cited: []int{3, 1, 2},
		},
		{
			name: "full-width brackets", answer: "结论见【1】，细节见【2】。", sourceCount: 2,
			cited: []int{1, 2},
		},
		{
			name: "a number no source has", answer: "见 [1] 与 [7]。", sourceCount: 3,
			cited: []int{1, 7}, unresolved: []int{7},
		},
		{
			name: "zero is not a source", answer: "见 [0]。", sourceCount: 2,
			cited: []int{0}, unresolved: []int{0},
		},
		{
			name: "no citations at all", answer: "这句话没有引用任何材料。", sourceCount: 2,
		},
		{
			name: "a year in brackets is not a citation", answer: "文档写于 [2026] 年。", sourceCount: 2,
		},
		{
			name: "an unclosed bracket is not a citation", answer: "见 [1 与 [2]。", sourceCount: 2,
			cited: []int{2},
		},
		{
			name: "nothing was returned, so nothing resolves", answer: "见 [1]。", sourceCount: 0,
			cited: []int{1}, unresolved: []int{1},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			citations := ParseCitations(testCase.answer, testCase.sourceCount)

			if !sameNumbers(citations.Cited, testCase.cited) {
				t.Errorf("cited = %v, want %v", citations.Cited, testCase.cited)
			}
			if !sameNumbers(citations.Unresolved, testCase.unresolved) {
				t.Errorf("unresolved = %v, want %v", citations.Unresolved, testCase.unresolved)
			}
		})
	}
}

// An answer that points at a number it was not given is reported and nothing
// else: no source is invented to make the number add up, and the answer is left
// as the model wrote it. Both halves of that are checked here, because the
// tempting mistake is to "fix" the answer.
func TestAnUnresolvedCitationInventsNothing(t *testing.T) {
	const answer = "根据材料，熔断器会打开 [7]，这一点 [1] 也提到了。"

	citations := ParseCitations(answer, 2)

	if len(citations.Cited) != 2 {
		t.Fatalf("cited = %v, want the two numbers the answer used", citations.Cited)
	}
	if len(citations.Unresolved) != 1 || citations.Unresolved[0] != 7 {
		t.Fatalf("unresolved = %v, want [7]", citations.Unresolved)
	}
	if !strings.Contains(answer, "[7]") {
		t.Error("the answer was rewritten, which is not this layer's to do")
	}
}

func TestSnippetCutsOnACharacterBoundaryAndSaysSo(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		maxChars int
		want     string
	}{
		{name: "short enough", text: "闭包", maxChars: 10, want: "闭包"},
		{name: "exactly the limit", text: "闭包捕获环境", maxChars: 6, want: "闭包捕获环境"},
		{name: "cut", text: "闭包捕获环境", maxChars: 4, want: "闭包捕…"},
		{name: "cut inside multi-byte characters", text: "🙂🙂🙂", maxChars: 2, want: "🙂…"},
		{name: "a cap of one character is the ellipsis", text: "闭包捕获环境", maxChars: 1, want: "…"},
		{name: "no limit means the whole text", text: "闭包捕获环境", maxChars: 0, want: "闭包捕获环境"},
		{name: "a trailing space is not kept", text: "闭包 捕获", maxChars: 3, want: "闭包…"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := snippet(testCase.text, testCase.maxChars); got != testCase.want {
				t.Errorf("snippet(%q, %d) = %q, want %q", testCase.text, testCase.maxChars, got, testCase.want)
			}
		})
	}
}

func TestSnippetNeverSplitsACharacter(t *testing.T) {
	text := strings.Repeat("闭包🙂", 40)

	for limit := 1; limit <= 20; limit++ {
		cut := snippet(text, limit)
		if !utf8.ValidString(cut) {
			t.Fatalf("snippet at %d is not valid UTF-8: %q", limit, cut)
		}
		if got := utf8.RuneCountInString(cut); got > limit {
			t.Errorf("snippet at %d is %d characters: %q", limit, got, cut)
		}
	}
}

// A source is made of the stored row and nothing else. Every field a client
// would use to open the citation is here, and none of them could have come from
// anywhere but this service.
func TestSourceOfDescribesTheStoredChunk(t *testing.T) {
	page := 3
	chunk := ChunkSource{
		ChunkID: "chk_doc_1_2_000004", DocumentID: "doc_1", KBID: "kb_1",
		FileID: "lib_1", Filename: "数学分析.pdf", Engine: "docling",
		Revision: 2, Ordinal: 4, Content: "闭包捕获环境。",
		CharStart: 6144, CharEnd: 6656, Page: &page,
	}

	source := sourceOf(chunk, 3, 0.0328, 0)

	if source.Index != 3 {
		t.Errorf("index = %d, want 3", source.Index)
	}
	want := event.Locator{
		Kind: locatorChunk, ChunkID: "chk_doc_1_2_000004", Ordinal: 4,
		CharStart: 6144, CharEnd: 6656, Page: &page,
	}
	if source.Locator != want {
		t.Errorf("locator = %+v, want %+v", source.Locator, want)
	}
	if source.DocumentID != "doc_1" || source.FileID != "lib_1" {
		t.Errorf("the source points at %s / %s", source.DocumentID, source.FileID)
	}
	if source.Title != "数学分析.pdf" || source.Engine != "docling" || source.Revision != 2 {
		t.Errorf("the source is %q read by %q at revision %d", source.Title, source.Engine, source.Revision)
	}
	if source.Snippet != chunk.Content {
		t.Errorf("snippet = %q, want the chunk's text", source.Snippet)
	}
	if source.Score != 0.0328 {
		t.Errorf("score = %v, want what the ranking gave it", source.Score)
	}
}

func sameNumbers(got, want []int) bool {
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
