package reading

// Taking a material back out, checked against a real seekdb.
//
// The method is one statement, and everything interesting about it is in the
// schema rather than in the statement: which rows follow a material out and which
// merely stop pointing at it. That difference is a handful of ON DELETE clauses
// spread across 00004 and 00012, and no double can say what they do — a fake can
// only restate what somebody believed they said.
//
// The method has one caller, a workspace import that could not finish (§2.13),
// which is why these cases are about the rows rather than about a permission or
// a route.

import (
	"context"
	"errors"
	"testing"

	"backend/internal/authz"
	"backend/internal/data/testdb"
)

func TestDBDeletingAMaterialTakesTheRowsThatHangOffIt(t *testing.T) {
	repo, resource := newDBRepository(t)
	// Two materials, because a pairing has two ends and the case for it is that
	// deleting either end takes the pairing away.
	storeMaterialRow(t, resource, "mat_1", "doc_1", atSecond(0), ptr(atSecond(1)))
	storeMaterialRow(t, resource, "mat_2", "doc_2", atSecond(0), ptr(atSecond(1)))
	storeSessionRow(t, resource, "ses_1", atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_workspaces "+
		"(workspace_id, title, creat_time) VALUES (?, ?, ?)", "ws_1", "reading", atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_positions "+
		"(material_id, session_id, locator_json, revision, creat_time) VALUES (?, ?, ?, ?, ?)",
		"mat_1", "ses_1", `{"start":0,"end":10}`, 1, atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_marks "+
		"(mark_id, material_id, session_id, kind, locator_json, body, revision, creat_time) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		"mark_1", "mat_1", "ses_1", "note", `{"start":0,"end":10}`, "worth rereading", 1, atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_workspace_materials "+
		"(workspace_id, material_id, creat_time) VALUES (?, ?, ?)", "ws_1", "mat_1", atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_pairings "+
		"(pairing_id, primary_material_id, counterpart_material_id, primary_language, "+
		"counterpart_language, creat_time) VALUES (?, ?, ?, ?, ?, ?)",
		"pair_1", "mat_1", "mat_2", "zh", "en", atSecond(0))

	if err := repo.DeleteMaterial(context.Background(), "mat_1", dbCaller); err != nil {
		t.Fatalf("DeleteMaterial() error = %v", err)
	}
	for _, table := range []string{"reading_positions", "reading_marks", "reading_pairings", "reading_workspace_materials"} {
		if rows := countRows(t, resource, table); rows != 0 {
			t.Errorf("%s still holds %d rows, want the material's rows gone with it", table, rows)
		}
	}
	if rows := countRows(t, resource, "reading_materials"); rows != 1 {
		t.Errorf("the catalogue holds %d rows, want the other material left", rows)
	}
	// The conversation is not the material's to take away: deleting a book does
	// not delete the chat it was read in.
	if rows := countRows(t, resource, "sessions"); rows != 1 {
		t.Errorf("the conversation went with the material, and it was not its to take")
	}
}

// TestDBDeletingAMaterialLeavesTheRowsThatOnlyPointAtIt covers the other half of
// the same foreign keys: a workspace whose active tab was the material keeps its
// row and loses the pointer.
func TestDBDeletingAMaterialLeavesTheRowsThatOnlyPointAtIt(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "mat_1", "doc_1", atSecond(0), nil)
	storeSessionRow(t, resource, "ses_1", atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_workspaces "+
		"(workspace_id, active_material_id, title, creat_time) VALUES (?, ?, ?, ?)",
		"ws_1", "mat_1", "reading", atSecond(0))
	testdb.Exec(t, resource, "INSERT INTO reading_workspace_sessions "+
		"(workspace_id, session_id, active_material_id, creat_time) VALUES (?, ?, ?, ?)",
		"ws_1", "ses_1", "mat_1", atSecond(0))

	if err := repo.DeleteMaterial(context.Background(), "mat_1", dbCaller); err != nil {
		t.Fatalf("DeleteMaterial() error = %v", err)
	}
	var active *string
	if err := resource.GORM().Raw("SELECT active_material_id FROM reading_workspaces WHERE workspace_id = ?", "ws_1").
		Scan(&active).Error; err != nil {
		t.Fatalf("read the workspace: %v", err)
	}
	if active != nil {
		t.Errorf("the workspace still points at %q, want the pointer dropped", *active)
	}
	if rows := countRows(t, resource, "reading_workspace_sessions"); rows != 1 {
		t.Errorf("the workspace session went with the material: %d rows", rows)
	}
}

func TestDBDeletingAnotherAccountsMaterialFindsNothing(t *testing.T) {
	repo, resource := newDBRepository(t)
	// A row owned by account 9, read as the administrator whose scope is
	// `owner_id = ? OR owner_id IS NULL`: another account's row is absent rather
	// than refused, which is AUTH-06's answer.
	storeOwnedMaterialRow(t, resource, "mat_theirs", "doc_1", 9, atSecond(0))

	err := repo.DeleteMaterial(context.Background(), "mat_theirs", dbCaller)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteMaterial() error = %v, want ErrNotFound", err)
	}
	if rows := countRows(t, resource, "reading_materials"); rows != 1 {
		t.Fatalf("another account's material was deleted anyway: %d rows left", rows)
	}
	// The owner's own delete is the same statement with a different scope, and it
	// is what says the refusal above was about the scope rather than about the
	// statement being wrong.
	if err := repo.DeleteMaterial(context.Background(), "mat_theirs", authz.Caller{UserID: 9, Role: authz.RoleUser}); err != nil {
		t.Fatalf("the owner's DeleteMaterial() error = %v", err)
	}
	if rows := countRows(t, resource, "reading_materials"); rows != 0 {
		t.Errorf("the owner's delete left %d rows", rows)
	}
}

func TestDBDeletingAMaterialThatIsNotThereIsNotFound(t *testing.T) {
	repo, resource := newDBRepository(t)
	if err := repo.DeleteMaterial(context.Background(), "mat_ghost", dbCaller); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteMaterial() error = %v, want ErrNotFound", err)
	}
	// Deleting twice is the same answer rather than a second success: a recovery
	// that ran twice must not be able to claim it removed two of something.
	storeMaterialRow(t, resource, "mat_1", "doc_1", atSecond(0), nil)
	if err := repo.DeleteMaterial(context.Background(), "mat_1", dbCaller); err != nil {
		t.Fatalf("DeleteMaterial() error = %v", err)
	}
	if err := repo.DeleteMaterial(context.Background(), "mat_1", dbCaller); !errors.Is(err, ErrNotFound) {
		t.Errorf("the second DeleteMaterial() error = %v, want ErrNotFound", err)
	}
}
