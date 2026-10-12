// Package tool defines the tool protocol: what a tool is, how tools are
// registered, and the stable order in which a tool plane is assembled.
//
// Tiering lives here rather than on the tools themselves. A tool declares what
// it does; where it sits in the plane is a deployment decision, so adding a tool
// does not mean editing its metadata in two places.
package tool

import (
	"context"
	"encoding/json"
)

// Definition is a tool as the model sees it.
type Definition struct {
	Name        string
	Description string
	// Parameters is the JSON Schema object for the arguments. A nil value sends
	// no schema, which is how a tool with no arguments is declared.
	Parameters json.RawMessage
}

// Call is one invocation requested by the model.
//
// Arguments is already-valid JSON. Whether the model's output decoded is
// decided by whoever parsed it, so a Call that exists is one whose arguments
// parsed; a Call with malformed arguments cannot be built.
type Call struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Result is what a tool produced.
//
// A failure is a value rather than an error: a refused or unsuccessful call
// still becomes a tool_result event that the model reads and reacts to.
type Result struct {
	Content  string
	Success  bool
	Metadata map[string]any
}

// Tool is one thing the model can be offered. Its Definition is what the model
// sees; Invoke is what runs.
//
// Invoke returns an error only when the tool itself failed, which the caller
// turns into a structured failure rather than passing the text to the model.
// Cancelling ctx must stop the work, as the turn's cancellation depends on it.
type Tool interface {
	Definition() Definition
	Invoke(ctx context.Context, call Call) (Result, error)
}
