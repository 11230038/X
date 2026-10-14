package handler

// What the memory layer looks like on the wire.
//
// These cases read the response as raw JSON rather than as typed values, because
// the questions here are the ones a typed decode cannot answer: whether a field
// is absent or empty, whether a JSON column arrived as an object or as a
// base64-encoded string, and whether an empty section is an empty list or a
// missing one. A client finds out about each of them in production, which is why
// they are pinned here rather than left to a reading of the converters.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/memory"
)

// memoryJSON reads a success envelope's data as raw JSON.
func memoryJSON(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	return envelope.Data
}

// anObservation is one thing a surface said, with a payload of its own.
func anObservation(id string) memory.Trace {
	return memory.Trace{
		ID: id, OwnerID: 7, Surface: memory.SurfaceMastery, Kind: "goal",
		Statement: "我在学 Go", Fingerprint: "b", Payload: []byte(`{"confidence":81}`),
		CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
	}
}

func TestAFactViewSaysWhenItChangedOnlyIfItDid(t *testing.T) {
	stub := &memoryStub{held: memory.FactWithEvidence{Fact: aStoredFact()}}
	router := newMemoryRouter(stub)

	data := memoryJSON(t, callAs(t, router, http.MethodGet, "/api/v1/memory/facts/mf_1", ""))
	fact, ok := data["fact"].(map[string]any)
	if !ok {
		t.Fatalf("the answer carried %#v, want one fact", data["fact"])
	}
	// A fact that has never been edited and never taken back has no such times, and
	// the zero time is not one: the fields are absent rather than the year one.
	for _, absent := range []string{"updated_at", "retract_time"} {
		if _, present := fact[absent]; present {
			t.Errorf("a fact with no %s reported %v", absent, fact[absent])
		}
	}
	// And no fingerprint: it is the fold the gate and the dedupe compare by, and a
	// client that held one would hold an internal key with nothing to do with it.
	if _, present := fact["fingerprint"]; present {
		t.Error("the fact's fingerprint is on the wire")
	}

	// A fact that was taken back says so, says when, and carries the observations
	// it was admitted on — which is why the fact and its evidence are one answer.
	retracted := aStoredFact()
	retracted.Status = memory.StatusRetracted
	retracted.UpdatedAt = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	retracted.RetractTime = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	stub.held = memory.FactWithEvidence{Fact: retracted, Traces: []memory.Trace{anObservation("mt_1")}}

	fact = memoryJSON(t, callAs(t, router, http.MethodGet, "/api/v1/memory/facts/mf_1", ""))["fact"].(map[string]any)
	if fact["status"] != string(memory.StatusRetracted) {
		t.Errorf("status = %v, want the state the fact is in", fact["status"])
	}
	if fact["retract_time"] == nil {
		t.Error("a fact that was taken back reports no retraction time")
	}
	evidence, ok := fact["evidence"].([]any)
	if !ok || len(evidence) != 1 || evidence[0].(map[string]any)["trace_id"] != "mt_1" {
		t.Errorf("the evidence chain arrived as %#v, want the observation it rests on", fact["evidence"])
	}
}

// TestAMemoryPayloadIsJSONAndNotBase64 pins the trap that both JSON columns here
// are []byte in the domain: marshalled as they are, they would come out as
// base64-encoded strings, and a client would be handed a blob where it expected
// the surface's own JSON.
func TestAMemoryPayloadIsJSONAndNotBase64(t *testing.T) {
	stub := &memoryStub{
		traces: []memory.Trace{anObservation("mt_1")},
		audits: []memory.Audit{{
			ID: "ma_1", OwnerID: 7, Actor: memory.ActorUser, Action: memory.ActionFactEdited,
			TargetKind: memory.TargetFact, TargetID: "mf_1", Result: memory.ResultDone,
			Detail:    []byte(`{"changed":["scope"]}`),
			CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		}},
	}
	router := newMemoryRouter(stub)

	data := memoryJSON(t, callAs(t, router, http.MethodGet, "/api/v1/memory/traces", ""))
	trace := data["traces"].([]any)[0].(map[string]any)
	payload, ok := trace["payload"].(map[string]any)
	if !ok || payload["confidence"] != float64(81) {
		t.Errorf("the payload arrived as %#v, want the surface's own JSON", trace["payload"])
	}

	data = memoryJSON(t, callAs(t, router, http.MethodGet, "/api/v1/memory/audit", ""))
	line := data["audits"].([]any)[0].(map[string]any)
	if detail, ok := line["detail"].(map[string]any); !ok || len(detail) != 1 {
		t.Errorf("the detail arrived as %#v, want the action's own JSON", line["detail"])
	}
	if line["action"] != string(memory.ActionFactEdited) || line["target_id"] != "mf_1" {
		t.Errorf("the row arrived as %#v, want the action and what it was about", line)
	}
	// The account is not on every row: every row in an answer belongs to the
	// account the request was made as, so saying it per row says one thing many
	// times.
	if _, present := line["owner_id"]; present {
		t.Error("an audit row names its account")
	}
}

func TestTheSynthesisViewNamesEverySectionEvenWhenEmpty(t *testing.T) {
	stub := &memoryStub{computed: memory.Synthesis{
		OwnerID: 7, ComputedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), WindowDays: 30,
		Recent: []memory.Fact{}, Profile: []memory.SurfaceProfile{},
		Scopes: []memory.ScopeGroup{}, Preferences: []memory.Fact{},
	}}

	data := memoryJSON(t, callAs(t, newMemoryRouter(stub), http.MethodGet, "/api/v1/memory/synthesis", ""))
	synthesis, ok := data["synthesis"].(map[string]any)
	if !ok {
		t.Fatalf("the answer carried %#v, want one synthesis", data["synthesis"])
	}
	// A memory with nothing in it is an empty synthesis and not a missing one.
	for _, section := range []string{"recent", "profile", "scopes", "preferences"} {
		list, ok := synthesis[section].([]any)
		if !ok {
			t.Errorf("%s arrived as %#v, want a list", section, synthesis[section])
			continue
		}
		if len(list) != 0 {
			t.Errorf("%s holds %d of nothing", section, len(list))
		}
	}
	// The window and the moment travel with the answer: "recent" is a question
	// about a window, and one that did not say which would be read as covering
	// everything.
	if synthesis["window_days"] != float64(30) || synthesis["computed_at"] == nil {
		t.Errorf("the synthesis says %v over %v", synthesis["computed_at"], synthesis["window_days"])
	}
}

// TestTheEmptyScopeIsAGroupOnTheWire pins the one place an empty string is a
// value on this wire: a fact that belongs nowhere in particular is a group like
// any other, and a group whose name vanished would be one a client could not tell
// from a rendering bug.
func TestTheEmptyScopeIsAGroupOnTheWire(t *testing.T) {
	scopeless := aStoredFact()
	scopeless.Scope = ""
	stub := &memoryStub{computed: memory.Synthesis{
		OwnerID: 7, ComputedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), WindowDays: 30,
		Recent: []memory.Fact{}, Profile: []memory.SurfaceProfile{}, Preferences: []memory.Fact{},
		Scopes: []memory.ScopeGroup{{Scope: "", Facts: []memory.Fact{scopeless}}},
	}}

	data := memoryJSON(t, callAs(t, newMemoryRouter(stub), http.MethodGet, "/api/v1/memory/synthesis", ""))
	group := data["synthesis"].(map[string]any)["scopes"].([]any)[0].(map[string]any)
	if scope, present := group["scope"]; !present || scope != "" {
		t.Errorf("the empty scope arrived as %#v (present: %v), want the group named", scope, present)
	}
	// The fact inside it, by contrast, has no scope field at all: on a fact the
	// absence of the field is the statement "no scope was named", and the two
	// spellings mean the same thing in the two places they are read.
	fact := group["facts"].([]any)[0].(map[string]any)
	if _, present := fact["scope"]; present {
		t.Errorf("a fact with no scope reported %v", fact["scope"])
	}
}

func TestTheExportIsTheWholeGraph(t *testing.T) {
	stub := &memoryStub{exported: memory.Export{
		OwnerID:    7,
		ExportedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Facts: []memory.FactWithEvidence{
			{Fact: aStoredFact(), Traces: []memory.Trace{anObservation("mt_held")}},
		},
		// An observation that supports nothing travels too: "the system heard this
		// and does not believe it" is a state the user has to be able to see, and
		// the export is where they see all of it.
		Observations: []memory.Trace{anObservation("mt_loose")},
		Actions: []memory.Audit{{
			ID: "ma_1", OwnerID: 7, Actor: memory.ActorUser, Action: memory.ActionMemoryExported,
			TargetKind: memory.TargetMemory, Result: memory.ResultDone,
			Detail:    []byte(`{"facts":1,"observations":1,"actions":0}`),
			CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		}},
	}}

	data := memoryJSON(t, callAs(t, newMemoryRouter(stub), http.MethodGet, "/api/v1/memory/export", ""))
	exported, ok := data["export"].(map[string]any)
	if !ok {
		t.Fatalf("the answer carried %#v, want one document", data["export"])
	}
	if exported["owner_id"] != float64(7) || exported["exported_at"] == nil {
		t.Errorf("the export says %#v, want whose memory and when", exported)
	}
	// The document's own account is on the document, which is where it belongs:
	// the rows under it are all that account's and do not repeat it.
	facts := exported["facts"].([]any)
	if len(facts) != 1 {
		t.Fatalf("the export carried %d facts, want the one", len(facts))
	}
	fact := facts[0].(map[string]any)
	if evidence, ok := fact["evidence"].([]any); !ok || len(evidence) != 1 {
		t.Errorf("the exported fact arrived with %#v, want the observation it rests on", fact["evidence"])
	}
	if len(exported["observations"].([]any)) != 1 || len(exported["actions"].([]any)) != 1 {
		t.Errorf("the export carried %d observations and %d actions, want both",
			len(exported["observations"].([]any)), len(exported["actions"].([]any)))
	}
}

func TestTheBatchReportsSayWhatTheyWouldDo(t *testing.T) {
	stub := &memoryStub{
		merge: memory.DedupeReport{
			DryRun: true, Result: memory.ResultDone, Superseded: 0,
			Groups: []memory.DedupeGroup{{
				Fingerprint: "f", Statement: "我在学 Go", Survivor: "mf_1",
				Superseded: []string{"mf_2"},
			}},
		},
		purged: memory.CleanupReport{
			Before: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			Purged: 2, Facts: []memory.Fact{aStoredFact()},
		},
	}
	router := newMemoryRouter(stub)

	data := memoryJSON(t, callAs(t, router, http.MethodPost, "/api/v1/memory/facts/dedupe", `{"dry_run":true}`))
	merge := data["dedupe"].(map[string]any)
	// A preview says it is one and reports nothing merged, which is what makes its
	// report comparable with the commit's rather than merely similar.
	if merge["dry_run"] != true || merge["superseded"] != float64(0) {
		t.Errorf("the preview reported %#v, want a preview that merged nothing", merge)
	}
	group := merge["groups"].([]any)[0].(map[string]any)
	if group["survivor"] != "mf_1" || group["statement"] != "我在学 Go" {
		t.Errorf("the group reported %#v, want the fact the others fold into", group)
	}
	if superseded, ok := group["superseded"].([]any); !ok || len(superseded) != 1 {
		t.Errorf("the group reported %#v, want the fact it would take", group["superseded"])
	}

	data = memoryJSON(t, callAs(t, router, http.MethodPost, "/api/v1/memory/cleanup", `{}`))
	cleanup := data["cleanup"].(map[string]any)
	if cleanup["dry_run"] != false || cleanup["purged"] != float64(2) {
		t.Errorf("the commit reported %#v, want what it removed", cleanup)
	}
	if cleanup["before"] == nil || len(cleanup["facts"].([]any)) != 1 {
		t.Errorf("the commit reported %#v, want the cutoff and the batch", cleanup)
	}
}
