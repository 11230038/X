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

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
)

// dbCaller is the account the storage contract tests read as.
//
// Every fixture row here is written by SQL with no owner_id, so it is a row from
// before V5: visible to the administrator, whose scope is `owner_id = ? OR
// owner_id IS NULL`, and to nobody else. Reading as the administrator is what
// keeps these tests about the SQL the adapter writes — order, composite keys,
// cascades — rather than about the filter, which has its own two accounts further
// down the file.
var dbCaller = authz.Caller{UserID: 1, Role: authz.RoleAdmin}

// dbTables are the tables these tests write, children first. sessions comes late
// because three of the reading tables are foreign keys into it, and emptying it
// is what those cascades are watched removing. users comes last: a material's
// owner column points at it.
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
	"users",
}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "reading")
	testdb.Reset(t, resource, dbTables...)
	seedAccounts(t, resource)
	return NewGORMRepository(resource.GORM()), resource
}

// seedAccounts writes the three accounts the ownership cases name.
//
// Migration 00014 made owner_id a foreign key into users. Most of this file's
// fixtures write the NULL owner — a row from before V5 — but the filter cases
// write an owner, and a row owned by an account that was never written is
// refused by the database before the filter is reached. The administrator the
// rest of the file reads as is account 1 and owns nothing, so it needs no row
// here; the three below are owners.
func seedAccounts(t *testing.T, resource *data.Resource) {
	t.Helper()
	testdb.Account(t, resource, 7, "reading_alice")
	testdb.Account(t, resource, 8, "reading_bob")
	testdb.Account(t, resource, 9, "reading_carol")
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
	materials, err := repo.Materials(context.Background(), workspaceID, dbCaller)
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
	materials, err := repo.Materials(context.Background(), workspaceID, dbCaller)
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
	material, err := repo.Material(context.Background(), "rm_a", dbCaller)
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
	found, err := repo.MaterialOfContent(ctx, "doc_1", dbCaller)
	if err != nil {
		t.Fatalf("read the material of a document: %v", err)
	}
	if found.MaterialID != "rm_early" {
		t.Errorf("the material of doc_1 is %q, want the earliest row", found.MaterialID)
	}
	if _, err := repo.MaterialOfContent(ctx, "doc_nobody_parsed", dbCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("a document with no material: %v, want %v", err, ErrNotFound)
	}
	// A blank document id is not a document that no material reads: there is
	// nothing to look up, and the answer is the same one.
	if _, err := repo.MaterialOfContent(ctx, "", dbCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading the material of no document: %v, want %v", err, ErrNotFound)
	}
	if _, err := repo.Material(ctx, "rm_missing", dbCaller); !errors.Is(err, ErrNotFound) {
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

	material, err := repo.Material(context.Background(), "rm_1", dbCaller)
	if err != nil {
		t.Fatalf("read a material: %v", err)
	}
	if material.ContentID != "whatever:not-a-document-id" {
		t.Errorf("the material reads content_id %q", material.ContentID)
	}
	// And a material with no source at all stores nothing rather than an empty
	// string: the column allows both and they are different answers. The row is
	// written here rather than through the helper above, because that one always
	// names a source — and "no source" is a state the domain has a word for, so
	// it has to be reachable from a test.
	testdb.Exec(t, resource, "INSERT INTO reading_materials "+
		"(material_id, content_id, filename, source_type, creat_time) VALUES (?, NULL, NULL, NULL, ?)",
		"rm_2", atSecond(2))
	material, err = repo.Material(context.Background(), "rm_2", dbCaller)
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
		CreatedAt: atSecond(3), UpdatedAt: atSecond(3),
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
	// Both times are the caller's and both are stored. update_time is the one
	// that matters: it is what the catalogue is ordered by, so an adapter that
	// dropped it would file every material the moment it was made at the bottom
	// of the list of everything the reader has touched.
	material, err := repo.Material(context.Background(), "rm_1", dbCaller)
	if err != nil {
		t.Fatalf("read it back: %v", err)
	}
	if !material.CreatedAt.Equal(atSecond(3)) || !material.UpdatedAt.Equal(atSecond(3)) {
		t.Errorf("the created material reads %+v, want the two times it was written with", material)
	}
}

// storeOwnedMaterialRow writes a catalogue row owned by one account. It is the
// other half of storeMaterialRow: that one writes the NULL owner every row from
// before V5 carries, and this one writes the owner a row made after V5 has.
func storeOwnedMaterialRow(t *testing.T, resource *data.Resource, id, contentID string, owner int64, at time.Time) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO reading_materials "+
		"(material_id, content_id, filename, source_type, creat_time, update_time, owner_id) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?)", id, contentID, "算法笔记.md", "library", at, at, owner)
}

// TestDBOneAccountsCatalogueIsNotAnothers is ownership as a filter rather than a
// check: each account's listing contains its own materials and not the other's,
// and an id belonging to the other account is answered exactly as an id that was
// never issued — not as a refusal that would say the row exists.
func TestDBOneAccountsCatalogueIsNotAnothers(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeOwnedMaterialRow(t, resource, "rm_alice", "doc_a", 7, atSecond(1))
	storeOwnedMaterialRow(t, resource, "rm_bob", "doc_b", 8, atSecond(2))

	alice := authz.Caller{UserID: 7, Role: authz.RoleUser}
	bob := authz.Caller{UserID: 8, Role: authz.RoleUser}
	ctx := context.Background()

	// The listing is narrowed before a row is ever returned, so account 7 never
	// sees account 8's material and vice versa.
	listingOf := func(caller authz.Caller) []string {
		t.Helper()
		materials, err := repo.Materials(ctx, "", caller)
		if err != nil {
			t.Fatalf("list the catalogue: %v", err)
		}
		ids := make([]string, 0, len(materials))
		for _, material := range materials {
			ids = append(ids, material.MaterialID)
		}
		return ids
	}
	if got := listingOf(alice); !slices.Equal(got, []string{"rm_alice"}) {
		t.Errorf("account 7's catalogue is %v, want only its own material", got)
	}
	if got := listingOf(bob); !slices.Equal(got, []string{"rm_bob"}) {
		t.Errorf("account 8's catalogue is %v, want only its own material", got)
	}

	// A read by id of the other account's material is the same answer a row that is
	// not there gets: another account's material is not one this caller may know
	// the name of.
	if _, err := repo.Material(ctx, "rm_alice", bob); !errors.Is(err, ErrNotFound) {
		t.Errorf("account 8 reading account 7's material = %v, want %v", err, ErrNotFound)
	}
	if _, err := repo.Material(ctx, "rm_bob", alice); !errors.Is(err, ErrNotFound) {
		t.Errorf("account 7 reading account 8's material = %v, want %v", err, ErrNotFound)
	}
	// The row is not gone; it is the caller's scope that removed it. An
	// administrator's scope is wider than a user's and is still not "every row":
	// it is the rows the administrator owns and the rows that belong to nobody
	// (data.OwnerScope), which is what the interface calls a row with no owner.
	// So account 7's material is no more readable by the administrator than it
	// was by account 8, and an ownerless one is readable by the administrator and
	// by nobody else until somebody claims it.
	testdb.Exec(t, resource, "INSERT INTO reading_materials "+
		"(material_id, content_id, filename, source_type, creat_time, update_time, owner_id) "+
		"VALUES (?, ?, ?, ?, ?, ?, NULL)",
		"rm_nobody", "doc_c", "算法笔记.md", "library", atSecond(3), atSecond(3))
	if _, err := repo.Material(ctx, "rm_alice", dbCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("the administrator reading account 7's material = %v, want %v", err, ErrNotFound)
	}
	if _, err := repo.Material(ctx, "rm_nobody", dbCaller); err != nil {
		t.Errorf("the administrator reading a material with no owner = %v, want it found", err)
	}
	if _, err := repo.Material(ctx, "rm_nobody", alice); !errors.Is(err, ErrNotFound) {
		t.Errorf("account 7 reading a material with no owner = %v, want %v", err, ErrNotFound)
	}
}

// TestDBAMarkIsNarrowedThroughItsMaterial is the derived-table rule for one of the
// rows that carries no owner column of its own: a mark is reachable only through a
// material the caller may read, so the other account's mark is a mark that is not
// there.
func TestDBAMarkIsNarrowedThroughItsMaterial(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeOwnedMaterialRow(t, resource, "rm_alice", "doc_a", 7, atSecond(1))
	testdb.Exec(t, resource, "INSERT INTO reading_marks "+
		"(mark_id, material_id, kind, locator_json, revision, creat_time) VALUES (?, ?, ?, ?, ?, ?)",
		"rmk_1", "rm_alice", "bookmark",
		`{"material_id":"rm_alice","ordinal":0,"char_start":0,"char_end":0}`, 1, atSecond(1))

	bob := authz.Caller{UserID: 8, Role: authz.RoleUser}
	alice := authz.Caller{UserID: 7, Role: authz.RoleUser}
	ctx := context.Background()

	if _, err := repo.Mark(ctx, "rmk_1", bob); !errors.Is(err, ErrNotFound) {
		t.Errorf("account 8 reading a mark on account 7's material = %v, want %v", err, ErrNotFound)
	}
	if marks, err := repo.Marks(ctx, "rm_alice", bob); err != nil || len(marks) != 0 {
		t.Errorf("account 8 listing account 7's marks = %v/%v, want none", marks, err)
	}
	// The caller who owns the material reads it back, so the filter is a scope and
	// not a wall.
	if mark, err := repo.Mark(ctx, "rmk_1", alice); err != nil || mark.MarkID != "rmk_1" {
		t.Errorf("account 7 reading its own mark = %+v/%v, want the row", mark, err)
	}
}
