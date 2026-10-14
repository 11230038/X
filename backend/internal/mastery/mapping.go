package mastery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Laying the tree out.
//
// A tree is drawn in visits, not in one go: the first turn writes the modules it
// can think of, and the ones after it add to what is there. That is what makes
// this different from AddKnowledgePoint — it takes a whole layer at once, in the
// shape the model wrote it, and it is where the ids come from. The model names
// things; this package numbers them (§2.5).

// MapRequest is a turn laying out or extending the tree.
type MapRequest struct {
	Change
	// Modules are the modules as the model wrote them, in the order it wrote
	// them. A module with a blank id is new; one carrying an id is a module that
	// is already in the tree, and what it carries is the knowledge points to add
	// to it.
	Modules []Module
}

// Map merges modules into the tree and issues the ids for everything new.
//
// The rules, and each of them is a decision rather than a convenience:
//
//   - a module id the tree does not have is treated as new *with that id*. The
//     model got it from a state it read, so the module it names is one that was
//     there and is not any more; giving it the id it asked for is the reading
//     that keeps the rest of the call meaningful.
//   - an existing module keeps its knowledge points. Merging is by addition
//     here: nothing in a `mastery_map` call removes anything, because removals
//     are `mastery_edit`'s and a call that both adds and forgets would make the
//     same arguments mean two things.
//   - the name and description a model writes for an existing module are taken
//     as a rewording. It would otherwise have to write the old name to say
//     nothing, and a model that has to repeat itself is a model that will get it
//     slightly wrong.
//   - every knowledge point is new, always. Knowledge points have no id in the
//     call — there is no way to say "this one is already there" — so a repeated
//     call adds what it says again, and the way not to duplicate a knowledge
//     point is not to write it twice.
func (s *Service) Map(ctx context.Context, request MapRequest) (Path, error) {
	if len(request.Modules) == 0 {
		return Path{}, fmt.Errorf("%w: no modules were named", ErrInvalidRequest)
	}
	return s.change(ctx, request.Change, EventPathMapped, func(tree Tree) (Tree, []byte, error) {
		next := tree
		modulesAdded := make([]string, 0, len(request.Modules))
		pointsAdded := make([]string, 0, len(request.Modules))

		for _, module := range request.Modules {
			id := strings.TrimSpace(module.ID)
			if id == "" {
				generated, err := newID(moduleIDPrefix)
				if err != nil {
					return Tree{}, nil, err
				}
				id = generated
			}

			existing, found := next.Module(id)
			switch {
			case found:
				merged := existing
				if name := strings.TrimSpace(module.Name); name != "" {
					merged.Name = name
				}
				merged.Description = module.Description
				next = next.WithModule(merged)
			default:
				next = next.WithModule(Module{
					ID:          id,
					Name:        module.Name,
					Description: module.Description,
				})
				modulesAdded = append(modulesAdded, id)
			}

			for _, point := range module.KnowledgePoints {
				pointID, err := newID(knowledgePointIDPrefix)
				if err != nil {
					return Tree{}, nil, err
				}
				added := KnowledgePoint{
					ID:          pointID,
					Name:        point.Name,
					Type:        point.Type,
					ModuleID:    id,
					Description: point.Description,
				}
				next, err = next.WithKnowledgePoint(id, added)
				if err != nil {
					return Tree{}, nil, err
				}
				pointsAdded = append(pointsAdded, pointID)
			}
		}

		payload, err := json.Marshal(map[string]any{
			"change":                 "tree_mapped",
			"modules_added":          modulesAdded,
			"knowledge_points_added": pointsAdded,
		})
		if err != nil {
			return Tree{}, nil, fmt.Errorf("encode the mapping of path %s: %w", request.PathID, err)
		}
		return next, payload, nil
	})
}
