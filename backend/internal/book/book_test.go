package book

import (
	"strings"
	"testing"
)

// The model's formatting habits are undone here rather than refused: a fence
// and a sentence before the JSON are things the model does, not things the
// request asked for.
func TestTheOutlineIsReadThroughAFenceAndProse(t *testing.T) {
	answer := "好的，下面是目录：\n```json\n{\"chapters\":[{\"title\":\"安装\",\"summary\":\"怎么装\"}]}\n```\n希望有帮助。"

	items, err := parseOutlineAnswer(answer)

	if err != nil {
		t.Fatalf("parseOutlineAnswer() error = %v", err)
	}
	if len(items) != 1 || items[0].Title != "安装" || items[0].Summary != "怎么装" {
		t.Errorf("items = %+v, want the one chapter the answer named", items)
	}
}

func TestAnAnswerWithNoJSONIsAnError(t *testing.T) {
	if _, err := parseOutlineAnswer("我无法完成这个请求。"); err == nil {
		t.Fatal("parseOutlineAnswer() accepted an answer with no JSON")
	}
}

func TestTheOutlineBoundsAreOneAndTwelve(t *testing.T) {
	one := []OutlineItemInput{{Title: "唯一一章"}}
	if _, err := validateOutline(one); err != nil {
		t.Errorf("validateOutline(1 chapter) error = %v", err)
	}

	twelve := make([]OutlineItemInput, 0, MaxOutlineChapters)
	for index := 0; index < MaxOutlineChapters; index++ {
		twelve = append(twelve, OutlineItemInput{Title: "第" + string(rune('一'+index)) + "章"})
	}
	if _, err := validateOutline(twelve); err != nil {
		t.Errorf("validateOutline(12 chapters) error = %v", err)
	}

	for name, items := range map[string][]OutlineItemInput{
		"no chapters":  {},
		"thirteen":     append(twelve, OutlineItemInput{Title: "第十三章"}),
		"blank title":  {{Title: "  "}},
		"duplicate":    {{Title: "安装"}, {Title: "安装"}},
		"long title":   {{Title: strings.Repeat("长", MaxOutlineTitleRunes+1)}},
		"long summary": {{Title: "安装", Summary: strings.Repeat("长", MaxOutlineSummaryRunes+1)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateOutline(items); err == nil {
				t.Fatal("validateOutline() accepted an outline it should refuse")
			}
		})
	}
}

func TestAnEditedOutlineIsTrimmedRatherThanRewritten(t *testing.T) {
	items, err := validateOutline([]OutlineItemInput{{Title: "  安装  ", Summary: "  怎么装  "}})
	if err != nil {
		t.Fatalf("validateOutline() error = %v", err)
	}
	if items[0].Title != "安装" || items[0].Summary != "怎么装" {
		t.Errorf("items = %+v, want the whitespace gone and the words kept", items)
	}
}

// A repeated heading is the one thing removed from a body. Any other opening
// heading may be the chapter's first section and is left alone.
func TestOnlyTheRepeatedChapterHeadingIsRemoved(t *testing.T) {
	for name, test := range map[string]struct{ content, title, want string }{
		"plain title": {
			content: "# 安装\n正文开始。", title: "安装", want: "正文开始。",
		},
		"numbered title": {
			content: "## 第 3 章 安装\n\n正文开始。", title: "安装", want: "正文开始。",
		},
		"chinese numbering": {
			content: "# 第三章 安装\n\n正文开始。", title: "安装", want: "正文开始。",
		},
		"a different section": {
			content: "## 准备工作\n正文开始。", title: "安装", want: "## 准备工作\n正文开始。",
		},
		"no heading": {
			content: "正文开始。", title: "安装", want: "正文开始。",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := stripChapterHeading(test.content, test.title); got != test.want {
				t.Errorf("stripChapterHeading() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAChapterWithNoBodyIsRefused(t *testing.T) {
	for name, answer := range map[string]string{
		"empty":      "",
		"whitespace": "  \n\n ",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateChapterContent(answer); err == nil {
				t.Fatal("validateChapterContent() accepted a chapter with no body")
			}
		})
	}
}

func TestAChapterLongerThanTheLimitIsRefused(t *testing.T) {
	if _, err := validateChapterContent(strings.Repeat("字", MaxChapterRunes+1)); err == nil {
		t.Fatal("validateChapterContent() accepted a chapter over the limit")
	}
}

func TestExportWritesOnlyTheChaptersItWasGiven(t *testing.T) {
	document := buildExport(
		Summary{Title: "保修手册", SourceKind: SourceFile, SourceTitle: "手册.pdf"},
		[]Chapter{
			{Ordinal: 1, Title: "安装", Content: "先把箱打开。"},
			{Ordinal: 3, Title: "维修", Content: "联系售后。"},
		},
	)

	for _, want := range []string{
		"# 保修手册",
		"> 来源：文件《手册.pdf》",
		"## 第 1 章 安装",
		"先把箱打开。",
		"## 第 3 章 维修",
		"联系售后。",
	} {
		if !strings.Contains(document.Content, want) {
			t.Errorf("Content = %q, want it to contain %q", document.Content, want)
		}
	}
	if strings.Contains(document.Content, "第 2 章") {
		t.Errorf("Content = %q, want no heading for a chapter that was not exported", document.Content)
	}
	if document.Filename != "保修手册.md" {
		t.Errorf("Filename = %q, want %q", document.Filename, "保修手册.md")
	}
}

func TestAnExportedFilenameIsSafeOnWindows(t *testing.T) {
	for name, test := range map[string]struct{ title, want string }{
		"illegal characters": {title: `保修/手册:2026?`, want: "保修_手册_2026_.md"},
		"trailing spaces":    {title: "  手册  ", want: "手册.md"},
		"nothing usable":     {title: "///", want: "book.md"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := exportFilename(test.title); got != test.want {
				t.Errorf("exportFilename() = %q, want %q", got, test.want)
			}
		})
	}
}
