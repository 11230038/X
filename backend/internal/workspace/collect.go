package workspace

// Gathering the objects a selection names, one kind at a time.
//
// §1.2 draws `workspace → audit, library, notebook, reading, session, kb`, and
// the reads themselves are not here for the reason `kb.FileSource` is not in
// `kb`: the shape that reaches a domain is the shape the domain's own service
// already has, and this package has no business knowing what a material is. What
// is here is the part that is about *packages* — that every ref that was asked
// for comes back exactly once, that the reads happen in an order two runs agree
// on, and that a kind with nowhere to read from fails at construction rather
// than at a user's request.
//
// The adapters that turn a session, a material, a question and a collection into
// bytes live in the process's own assembly, beside the file library's, because
// that is the one place that has both sides.

import (
	"context"
	"fmt"

	"backend/internal/authz"
)

// Source reads the objects of one kind.
//
// It answers one object per ref it was given. Returning fewer is not a partial
// success to be sorted out upstream: a package that holds everything except the
// thing somebody asked for is a package whose manifest is wrong, and the
// manifest is the only reason a package is worth more than a folder of JSON.
type Source interface {
	Objects(ctx context.Context, caller authz.Caller, refs []Ref) ([]Object, error)
}

// Sources is the Collector over one Source per kind.
type Sources struct {
	sources map[Kind]Source
}

// NewSources builds a collector, and refuses one that cannot answer for every
// kind.
//
// The refusal is what makes the closed set in types.go worth having. A kind
// added there is a kind every deployment can now write, and without this check
// the first anybody would hear about a missing reader is an export a user asked
// for failing halfway through — in the one path where the user has already been
// promised a package.
func NewSources(sources map[Kind]Source) (*Sources, error) {
	held := make(map[Kind]Source, len(Kinds))
	for _, kind := range Kinds {
		if sources[kind] == nil {
			return nil, fmt.Errorf("%w: %s has no source", ErrNotConfigured, kind)
		}
		held[kind] = sources[kind]
	}
	return &Sources{sources: held}, nil
}

// Collect reads every ref, in the order it was asked for.
//
// The order is the caller's rather than the sources': an export's manifest is
// sorted by the writer, and what this preserves is that the same selection asked
// for twice reads the domains in the same sequence — which is what makes a
// failure reproducible rather than a race.
func (s *Sources) Collect(ctx context.Context, caller authz.Caller, refs []Ref) ([]Object, error) {
	if s == nil {
		return nil, ErrNotConfigured
	}
	wanted := make(map[Kind][]Ref, len(Kinds))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		wanted[ref.Kind] = append(wanted[ref.Kind], ref)
	}

	collected := make(map[Ref]Object, len(refs))
	for _, kind := range Kinds {
		asked := wanted[kind]
		if len(asked) == 0 {
			continue
		}
		objects, err := s.sources[kind].Objects(ctx, caller, asked)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", kind, err)
		}
		for _, object := range objects {
			if object.Kind != kind {
				// A source answering with something other than what it was asked
				// for would put an object in the package under a kind nobody
				// requested, and the manifest would then be right about a
				// package that is wrong.
				return nil, fmt.Errorf("workspace: the %s source answered with a %s", kind, object.Kind)
			}
			if _, repeated := collected[object.Ref]; repeated {
				return nil, fmt.Errorf("workspace: the %s source answered %s twice", kind, object.Ref)
			}
			collected[object.Ref] = object
		}
	}

	objects := make([]Object, 0, len(refs))
	for _, ref := range refs {
		object, found := collected[ref]
		if !found {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
		}
		objects = append(objects, object)
	}
	return objects, nil
}

var _ Collector = (*Sources)(nil)
