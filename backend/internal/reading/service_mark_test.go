package reading

// What the service decides about a mark before storage is asked anything: that a
// note is the word and the other two kinds are places, that a kind outside the
// three is not a mark, and that a mark is written once, at the first revision,
// with the body the reader typed rather than the whitespace around it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAMarkIsWrittenAtTheFirstRevisionAndTrimmed(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	mark, err := service.AddMark(context.Background(), MarkRequest{
		MaterialID: "rm_1", SessionID: "s_1", Kind: MarkNote,
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 20},
		Body:    "  这里说的是捕获  ",
	}, testCaller)
	if err != nil {
		t.Fatalf("AddMark() error = %v", err)
	}
	if !strings.HasPrefix(mark.MarkID, markIDPrefix) {
		t.Errorf("AddMark() minted the id %q, want it prefixed %q", mark.MarkID, markIDPrefix)
	}
	if mark.Body != "这里说的是捕获" {
		t.Errorf("AddMark() stored the body %q, want it trimmed", mark.Body)
	}
	// A mark is a thing the reader put on the text; editing it is a later
	// version's business, and what the revision is for is being able to say
	// whether the row has moved.
	if mark.Revision != 1 {
		t.Errorf("AddMark() wrote revision %d, want the first one", mark.Revision)
	}
	if mark.CreatedAt.IsZero() || mark.CreatedAt.Location() != time.UTC {
		t.Errorf("AddMark() stamped %v, want a UTC instant", mark.CreatedAt)
	}
}

func TestAMarkRefusesWhatItCannotBe(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	good := MarkRequest{
		MaterialID: "rm_1", SessionID: "s_1", Kind: MarkBookmark,
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}

	for _, test := range []struct {
		name    string
		mutate  func(*MarkRequest)
		want    string
		refused bool
	}{
		{name: "a bookmark with no word of its own", want: ""},
		{name: "a highlight with no word of its own", mutate: func(r *MarkRequest) { r.Kind = MarkHighlight }},
		{
			// A bookmark and a highlight are places in the text; a note is the
			// word. Refusing an empty one is what keeps the three kinds from
			// collapsing into "a mark with some optional text".
			name: "a note with no word of its own", mutate: func(r *MarkRequest) { r.Kind = MarkNote },
			want: "a note with no text", refused: true,
		},
		{name: "a note of nothing but spaces",
			mutate: func(r *MarkRequest) { r.Kind, r.Body = MarkNote, "   " },
			want:   "a note with no text", refused: true},
		{name: "a kind that does not exist",
			mutate: func(r *MarkRequest) { r.Kind = MarkKind("sticker") },
			want:   "is not a kind of mark", refused: true},
		{name: "no material was named", mutate: func(r *MarkRequest) { r.MaterialID = "" },
			want: "no material", refused: true},
		{name: "no conversation was named", mutate: func(r *MarkRequest) { r.SessionID = "" },
			want: "no conversation", refused: true},
		{name: "a locator about another material",
			mutate: func(r *MarkRequest) { r.Locator.MaterialID = "rm_2" },
			want:   "another material", refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := good
			if test.mutate != nil {
				test.mutate(&request)
			}
			// Counted before the call, not after: the table's first two rows are
			// marks the service accepts, so by the time a refusal is reached the
			// store is not empty and the question is whether this call added to it.
			before := len(repository.marks)
			_, err := service.AddMark(context.Background(), request, testCaller)
			if test.refused {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("AddMark() error = %v, want an invalid request", err)
				}
				if !strings.Contains(err.Error(), test.want) {
					t.Errorf("AddMark() refused with %q, want it to mention %q", err, test.want)
				}
				if len(repository.marks) != before {
					t.Errorf("AddMark() wrote a mark it refused: %+v", repository.marks[before:])
				}
				return
			}
			if err != nil {
				t.Errorf("AddMark() error = %v", err)
			}
		})
	}
}

func TestAMarkIsNotWrittenForAMaterialThatIsNotThere(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)

	_, err := service.AddMark(context.Background(), MarkRequest{
		MaterialID: "rm_missing", SessionID: "s_1", Kind: MarkBookmark,
		Locator: Locator{MaterialID: "rm_missing", CharStart: 10, CharEnd: 10},
	}, testCaller)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddMark() error = %v, want a missing resource", err)
	}
	if len(repository.marks) != 0 {
		t.Errorf("AddMark() wrote %+v", repository.marks)
	}
}

func TestTheMarksOfOneMaterialAreTheOnesThatComeBack(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	repository.marks = []Mark{
		{MarkID: "rmk_1", MaterialID: "rm_1", Kind: MarkBookmark},
		{MarkID: "rmk_2", MaterialID: "rm_2", Kind: MarkNote, Body: "别人的"},
		{MarkID: "rmk_3", MaterialID: "rm_1", Kind: MarkHighlight},
	}

	marks, err := service.Marks(context.Background(), "rm_1", testCaller)
	if err != nil {
		t.Fatalf("Marks() error = %v", err)
	}
	if len(marks) != 2 || marks[0].MarkID != "rmk_1" || marks[1].MarkID != "rmk_3" {
		t.Errorf("Marks() returned %+v", marks)
	}
	if _, err := service.Marks(context.Background(), "rm_missing", testCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("Marks() of a missing material error = %v, want a missing resource", err)
	}
}

func TestRemovingAMarkThatIsNotThereIsNotFound(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.marks = []Mark{{MarkID: "rmk_1", MaterialID: "rm_1", Kind: MarkBookmark}}
	ctx := context.Background()

	if err := service.RemoveMark(ctx, "rmk_1", testCaller); err != nil {
		t.Fatalf("RemoveMark() error = %v", err)
	}
	if len(repository.deletes) != 1 || repository.deletes[0] != "rmk_1" {
		t.Errorf("RemoveMark() deleted %v", repository.deletes)
	}
	if err := service.RemoveMark(ctx, "rmk_missing", testCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveMark() error = %v, want a missing resource", err)
	}
}

func TestTheCatalogueAndOneRowOfItAreTheSameRead(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	repository.withMaterial(sampleMaterial("rm_2", "doc_2"))

	materials, err := service.Materials(context.Background(), "   ", testCaller)
	if err != nil {
		t.Fatalf("Materials() error = %v", err)
	}
	if len(materials) != 2 {
		t.Errorf("Materials() returned %+v", materials)
	}
	material, err := service.Material(context.Background(), "  rm_2  ", testCaller)
	if err != nil {
		t.Fatalf("Material() error = %v", err)
	}
	if material.MaterialID != "rm_2" {
		t.Errorf("Material() returned %+v, want the row that was asked for by a padded id", material)
	}
	if _, err := service.Material(context.Background(), "rm_missing", testCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("Material() error = %v, want a missing resource", err)
	}
}
