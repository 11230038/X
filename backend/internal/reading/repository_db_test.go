package reading

// The reading layer's storage contract, checked against a real seekdb: the order
// a catalogue promises, the composite key a position is one row per, the compare
// and swap that makes two tabs a conflict rather than a silent overwrite, and the
// two foreign keys that point in opposite directions.
//
// None of it is knowable from a double. The fake in fake_test.go restates the
// contract and then agrees with itself; what actually decides these answers is
// the primary key, the CHECK constraints and the ON DELETE clauses in migration
// 00012, and the SQL the adapter writes.
//
// One database per package means nothing here calls t.Parallel, and every test
// starts on empty tables.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// dbTables are the tables these tests write, children first. sessions comes last
// because three of the reading tables are foreign keys into it, and emptying it
// is what those cascades are watched removing.
var dbTables = []string{
	"reading_quiz_pending",
	"reading_marks",
	"reading_pairings",
	"reading_positions",
	"reading_workspace_materials",
	"reading_workspace_sessions",
	"reading_workspaces",
	"reading_materials",
	"sessions",
}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "reading")
	testdb.Reset(t, resource, dbTables...)
	return NewGORMRepository(resource.GORM()), resource
}

// atSecond is a whole second on a fixed day. Timestamps are spelled out because
// DATETIME keeps whole seconds: two rows written in the same second tie, and an
// ordering asserted over tied rows asserts whatever the plan did that time.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// moment is a time as a statement argument. A nil pointer passed straight to
// database/sql arrives as a type it does not know, so the nothing has to be said
// as nothing.
func moment(at *time.Time) any {
	if at == nil {
		return nil
	}
	return *at
}

// ptr is an address for the times a row wants an instant rather than nothing.
func ptr(at time.Time) *time.Time { return &at }

// storeMaterialRow writes one catalogue row with the times the caller chooses,
// which is the only way an ordering can be asserted rather than observed.
func storeMaterialRow(t *testing.T, resource *data.Resource, id string,
	contentID any, created time.Time, updated *time.Time) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO reading_materials "+
		"(material_id, content_id, filename, source_type, creat_time, update_time) "+
		"VALUES (?, ?, ?, ?, ?, ?)",
		id, contentID, "算法笔记.md", "library", created, moment(updated))
}

// storeSessionRow writes the conversation a position or a mark hangs off. The
// sessions table belongs to another package; this is the one row of it the
// reading layer's foreign keys need.
func storeSessionRow(t *testing.T, resource *data.Resource, id string, at time.Time) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO sessions "+
		"(session_id, title, creat_time, update_time) VALUES (?, ?, ?, ?)", id, id, at, at)
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error; err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// catalogueIDs is a listing by id, which is what its order is about.
func catalogueIDs(t *testing.T, repo *GORMRepository, workspaceID string) []string {
	t.Helper()
	materials, err := repo.Materials(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("list the catalogue: %v", err)
	}
	ids := make([]string, 0, len(materials))
	for _, material := range materials {
		ids = append(ids, material.MaterialID)
	}
	return ids
}

// pinnedOf reports a tab's two nullable flags as they read, so that "nobody has
// said" can be told from "somebody said no".
func pinnedOf(t *testing.T, repo *GORMRepository, workspaceID, materialID string) (string, string) {
	t.Helper()
	materials, err := repo.Materials(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("list the catalogue: %v", err)
	}
	for _, material := range materials {
		if material.MaterialID == materialID {
			return flag(material.Pinned), flag(material.Opened)
		}
	}
	t.Fatalf("material %s is not in the catalogue", materialID)
	return "", ""
}

func flag(value *bool) string {
	if value == nil {
		return "unknown"
	}
	if *value {
		return "yes"
	}
	return "no"
}

func TestDBTheCatalogueIsMostRecentlyTouchedFirstThenByID(t *testing.T) {
	repo, resource := newDBRepository(t)
	updated := atSecond(5)
	storeMaterialRow(t, resource, "rm_a", "doc_1", atSecond(1), ptr(atSecond(3)))
	// A material nobody has written to has no update time at all, and it belongs
	// after the ones that have: "not touched since it was made" is the oldest
	// thing there is.
	storeMaterialRow(t, resource, "rm_b", "doc_2", atSecond(2), nil)
	storeMaterialRow(t, resource, "rm_c", "doc_3", atSecond(1), &updated)
	storeMaterialRow(t, resource, "rm_d", "doc_4", atSecond(1), &updated)

	// Most recently touched first, and the two that share a second are decided by
	// id — without that, a catalogue whose order changed between identical
	// requests would make a reader's list rearrange itself for no reason.
	want := []string{"rm_c", "rm_d", "rm_a", "rm_b"}
	if got := catalogueIDs(t, repo, ""); !slices.Equal(got, want) {
		t.Errorf("the catalogue is %v, want %v", got, want)
	}

	// The row itself is what the fixture wrote, or the order above is asserting
	// against a clock the fixture does not control.
	material, err := repo.Material(context.Background(), "rm_a")
	if err != nil {
		t.Fatalf("read a material back: %v", err)
	}
	if !material.UpdatedAt.Equal(atSecond(3)) || !material.CreatedAt.Equal(atSecond(1)) {
		t.Errorf("the stored material is %+v, want the times the fixture wrote", material)
	}
	if material.Filename != "算法笔记.md" || material.SourceType != "library" {
		t.Errorf("the stored material is %+v", material)
	}
}

func TestDBTheTabStateOfOneWorkspaceIsTheOnlyOneThatComesBack(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_a", "doc_1", atSecond(1), ptr(atSecond(3)))
	storeMaterialRow(t, resource, "rm_b", "doc_2", atSecond(1), ptr(atSecond(2)))
	testdb.Exec(t, resource, "INSERT INTO reading_workspaces (workspace_id, creat_time) VALUES (?, ?)",
		"rw_1", atSecond(1))
	testdb.Exec(t, resource, "INSERT INTO reading_workspaces (workspace_id, creat_time) VALUES (?, ?)",
		"rw_2", atSecond(1))
	testdb.Exec(t, resource, "INSERT INTO reading_workspace_materials "+
		"(workspace_id, material_id, tab_order, pinned, opened, creat_time) VALUES (?, ?, ?, ?, ?, ?)",
		"rw_1", "rm_a", 0, true, false, atSecond(1))
	// The second tab has never been pinned or unpinned, and NULL is how it says
	// so: a boolean that collapsed the two would file every untouched material
	// under "explicitly not pinned".
	testdb.Exec(t, resource, "INSERT INTO reading_workspace_materials "+
		"(workspace_id, material_id, tab_order, creat_time) VALUES (?, ?, ?, ?)",
		"rw_1", "rm_b", 1, atSecond(1))
	testdb.Exec(t, resource, "INSERT INTO reading_workspace_materials "+
		"(workspace_id, material_id, tab_order, pinned, opened, creat_time) VALUES (?, ?, ?, ?, ?, ?)",
		"rw_2", "rm_a", 0, false, true, atSecond(1))

	if pinned, opened := pinnedOf(t, repo, "rw_1", "rm_a"); pinned != "yes" || opened != "no" {
		t.Errorf("the first tab reads pinned=%s opened=%s, want yes and no", pinned, opened)
	}
	if pinned, opened := pinnedOf(t, repo, "rw_1", "rm_b"); pinned != "unknown" || opened != "unknown" {
		t.Errorf("a tab nobody has said anything about reads pinned=%s opened=%s", pinned, opened)
	}
	// The same material in another workspace is somebody else's tab, and reading
	// one workspace's catalogue must not pick up another's state.
	if pinned, opened := pinnedOf(t, repo, "rw_2", "rm_a"); pinned != "no" || opened != "yes" {
		t.Errorf("the other workspace's tab reads pinned=%s opened=%s, want no and yes", pinned, opened)
	}
	// And a read outside any workspace is the same catalogue seen by somebody
	// with no tabs open: one read, one statement, no tab state at all.
	if pinned, opened := pinnedOf(t, repo, "", "rm_a"); pinned != "unknown" || opened != "unknown" {
		t.Errorf("a read outside a workspace reads pinned=%s opened=%s", pinned, opened)
	}
	// The listing is the same either way: a workspace changes the last two
	// columns, not which rows are there.
	if got := catalogueIDs(t, repo, "rw_1"); !slices.Equal(got, []string{"rm_a", "rm_b"}) {
		t.Errorf("the workspace catalogue is %v, want both tabs", got)
	}
}

func TestDBAMaterialIsFoundByItsIDAndByItsDocument(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_late", "doc_1", atSecond(5), ptr(atSecond(5)))
	storeMaterialRow(t, resource, "rm_early", "doc_1", atSecond(1), ptr(atSecond(9)))

	// Two materials over one document should not happen, and if it ever has, which
	// one a reader gets must not change between runs: the order is spelled out
	// rather than left to the storage engine.
	ctx := context.Background()
	found, err := repo.MaterialOfContent(ctx, "doc_1")
	if err != nil {
		t.Fatalf("read the material of a document: %v", err)
	}
	if found.MaterialID != "rm_early" {
		t.Errorf("the material of doc_1 is %q, want the earliest row", found.MaterialID)
	}
	if _, err := repo.MaterialOfContent(ctx, "doc_nobody_parsed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a document with no material: %v, want %v", err, ErrNotFound)
	}
	// A blank document id is not a document that no material reads: there is
	// nothing to look up, and the answer is the same one.
	if _, err := repo.MaterialOfContent(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading the material of no document: %v, want %v", err, ErrNotFound)
	}
	if _, err := repo.Material(ctx, "rm_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a material that is not there: %v, want %v", err, ErrNotFound)
	}
}

func TestDBAMaterialPointsAtWhateverItsSourceNames(t *testing.T) {
	repo, resource := newDBRepository(t)
	// content_id carries no foreign key, on purpose: a material id points into
	// whichever store its source names, and this is what that means in the schema
	// — a coordinate the reading layer holds without the other domain's table
	// being consulted or required.
	storeMaterialRow(t, resource, "rm_1", "whatever:not-a-document-id", atSecond(1), nil)

	material, err := repo.Material(context.Background(), "rm_1")
	if err != nil {
		t.Fatalf("read a material: %v", err)
	}
	if material.ContentID != "whatever:not-a-document-id" {
		t.Errorf("the material reads content_id %q", material.ContentID)
	}
	// And a material with no source at all stores nothing rather than an empty
	// string: the column allows both and they are different answers.
	storeMaterialRow(t, resource, "rm_2", nil, atSecond(2), nil)
	material, err = repo.Material(context.Background(), "rm_2")
	if err != nil {
		t.Fatalf("read a material with no document: %v", err)
	}
	if material.ContentID != "" || material.Filename != "" || material.SourceType != "" {
		t.Errorf("a material with no document reads %+v", material)
	}
	var nulls struct {
		ContentID  *string `gorm:"column:content_id"`
		Filename   *string `gorm:"column:filename"`
		SourceType *string `gorm:"column:source_type"`
	}
	err = resource.GORM().Raw("SELECT content_id, filename, source_type FROM reading_materials " +
		"WHERE material_id = 'rm_2'").Scan(&nulls).Error
	if err != nil {
		t.Fatalf("read the three columns: %v", err)
	}
	if nulls.ContentID != nil || nulls.Filename != nil || nulls.SourceType != nil {
		t.Errorf("the columns are %v/%v/%v, want nothing rather than empty strings",
			nulls.ContentID, nulls.Filename, nulls.SourceType)
	}
}

func TestDBCreateMaterialWritesTheEmptyColumnsAsNothing(t *testing.T) {
	repo, resource := newDBRepository(t)
	// The adapter turns an empty string into NULL, so the same write through the
	// adapter and through SQL have to land the same way.
	err := repo.CreateMaterial(context.Background(), Material{
		MaterialID: "rm_1", Filename: "算法笔记.md", SourceType: "library",
	})
	if err != nil {
		t.Fatalf("create a material: %v", err)
	}
	var row struct {
		ContentID *string `gorm:"column:content_id"`
	}
	err = resource.GORM().Raw("SELECT content_id FROM reading_materials WHERE material_id = 'rm_1'").
		Scan(&row).Error
	if err != nil {
		t.Fatalf("read the column: %v", err)
	}
	if row.ContentID != nil {
		t.Errorf("a material created with no document stored content_id %q, want NULL", *row.ContentID)
	}
	// creat_time is the database's to fill, and it is filled: a catalogue ordered
	// by when a material was made needs one on every row.
	material, err := repo.Material(context.Background(), "rm_1")
	if err != nil {
		t.Fatalf("read it back: %v", err)
	}
	if material.CreatedAt.IsZero() || material.UpdatedAt.IsZero() {
		t.Errorf("the created material reads %+v, want both times set", material)
	}
}
