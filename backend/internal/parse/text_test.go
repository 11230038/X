package parse

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func parseMarkdown(t *testing.T, content string, maxChars int) (Result, error) {
	t.Helper()
	return textParser{}.Parse(context.Background(), Request{
		Filename: "notes.md", MIME: "text/markdown", Category: CategoryMarkdown,
		Content: strings.NewReader(content), MaxChars: maxChars,
	})
}

func TestTextParserReadsAndNormalizes(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "plain text", content: "# Heading\n\nBody.\n", want: "# Heading\n\nBody.\n"},
		{name: "empty file", content: "", want: ""},
		{
			name:    "byte order mark",
			content: "\ufeff# Heading",
			want:    "# Heading",
		},
		{
			name:    "windows line endings",
			content: "one\r\ntwo\r\n",
			want:    "one\ntwo\n",
		},
		{
			name:    "old mac line endings",
			content: "one\rtwo\r",
			want:    "one\ntwo\n",
		},
		{
			name:    "mixed line endings",
			content: "one\r\ntwo\rthree\n",
			want:    "one\ntwo\nthree\n",
		},
		{
			// Only the one in front of the first character is a byte order
			// mark. A zero-width space in the middle of a sentence is a
			// character the author put there.
			name:    "a byte order mark that is not first",
			content: "one\ufefftwo",
			want:    "one\ufefftwo",
		},
		{
			name:    "characters that are not ascii",
			content: "闭包是函数",
			want:    "闭包是函数",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := parseMarkdown(t, testCase.content, 1000)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if result.Text != testCase.want {
				t.Errorf("Text = %q, want %q", result.Text, testCase.want)
			}
			if result.Engine != EngineText {
				t.Errorf("Engine = %q, want %q", result.Engine, EngineText)
			}
			if len(result.Pages) != 0 {
				t.Errorf("Pages = %v, want none: a text file has no pages", result.Pages)
			}
		})
	}
}

// Replacing undecodable bytes would store text the user did not write and shift
// every offset after the replacement, so the file is refused instead — and the
// message says what to do.
func TestTextParserRefusesBytesThatAreNotUTF8(t *testing.T) {
	// A latin-1 encoded "café": the é is one byte, which is not valid UTF-8.
	result, err := parseMarkdown(t, "caf\xe9", 1000)

	if CodeOf(err) != CodeParseFailed {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
	}
	if result.Text != "" {
		t.Errorf("Text = %q, want nothing alongside a failure", result.Text)
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("error = %v, want it to name UTF-8 as the fix", err)
	}
}

// The engine does not decide that a document is too long — it reads, and
// reports what it read. What it does owe the registry is enough text to make
// that decision with, which means more than the limit whenever there was more.
func TestTextParserNeverDecidesTheDocumentIsTooLong(t *testing.T) {
	t.Run("a file that fits comes back whole", func(t *testing.T) {
		result, err := parseMarkdown(t, "abcdef", 3)
		if err != nil {
			t.Fatalf("Parse() error = %v, want the engine to report what it read", err)
		}
		if result.Text != "abcdef" {
			t.Errorf("Text = %q, want the whole file", result.Text)
		}
	})

	t.Run("a file that does not comes back longer than the limit", func(t *testing.T) {
		result, err := parseMarkdown(t, strings.Repeat("a", 5000), 3)
		if err != nil {
			t.Fatalf("Parse() error = %v, want no length failure from the engine", err)
		}
		if len(result.Text) != 4 {
			t.Errorf("Text has %d characters, want the limit plus one", len(result.Text))
		}
	})
}

// The byte ceiling is four bytes per character, so a run of three-byte
// characters passes it in the middle of one. Leaving that half character in
// would make every Chinese document over the limit look like a broken file —
// and the count that comes back is in characters, not in what fitted.
func TestTextParserKeepsWholeCharactersAtTheCeiling(t *testing.T) {
	const source = "闭包是函数是一段很长的中文文本"

	cases := []struct {
		maxChars int
		want     string
	}{
		{maxChars: 1, want: "闭包"},
		{maxChars: 2, want: "闭包是"},
		{maxChars: 3, want: "闭包是函"},
	}

	for _, testCase := range cases {
		result, err := parseMarkdown(t, source, testCase.maxChars)
		if err != nil {
			t.Fatalf("maxChars %d: Parse() error = %v", testCase.maxChars, err)
		}
		if result.Text != testCase.want {
			t.Errorf("maxChars %d: Text = %q, want %q", testCase.maxChars, result.Text, testCase.want)
		}
		if len([]rune(result.Text)) != testCase.maxChars+1 {
			t.Errorf("maxChars %d: Text has %d characters, want %d",
				testCase.maxChars, len([]rune(result.Text)), testCase.maxChars+1)
		}
	}
}

// A four-byte character (an emoji) has to survive the same cut.
func TestTextParserKeepsEmojiWhole(t *testing.T) {
	result, err := parseMarkdown(t, "🙂🙂🙂🙂🙂🙂", 2)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.Text != "🙂🙂🙂" {
		t.Fatalf("Text = %q, want three whole emoji", result.Text)
	}
}

// A file that ends in a half character is broken on its own account, and that is
// a different thing from a file this service stopped reading.
func TestTextParserRefusesAFileThatEndsMidCharacter(t *testing.T) {
	_, err := parseMarkdown(t, "闭包\xe6\x98", 1000)
	if CodeOf(err) != CodeParseFailed {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
	}
}

func TestTextParserReportsAReadFailure(t *testing.T) {
	want := errors.New("the disk went away")
	result, err := textParser{}.Parse(context.Background(), Request{
		Category: CategoryMarkdown, MaxChars: 10,
		Content: io.MultiReader(strings.NewReader("start"), errorReader{want}),
	})

	if CodeOf(err) != CodeParseFailed {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
	}
	if result.Text != "" {
		t.Errorf("Text = %q, want nothing alongside a failure", result.Text)
	}
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want the read failure to be reachable through it", err)
	}
}

func TestTextParserReturnsCancellationAsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := textParser{}.Parse(ctx, Request{
		Category: CategoryMarkdown, MaxChars: 10,
		Content: strings.NewReader("some text"),
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if CodeOf(err) != "" {
		t.Fatalf("code = %q, want none: a caller that gave up is not a failed document", CodeOf(err))
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestByteCeilingAllowsFourBytesPerCharacterPlusOne(t *testing.T) {
	cases := []struct {
		maxChars int
		want     int64
	}{
		{maxChars: 1, want: 8},
		{maxChars: 512, want: 2052},
	}

	for _, testCase := range cases {
		if got := byteCeiling(testCase.maxChars); got != testCase.want {
			t.Errorf("byteCeiling(%d) = %d, want %d", testCase.maxChars, got, testCase.want)
		}
	}
}

// A limit large enough to multiply into an overflow must come out as "no
// limit", never as a negative number that reads nothing.
func TestByteCeilingSaturatesInsteadOfOverflowing(t *testing.T) {
	got := byteCeiling(int(^uint(0) >> 1))
	if got <= 0 {
		t.Fatalf("byteCeiling(max int) = %d, want a positive ceiling", got)
	}
}
