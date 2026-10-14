package models

// The governance tables' mapping, and — more to the point — the shapes this
// mapping refuses to have.
//
// Two of them are worth a test rather than only a comment. An audit event has no
// column that could hold a conversation or a prompt, which is what makes "we do
// not store prompt text" a property of the schema; and the grants table keys on
// an empty string rather than NULL for "the whole kind", because NULL repeats in
// a unique index and the decision for a whole kind would then be several rows.

import (
	"reflect"
	"testing"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

func TestUserGrantModelMapping(t *testing.T) {
	parsed, err := schema.Parse(&UserGrant{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if parsed.Table != "user_grants" {
		t.Errorf("table = %q, want user_grants", parsed.Table)
	}
	if got := primaryDBNames(parsed); !reflect.DeepEqual(got, []string{"grant_id"}) {
		t.Errorf("primary fields = %v, want [grant_id]", got)
	}
	want := []string{
		"grant_id", "user_id", "item_kind", "item_id", "allowed",
		"granted_by", "creat_time", "update_time",
	}
	if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(want)) {
		t.Errorf("columns = %v, want %v", got, sortedCopy(want))
	}
	// The whole-kind decision is the empty string, and a Go zero value is that
	// same empty string: a row written without an item id means every item of
	// its kind, which is the answer the column's default gives too.
	if grant := (UserGrant{}); grant.ItemID != "" {
		t.Errorf("the zero ItemID is %q, want the empty string the column defaults to", grant.ItemID)
	}
	if reflect.TypeOf(UserGrant{}.GrantedBy) != reflect.TypeOf((*int64)(nil)) {
		t.Errorf("GrantedBy type = %T, want *int64: an anonymous grant is not a grant by account zero",
			UserGrant{}.GrantedBy)
	}
}

func TestGuardianLinkModelMapping(t *testing.T) {
	parsed, err := schema.Parse(&GuardianLink{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if parsed.Table != "guardian_links" {
		t.Errorf("table = %q, want guardian_links", parsed.Table)
	}
	if got := primaryDBNames(parsed); !reflect.DeepEqual(got, []string{"link_id"}) {
		t.Errorf("primary fields = %v, want [link_id]", got)
	}
	want := []string{"link_id", "guardian_id", "learner_id", "created_by", "creat_time"}
	if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(want)) {
		t.Errorf("columns = %v, want %v", got, sortedCopy(want))
	}
}

func TestGuardianPermissionKeysOnThePair(t *testing.T) {
	// Both columns are the key: a permission without its link names nothing, and
	// a link without its permission is not what the table stores. That is why
	// there is no separate permission_id — the row's existence is the permission
	// (§2.3), and a surrogate key would let two rows claim the same one.
	parsed, err := schema.Parse(&GuardianPermission{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if parsed.Table != "guardian_permissions" {
		t.Errorf("table = %q, want guardian_permissions", parsed.Table)
	}
	if got := primaryDBNames(parsed); !reflect.DeepEqual(got, []string{"link_id", "permission"}) {
		t.Errorf("primary fields = %v, want [link_id permission]", got)
	}
}

func TestAuditEventHasNoColumnForWhatWasSaid(t *testing.T) {
	parsed, err := schema.Parse(&AuditEvent{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if parsed.Table != "audit_events" {
		t.Errorf("table = %q, want audit_events", parsed.Table)
	}
	want := []string{
		"audit_id", "actor_id", "actor_name", "actor_role", "action",
		"target_kind", "target_id", "subject_id", "result", "detail_json",
		"request_id", "creat_time",
	}
	if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(want)) {
		t.Errorf("columns = %v, want %v", got, sortedCopy(want))
	}
	// The claim §2.6 makes is about absence, so it is checked as absence: no
	// column of this table is named for content, and the one JSON column is
	// documented as ids and counts. A future field called `prompt` or `content`
	// would fail here rather than be noticed in a review.
	for name := range parsed.FieldsByDBName {
		switch name {
		case "prompt", "prompt_text", "content", "body", "message", "request_body", "response_body":
			t.Errorf("audit_events has a %q column: an audit is a pointer, not a copy", name)
		}
	}
	if field := parsed.FieldsByDBName["detail_json"]; field == nil || field.TagSettings["TYPE"] != "json" {
		t.Errorf("detail_json field missing or does not declare JSON storage")
	}
	if reflect.TypeOf(AuditEvent{}.Detail) != reflect.TypeOf(datatypes.JSON{}) {
		t.Errorf("Detail type = %T, want datatypes.JSON", AuditEvent{}.Detail)
	}
}

func TestWorkspaceJobModelMapping(t *testing.T) {
	parsed, err := schema.Parse(&WorkspaceJob{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if parsed.Table != "workspace_jobs" {
		t.Errorf("table = %q, want workspace_jobs", parsed.Table)
	}
	want := []string{
		"job_id", "kind", "state", "owner_id", "archive_path", "manifest_json",
		"written_json", "counts_json", "failure_code", "creat_time", "update_time",
	}
	if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(want)) {
		t.Errorf("columns = %v, want %v", got, sortedCopy(want))
	}
	// A job whose owner is gone is a job an administrator still has to be able to
	// see finish; the column is nullable and its foreign key is SET NULL for that
	// reason.
	if reflect.TypeOf(WorkspaceJob{}.OwnerID) != reflect.TypeOf((*int64)(nil)) {
		t.Errorf("OwnerID type = %T, want *int64", WorkspaceJob{}.OwnerID)
	}
}
