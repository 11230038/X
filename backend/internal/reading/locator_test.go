package reading

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A material's text, in characters rather than bytes, so that a test asserting on
// a window is asserting on what a reader would see.
const sampleText = "闭包捕获的是变量而不是值，所以循环里的函数看到的是同一个变量。"

// sampleDocuments is one document with that text, already parsed.
func sampleDocuments() *fakeDocuments {
	return newFakeDocuments().withDocument(Document{
		ID: "doc_1", FileID: "lib_1", Filename: "算法笔记.md",
		TextChars: int64(len([]rune(sampleText))), Revision: 3,
	}, sampleText)
}

// newSampleService is newFakeService over that document.
//
// The tests that resolve a window of a document need the document to be there —
// a material names one, and the text belongs to the knowledge base — so the
// premise is set up in one line rather than repeated. The tests that are about a
// request being refused before anything is read use newFakeService instead, and
// start with nothing parsed on purpose.
func newSampleService(t *testing.T, unitMaxChars int) (*Service, *fakeRepository, *fakeDocuments) {
	t.Helper()
	repository, documents := newFakeRepository(), sampleDocuments()
	service, err := NewService(repository, documents, Options{UnitMaxChars: unitMaxChars})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, repository, documents
}

func TestALocatorResolvesToTheTextItNames(t *testing.T) {
	service, repository, documents := newSampleService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	passage, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", Ordinal: 2, CharStart: 0, CharEnd: 5,
	}, testCaller)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := string([]rune(sampleText)[:5]); passage.Text != want {
		t.Errorf("Resolve() read %q, want %q", passage.Text, want)
	}
	// What was read is the window that was asked for, and not the document: the
	// knowledge base owns the text and the offsets, and this layer owns neither.
	if len(documents.reads) != 1 || documents.reads[0] != "doc_1:0:5" {
		t.Errorf("Resolve() read the windows %v, want exactly doc_1:0:5", documents.reads)
	}
	// A passage is handed to a turn as a source, and a source is a citation: what
	// opens one is the file and the span, and the revision is how a reader can see
	// that the document has moved underneath them.
	if passage.DocumentID != "doc_1" || passage.FileID != "lib_1" ||
		passage.Filename != "算法笔记.md" || passage.Revision != 3 {
		t.Errorf("Resolve() returned a passage of %+v", passage)
	}
	if passage.Locator.CharStart != 0 || passage.Locator.CharEnd != 5 {
		t.Errorf("Resolve() returned the locator %+v", passage.Locator)
	}
}

func TestTheOffsetsAreTheCoordinateAndTheOrdinalIsAHint(t *testing.T) {
	service, repository, documents := newSampleService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	// The ordinal says unit 9 and the offsets say characters 10 to 20. Only one of
	// them can decide where the text is, and it is the one the text is actually
	// read by.
	passage, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", Ordinal: 9, CharStart: 10, CharEnd: 20,
	}, testCaller)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := string([]rune(sampleText)[10:20]); passage.Text != want {
		t.Errorf("Resolve() read %q, want %q", passage.Text, want)
	}
	if len(documents.reads) != 1 || documents.reads[0] != "doc_1:10:20" {
		t.Errorf("Resolve() read the windows %v, want exactly doc_1:10:20", documents.reads)
	}
	// The ordinal rides back out unchanged: it is the client's own unit number,
	// and this layer has no business rewriting a number it did not use.
	if passage.Locator.Ordinal != 9 {
		t.Errorf("Resolve() returned ordinal %d, want the one it was handed", passage.Locator.Ordinal)
	}
}

func TestALocatorPastTheEndOfTheTextIsRefusedRatherThanClamped(t *testing.T) {
	service, repository, documents := newSampleService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	length := int64(len([]rune(sampleText)))

	_, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", CharStart: 0, CharEnd: length + 1,
	}, testCaller)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Resolve() error = %v, want an invalid request", err)
	}
	// The numbers are in the message because the two things a stale client needs
	// to know are where it asked and how long the text actually is.
	if !strings.Contains(err.Error(), "past the") {
		t.Errorf("Resolve() refused with %q, which does not say what it was refused for", err)
	}
	// Storage clamps; this layer does not, because answering about a shorter span
	// than the one asked for would hide that the client's view is stale.
	if len(documents.reads) != 0 {
		t.Errorf("Resolve() read %v after refusing the locator", documents.reads)
	}
}

func TestACharacterSpanAtTheEndOfTheTextIsTheDocumentAndNotPastIt(t *testing.T) {
	service, repository, _ := newSampleService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	length := int64(len([]rune(sampleText)))

	passage, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", CharStart: length - 2, CharEnd: length,
	}, testCaller)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := string([]rune(sampleText)[length-2:]); passage.Text != want {
		t.Errorf("Resolve() read %q, want %q", passage.Text, want)
	}
}

func TestAPlaceIsNotAPassage(t *testing.T) {
	service, repository, documents := newSampleService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	// A zero-width locator is legal and is what a saved position is: where the
	// reader stopped is a place. A place is not a passage — there is no text to
	// hand a model — and the two are refused differently because the reader's next
	// move differs.
	_, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", CharStart: 12, CharEnd: 12,
	}, testCaller)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Resolve() error = %v, want an invalid request", err)
	}
	if !strings.Contains(err.Error(), "no text") {
		t.Errorf("Resolve() refused a place with %q", err)
	}
	if len(documents.reads) != 0 {
		t.Errorf("Resolve() read %v for a place", documents.reads)
	}
}

func TestAResolveRefusesWhatItCannotActOn(t *testing.T) {
	for _, test := range []struct {
		name      string
		material  string
		locator   Locator
		want      string
		wantReads int
	}{
		{
			name:    "no material was named",
			locator: Locator{MaterialID: "rm_1", CharEnd: 5},
			want:    "no material",
		},
		{
			name:     "the locator names no material",
			material: "rm_1", locator: Locator{CharEnd: 5},
			want: "no material",
		},
		{
			name:     "the locator is about another material",
			material: "rm_1", locator: Locator{MaterialID: "rm_2", CharEnd: 5},
			want: "another material",
		},
		{
			name:     "the locator ends before it starts",
			material: "rm_1", locator: Locator{MaterialID: "rm_1", CharStart: 9, CharEnd: 4},
			want: "ends before it starts",
		},
		{
			name:     "the locator starts before the text does",
			material: "rm_1", locator: Locator{MaterialID: "rm_1", CharStart: -1, CharEnd: 4},
			want: "before the text",
		},
		{
			name:     "the locator names a page before the first one",
			material: "rm_1", locator: Locator{MaterialID: "rm_1", CharEnd: 4, Page: page(0)},
			want: "before the first one",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, repository, documents := newFakeService(t, 100)
			repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

			_, err := service.Resolve(context.Background(), test.material, test.locator, testCaller)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Resolve() error = %v, want an invalid request", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("Resolve() refused with %q, want it to mention %q", err, test.want)
			}
			// A request the service can see is wrong never reaches the knowledge
			// base, and never costs a read of the catalogue either.
			if len(documents.reads) != test.wantReads {
				t.Errorf("Resolve() read the windows %v", documents.reads)
			}
			if repository.reads != 0 {
				t.Errorf("Resolve() read the catalogue %d times for a locator it refused", repository.reads)
			}
		})
	}
}

func TestAMaterialWithNothingBehindItIsNotAPassage(t *testing.T) {
	service, repository, documents := newFakeService(t, 100)
	// A material row can exist without a document: the catalogue should list it,
	// and what it lacks is a text to be a coordinate into.
	repository.withMaterial(Material{MaterialID: "rm_1", Filename: "还没有解析的书"})

	_, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", CharStart: 0, CharEnd: 5,
	}, testCaller)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Resolve() error = %v, want an invalid request", err)
	}
	if !strings.Contains(err.Error(), "no text behind it") {
		t.Errorf("Resolve() refused with %q", err)
	}
	if len(documents.reads) != 0 {
		t.Errorf("Resolve() read the windows %v", documents.reads)
	}
}

func TestAPassageWiderThanAUnitIsRefused(t *testing.T) {
	service, repository, documents := newSampleService(t, 8)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	// The bound is the same one the outline cuts the text by, on purpose: the
	// passage a model is handed is a unit a reader could have been looking at.
	_, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", CharStart: 0, CharEnd: 9,
	}, testCaller)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Resolve() error = %v, want an invalid request", err)
	}
	if !strings.Contains(err.Error(), "more than the 8") {
		t.Errorf("Resolve() refused with %q, which does not name the bound", err)
	}
	if len(documents.reads) != 0 {
		t.Errorf("Resolve() read %v past the bound", documents.reads)
	}

	// A span exactly at the bound is a unit, and is served.
	if _, err := service.Resolve(context.Background(), "rm_1", Locator{
		MaterialID: "rm_1", CharStart: 0, CharEnd: 8,
	}, testCaller); err != nil {
		t.Errorf("Resolve() refused a span at the bound: %v", err)
	}
}

func TestAMaterialThatIsNotThereIsNotAPassage(t *testing.T) {
	service, _, _ := newFakeService(t, 100)

	_, err := service.Resolve(context.Background(), "rm_missing", Locator{
		MaterialID: "rm_missing", CharStart: 0, CharEnd: 5,
	}, testCaller)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve() error = %v, want a missing resource", err)
	}
}

func TestUnitsAreRunsOfChunksBoundedByTheReadingSize(t *testing.T) {
	for _, test := range []struct {
		name     string
		maxChars int
		spans    []ChunkSpan
		want     []Unit
	}{
		{
			name:  "no chunks is no units",
			spans: nil,
			want:  []Unit{},
		},
		{
			name:  "one chunk is one unit",
			spans: []ChunkSpan{{CharStart: 0, CharEnd: 40}},
			want:  []Unit{{Ordinal: 0, CharStart: 0, CharEnd: 40}},
		},
		{
			// The merge is the whole point: a chunk is sized for retrieval and a
			// unit for reading.
			name: "chunks that fit together are one unit",
			spans: []ChunkSpan{
				{CharStart: 0, CharEnd: 40}, {CharStart: 40, CharEnd: 70}, {CharStart: 70, CharEnd: 100},
			},
			want: []Unit{{Ordinal: 0, CharStart: 0, CharEnd: 100}},
		},
		{
			name: "a unit ends where the next chunk would cross the bound",
			spans: []ChunkSpan{
				{CharStart: 0, CharEnd: 40}, {CharStart: 40, CharEnd: 70}, {CharStart: 70, CharEnd: 100},
			},
			maxChars: 60,
			want: []Unit{
				{Ordinal: 0, CharStart: 0, CharEnd: 40},
				{Ordinal: 1, CharStart: 40, CharEnd: 100},
			},
		},
		{
			// Splitting it would invent an offset in the middle of a passage the
			// knowledge base chose as one, and the bound is a preference about size
			// rather than a rule the text has to obey.
			name:     "a chunk wider than the bound stays a unit of its own",
			spans:    []ChunkSpan{{CharStart: 0, CharEnd: 500}, {CharStart: 500, CharEnd: 510}},
			maxChars: 100,
			want: []Unit{
				{Ordinal: 0, CharStart: 0, CharEnd: 500},
				{Ordinal: 1, CharStart: 500, CharEnd: 510},
			},
		},
		{
			name:     "the ordinals number the units and not the chunks",
			spans:    []ChunkSpan{{CharStart: 0, CharEnd: 10}, {CharStart: 10, CharEnd: 20}, {CharStart: 20, CharEnd: 30}},
			maxChars: 10,
			want: []Unit{
				{Ordinal: 0, CharStart: 0, CharEnd: 10},
				{Ordinal: 1, CharStart: 10, CharEnd: 20},
				{Ordinal: 2, CharStart: 20, CharEnd: 30},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			maxChars := test.maxChars
			if maxChars == 0 {
				maxChars = 1000
			}
			got := unitsOf(test.spans, maxChars)
			if len(got) != len(test.want) {
				t.Fatalf("unitsOf() = %+v, want %+v", got, test.want)
			}
			for index := range got {
				if got[index].Ordinal != test.want[index].Ordinal ||
					got[index].CharStart != test.want[index].CharStart ||
					got[index].CharEnd != test.want[index].CharEnd {
					t.Fatalf("unitsOf() = %+v, want %+v", got, test.want)
				}
			}
		})
	}
}

func TestAPageRidesAlongOnlyWhileTheUnitAgreesOnOne(t *testing.T) {
	// A unit that straddles a page break has no single page, and a page number
	// invented for it is exactly the guess the citation rules forbid.
	units := unitsOf([]ChunkSpan{
		{CharStart: 0, CharEnd: 10, Page: page(1)},
		{CharStart: 10, CharEnd: 20, Page: page(1)},
	}, 1000)
	if len(units) != 1 || units[0].Page == nil || *units[0].Page != 1 {
		t.Fatalf("a unit wholly on one page came back as %+v", units)
	}

	units = unitsOf([]ChunkSpan{
		{CharStart: 0, CharEnd: 10, Page: page(1)},
		{CharStart: 10, CharEnd: 20, Page: page(2)},
	}, 1000)
	if len(units) != 1 || units[0].Page != nil {
		t.Errorf("a unit across a page break kept a page: %+v", units[0])
	}

	// A chunk with no page at all is not agreement with a page: one chunk that
	// does not know takes the unit's page away rather than voting with the other.
	units = unitsOf([]ChunkSpan{
		{CharStart: 0, CharEnd: 10, Page: page(1)},
		{CharStart: 10, CharEnd: 20},
	}, 1000)
	if len(units) != 1 || units[0].Page != nil {
		t.Errorf("a unit with one unknown chunk kept a page: %+v", units[0])
	}
}

// page is a page number for the tests that need one.
func page(number int) *int { return &number }
