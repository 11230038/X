package handler

// What the memory layer looks like on the wire.
//
// The views are here rather than beside the handlers because they are a contract
// and the handlers are not: a client holds a fact's id and its version, and it
// groups a synthesis by the scopes the server named. Keeping them in one file is
// what makes "what a fact looks like" a single thing to read, and what makes the
// one thing worth checking — that a fact in a listing and a fact on its own are
// the same fact — visible at a glance.
//
// Two decisions run through all of them. A time the domain spells as the zero
// time is absent on the wire (optionalTime), because a client reading
// `0001-01-01T00:00:00Z` would be reading a date rather than the absence of one.
// And no view carries a fingerprint: it is the key the gate and the dedupe
// compare by, it is derived from the statement, and a client that had it would
// have a copy of an internal fold with nothing to do with it.

import (
	"encoding/json"
	"time"

	"backend/internal/memory"
)

// traceView is one observation (L1): what a surface said, and that it said it.
//
// The payload is the surface's own JSON, embedded as it was stored rather than
// re-encoded into a shape of this package's invention — it is where a surface puts
// what it saw beyond the sentence, and this layer has no business deciding what
// that is.
type traceView struct {
	TraceID   string          `json:"trace_id"`
	Surface   memory.Surface  `json:"surface"`
	Kind      memory.Kind     `json:"kind"`
	Statement string          `json:"statement"`
	SessionID string          `json:"session_id,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// factView is one fact (L2): what the system is prepared to act on.
type factView struct {
	FactID  string         `json:"fact_id"`
	Surface memory.Surface `json:"surface"`
	// Scope is where the fact belongs in the synthesis, and it is absent when the
	// admitting observation named none — which is a different thing from a scope
	// named "" and is spelled the way the rest of the wire spells a missing field.
	Scope     string `json:"scope,omitempty"`
	Statement string `json:"statement"`
	// Confidence is the number the admitting observation reported, kept so that
	// what the gate decided can still be explained.
	Confidence  int              `json:"confidence"`
	Status      memory.Status    `json:"status"`
	Sensitive   bool             `json:"sensitive"`
	ConfirmedBy memory.Confirmer `json:"confirmed_by"`
	// Version is what a client sends back with a write it wants to be held to.
	Version int32 `json:"version"`
	// CreatedAt is when it was admitted; UpdatedAt and RetractTime are absent while
	// they have never happened.
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	RetractTime *time.Time `json:"retract_time,omitempty"`
}

// factDetailView is a fact and the observations it was admitted on (MEM-01).
//
// The fact is embedded rather than repeated, so that a fact in a listing and a
// fact on its own are one shape and cannot drift apart.
type factDetailView struct {
	factView
	Evidence []traceView `json:"evidence"`
}

// surfaceProfileView is one surface's line in the synthesis: what it has settled,
// and how many of its facts the per-surface bound left out.
type surfaceProfileView struct {
	Surface memory.Surface `json:"surface"`
	Facts   []factView     `json:"facts"`
	// Truncated is reported rather than absorbed: a bound nobody can see would make
	// a bounded synthesis read as the whole picture.
	Truncated int `json:"truncated"`
}

// scopeGroupView is the facts that named one scope.
//
// Its scope is not omitempty, and it is the one place on this wire where an empty
// string is a value: the empty scope is a group like any other — "this belongs
// nowhere in particular" — and a group whose name vanished would be a group a
// client could not tell from a rendering bug.
type scopeGroupView struct {
	Scope string     `json:"scope"`
	Facts []factView `json:"facts"`
}

// synthesisView is what memory looks like now (§2.6).
//
// It carries the moment it describes and the window it was computed over, because
// both are inputs a client would otherwise have to be told by the deployment:
// "recent" is a question about a window, and an answer that did not say which one
// would be read as if it covered everything.
type synthesisView struct {
	OwnerID     int64                `json:"owner_id"`
	ComputedAt  time.Time            `json:"computed_at"`
	WindowDays  int                  `json:"window_days"`
	Recent      []factView           `json:"recent"`
	Profile     []surfaceProfileView `json:"profile"`
	Scopes      []scopeGroupView     `json:"scopes"`
	Preferences []factView           `json:"preferences"`
}

// auditView is one thing that was done to a memory (MEM-02).
//
// It does not say whose account, on every row, and that is not an omission: every
// row in an answer belongs to the account the request was made as, so saying it
// once per row would be saying one thing many times.
type auditView struct {
	AuditID    string            `json:"audit_id"`
	Actor      memory.Actor      `json:"actor"`
	Action     memory.Action     `json:"action"`
	TargetKind memory.TargetKind `json:"target_kind"`
	TargetID   string            `json:"target_id,omitempty"`
	Result     memory.Result     `json:"result"`
	Detail     json.RawMessage   `json:"detail,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

// dedupeGroupView is one set of facts that say the same thing, and what happened
// to them.
type dedupeGroupView struct {
	Fingerprint string   `json:"fingerprint"`
	Statement   string   `json:"statement"`
	Survivor    string   `json:"survivor"`
	Superseded  []string `json:"superseded"`
}

// dedupeReportView is what a merge found and what it did.
type dedupeReportView struct {
	DryRun bool              `json:"dry_run"`
	Groups []dedupeGroupView `json:"groups"`
	// Superseded is zero on a preview, which is what makes the preview's report
	// comparable with the commit's rather than merely similar.
	Superseded int           `json:"superseded"`
	Result     memory.Result `json:"result"`
}

// cleanupReportView is what a cleanup named and what it removed.
type cleanupReportView struct {
	DryRun bool       `json:"dry_run"`
	Before time.Time  `json:"before"`
	Facts  []factView `json:"facts"`
	Purged int        `json:"purged"`
}

// exportView is the whole of one account's memory, as a document (§2.14).
//
// The three parts and their order are the graph: the facts with their evidence,
// then every observation — including the ones that support nothing, which are the
// rows a user asking "what have you written about me" most wants to see — and then
// the trail, which is the part that cannot be re-derived from the rest.
type exportView struct {
	OwnerID      int64            `json:"owner_id"`
	ExportedAt   time.Time        `json:"exported_at"`
	Facts        []factDetailView `json:"facts"`
	Observations []traceView      `json:"observations"`
	Actions      []auditView      `json:"actions"`
}

func toTraceView(trace memory.Trace) traceView {
	return traceView{
		TraceID: trace.ID, Surface: trace.Surface, Kind: trace.Kind,
		Statement: trace.Statement, SessionID: trace.SessionID, TurnID: trace.TurnID,
		Payload: json.RawMessage(trace.Payload), CreatedAt: trace.CreatedAt,
	}
}

func toTraceViews(traces []memory.Trace) []traceView {
	listed := make([]traceView, 0, len(traces))
	for _, trace := range traces {
		listed = append(listed, toTraceView(trace))
	}
	return listed
}

func toFactView(fact memory.Fact) factView {
	return factView{
		FactID: fact.ID, Surface: fact.Surface, Scope: fact.Scope,
		Statement: fact.Statement, Confidence: fact.Confidence, Status: fact.Status,
		Sensitive: fact.Sensitive, ConfirmedBy: fact.ConfirmedBy, Version: fact.Version,
		CreatedAt: fact.CreatedAt, UpdatedAt: optionalTime(fact.UpdatedAt),
		RetractTime: optionalTime(fact.RetractTime),
	}
}

func toFactViews(facts []memory.Fact) []factView {
	listed := make([]factView, 0, len(facts))
	for _, fact := range facts {
		listed = append(listed, toFactView(fact))
	}
	return listed
}

func toFactDetailView(held memory.FactWithEvidence) factDetailView {
	return factDetailView{factView: toFactView(held.Fact), Evidence: toTraceViews(held.Traces)}
}

func toAuditView(row memory.Audit) auditView {
	return auditView{
		AuditID: row.ID, Actor: row.Actor, Action: row.Action,
		TargetKind: row.TargetKind, TargetID: row.TargetID, Result: row.Result,
		Detail: json.RawMessage(row.Detail), CreatedAt: row.CreatedAt,
	}
}

func toAuditViews(rows []memory.Audit) []auditView {
	listed := make([]auditView, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, toAuditView(row))
	}
	return listed
}

func toSynthesisView(computed memory.Synthesis) synthesisView {
	profile := make([]surfaceProfileView, 0, len(computed.Profile))
	for _, row := range computed.Profile {
		profile = append(profile, surfaceProfileView{
			Surface: row.Surface, Facts: toFactViews(row.Facts), Truncated: row.Truncated,
		})
	}
	groups := make([]scopeGroupView, 0, len(computed.Scopes))
	for _, group := range computed.Scopes {
		groups = append(groups, scopeGroupView{Scope: group.Scope, Facts: toFactViews(group.Facts)})
	}
	return synthesisView{
		OwnerID: computed.OwnerID, ComputedAt: computed.ComputedAt,
		WindowDays: computed.WindowDays, Recent: toFactViews(computed.Recent),
		Profile: profile, Scopes: groups, Preferences: toFactViews(computed.Preferences),
	}
}

func toDedupeReportView(report memory.DedupeReport) dedupeReportView {
	groups := make([]dedupeGroupView, 0, len(report.Groups))
	for _, group := range report.Groups {
		groups = append(groups, dedupeGroupView{
			Fingerprint: group.Fingerprint, Statement: group.Statement,
			Survivor: group.Survivor, Superseded: group.Superseded,
		})
	}
	return dedupeReportView{
		DryRun: report.DryRun, Groups: groups,
		Superseded: report.Superseded, Result: report.Result,
	}
}

func toCleanupReportView(report memory.CleanupReport) cleanupReportView {
	return cleanupReportView{
		DryRun: report.DryRun, Before: report.Before,
		Facts: toFactViews(report.Facts), Purged: report.Purged,
	}
}

func toExportView(exported memory.Export) exportView {
	facts := make([]factDetailView, 0, len(exported.Facts))
	for _, held := range exported.Facts {
		facts = append(facts, toFactDetailView(held))
	}
	return exportView{
		OwnerID: exported.OwnerID, ExportedAt: exported.ExportedAt,
		Facts: facts, Observations: toTraceViews(exported.Observations),
		Actions: toAuditViews(exported.Actions),
	}
}

// optionalTime renders a time the domain spells as the zero time as an absence:
// a fact that has never been edited has no edit time, and the year one is not one.
func optionalTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}
