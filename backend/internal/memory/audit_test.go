package memory

// The audit trail's shape, which is the only place append-only can be checked.
//
// MEM-02 asks for a record of every action, and "a record" is not a table
// property — this engine has no way to forbid an UPDATE. What it does have is the
// interface: a caller is handed a repository, and if the repository cannot
// express "change this line", no line gets changed. So the guarantee is a claim
// about a Go type, and it is checked by reading that type rather than by trying
// to write and watching it fail.

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestTheAuditTrailCannotBeChanged reads the interface's method set and asserts
// that everything it says about the audit trail is an append or a read.
//
// It is deliberately a test about names and not about behaviour: a method that
// updated an audit row would be a new name on this interface, and the point is
// that adding one is not a quiet thing to do. A test that called the methods
// instead could only ever show that the four methods it knows about behave; this
// one fails the moment a fifth appears, whatever it does.
func TestTheAuditTrailCannotBeChanged(t *testing.T) {
	shape := reflect.TypeOf((*Repository)(nil)).Elem()

	var overTheTrail []string
	for index := 0; index < shape.NumMethod(); index++ {
		name := shape.Method(index).Name
		// AppendAudit and Audits are the two, and they are matched by the word
		// rather than by a list so that a method that named the table some other
		// way — PurgeAudit, RewriteAudit — still turns up here.
		if strings.Contains(strings.ToLower(name), "audit") {
			overTheTrail = append(overTheTrail, name)
		}
	}
	sort.Strings(overTheTrail)

	want := []string{"AppendAudit", "Audits"}
	if !reflect.DeepEqual(overTheTrail, want) {
		t.Errorf("the repository says %v about the audit trail, want %v: an append to write a line and a read to list them, and nothing that could change one",
			overTheTrail, want)
	}
}

// TestTheWordsForAnActionAreAllOfThem checks that the vocabulary the audit trail
// records is exactly the actions §2.14 exposes, and that no word for deleting is
// among them.
//
// Every route that changes memory writes one of these, so a route added without a
// word would be a route that changes memory and leaves no record. The check runs
// in both directions: a word with no route means something writes to the trail
// that nothing asked for.
func TestTheWordsForAnActionAreAllOfThem(t *testing.T) {
	// The routes that act on memory (§2.14). `GET /memory/audit` itself is not
	// here: reading the trail is not an action on memory.
	asked := map[Action]string{
		ActionFactStated:     "POST /memory/facts",
		ActionFactEdited:     "PATCH /memory/facts/:factID",
		ActionFactRetracted:  "POST /memory/facts/:factID/retract",
		ActionFactRestored:   "POST /memory/facts/:factID/restore",
		ActionFactMerged:     "POST /memory/facts/dedupe",
		ActionFactPurged:     "POST /memory/cleanup",
		ActionMemoryExported: "GET /memory/export",
	}
	for action, route := range asked {
		if !action.Valid() {
			t.Errorf("%s has no word in memory_audit", route)
		}
	}
	if len(asked) != 7 {
		t.Fatalf("the routes table lists %d actions, want the seven §2.14 has", len(asked))
	}
	// The one word that must never exist. §2.14 says a single-row DELETE is not
	// offered on purpose, and MEM-02's three verbs are 撤销 / 取消 / 清理.
	for _, word := range []Action{"fact.deleted", "fact.removed", "fact.purged_all"} {
		if word.Valid() {
			t.Errorf("the audit vocabulary contains %q", word)
		}
	}
}

func TestAnAuditRowSaysWhatItIsAbout(t *testing.T) {
	good := Audit{
		ID: "ma_1", OwnerID: 7, Actor: ActorUser, Action: ActionFactRetracted,
		TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone,
		Detail:    []byte(`{"why":"the user asked"}`),
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("a well-formed audit row was refused: %v", err)
	}
	// An action about the account as a whole names no row, and that is the reason
	// TargetID is nullable rather than the reason an export goes unrecorded.
	broad := good
	broad.TargetKind, broad.TargetID, broad.Action = TargetMemory, "", ActionMemoryExported
	if err := broad.Validate(); err != nil {
		t.Errorf("an export was refused for naming no single fact: %v", err)
	}

	for _, test := range []struct {
		name   string
		change func(Audit) Audit
	}{
		{"no id", func(a Audit) Audit { a.ID = "  "; return a }},
		{"no owner", func(a Audit) Audit { a.OwnerID = 0; return a }},
		{"no actor", func(a Audit) Audit { a.Actor = ""; return a }},
		{"a word that is not an action", func(a Audit) Audit { a.Action = "fact.deleted"; return a }},
		{"a target that is not one", func(a Audit) Audit { a.TargetKind = "turn"; return a }},
		{"a result that is not one", func(a Audit) Audit { a.Result = "failed"; return a }},
		{"detail that is not JSON", func(a Audit) Audit { a.Detail = []byte("not json"); return a }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.change(good).Validate(); err == nil {
				t.Error("Validate() accepted an audit row that cannot be read back")
			}
		})
	}
}

func TestTheTrailIsReadNewestFirstAndByOneAccount(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repository.audits = []Audit{
		{ID: "ma_1", OwnerID: 7, Actor: ActorUser, Action: ActionFactStated,
			TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: at},
		{ID: "ma_2", OwnerID: 7, Actor: ActorUser, Action: ActionFactRetracted,
			TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: at.Add(time.Second)},
		{ID: "ma_3", OwnerID: 8, Actor: ActorUser, Action: ActionFactStated,
			TargetKind: TargetFact, TargetID: "mf_9", Result: ResultDone, CreatedAt: at.Add(2 * time.Second)},
	}

	read, err := service.Audits(context.Background(), AuditQuery{OwnerID: 7})
	if err != nil {
		t.Fatalf("Audits() error = %v", err)
	}
	if len(read) != 2 || read[0].ID != "ma_2" || read[1].ID != "ma_1" {
		t.Errorf("Audits() = %+v, want account 7's two lines newest first", read)
	}

	filtered, err := service.Audits(context.Background(), AuditQuery{OwnerID: 7, Action: ActionFactStated})
	if err != nil {
		t.Fatalf("Audits() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "ma_1" {
		t.Errorf("Audits() filtered = %+v, want the one statement", filtered)
	}

	if _, err := service.Audits(context.Background(), AuditQuery{OwnerID: 7, Action: "fact.deleted"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Audits() filtered by a word that is not an action = %v, want a refusal", err)
	}
	if _, err := service.Audits(context.Background(), AuditQuery{OwnerID: 7, Limit: -1}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Audits() with a negative limit = %v, want a refusal", err)
	}
	if _, err := service.Audits(context.Background(), AuditQuery{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Audits() with no owner = %v, want a refusal", err)
	}
}
