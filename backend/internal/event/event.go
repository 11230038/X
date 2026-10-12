package event

import (
	"context"
	"time"
)

// SourceTurnRuntime is the source the runtime publishes its own events under.
// Any other source is the name of the capability that produced the event.
const SourceTurnRuntime = "turn_runtime"

// The metadata keys the protocol defines. They are constants so that the code
// writing an event and the code reading it cannot disagree on a spelling.
const (
	// MetaArgs carries a tool call's arguments on a tool_call event.
	MetaArgs = "args"
	// MetaErrorCode names the failure on a terminal error event.
	MetaErrorCode = "error_code"
	// MetaRetryable says whether repeating the turn could succeed.
	MetaRetryable = "retryable"
	// MetaPartialResponse says whether some answer text was already produced.
	MetaPartialResponse = "partial_response"
	// MetaStatus mirrors the turn's final status onto its terminal events.
	MetaStatus = "status"
	// MetaTurnTerminal marks an error event as ending the turn.
	MetaTurnTerminal = "turn_terminal"
	// MetaUsageSummary carries the token accounting on a result event.
	MetaUsageSummary = "usage_summary"
	// MetaCostSummary carries the cost accounting on a result event.
	MetaCostSummary = "cost_summary"
)

// Event is one thing that happened during a turn.
//
// TurnID, Seq and Timestamp are not the producer's to set: the Emitter assigns
// them, so that seq stays gap-free and ordered no matter how many producers a
// turn has. A producer fills in the rest.
type Event struct {
	TurnID string
	// Seq orders events within a turn, starting at 1. Zero means "not yet
	// assigned", which is why the first event is 1 rather than 0.
	Seq  int64
	Type Type
	// Source names what produced the event: a capability, or the runtime.
	Source string
	// Stage names the capability stage the event belongs to, if any.
	Stage string
	// Content is the event's text, or the simple value it carries. A tool_call
	// puts the tool name here and its arguments in Metadata.
	Content string
	// Metadata is the structured payload. A result puts its capability payload
	// here rather than in Content, so clients must read it from here.
	Metadata  map[string]any
	Timestamp time.Time
}

// Emitter is where a turn's events go. A turn has exactly one, and everything
// that happens inside the turn publishes through it.
//
// Emit assigns Seq and Timestamp, persists the event and only then publishes it
// to subscribers. Callers describe what happened while leaving where it lands
// to the implementation, which is why Emit takes the event by value: the caller
// never sees the assigned Seq and so cannot depend on it.
type Emitter interface {
	Emit(ctx context.Context, event Event) error
}
