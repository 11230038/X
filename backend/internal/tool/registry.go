package tool

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnknownTool reports a tool name that nothing registered. It is returned
// rather than ignored because a name that resolves to nothing would otherwise
// reach the model as a callable tool that can never run.
var ErrUnknownTool = errors.New("tool: unknown tool")

// Registry holds the tools a process offers.
//
// Registration happens at startup and lookups happen per turn, so the maps are
// read concurrently only after registration is finished; Register must not be
// called once turns are being served.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. A blank or duplicate name is rejected: two tools with
// one name would make the plane depend on registration order.
func (r *Registry) Register(t Tool) error {
	definition := t.Definition()
	name := strings.TrimSpace(definition.Name)
	if name == "" {
		return errors.New("tool: refusing to register a tool with a blank name")
	}
	if definition.Name != name {
		return fmt.Errorf("tool: tool name %q has surrounding whitespace", definition.Name)
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool: %q is already registered", name)
	}
	r.tools[name] = t
	return nil
}

// Lookup returns the named tool.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names returns every registered name, sorted so that a startup error reads the
// same way on every run.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Definitions resolves names to definitions, keeping the order it was given
// because that order is the tool plane the model is offered.
//
// It fails on the first unknown name instead of skipping it. A plane that
// silently drops a tool would let a turn run without something its capability
// declared, which is the failure mounting is supposed to make impossible.
func (r *Registry) Definitions(names []string) ([]Definition, error) {
	definitions := make([]Definition, 0, len(names))
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownTool, name)
		}
		definitions = append(definitions, t.Definition())
	}
	return definitions, nil
}
