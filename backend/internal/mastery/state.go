package mastery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The tree is what `mastery_paths.state_json` holds: modules, and knowledge
// points inside them.
//
// It is a struct and not a map, and every mutation returns a new tree rather
// than editing this one in place. Both of those are load-bearing:
//
//   - a map would serialise with its keys in Go's own order, which is stable
//     within a process and is not a promise across builds — and the state is
//     written back as bytes, so a state that encodes differently on a second
//     pass is a state that can never be compared with itself;
//   - a mutation that edited in place would leave the caller holding a tree that
//     already changed under it, and the version guard is the only thing standing
//     between that and a lost update.

// KnowledgePoint is one thing to learn, at its place in the tree.
//
// ModuleID is redundant with the knowledge point's position and is kept anyway,
// because it is part of the definition the reference schema states
// (DATABASE_SCHEMA.md:475) and because a knowledge point id is quoted on its own
// by `notebook_entries.knowledge_point_id`, where its module is not visible.
// Validate requires the two to agree, so a hand-edited state cannot drift
// silently.
type KnowledgePoint struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	ModuleID    string `json:"module_id"`
	Description string `json:"description,omitempty"`
	// Mastery is a cache of what the evidence says (gate.go). It is written when
	// the state is written and never read as truth — the evidence table is.
	Mastery Level `json:"mastery,omitempty"`
}

// Module is a group of knowledge points.
type Module struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Description     string           `json:"description,omitempty"`
	KnowledgePoints []KnowledgePoint `json:"knowledge_points"`
}

// Tree is the whole of a path's state.
type Tree struct {
	Modules []Module `json:"modules"`
}

// Validate reports whether the tree is one this package will store.
//
// The rule that needs saying is the last one: knowledge point ids are unique
// across the whole tree, not within their module. A knowledge point id is quoted
// by itself — `notebook_entries` carries it beside the path id and nothing else
// — so two knowledge points sharing an id would make every question filed
// against it a question about both, and no reader could tell which.
func (t Tree) Validate() error {
	seenModules := make(map[string]struct{}, len(t.Modules))
	seenPoints := make(map[string]struct{})
	for _, module := range t.Modules {
		if strings.TrimSpace(module.ID) == "" {
			return fmt.Errorf("%w: a module has no id", ErrInvalidRequest)
		}
		if _, duplicate := seenModules[module.ID]; duplicate {
			return fmt.Errorf("%w: two modules are called %s", ErrInvalidRequest, module.ID)
		}
		seenModules[module.ID] = struct{}{}

		for _, point := range module.KnowledgePoints {
			if strings.TrimSpace(point.ID) == "" {
				return fmt.Errorf("%w: a knowledge point in module %s has no id",
					ErrInvalidRequest, module.ID)
			}
			if point.ModuleID != module.ID {
				return fmt.Errorf("%w: knowledge point %s says it is in %s and is stored in %s",
					ErrInvalidRequest, point.ID, point.ModuleID, module.ID)
			}
			if _, duplicate := seenPoints[point.ID]; duplicate {
				return fmt.Errorf("%w: two knowledge points are called %s", ErrInvalidRequest, point.ID)
			}
			seenPoints[point.ID] = struct{}{}
		}
	}
	return nil
}

// Module returns one module by id.
func (t Tree) Module(id string) (Module, bool) {
	for _, module := range t.Modules {
		if module.ID == id {
			return module, true
		}
	}
	return Module{}, false
}

// KnowledgePoint returns one knowledge point by id, and the module holding it.
func (t Tree) KnowledgePoint(id string) (KnowledgePoint, Module, bool) {
	for _, module := range t.Modules {
		for _, point := range module.KnowledgePoints {
			if point.ID == id {
				return point, module, true
			}
		}
	}
	return KnowledgePoint{}, Module{}, false
}

// KnowledgePointIDs lists every knowledge point id, module by module and in
// order. The insight endpoint and the tests both want this, and neither wants to
// walk the tree itself.
func (t Tree) KnowledgePointIDs() []string {
	ids := make([]string, 0, len(t.Modules))
	for _, module := range t.Modules {
		for _, point := range module.KnowledgePoints {
			ids = append(ids, point.ID)
		}
	}
	return ids
}

// WithModule adds or replaces a module, keeping the position of one that is
// already there and appending one that is not.
func (t Tree) WithModule(module Module) Tree {
	next := t.clone()
	for index := range next.Modules {
		if next.Modules[index].ID == module.ID {
			next.Modules[index] = module
			return next
		}
	}
	next.Modules = append(next.Modules, module)
	return next
}

// WithKnowledgePoint adds a knowledge point to a module, or replaces the one
// already carrying that id.
//
// It does not move a knowledge point between modules: a replacement keeps the
// module it was found in and refuses the move, because "the same knowledge
// point, now in a different module" and "a knowledge point with the same id in
// another module" are indistinguishable at the call site and only one of them is
// what the caller meant. Moving is MoveKnowledgePoint's job.
func (t Tree) WithKnowledgePoint(moduleID string, point KnowledgePoint) (Tree, error) {
	if _, _, ok := t.KnowledgePoint(point.ID); ok {
		return Tree{}, fmt.Errorf("%w: knowledge point %s is already in this path", ErrDuplicate, point.ID)
	}
	next := t.clone()
	for index := range next.Modules {
		if next.Modules[index].ID != moduleID {
			continue
		}
		point.ModuleID = moduleID
		next.Modules[index].KnowledgePoints = append(clonePoints(next.Modules[index].KnowledgePoints), point)
		return next, nil
	}
	return Tree{}, fmt.Errorf("%w: module %s is not in this path", ErrNotFound, moduleID)
}

// Edit is a change to a knowledge point's wording. A nil field is one the caller
// did not name, which is not the same as naming an empty string.
type Edit struct {
	Name        *string
	Type        *string
	Description *string
}

// Empty reports whether the edit names nothing.
func (e Edit) Empty() bool {
	return e.Name == nil && e.Type == nil && e.Description == nil
}

// EditKnowledgePoint applies an edit to the knowledge point already in the tree.
func (t Tree) EditKnowledgePoint(id string, edit Edit) (Tree, error) {
	if edit.Empty() {
		return Tree{}, fmt.Errorf("%w: the edit names nothing", ErrInvalidRequest)
	}
	next := t.clone()
	for module := range next.Modules {
		for index := range next.Modules[module].KnowledgePoints {
			point := &next.Modules[module].KnowledgePoints[index]
			if point.ID != id {
				continue
			}
			if edit.Name != nil {
				point.Name = *edit.Name
			}
			if edit.Type != nil {
				point.Type = *edit.Type
			}
			if edit.Description != nil {
				point.Description = *edit.Description
			}
			return next, nil
		}
	}
	return Tree{}, fmt.Errorf("%w: knowledge point %s is not in this path", ErrNotFound, id)
}

// MoveKnowledgePoint puts a knowledge point into a module at a position. A
// negative position appends.
func (t Tree) MoveKnowledgePoint(id, toModuleID string, position int) (Tree, error) {
	point, from, ok := t.KnowledgePoint(id)
	if !ok {
		return Tree{}, fmt.Errorf("%w: knowledge point %s is not in this path", ErrNotFound, id)
	}
	if _, ok := t.Module(toModuleID); !ok {
		return Tree{}, fmt.Errorf("%w: module %s is not in this path", ErrNotFound, toModuleID)
	}
	next := t.clone()
	for module := range next.Modules {
		if next.Modules[module].ID == from.ID {
			next.Modules[module].KnowledgePoints = withoutPoint(next.Modules[module].KnowledgePoints, id)
		}
	}
	point.ModuleID = toModuleID
	for module := range next.Modules {
		if next.Modules[module].ID != toModuleID {
			continue
		}
		points := next.Modules[module].KnowledgePoints
		position = clampPosition(position, len(points))
		moved := make([]KnowledgePoint, 0, len(points)+1)
		moved = append(moved, points[:position]...)
		moved = append(moved, point)
		moved = append(moved, points[position:]...)
		next.Modules[module].KnowledgePoints = moved
		return next, nil
	}
	// Unreachable: the module was found above and the clone has the same modules.
	return Tree{}, fmt.Errorf("%w: module %s is not in this path", ErrNotFound, toModuleID)
}

// RemoveKnowledgePoint drops a knowledge point and reports the tree without it.
func (t Tree) RemoveKnowledgePoint(id string) (Tree, error) {
	_, from, ok := t.KnowledgePoint(id)
	if !ok {
		return Tree{}, fmt.Errorf("%w: knowledge point %s is not in this path", ErrNotFound, id)
	}
	next := t.clone()
	for module := range next.Modules {
		if next.Modules[module].ID == from.ID {
			next.Modules[module].KnowledgePoints = withoutPoint(next.Modules[module].KnowledgePoints, id)
		}
	}
	return next, nil
}

// SetLevel writes the cached mastery level of one knowledge point. It is the one
// write the gate makes to the tree, and it is the reason the tree is rewritten
// when evidence lands.
func (t Tree) SetLevel(id string, level Level) Tree {
	next := t.clone()
	for module := range next.Modules {
		for index := range next.Modules[module].KnowledgePoints {
			if next.Modules[module].KnowledgePoints[index].ID == id {
				next.Modules[module].KnowledgePoints[index].Mastery = level
			}
		}
	}
	return next
}

// clone copies the tree deeply enough that no mutation of the copy can reach the
// original. The knowledge point slices are the only shared state, and they are
// copied one level down — KnowledgePoint holds no slices of its own, so that is
// the whole of it.
func (t Tree) clone() Tree {
	next := Tree{Modules: make([]Module, len(t.Modules))}
	copy(next.Modules, t.Modules)
	for index := range next.Modules {
		next.Modules[index].KnowledgePoints = clonePoints(t.Modules[index].KnowledgePoints)
	}
	return next
}

func clonePoints(points []KnowledgePoint) []KnowledgePoint {
	if len(points) == 0 {
		return nil
	}
	clone := make([]KnowledgePoint, len(points))
	copy(clone, points)
	return clone
}

func withoutPoint(points []KnowledgePoint, id string) []KnowledgePoint {
	kept := make([]KnowledgePoint, 0, len(points))
	for _, point := range points {
		if point.ID != id {
			kept = append(kept, point)
		}
	}
	return kept
}

func clampPosition(position, length int) int {
	switch {
	case position < 0:
		return length
	case position > length:
		return length
	default:
		return position
	}
}

// DecodeTree reads a stored state_json.
//
// An empty column is an empty tree rather than an error: mastery_paths.state_json
// is nullable in the schema, so a row written by something other than this
// package has a NULL there, and "no tree" is what that means.
func DecodeTree(raw []byte) (Tree, error) {
	if len(raw) == 0 {
		return Tree{}, nil
	}
	var tree Tree
	if err := json.Unmarshal(raw, &tree); err != nil {
		return Tree{}, fmt.Errorf("read the state of the path: %w", err)
	}
	return tree, nil
}

// EncodeTree writes a tree as the bytes the state column holds.
//
// The output is a function of the tree and nothing else — the struct's field
// order fixes the key order, and there is no map anywhere inside it — so
// encoding a tree that was decoded from a column reproduces that column's bytes.
// That property is what makes the state's round trip testable at all, and a
// change that broke it would not fail anywhere else: it would show up as the
// state column changing on every read.
func EncodeTree(tree Tree) ([]byte, error) {
	raw, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("write the state of the path: %w", err)
	}
	return raw, nil
}
