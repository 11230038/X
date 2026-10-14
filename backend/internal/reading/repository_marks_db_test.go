package reading

// The marginalia half of the storage contract: the order marks come back in, the
// two foreign keys that point in opposite directions, the closed set of kinds,
// and the key a question asked about a passage hangs off.
//
// The sentence migration 00012 writes about the directions is the one being
// checked here: a material owns its marks, while a conversation is only where
// they were written. Losing what somebody wrote because they tidied their list of
// conversations would be the wrong half of the pair to keep.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// storeMarkRow writes one mark with the time the caller chooses, which is the
// only way the order it comes back in can be asserted rather than observed. A nil
// session is a mark whose conversation has been deleted.
func storeMarkRow(t *testing.T, resource *data.Resource, markID, materialID string,
	sessionID any, kind string, at *time.Time) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO reading_marks "+
		"(mark_id, material_id, session_id, kind, locator_json, body, revision, creat_time) "+
		"VALUES (?, ?, ?, ?, ?, ?, 1, ?)",
		markID, materialID, sessionID, kind,
		`{"material_id":"`+materialID+`","ordinal":0,"char_start":10,"char_end":20}`,
		"这里说的", moment(at))
}

func TestDBMarksAreOldestFirstThenByID(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	// Two of the three share a second, so the order between them is decided by
	// id. Written out of order on purpose: a margin whose marks rearranged
	// themselves between two reads of the same material would be a reader's
	// annotations moving around without them.
	storeMarkRow(t, resource, "rmk_b", "rm_1", "s_1", "bookmark", ptr(atSecond(1)))
	storeMarkRow(t, resource, "rmk_a", "rm_1", "s_1", "note", ptr(atSecond(1)))
	storeMarkRow(t, resource, "rmk_late", "rm_1", nil, "highlight", ptr(atSecond(4)))

	marks, err := repo.Marks(context.Background(), "rm_1")
	if err != nil {
		t.Fatalf("list the marks: %v", err)
	}
	ids := make([]string, 0, len(marks))
	for _, mark := range marks {
		ids = append(ids, mark.MarkID)
	}
	if want := []string{"rmk_a", "rmk_b", "rmk_late"}; !slices.Equal(ids, want) {
		t.Errorf("the marks are %v, want %v", ids, want)
	}
	// The order is the order they were written, and it is the only order
	// available: a mark's place in the document lives inside its locator, which
	// is JSON, and a database cannot sort by a coordinate inside a document.
	if !marks[0].CreatedAt.Equal(atSecond(1)) || marks[0].Kind != MarkNote {
		t.Errorf("the first mark is %+v", marks[0])
	}

	// A mark written outside any conversation reads with none, which is a state
	// the column allows and the domain has a word for.
	if marks[2].SessionID != "" {
		t.Errorf("a mark with no conversation reads session %q", marks[2].SessionID)
	}
	// Marks belong to one material: a listing is about a book, not about every
	// book at once.
	if _, err := repo.Marks(context.Background(), "rm_missing"); err != nil {
		t.Errorf("listing the marks of a material with none: %v", err)
	}
}

func TestDBAMarkIsReadBackWhole(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	ctx := context.Background()

	err := repo.CreateMark(ctx, Mark{
		MarkID: "rmk_1", MaterialID: "rm_1", SessionID: "s_1", Kind: MarkHighlight,
		Locator: Locator{MaterialID: "rm_1", Ordinal: 3, CharStart: 10, CharEnd: 20, Page: page(7)},
	})
	if err != nil {
		t.Fatalf("create a highlight: %v", err)
	}
	// A highlight is a place in the text and can stand without a word, so the
	// body column stores nothing rather than an empty string.
	err = repo.CreateMark(ctx, Mark{
		MarkID: "rmk_2", MaterialID: "rm_1", SessionID: "s_1", Kind: MarkNote,
		Locator: Locator{MaterialID: "rm_1", CharStart: 30, CharEnd: 40}, Body: "这里说的是捕获",
	})
	if err != nil {
		t.Fatalf("create a note: %v", err)
	}

	highlight, err := repo.Mark(ctx, "rmk_1")
	if err != nil {
		t.Fatalf("read the highlight back: %v", err)
	}
	// The coordinate is decoded out of the JSON column, page and all: a mark
	// whose page was dropped on the way through storage would reopen in the wrong
	// place.
	if highlight.Locator.Ordinal != 3 || highlight.Locator.CharStart != 10 ||
		highlight.Locator.CharEnd != 20 || highlight.Locator.Page == nil || *highlight.Locator.Page != 7 {
		t.Errorf("the stored highlight carries %+v", highlight.Locator)
	}
	if highlight.Kind != MarkHighlight || highlight.SessionID != "s_1" || highlight.Body != "" {
		t.Errorf("the stored highlight is %+v", highlight)
	}
	if highlight.Revision != 1 || highlight.CreatedAt.IsZero() {
		t.Errorf("the stored highlight is %+v, want the first revision and a moment", highlight)
	}
	note, err := repo.Mark(ctx, "rmk_2")
	if err != nil {
		t.Fatalf("read the note back: %v", err)
	}
	if note.Body != "这里说的是捕获" || note.Kind != MarkNote {
		t.Errorf("the stored note is %+v", note)
	}
	if _, err := repo.Mark(ctx, "rmk_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a mark that is not there: %v, want %v", err, ErrNotFound)
	}
}

func TestDBAMarkIsOneOfThreeKindsAndOutlivesWhatItIsNotOwnedBy(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeMaterialRow(t, resource, "rm_2", "doc_2", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	ctx := context.Background()
	for _, kind := range []MarkKind{MarkBookmark, MarkHighlight, MarkNote} {
		err := repo.CreateMark(ctx, Mark{
			MarkID: "rmk_" + string(kind), MaterialID: "rm_1", SessionID: "s_1", Kind: kind,
			Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 20},
		})
		if err != nil {
			t.Fatalf("create a %s: %v", kind, err)
		}
	}

	// The material owns the mark: delete the book and the marginalia goes with it.
	// The two marks on the other material are not the same book and stay.
	testdb.Exec(t, resource, "DELETE FROM reading_materials WHERE material_id = ?", "rm_1")
	if got := countRows(t, resource, "reading_marks"); got != 0 {
		t.Errorf("%d marks are left after the material went", got)
	}
	err := repo.CreateMark(ctx, Mark{
		MarkID: "rmk_kept", MaterialID: "rm_2", SessionID: "s_1", Kind: MarkNote,
		Locator: Locator{MaterialID: "rm_2", CharStart: 10, CharEnd: 20}, Body: "另一本书上的",
	})
	if err != nil {
		t.Fatalf("create a mark on the other material: %v", err)
	}

	// The conversation is only where it was written: deleting it leaves what the
	// reader wrote standing, with no session recorded.
	testdb.Exec(t, resource, "DELETE FROM sessions WHERE session_id = ?", "s_1")
	if got := countRows(t, resource, "reading_marks"); got != 1 {
		t.Fatalf("%d marks are left, want the one: a tidied conversation list must not "+
			"take somebody's notes with it", got)
	}
	kept, err := repo.Mark(ctx, "rmk_kept")
	if err != nil {
		t.Fatalf("read the mark back: %v", err)
	}
	if kept.SessionID != "" || kept.Body != "另一本书上的" {
		t.Errorf("the mark that outlived its conversation is %+v", kept)
	}
}

func TestDBAMarkCannotExistHalfWritten(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))

	// The schema states what the domain states, and it states it for every
	// writer: a fourth kind would reach the reader as a shape it has no renderer
	// for, so the column's closed set is a rule rather than a comment.
	for _, scenario := range []struct{ name, query string }{
		{"a kind outside the three",
			"INSERT INTO reading_marks (mark_id, material_id, kind, locator_json, revision) " +
				"VALUES ('rmk_1', 'rm_1', 'sticker', '{}', 1)"},
		{"a mark at a revision before the first one",
			"INSERT INTO reading_marks (mark_id, material_id, kind, locator_json, revision) " +
				"VALUES ('rmk_1', 'rm_1', 'bookmark', '{}', 0)"},
		{"a mark with no id",
			"INSERT INTO reading_marks (mark_id, material_id, kind, locator_json, revision) " +
				"VALUES ('', 'rm_1', 'bookmark', '{}', 1)"},
		{"a mark with no material named",
			"INSERT INTO reading_marks (mark_id, material_id, kind, locator_json, revision) " +
				"VALUES ('rmk_1', '', 'bookmark', '{}', 1)"},
		{"a mark over a material that is not there",
			"INSERT INTO reading_marks (mark_id, material_id, kind, locator_json, revision) " +
				"VALUES ('rmk_1', 'rm_missing', 'bookmark', '{}', 1)"},
		{"a mark over a conversation that is not there",
			"INSERT INTO reading_marks (mark_id, material_id, session_id, kind, locator_json, revision) " +
				"VALUES ('rmk_1', 'rm_1', 's_missing', 'bookmark', '{}', 1)"},
	} {
		if err := testdb.Try(resource, scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}
	if got := countRows(t, resource, "reading_marks"); got != 0 {
		t.Errorf("%d marks are stored, want none", got)
	}

	// A mark is a thing the reader put on the text, and taking it off reports the
	// rows it removed — so a second removal has an answer and does not have to
	// read the table to find it.
	if err := repo.CreateMark(context.Background(), Mark{
		MarkID: "rmk_1", MaterialID: "rm_1", Kind: MarkBookmark,
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}); err != nil {
		t.Fatalf("create a bookmark: %v", err)
	}
	if err := repo.DeleteMark(context.Background(), "rmk_1"); err != nil {
		t.Fatalf("remove the bookmark: %v", err)
	}
	if err := repo.DeleteMark(context.Background(), "rmk_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing the same mark twice: %v, want %v", err, ErrNotFound)
	}
}

func TestDBThePendingQuizKeyNamesThePassageAndNotJustTheQuestion(t *testing.T) {
	_, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeMaterialRow(t, resource, "rm_2", "doc_2", atSecond(1), nil)

	// A question about paragraph three of one book cannot be told from a question
	// about paragraph nine of another by its id alone, so the key names the
	// passage as well. The same id under two materials and two locators is two
	// rows, and the same triple twice is one.
	for _, row := range []struct{ material, locator string }{
		{"rm_1", `{"ordinal":3}`}, {"rm_1", `{"ordinal":9}`}, {"rm_2", `{"ordinal":3}`},
	} {
		testdb.Exec(t, resource, "INSERT INTO reading_quiz_pending "+
			"(material_id, locator, question_id, question_json, creat_time) VALUES (?, ?, ?, ?, ?)",
			row.material, row.locator, "q_1", `{"question":"哪一个？"}`, atSecond(1))
	}
	if got := countRows(t, resource, "reading_quiz_pending"); got != 3 {
		t.Errorf("%d pending questions are stored, want the three passages", got)
	}
	err := testdb.Try(resource, "INSERT INTO reading_quiz_pending "+
		"(material_id, locator, question_id, question_json) VALUES (?, ?, ?, ?)",
		"rm_1", `{"ordinal":3}`, "q_1", `{}`)
	if err == nil {
		t.Error("the same question about the same passage twice was accepted")
	}
	// A question about a passage of a book that is gone is not a question about
	// anything.
	testdb.Exec(t, resource, "DELETE FROM reading_materials WHERE material_id = ?", "rm_1")
	if got := countRows(t, resource, "reading_quiz_pending"); got != 1 {
		t.Errorf("%d pending questions are left, want only the other material's one", got)
	}
}
