package kb

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// checkSplit runs Split and holds it to every property the rest of the service
// leans on. Returning the chunks lets a test go on to assert where the cuts
// landed.
func checkSplit(t *testing.T, text string, chunking Chunking) []Chunk {
	t.Helper()

	chunks, err := Split(text, chunking)
	if err != nil {
		t.Fatalf("Split(%q, %+v) error = %v", text, chunking, err)
	}
	if text == "" {
		if len(chunks) != 0 {
			t.Fatalf("Split(\"\", %+v) returned %d chunks, want none", chunking, len(chunks))
		}
		return chunks
	}
	if len(chunks) == 0 {
		t.Fatalf("Split(%q, %+v) returned no chunks", text, chunking)
	}

	// The property everything else rests on: laid back down in order with the
	// overlap removed, the chunks are the original text character for character.
	var rebuilt strings.Builder
	rebuilt.WriteString(chunks[0].Content)
	for _, chunk := range chunks[1:] {
		content := []rune(chunk.Content)
		if len(content) < chunking.Overlap {
			t.Fatalf("a middle chunk is %d characters, shorter than the %d-character overlap",
				len(content), chunking.Overlap)
		}
		rebuilt.WriteString(string(content[chunking.Overlap:]))
	}
	if rebuilt.String() != text {
		t.Fatalf("the chunks do not rebuild the text\n got: %q\nwant: %q", rebuilt.String(), text)
	}

	runes := []rune(text)
	for index, chunk := range chunks {
		if chunk.Ordinal != index {
			t.Errorf("chunk %d has ordinal %d", index, chunk.Ordinal)
		}
		if chunk.CharStart >= chunk.CharEnd {
			t.Fatalf("chunk %d is empty: [%d, %d)", index, chunk.CharStart, chunk.CharEnd)
		}
		if chunk.CharEnd > int64(len(runes)) {
			t.Fatalf("chunk %d ends at %d, past the %d characters of text", index, chunk.CharEnd, len(runes))
		}
		// The content is the text between the offsets, on character boundaries:
		// a cut inside a character would store a broken one and put every later
		// offset out by what was cut.
		if got := string(runes[chunk.CharStart:chunk.CharEnd]); got != chunk.Content {
			t.Errorf("chunk %d content = %q, want %q", index, chunk.Content, got)
		}
		if !utf8.ValidString(chunk.Content) {
			t.Errorf("chunk %d is not valid UTF-8", index)
		}

		if index == 0 {
			if chunk.CharStart != 0 {
				t.Errorf("the first chunk starts at %d, want 0", chunk.CharStart)
			}
			continue
		}
		previous := chunks[index-1]
		want := previous.CharEnd - int64(chunking.Overlap)
		if chunk.CharStart != want {
			t.Errorf("chunk %d starts at %d, want %d (the overlap taken from the chunk before it)",
				index, chunk.CharStart, want)
		}
		if length := chunk.CharEnd - chunk.CharStart; length <= int64(chunking.Overlap) {
			t.Errorf("chunk %d is %d characters: it does not advance past the overlap", index, length)
		}
	}

	if last := chunks[len(chunks)-1]; last.CharEnd != int64(len(runes)) {
		t.Errorf("the last chunk ends at %d, want the end of the text at %d", last.CharEnd, len(runes))
	}
	return chunks
}

func TestSplitRebuildsTheTextItWasGiven(t *testing.T) {
	// The shipped window, and windows small enough to put a boundary, an overlap
	// and a hard cut within a few characters of each other.
	chunkings := []Chunking{{Size: 512, Overlap: 50}, {Size: 10, Overlap: 3}, {Size: 4, Overlap: 1}, {Size: 7, Overlap: 0}}

	texts := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "one character", text: "a"},
		{name: "one paragraph", text: "闭包是一个函数和它捕获的环境。"},
		{name: "two paragraphs", text: "第一段说的是什么。\n\n第二段说的是别的。"},
		{name: "many short paragraphs", text: strings.Repeat("短。\n\n", 40)},
		{name: "one long paragraph", text: strings.Repeat("这是一句没有段落分隔的话。", 60)},
		{name: "no punctuation at all", text: strings.Repeat("abcdefghij", 30)},
		{name: "english sentences", text: "One sentence. Two sentences! Three? Four; five. "},
		{name: "quotes and closers", text: "他说：「这样。」\n然后走了。'Done.' she said."},
		{name: "emoji four bytes wide", text: strings.Repeat("🙂", 30)},
		{name: "blank lines and spaces", text: "  a  \n\n  b  \n\n\n  c  "},
		{name: "windows line endings already normalized", text: "one\ntwo\n\nthree\n"},
	}

	for _, testCase := range texts {
		for _, chunking := range chunkings {
			t.Run(testCase.name, func(t *testing.T) {
				checkSplit(t, testCase.text, chunking)
			})
		}
	}
}

// The fixtures above are all shapes someone thought of. This one is shapes
// nobody thought of, held to the same properties.
func TestSplitHoldsForGeneratedText(t *testing.T) {
	alphabet := []rune("ab \n\n\n。！？，.😀闭包☺")
	random := rand.New(rand.NewSource(20261013))

	for iteration := 0; iteration < 400; iteration++ {
		size := 1 + random.Intn(40)
		overlap := 0
		if size > 1 {
			overlap = random.Intn(size)
		}
		length := random.Intn(200)
		text := make([]rune, 0, length)
		for index := 0; index < length; index++ {
			text = append(text, alphabet[random.Intn(len(alphabet))])
		}
		checkSplit(t, string(text), Chunking{Size: size, Overlap: overlap})
	}
}

func TestSplitEndsAChunkOnTheLastBreakInTheWindow(t *testing.T) {
	// Window 7, overlap 2: the first chunk may run to character 7 and must
	// contribute at least three characters.
	const text = "第一段。\n\n第二段。"

	chunks := checkSplit(t, text, Chunking{Size: 7, Overlap: 2})

	// The paragraph break is at character 6, inside the window and past the
	// floor of 3, so the chunk ends on it rather than running to the limit.
	if chunks[0].Content != "第一段。\n\n" {
		t.Errorf("the first chunk is %q, want it to end on the paragraph break", chunks[0].Content)
	}
	// What is left is shorter than a window, so it is the last chunk — and it
	// starts where the overlap reaches back to, which here is the blank line the
	// first chunk ended on.
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %q", len(chunks), contents(chunks))
	}
	if chunks[1].Content != "\n\n第二段。" {
		t.Errorf("the second chunk is %q", chunks[1].Content)
	}
}

func TestSplitFallsBackToSentenceEnds(t *testing.T) {
	// No blank line anywhere: the only breaks are the sentence ends.
	const text = "一句。二句。三句。四句。五句。"

	chunks := checkSplit(t, text, Chunking{Size: 6, Overlap: 1})

	for index, chunk := range chunks[:len(chunks)-1] {
		isBreak := strings.HasSuffix(chunk.Content, "。")
		if !isBreak {
			t.Errorf("chunk %d is %q, want it to end at a sentence", index, chunk.Content)
		}
	}
}

// A window with no break in it at all is cut at the limit: something has to give
// way, and a cut in the middle of a long unbroken run is what the limit is for.
func TestSplitHardCutsWhenThereIsNoBreak(t *testing.T) {
	text := strings.Repeat("a", 30)

	chunks := checkSplit(t, text, Chunking{Size: 10, Overlap: 2})

	for index, chunk := range chunks[:len(chunks)-1] {
		if len(chunk.Content) != 10 {
			t.Fatalf("chunk %d is %d characters, want the full window", index, len(chunk.Content))
		}
	}
}

func TestSplitEdgeCases(t *testing.T) {
	t.Run("a text exactly one window long is one chunk", func(t *testing.T) {
		text := strings.Repeat("x", 10)
		chunks := checkSplit(t, text, Chunking{Size: 10, Overlap: 3})

		if len(chunks) != 1 {
			t.Fatalf("got %d chunks, want 1", len(chunks))
		}
		if chunks[0].Content != text {
			t.Errorf("content = %q, want the whole text", chunks[0].Content)
		}
	})

	t.Run("a text one character longer is not", func(t *testing.T) {
		chunks := checkSplit(t, strings.Repeat("x", 11), Chunking{Size: 10, Overlap: 3})
		if len(chunks) != 2 {
			t.Fatalf("got %d chunks, want 2", len(chunks))
		}
	})

	t.Run("no overlap means the chunks tile the text", func(t *testing.T) {
		chunks := checkSplit(t, strings.Repeat("ab", 20), Chunking{Size: 3, Overlap: 0})
		for index := 1; index < len(chunks); index++ {
			if chunks[index].CharStart != chunks[index-1].CharEnd {
				t.Fatalf("chunk %d starts at %d, want %d", index, chunks[index].CharStart, chunks[index-1].CharEnd)
			}
		}
	})
}

func TestSplitRefusesAWindowThatCannotAdvance(t *testing.T) {
	cases := []struct {
		name     string
		chunking Chunking
	}{
		{name: "no size", chunking: Chunking{Size: 0, Overlap: 0}},
		{name: "negative size", chunking: Chunking{Size: -1, Overlap: 0}},
		{name: "negative overlap", chunking: Chunking{Size: 10, Overlap: -1}},
		{name: "overlap equal to the size", chunking: Chunking{Size: 10, Overlap: 10}},
		{name: "overlap larger than the size", chunking: Chunking{Size: 10, Overlap: 99}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Split("some text", testCase.chunking); err == nil {
				t.Fatal("Split() error = nil, want an error")
			}
		})
	}
}

// The ids of one revision of one document share a prefix, and the ordinal in
// them sorts in order, because the only way to read a document's chunks back is
// a range scan over that prefix.
func TestChunkIDsShareAPrefixAndSortInOrder(t *testing.T) {
	const documentID = "doc_01H8XYZ"
	prefix := ChunkIDPrefix(documentID, 3)

	if !strings.HasPrefix(ChunkID(documentID, 3, 12), prefix) {
		t.Fatalf("ChunkID() = %q, want it to start with %q", ChunkID(documentID, 3, 12), prefix)
	}
	// A different revision of the same document is a different range: the old
	// chunks are still there while the new ones are written.
	if strings.HasPrefix(ChunkID(documentID, 4, 0), prefix) {
		t.Fatal("a later revision's chunk id is inside the earlier revision's range")
	}

	ids := []string{ChunkID(documentID, 3, 2), ChunkID(documentID, 3, 10), ChunkID(documentID, 3, 1)}
	// A string comparison has to agree with the ordinal order, or a range scan
	// returns the chunks of a document shuffled.
	if !(ids[2] < ids[0] && ids[0] < ids[1]) {
		t.Fatalf("the ids do not sort in ordinal order: %q", ids)
	}
}

func contents(chunks []Chunk) []string {
	texts := make([]string, len(chunks))
	for index, chunk := range chunks {
		texts[index] = chunk.Content
	}
	return texts
}
