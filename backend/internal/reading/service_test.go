package reading

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestANewServiceNeedsWhatItCannotDoWithout(t *testing.T) {
	repository, documents := newFakeRepository(), newFakeDocuments()
	for _, test := range []struct {
		name       string
		repository Repository
		documents  Documents
		options    Options
	}{
		{name: "no repository", documents: documents, options: Options{UnitMaxChars: 100}},
		{name: "no knowledge base", repository: repository, options: Options{UnitMaxChars: 100}},
		{name: "a bound of zero", repository: repository, documents: documents},
		{name: "a negative bound", repository: repository, documents: documents, options: Options{UnitMaxChars: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.repository, test.documents, test.options); err == nil {
				t.Error("NewService() accepted a service it cannot run")
			}
		})
	}
}

func TestTheOutlineIsTheMaterialsUnitsAndHowLongItIs(t *testing.T) {
	service, repository, documents := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	documents.withDocument(Document{
		ID: "doc_1", FileID: "lib_1", Filename: "算法笔记.md", TextChars: 100, Revision: 2,
	}, sampleText, ChunkSpan{CharStart: 0, CharEnd: 40}, ChunkSpan{CharStart: 40, CharEnd: 70})

	outline, err := service.Outline(context.Background(), "rm_1", testCaller)
	if err != nil {
		t.Fatalf("Outline() error = %v", err)
	}
	if outline.Material.MaterialID != "rm_1" {
		t.Errorf("Outline() described %+v", outline.Material)
	}
	// How long the text is comes from the document, not from the units: the units
	// may stop short of it and a reader still needs to know how far they can go.
	if outline.TextChars != 100 {
		t.Errorf("Outline() says the text is %d characters, want 100", outline.TextChars)
	}
	if len(outline.Units) != 1 || outline.Units[0].CharStart != 0 || outline.Units[0].CharEnd != 70 {
		t.Errorf("Outline() came back with the units %+v", outline.Units)
	}
	// An outline is coordinates. It never reads a word of the text, which is what
	// keeps listing a catalogue from being a second reader of every document in it.
	if len(documents.reads) != 0 {
		t.Errorf("Outline() read the windows %v", documents.reads)
	}
}

func TestAnOutlineOfAMaterialWithNoDocumentIsAnEmptyOutline(t *testing.T) {
	service, repository, documents := newFakeService(t, 100)
	repository.withMaterial(Material{MaterialID: "rm_1", Filename: "还没有解析的书"})

	// The row is there and the catalogue should list it; what it lacks is a text
	// to be a coordinate into. Saying so with an empty outline is truer than
	// refusing to describe a row that exists.
	outline, err := service.Outline(context.Background(), "rm_1", testCaller)
	if err != nil {
		t.Fatalf("Outline() error = %v", err)
	}
	if len(outline.Units) != 0 || outline.TextChars != 0 {
		t.Errorf("Outline() came back as %+v", outline)
	}
	if outline.Material.MaterialID != "rm_1" {
		t.Errorf("Outline() dropped the material it was asked about: %+v", outline.Material)
	}
	if len(documents.reads) != 0 {
		t.Errorf("Outline() read the windows %v", documents.reads)
	}
}

func TestAnOutlineOfAMaterialThatIsNotThereIsNotFound(t *testing.T) {
	service, _, _ := newFakeService(t, 100)

	if _, err := service.Outline(context.Background(), "rm_missing", testCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("Outline() error = %v, want a missing resource", err)
	}
}

func TestCreateOpensTheDocumentAFileWasParsedInto(t *testing.T) {
	service, repository, documents := newFakeService(t, 100)
	documents.withDocument(Document{ID: "doc_1", FileID: "lib_1", Filename: "算法笔记.md"}, sampleText)

	material, err := service.Create(context.Background(), "lib_1", "library", testCaller)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.HasPrefix(material.MaterialID, materialIDPrefix) {
		t.Errorf("Create() minted the id %q, want it prefixed %q", material.MaterialID, materialIDPrefix)
	}
	// The material is a row pointing at a document rather than a copy of one, and
	// the name a reader sees is the file's.
	if material.ContentID != "doc_1" || material.Filename != "算法笔记.md" || material.SourceType != "library" {
		t.Errorf("Create() built %+v", material)
	}
	if len(repository.materials) != 1 {
		t.Errorf("Create() wrote %d materials, want one", len(repository.materials))
	}
}

func TestCreateOfAFileThatIsAlreadyOpenIsTheSameMaterial(t *testing.T) {
	service, repository, documents := newFakeService(t, 100)
	documents.withDocument(Document{ID: "doc_1", FileID: "lib_1", Filename: "算法笔记.md"}, sampleText)
	repository.withMaterial(sampleMaterial("rm_existing", "doc_1"))

	// "Open this file in the reader" clicked twice is one tab, so the second
	// answer is the first row rather than a second one.
	material, err := service.Create(context.Background(), "lib_1", "library", testCaller)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if material.MaterialID != "rm_existing" {
		t.Errorf("Create() returned %q, want the material that was already there", material.MaterialID)
	}
	if len(repository.materials) != 1 {
		t.Errorf("Create() left %d materials behind", len(repository.materials))
	}
}

func TestCreateRefusesWhatItCannotActOn(t *testing.T) {
	service, _, _ := newFakeService(t, 100)

	if _, err := service.Create(context.Background(), "   ", "library", testCaller); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Create() with no file error = %v, want an invalid request", err)
	}
	// A file no parse has reached yet is not a material and not an error of the
	// caller's: the knowledge base says which of the two it is.
	if _, err := service.Create(context.Background(), "lib_missing", "library", testCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("Create() of an unparsed file error = %v, want a missing resource", err)
	}
}

func TestAPositionIsCreatedTheFirstTimeAndMovedAfterThat(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	ctx := context.Background()

	// A revision of zero is a request to create rather than a promise about the
	// row, which is why it is not simply "revision zero is a conflict".
	created, err := service.SavePosition(ctx, SavePositionRequest{
		MaterialID: "rm_1", SessionID: "s_1",
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}, testCaller)
	if err != nil {
		t.Fatalf("SavePosition() error = %v", err)
	}
	if created.Revision != 1 {
		t.Errorf("the first save wrote revision %d, want 1", created.Revision)
	}
	if len(repository.created) != 1 || len(repository.moved) != 0 {
		t.Fatalf("the first save created %d and moved %d", len(repository.created), len(repository.moved))
	}

	moved, err := service.SavePosition(ctx, SavePositionRequest{
		MaterialID: "rm_1", SessionID: "s_1", Revision: created.Revision,
		Locator: Locator{MaterialID: "rm_1", CharStart: 40, CharEnd: 40},
	}, testCaller)
	if err != nil {
		t.Fatalf("SavePosition() error = %v", err)
	}
	if moved.Revision != 2 || moved.Locator.CharStart != 40 {
		t.Errorf("the move left the position as %+v", moved)
	}
	if len(repository.created) != 1 || len(repository.moved) != 1 {
		t.Errorf("the move created %d and moved %d", len(repository.created), len(repository.moved))
	}
}

func TestAPositionWriteAgainstARevisionThatHasMovedIsAConflict(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	repository.withPosition(Position{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 4,
		Locator: Locator{MaterialID: "rm_1", CharStart: 90, CharEnd: 90},
	})

	// Somebody else saved first. The answer is not a failure but a conflict: the
	// caller reads the row again and decides (the third appearance of this shape
	// in this version, after the review state and the path state).
	_, err := service.SavePosition(context.Background(), SavePositionRequest{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 3,
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}, testCaller)
	if !errors.Is(err, ErrStalePosition) {
		t.Fatalf("SavePosition() error = %v, want a stale position", err)
	}
	if !strings.Contains(err.Error(), "no longer current") {
		t.Errorf("SavePosition() refused with %q", err)
	}
	if len(repository.moved) != 0 {
		t.Errorf("SavePosition() moved the position anyway: %+v", repository.moved)
	}
	// The row is untouched: nothing was written over what the other writer left.
	if repository.positions[0].Revision != 4 || repository.positions[0].Locator.CharStart != 90 {
		t.Errorf("the refused write changed the row to %+v", repository.positions[0])
	}
}

func TestAPositionThatIsGoneIsRefusedDifferentlyFromOneThatHasMoved(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	// Both look alike from the write, and reading the row tells them apart. "Your
	// position is gone" and "your position has moved" invite different next steps.
	_, err := service.SavePosition(context.Background(), SavePositionRequest{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 1,
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}, testCaller)
	if !errors.Is(err, ErrStalePosition) {
		t.Fatalf("SavePosition() error = %v, want a stale position", err)
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("SavePosition() refused with %q, which does not say the row is gone", err)
	}
}

func TestTwoTabsCreatingTheSamePositionAtOnceIsAConflict(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	// The other tab got there between this one reading nothing and this one
	// writing, which storage reports as a collision on the pair.
	repository.withPosition(Position{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 1,
		Locator: Locator{MaterialID: "rm_1", CharStart: 5, CharEnd: 5},
	})

	_, err := service.SavePosition(context.Background(), SavePositionRequest{
		MaterialID: "rm_1", SessionID: "s_1",
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}, testCaller)
	// A collision on the pair is a brand new conflict to the caller, not the
	// storage error it arrived as: the way out is the same one — read and decide.
	if !errors.Is(err, ErrStalePosition) {
		t.Fatalf("SavePosition() error = %v, want a stale position", err)
	}
	if !strings.Contains(err.Error(), "another tab") {
		t.Errorf("SavePosition() refused with %q", err)
	}
	if len(repository.created) != 0 {
		t.Errorf("SavePosition() wrote a second row for the pair: %+v", repository.created)
	}
}

func TestASavePositionRequestThatNamesTwoMaterialsIsRefused(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))

	for _, test := range []struct {
		name    string
		request SavePositionRequest
		want    string
	}{
		{
			name: "no material was named",
			request: SavePositionRequest{SessionID: "s_1",
				Locator: Locator{MaterialID: "rm_1", CharStart: 1, CharEnd: 5}},
			want: "no material",
		},
		{
			name: "no conversation was named",
			request: SavePositionRequest{MaterialID: "rm_1",
				Locator: Locator{MaterialID: "rm_1", CharStart: 1, CharEnd: 5}},
			want: "no conversation",
		},
		{
			name: "a negative revision",
			request: SavePositionRequest{MaterialID: "rm_1", SessionID: "s_1", Revision: -1,
				Locator: Locator{MaterialID: "rm_1", CharStart: 1, CharEnd: 5}},
			want: "negative",
		},
		{
			// The material is one field said twice, and a request that says them
			// differently has no reading: it is one position about two materials.
			name: "a locator about another material",
			request: SavePositionRequest{MaterialID: "rm_1", SessionID: "s_1",
				Locator: Locator{MaterialID: "rm_2", CharStart: 1, CharEnd: 5}},
			want: "another material",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.SavePosition(context.Background(), test.request, testCaller)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("SavePosition() error = %v, want an invalid request", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("SavePosition() refused with %q, want it to mention %q", err, test.want)
			}
			if len(repository.created)+len(repository.moved) != 0 {
				t.Errorf("SavePosition() reached storage with a request it could not act on")
			}
		})
	}
}

func TestAPositionIsNotSavedForAMaterialThatIsNotThere(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)

	_, err := service.SavePosition(context.Background(), SavePositionRequest{
		MaterialID: "rm_missing", SessionID: "s_1",
		Locator: Locator{MaterialID: "rm_missing", CharStart: 1, CharEnd: 5},
	}, testCaller)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SavePosition() error = %v, want a missing resource", err)
	}
	if len(repository.created)+len(repository.moved) != 0 {
		t.Error("SavePosition() wrote a position for a material that is not there")
	}
}

func TestAPositionThatWasNeverSavedIsAnAnswerAndNotAnError(t *testing.T) {
	service, repository, _ := newFakeService(t, 100)
	repository.withMaterial(sampleMaterial("rm_1", "doc_1"))
	ctx := context.Background()

	// A reader who has not scrolled yet is not a reader with a problem.
	_, found, err := service.Position(ctx, "rm_1", "s_1", testCaller)
	if err != nil {
		t.Fatalf("Position() error = %v", err)
	}
	if found {
		t.Error("Position() reported a saved position where none was saved")
	}
	// A material that is not there is a different answer from a material nobody
	// has read yet, and the two must not collapse.
	if _, _, err := service.Position(ctx, "rm_missing", "s_1", testCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("Position() of a missing material error = %v, want a missing resource", err)
	}
}
