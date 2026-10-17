package persona

// The storage and service contract, run against a real seekdb.
//
// The claims here are the ones a double cannot make: the owner column is what
// scopes every read (there is no administrator exception), the unique key turns
// a second persona with the same name into ErrNameTaken, the check constraints
// refuse a blank row, and deleting the account takes its personas with it.
//
// The database is the test one named after the configured database; see
// data/testdb. Nothing here calls t.Parallel, because one package shares one
// database.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
)

const (
	dbOwnerID int64 = 7201
	dbOtherID int64 = 7202
)

func testCaller(userID int64) authz.Caller {
	return authz.Caller{UserID: userID}
}

func newDBService(t *testing.T) (*Service, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "persona")
	testdb.Reset(t, resource, "personas", "users")
	testdb.Account(t, resource, dbOwnerID, "persona_owner")
	testdb.Account(t, resource, dbOtherID, "persona_other")

	service, err := NewService(NewGORMRepository(resource.GORM()))
	if err != nil {
		t.Fatalf("build the persona service: %v", err)
	}
	return service, resource
}

func at(second int) time.Time {
	return time.Date(2026, 10, 17, 9, 0, second, 0, time.UTC)
}

func createPersona(t *testing.T, service *Service, caller authz.Caller, input Input) Persona {
	t.Helper()
	created, err := service.Create(context.Background(), input, caller)
	if err != nil {
		t.Fatalf("create persona %q: %v", input.Name, err)
	}
	return created
}

func listPersonas(t *testing.T, service *Service, caller authz.Caller) []string {
	t.Helper()
	found, err := service.List(context.Background(), caller)
	if err != nil {
		t.Fatalf("list personas: %v", err)
	}
	names := make([]string, 0, len(found))
	for _, record := range found {
		names = append(names, record.Name)
	}
	return names
}

func countPersonas(t *testing.T, resource *data.Resource) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Raw("SELECT COUNT(*) FROM personas").Scan(&count).Error; err != nil {
		t.Fatalf("count personas: %v", err)
	}
	return count
}

func TestDBPersonasBelongToOneAccount(t *testing.T) {
	service, resource := newDBService(t)
	owner := testCaller(dbOwnerID)
	other := testCaller(dbOtherID)

	mine := createPersona(t, service, owner, Input{
		Name: "耐心的物理老师", Description: "解释概念", Content: "用类比讲清楚。",
	})
	theirs := createPersona(t, service, other, Input{
		Name: "耐心的物理老师", Description: "同名但不同账号", Content: "这是另一个人的。",
	})
	if !strings.HasPrefix(mine.ID, "pers_") {
		t.Errorf("the persona id is %q, want the pers_ prefix", mine.ID)
	}
	if mine.OwnerID != dbOwnerID || theirs.OwnerID != dbOtherID {
		t.Errorf("owners are %d and %d, want %d and %d",
			mine.OwnerID, theirs.OwnerID, dbOwnerID, dbOtherID)
	}

	// Same name, two accounts: the unique key is per owner, not global.
	if got := listPersonas(t, service, owner); !slices.Equal(got, []string{"耐心的物理老师"}) {
		t.Errorf("the owner's listing is %v", got)
	}
	if got := listPersonas(t, service, other); !slices.Equal(got, []string{"耐心的物理老师"}) {
		t.Errorf("the other account's listing is %v", got)
	}

	// Every read is scoped. A row that belongs to somebody else is not found,
	// never "found but hidden":
	if _, err := service.Get(context.Background(), theirs.ID, owner); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading another account's persona: %v, want %v", err, ErrNotFound)
	}
	admin := authz.Caller{UserID: dbOtherID, Role: authz.RoleAdmin}
	if _, err := service.Get(context.Background(), mine.ID, admin); !errors.Is(err, ErrNotFound) {
		t.Errorf("an administrator reading somebody else's persona: %v, want %v", err, ErrNotFound)
	}
	if _, err := service.Update(context.Background(), mine.ID, Input{
		Name: "改个名字", Content: "改掉它。",
	}, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("updating another account's persona: %v, want %v", err, ErrNotFound)
	}
	if err := service.Delete(context.Background(), mine.ID, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting another account's persona: %v, want %v", err, ErrNotFound)
	}
	if got := countPersonas(t, resource); got != 2 {
		t.Errorf("%d personas are stored, want both refusals to have changed nothing", got)
	}

	if err := service.Delete(context.Background(), mine.ID, owner); err != nil {
		t.Fatalf("delete one's own persona: %v", err)
	}
	if got := listPersonas(t, service, owner); len(got) != 0 {
		t.Errorf("after the delete the listing is %v, want empty", got)
	}
	if got := listPersonas(t, service, other); !slices.Equal(got, []string{"耐心的物理老师"}) {
		t.Errorf("deleting one account's persona changed another's: %v", got)
	}
}

func TestDBASecondNameForOneAccountIsRefused(t *testing.T) {
	service, resource := newDBService(t)
	owner := testCaller(dbOwnerID)

	first := createPersona(t, service, owner, Input{Name: "老师", Content: "第一版。"})

	_, err := service.Create(context.Background(), Input{Name: "老师", Content: "第二版。"}, owner)
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("a second persona with the same name: %v, want %v", err, ErrNameTaken)
	}

	// The same rule holds on update, where it is a write of an existing row.
	second := createPersona(t, service, owner, Input{Name: "编辑", Content: "改稿。"})
	_, err = service.Update(context.Background(), second.ID, Input{
		Name: "老师", Description: "想改成重名", Content: "内容。",
	}, owner)
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("renaming a persona onto a name the account uses: %v, want %v", err, ErrNameTaken)
	}

	// The refused update left the row as it was.
	stored, err := service.Get(context.Background(), second.ID, owner)
	if err != nil {
		t.Fatalf("read the persona back: %v", err)
	}
	if stored.Name != "编辑" {
		t.Errorf("the refused update wrote %q, want the old name", stored.Name)
	}
	if got := countPersonas(t, resource); got != 2 {
		t.Errorf("%d personas are stored, want the refused create not to have written a row", got)
	}
	if stored.ID == first.ID {
		t.Errorf("the two personas share the id %q", stored.ID)
	}
}

func TestDBTheListingIsByNameThenID(t *testing.T) {
	service, _ := newDBService(t)
	owner := testCaller(dbOwnerID)

	created := map[string]Persona{}
	for _, name := range []string{"zeta", "alpha", "mu"} {
		created[name] = createPersona(t, service, owner, Input{Name: name, Content: "内容。"})
	}

	want := []string{"alpha", "mu", "zeta"}
	if got := listPersonas(t, service, owner); !slices.Equal(got, want) {
		t.Errorf("the listing is %v, want %v", got, want)
	}

	// Renaming moves the row, because the listing is by name rather than by when
	// it was written.
	updated, err := service.Update(context.Background(), created["zeta"].ID, Input{
		Name: "beta", Content: "内容。",
	}, owner)
	if err != nil {
		t.Fatalf("rename a persona: %v", err)
	}
	if updated.Name != "beta" {
		t.Errorf("the renamed persona is %q", updated.Name)
	}
	want = []string{"alpha", "beta", "mu"}
	if got := listPersonas(t, service, owner); !slices.Equal(got, want) {
		t.Errorf("after the rename the listing is %v, want %v", got, want)
	}
}

func TestDBUpdateReplacesTheWholeRecord(t *testing.T) {
	service, _ := newDBService(t)
	owner := testCaller(dbOwnerID)
	service.now = func() time.Time { return at(1) }

	created := createPersona(t, service, owner, Input{
		Name: "旧名字", Description: "旧说明", Content: "旧提示词",
	})
	if !created.CreatedAt.Equal(at(1)) || !created.UpdatedAt.Equal(at(1)) {
		t.Fatalf("the created record carries %+v", created)
	}

	service.now = func() time.Time { return at(9) }
	updated, err := service.Update(context.Background(), created.ID, Input{
		Name: "新名字", Description: "", Content: "新提示词",
	}, owner)
	if err != nil {
		t.Fatalf("update the persona: %v", err)
	}
	if updated.Name != "新名字" || updated.Description != "" || updated.Content != "新提示词" {
		t.Errorf("the updated record is %+v, want every editable field replaced", updated)
	}
	if !updated.CreatedAt.Equal(at(1)) || !updated.UpdatedAt.Equal(at(9)) {
		t.Errorf("the updated record carries %+v, want created kept and updated moved", updated)
	}

	stored, err := service.Get(context.Background(), created.ID, owner)
	if err != nil {
		t.Fatalf("read the updated persona: %v", err)
	}
	if stored.Name != "新名字" || stored.Description != "" || stored.Content != "新提示词" {
		t.Errorf("what was stored is %+v", stored)
	}
}

func TestDBInstructionResolvesOnlyTheCallersPersona(t *testing.T) {
	service, _ := newDBService(t)
	owner := testCaller(dbOwnerID)

	created := createPersona(t, service, owner, Input{
		Name: "老师", Content: "先用一句话说明直觉，再给公式。",
	})

	text, found, err := service.Instruction(context.Background(), created.ID, owner)
	if err != nil || !found || text != "先用一句话说明直觉，再给公式。" {
		t.Fatalf("resolving one's own persona: (%q, %v, %v)", text, found, err)
	}

	// An id naming nothing is not an error: the runtime turns "not found" into a
	// refused turn, and storage failures into a failure it cannot fix.
	for _, test := range []struct {
		name string
		id   string
		who  authz.Caller
	}{
		{"another account's persona", created.ID, testCaller(dbOtherID)},
		{"an id that is not stored", "pers_missing", owner},
		{"no id at all", "   ", owner},
	} {
		t.Run(test.name, func(t *testing.T) {
			text, found, err := service.Instruction(context.Background(), test.id, test.who)
			if err != nil || found || text != "" {
				t.Fatalf("got (%q, %v, %v), want (\"\", false, nil)", text, found, err)
			}
		})
	}
}

func TestDBTheSchemaRefusesWhatTheServiceReliesOn(t *testing.T) {
	service, resource := newDBService(t)
	owner := testCaller(dbOwnerID)
	created := createPersona(t, service, owner, Input{Name: "老师", Content: "内容。"})

	// A row the service would never write is still refused by the schema: the
	// service is not the only thing that can write to this table.
	cases := []struct {
		name   string
		query  string
		args   []any
		reason string
	}{
		{
			"blank name",
			"INSERT INTO personas (persona_id, owner_id, name, description, content) VALUES (?, ?, ?, '', ?)",
			[]any{"pers_blank_name", dbOwnerID, "   ", "内容"},
			"the name CHECK",
		},
		{
			"blank content",
			"INSERT INTO personas (persona_id, owner_id, name, description, content) VALUES (?, ?, ?, '', ?)",
			[]any{"pers_blank_content", dbOwnerID, "另一个名字", "  "},
			"the content CHECK",
		},
		{
			"owner that is not an account",
			"INSERT INTO personas (persona_id, owner_id, name, description, content) VALUES (?, ?, ?, '', ?)",
			[]any{"pers_no_owner", int64(999999), "悬空的人格", "内容"},
			"the owner foreign key",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := testdb.Try(resource, test.query, test.args...); err == nil {
				t.Fatalf("the database accepted a row refused by %s", test.reason)
			}
		})
	}

	// Deleting the account takes its personas with it. The alternative — an
	// orphan row nobody can read or delete — is worse than losing the prompt.
	testdb.Exec(t, resource, "DELETE FROM users WHERE user_id = ?", dbOwnerID)
	if _, err := service.Get(context.Background(), created.ID, owner); !errors.Is(err, ErrNotFound) {
		t.Errorf("a persona survived its owner: %v", err)
	}
	if got := countPersonas(t, resource); got != 0 {
		t.Errorf("%d personas are stored after the owner was deleted", got)
	}
}
