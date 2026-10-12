// Package event defines the turn event protocol: the kinds of event a turn can
// publish, the shape of one event, and the port a turn writes them through.
//
// The names and numeric ids here are a contract shared with two other places:
// the turn_event_types table seeded by migration 00002, and the WebSocket
// protocol that carries the name form to clients. Neither may drift from this
// file.
package event

// Type names one kind of turn event. The name string is what travels on the
// wire; the numeric id is what turn_events.type stores.
type Type string

// The event types, in the order migration 00002 seeds them.
const (
	TypeStageStart   Type = "stage_start"
	TypeStageEnd     Type = "stage_end"
	TypeThinking     Type = "thinking"
	TypeObservation  Type = "observation"
	TypeContent      Type = "content"
	TypeToolCall     Type = "tool_call"
	TypeToolResult   Type = "tool_result"
	TypeProgress     Type = "progress"
	TypeSources      Type = "sources"
	TypeResult       Type = "result"
	TypeError        Type = "error"
	TypeSession      Type = "session"
	TypeSessionMeta  Type = "session_meta"
	TypeDone         Type = "done"
	TypeWaitForInput Type = "wait_for_input"
)

// typeIDs is the id half of the contract. turn_events.type is a foreign key
// into turn_event_types, so an id that is not listed here would be rejected by
// the database rather than by anything that can explain itself.
var typeIDs = map[Type]int16{
	TypeStageStart:   1,
	TypeStageEnd:     2,
	TypeThinking:     3,
	TypeObservation:  4,
	TypeContent:      5,
	TypeToolCall:     6,
	TypeToolResult:   7,
	TypeProgress:     8,
	TypeSources:      9,
	TypeResult:       10,
	TypeError:        11,
	TypeSession:      12,
	TypeSessionMeta:  13,
	TypeDone:         14,
	TypeWaitForInput: 15,
}

// idTypes is the reverse of typeIDs, for reading stored events back.
var idTypes = func() map[int16]Type {
	types := make(map[int16]Type, len(typeIDs))
	for eventType, id := range typeIDs {
		types[id] = eventType
	}
	return types
}()

// ID returns the id that turn_events.type stores for this type.
func (t Type) ID() (int16, bool) {
	id, ok := typeIDs[t]
	return id, ok
}

// Valid reports whether the type is one this protocol defines.
func (t Type) Valid() bool {
	_, ok := typeIDs[t]
	return ok
}

// TypeByID returns the type stored as id.
func TypeByID(id int16) (Type, bool) {
	eventType, ok := idTypes[id]
	return eventType, ok
}

// Types returns every type in id order. It exists so that a startup check can
// compare the protocol against the seeded table.
func Types() []Type {
	types := make([]Type, 0, len(typeIDs))
	for id := int16(1); id <= int16(len(typeIDs)); id++ {
		types = append(types, idTypes[id])
	}
	return types
}
