package usage

// The usage table, checked against a real seekdb.
//
// Everything here is a claim the Go code cannot make about itself: that the row
// written in two halves is one row, that a call made outside a conversation is
// stored as NULL rather than as an empty name (migration 00017), that the
// numbers in the columns and the numbers in usage_json are written from one
// value, and that the constraints still refuse what they were written to refuse
// — an outcome outside the three words, a usage value that is not an object, a
// second row under the same call id, and a call charged to an account that does
// not exist.
//
// Nothing here calls t.Parallel: one database per package, emptied at each test.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/llm"
)

// The times are written out rather than taken from a clock, because the columns
// are DATETIMEs of whole seconds and two calls made by a fast test would
// otherwise land in the same one.
var (
	atNine   = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	atNine30 = time.Date(2026, 10, 1, 9, 0, 30, 0, time.UTC)
	atTen    = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
)

func newDBStore(t *testing.T) (*GORMStore, *data.Resource, int64) {
	t.Helper()
	resource := testdb.Open(t, "usage")
	// The call rows first: they carry a foreign key to the account.
	testdb.Reset(t, resource, "llm_calls", "users")
	return NewGORMStore(resource.GORM()), resource, createAccount(t, resource, "alice")
}

// createAccount inserts the account a quota is summed for. The column carries a
// foreign key to `users`, so a test that recorded a call for nobody would be
// testing that constraint rather than the store.
func createAccount(t *testing.T, resource *data.Resource, username string) int64 {
	t.Helper()
	testdb.Exec(t, resource,
		"INSERT INTO users (username, password, learner_profile, extra) VALUES (?, 'hash', '{}', '{}')",
		username)
	var account struct {
		UserID int64 `gorm:"column:user_id"`
	}
	if err := resource.GORM().Raw("SELECT user_id FROM users WHERE username = ?", username).
		Scan(&account).Error; err != nil {
		t.Fatalf("read the account %q: %v", username, err)
	}
	return account.UserID
}

// storedCall is the table's answer to what is in it, read as columns rather than
// through the model: an assertion written against the model would agree with the
// model even where the table disagrees.
type storedCall struct {
	CallID           string     `gorm:"column:call_id"`
	StartedAt        time.Time  `gorm:"column:started_at"`
	SessionID        *string    `gorm:"column:session_id"`
	TurnID           *string    `gorm:"column:turn_id"`
	Source           string     `gorm:"column:source"`
	Usage            []byte     `gorm:"column:usage_json"`
	OwnerID          *int64     `gorm:"column:owner_id"`
	Provider         string     `gorm:"column:provider"`
	Model            string     `gorm:"column:model"`
	PromptTokens     int64      `gorm:"column:prompt_tokens"`
	CompletionTokens int64      `gorm:"column:completion_tokens"`
	TotalTokens      int64      `gorm:"column:total_tokens"`
	DurationMs       int64      `gorm:"column:duration_ms"`
	TTFTMs           int64      `gorm:"column:ttft_ms"`
	FinishedAt       *time.Time `gorm:"column:finished_at"`
	Outcome          string     `gorm:"column:outcome"`
}

func readCall(t *testing.T, resource *data.Resource, callID string) storedCall {
	t.Helper()
	var row storedCall
	err := resource.GORM().Raw(
		"SELECT call_id, started_at, session_id, turn_id, source, usage_json, owner_id, "+
			"provider, model, prompt_tokens, completion_tokens, total_tokens, duration_ms, "+
			"ttft_ms, finished_at, outcome FROM llm_calls WHERE call_id = ?", callID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the call %s: %v", callID, err)
	}
	if row.CallID == "" {
		t.Fatalf("there is no row for the call %s", callID)
	}
	return row
}

// openCall is what the recorder hands the store before a call: an account, a
// conversation, and which part of the product asked.
func openCall(callID string, ownerID int64) OpenCall {
	return OpenCall{
		CallID:    callID,
		Call:      Call{OwnerID: ownerID, SessionID: "ses_1", TurnID: "turn_1", Source: "chat"},
		Provider:  "deepseek",
		Model:     "deepseek-chat",
		StartedAt: atNine,
	}
}

func completed() Completion {
	return Completion{
		FinishedAt:   atNine30,
		Usage:        llm.Usage{PromptTokens: 120, CompletionTokens: 45, TotalTokens: 165},
		DurationMs:   30000,
		TTFTMs:       420,
		Outcome:      OutcomeOK,
		FinishReason: "stop",
	}
}

// The first half is a row that says a call is happening: everything about it
// that is known before it is made, and nothing about what it produced.
func TestDBTheOpenHalfIsARowThatSaysACallIsHappening(t *testing.T) {
	store, resource, owner := newDBStore(t)
	if err := store.Open(context.Background(), openCall("call_first", owner)); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	row := readCall(t, resource, "call_first")

	if row.Source != "chat" || row.Provider != "deepseek" || row.Model != "deepseek-chat" {
		t.Errorf("the row does not say where it came from: %+v", row)
	}
	if row.OwnerID == nil || *row.OwnerID != owner {
		t.Errorf("OwnerID = %v, want the account the call is charged to", row.OwnerID)
	}
	if !row.StartedAt.Equal(atNine) {
		t.Errorf("StartedAt = %s, want %s", row.StartedAt, atNine)
	}
	if row.FinishedAt != nil {
		t.Errorf("FinishedAt = %v, want NULL: the call has not come back yet", *row.FinishedAt)
	}
	if row.Outcome != "" {
		t.Errorf("Outcome = %q, want the empty string while the call is open", row.Outcome)
	}
	for _, count := range []int64{row.PromptTokens, row.CompletionTokens, row.TotalTokens,
		row.DurationMs, row.TTFTMs} {
		if count != 0 {
			t.Errorf("a count is %d on a call that has not finished: %+v", count, row)
		}
	}
	if string(row.Usage) == "" {
		t.Error("usage_json is empty: the column is NOT NULL and holds an object")
	}
}

// The second half fills in the same row. The identity written before the call is
// still what the row says afterwards: a completion that rewrote it would be a
// call that changed hands while it was being made.
func TestDBTheSecondHalfFillsTheSameRow(t *testing.T) {
	store, resource, owner := newDBStore(t)
	ctx := context.Background()
	if err := store.Open(ctx, openCall("call_second", owner)); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Complete(ctx, "call_second", completed()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	row := readCall(t, resource, "call_second")

	if row.FinishedAt == nil || !row.FinishedAt.Equal(atNine30) {
		t.Errorf("FinishedAt = %v, want %s", row.FinishedAt, atNine30)
	}
	if row.Outcome != string(OutcomeOK) {
		t.Errorf("Outcome = %q, want %q", row.Outcome, OutcomeOK)
	}
	if row.PromptTokens != 120 || row.CompletionTokens != 45 || row.TotalTokens != 165 {
		t.Errorf("the token columns are %d/%d/%d, want 120/45/165",
			row.PromptTokens, row.CompletionTokens, row.TotalTokens)
	}
	if row.DurationMs != 30000 || row.TTFTMs != 420 {
		t.Errorf("the timings are %d/%d, want 30000/420", row.DurationMs, row.TTFTMs)
	}
	if row.Source != "chat" || row.Provider != "deepseek" || row.Model != "deepseek-chat" {
		t.Errorf("completing a call changed where it came from: %+v", row)
	}
	if row.OwnerID == nil || *row.OwnerID != owner {
		t.Errorf("OwnerID = %v, want the account it was opened for", row.OwnerID)
	}
	if !row.StartedAt.Equal(atNine) {
		t.Errorf("StartedAt = %s, want the moment the call started", row.StartedAt)
	}
	if row.SessionID == nil || *row.SessionID != "ses_1" {
		t.Errorf("SessionID = %v, want the conversation", row.SessionID)
	}
}

// The numbers are in two places and are written from one value (§2.8). This is
// the discipline the migration asks for, and the only thing that can hold it is
// a test: a reader that goes to the columns would never notice the two drifting.
func TestDBTheJSONAgreesWithTheColumns(t *testing.T) {
	store, resource, owner := newDBStore(t)
	ctx := context.Background()
	done := completed()
	if err := store.Open(ctx, openCall("call_json", owner)); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Complete(ctx, "call_json", done); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	row := readCall(t, resource, "call_json")

	var said map[string]any
	if err := json.Unmarshal(row.Usage, &said); err != nil {
		t.Fatalf("usage_json is not readable as an object: %v (%s)", err, row.Usage)
	}
	numbers := map[string]int64{
		"prompt_tokens":     row.PromptTokens,
		"completion_tokens": row.CompletionTokens,
		"total_tokens":      row.TotalTokens,
		"duration_ms":       row.DurationMs,
		"ttft_ms":           row.TTFTMs,
	}
	for field, want := range numbers {
		got, ok := said[field].(float64)
		if !ok {
			t.Errorf("usage_json does not carry %s: %s", field, row.Usage)
			continue
		}
		if int64(got) != want {
			t.Errorf("usage_json says %s = %d and the column says %d", field, int64(got), want)
		}
	}
	// The one thing the columns do not carry, and the reason the column is kept
	// at all: what the provider said about why it stopped.
	if said["finish_reason"] != done.FinishReason {
		t.Errorf("usage_json finish_reason = %v, want %q", said["finish_reason"], done.FinishReason)
	}
}

// A call made outside a conversation has no session and no turn, and the row
// says so rather than holding a blank. This is migration 00017's whole contract,
// and an empty string would satisfy a reader and quietly mislead a count.
func TestDBAConversationlessCallIsStoredAsNULL(t *testing.T) {
	store, resource, owner := newDBStore(t)
	open := openCall("call_short", owner)
	open.Call = Call{OwnerID: owner, Source: "practice.judge"}
	if err := store.Open(context.Background(), open); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	row := readCall(t, resource, "call_short")

	if row.SessionID != nil {
		t.Errorf("SessionID = %q, want NULL: this call belongs to no conversation", *row.SessionID)
	}
	if row.TurnID != nil {
		t.Errorf("TurnID = %q, want NULL", *row.TurnID)
	}
	if row.Source != "practice.judge" {
		t.Errorf("Source = %q, want the part of the product that asked", row.Source)
	}
	if row.OwnerID == nil || *row.OwnerID != owner {
		t.Errorf("OwnerID = %v, want the account it is charged to", row.OwnerID)
	}
}

// The account column is a foreign key, and this is the constraint the recorder
// is written against: a call charged to an account that does not exist is not a
// row any quota can be summed over.
func TestDBAWriteForAnAccountThatIsNotThereIsRefused(t *testing.T) {
	store, _, _ := newDBStore(t)
	open := openCall("call_ghost", 9999)

	err := store.Open(context.Background(), open)

	if err == nil {
		t.Fatal("the call was recorded for an account that does not exist")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") &&
		!strings.Contains(strings.ToLower(err.Error()), "constraint") {
		t.Errorf("error = %v, want the foreign key refusing it", err)
	}
}

// call_id is the primary key, which is what makes §17.3's "deduplicate by call
// id" free: there is nothing to deduplicate against an id that cannot repeat.
func TestDBOneCallIDIsOneRow(t *testing.T) {
	store, resource, owner := newDBStore(t)
	ctx := context.Background()
	if err := store.Open(ctx, openCall("call_twice", owner)); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	second := openCall("call_twice", owner)
	second.Model = "a-different-model"

	if err := store.Open(ctx, second); err == nil {
		t.Fatal("a second row was written under a call id that was taken")
	}
	row := readCall(t, resource, "call_twice")
	if row.Model != "deepseek-chat" {
		t.Errorf("Model = %q: the refused write changed the row that was there", row.Model)
	}
}

// The three words are the column's, not the Go code's. A writer that invented a
// fourth would be refused here, which is the point of writing the vocabulary as
// a constraint.
func TestDBAnOutcomeNobodyCanCountIsRefused(t *testing.T) {
	_, resource, owner := newDBStore(t)
	err := testdb.Try(resource,
		"INSERT INTO llm_calls (call_id, started_at, source, usage_json, owner_id, outcome) "+
			"VALUES ('call_maybe', ?, 'chat', '{}', ?, 'maybe')",
		atNine, owner)
	if err == nil {
		t.Error("an outcome outside the three words was written")
	}
}

// usage_json holds an object, by constraint (00005). An empty row writes `{}`
// and a completed one writes the numbers; neither is a list or a string.
func TestDBTheUsageColumnHasToBeAnObject(t *testing.T) {
	_, resource, owner := newDBStore(t)
	testdb.Exec(t, resource,
		"INSERT INTO llm_calls (call_id, started_at, source, usage_json, owner_id) "+
			"VALUES ('call_list', ?, 'chat', '{}', ?)",
		atNine, owner)

	err := testdb.Try(resource,
		"UPDATE llm_calls SET usage_json = '[]' WHERE call_id = 'call_list'")
	if err == nil {
		t.Error("usage_json was allowed to hold something that is not an object")
	}
}

// "This call did not come back" is a question the table answers, and the answer
// is a query rather than a missing row. It is the state the two-part write
// exists to produce (§2.7).
func TestDBAnUnfinishedCallIsTheOneWithNoFinish(t *testing.T) {
	store, resource, owner := newDBStore(t)
	ctx := context.Background()
	if err := store.Open(ctx, openCall("call_finished", owner)); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.Complete(ctx, "call_finished", completed()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := store.Open(ctx, openCall("call_stuck", owner)); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	var open []string
	err := resource.GORM().Raw(
		"SELECT call_id FROM llm_calls WHERE finished_at IS NULL").Scan(&open).Error
	if err != nil {
		t.Fatalf("read the calls that did not come back: %v", err)
	}
	if len(open) != 1 || open[0] != "call_stuck" {
		t.Errorf("the unfinished calls are %v, want just the one that was never completed", open)
	}
}

// Completing a row that is not there is reported. The recorder logs it and lets
// the answer through, so this is the branch that turns a lost record into
// something an operator can see.
func TestDBCompletingARowThatIsNotThereIsAFailure(t *testing.T) {
	store, _, _ := newDBStore(t)

	err := store.Complete(context.Background(), "call_nobody", completed())

	if err == nil {
		t.Fatal("completing a call with no row reported success")
	}
	if !strings.Contains(err.Error(), "call_nobody") {
		t.Errorf("error = %v, want the call it is about", err)
	}
}

// A store with no handle is a wiring mistake, and the one thing it must not do
// is answer that a call was recorded.
func TestAnUnwiredStoreRefusesRatherThanReportingSuccess(t *testing.T) {
	var store *GORMStore
	ctx := context.Background()

	if err := store.Open(ctx, openCall("call_any", 1)); err == nil {
		t.Error("an unwired store reported that it opened a call")
	}
	if err := store.Complete(ctx, "call_any", completed()); err == nil {
		t.Error("an unwired store reported that it completed a call")
	}
}
