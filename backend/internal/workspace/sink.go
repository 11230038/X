package workspace

// Putting a package back, one kind at a time.
//
// This file is collect.go's mirror image and it is deliberately its shape: a
// narrow port per kind, declared here, implemented in the process's own assembly
// (workspacesinks.go), and joined into one Writer by a Sinks that knows nothing
// about any domain. The two directions read the same way on purpose — a reader
// who has understood the export's four sources has understood the import's four
// sinks.
//
// Three methods per kind rather than two, and the third is the one worth
// arguing about. `Undo` exists because §2.13 chose recovery-by-record over
// rollback: a package can be tens of thousands of rows, which cannot be one
// transaction, so "take it back" has to be a set of deletes driven by a list of
// what was written. A sink that had no inverse would be a kind an import could
// write and never take back, which is not a partial feature but a broken promise
// — the recovery button would exist and would quietly do nothing for that kind.
//
// What a sink may assume: the objects it is given are the objects of its own
// kind, in the package's order, each exactly once. Sinks does that. What it may
// not assume is that the world has not moved since the plan: a preview and a
// commit are two requests and a row may have appeared between them. A write that
// collides with one is a failure of the job, which is recoverable, rather than a
// silent overwrite.

import (
	"context"
	"fmt"

	"backend/internal/authz"
)

// Sink writes the objects of one kind, and takes them back again.
type Sink interface {
	// Plan reports what writing these objects would do, and writes nothing at
	// all. It answers one item per object, in the order it was given them.
	Plan(ctx context.Context, caller authz.Caller, objects []Object) ([]Item, error)
	// Write writes these objects and reports the ones that landed.
	//
	// It returns what it wrote *as well as* the error, because a batch that
	// failed halfway through wrote the first half. The caller records that half:
	// what is not written down is what a recovery cannot take back, and a
	// recovery that takes back less than it should is worse than one that says it
	// could not.
	Write(ctx context.Context, caller authz.Caller, objects []Object) ([]Written, error)
	// Undo takes back rows this sink wrote, by the ids it reported for them.
	//
	// It is given only refs of its own kind. Undoing a row that is already gone
	// is not an error: recovery runs on a job that failed, and a batch that
	// failed halfway through may have written less than it reported — the two
	// answers "it was there and now it is not" and "it was never there" are the
	// same answer to the person pressing the button.
	Undo(ctx context.Context, caller authz.Caller, written []Written) error
}

// Writer is how the service puts the objects of a package back.
//
// It is a port for the reason Collector is: the job state machine, the batching
// and the recovery are worth testing without four domains behind them, and the
// part that knows what a material is belongs in the assembly where both sides
// are visible.
type Writer interface {
	// Plan answers one item per object, in the order it was given them.
	Plan(ctx context.Context, caller authz.Caller, objects []Object) ([]Item, error)
	// Write writes the objects and reports the ones that landed.
	Write(ctx context.Context, caller authz.Caller, objects []Object) ([]Written, error)
	// Undo takes back what was written.
	Undo(ctx context.Context, caller authz.Caller, written []Written) error
}

// Sinks is the Writer over one Sink per kind.
type Sinks struct {
	sinks map[Kind]Sink
}

// NewSinks builds a writer, and refuses one that cannot answer for every kind.
//
// The refusal is the same one NewSources makes and for the same reason: a kind
// in the closed set that has no sink is a kind every export can pack and no
// import can unpack, and without this check the first anybody would hear about
// it is a user whose package was accepted and then half-written.
func NewSinks(sinks map[Kind]Sink) (*Sinks, error) {
	held := make(map[Kind]Sink, len(Kinds))
	for _, kind := range Kinds {
		if sinks[kind] == nil {
			return nil, fmt.Errorf("%w: %s has no sink", ErrNotConfigured, kind)
		}
		held[kind] = sinks[kind]
	}
	return &Sinks{sinks: held}, nil
}

// Plan asks each kind what it would do, in the closed set's order.
func (s *Sinks) Plan(ctx context.Context, caller authz.Caller, objects []Object) ([]Item, error) {
	if s == nil {
		return nil, ErrNotConfigured
	}
	grouped, err := s.group(objects)
	if err != nil {
		return nil, err
	}
	answered := make(map[Ref]Item, len(objects))
	for _, kind := range Kinds {
		asked := grouped[kind]
		if len(asked) == 0 {
			continue
		}
		items, err := s.sinks[kind].Plan(ctx, caller, asked)
		if err != nil {
			return nil, fmt.Errorf("plan %s: %w", kind, err)
		}
		if len(items) != len(asked) {
			// A kind that answered fewer items than it was given objects would
			// leave the preview silent about an object it is about to write, and
			// a preview that does not mention something is a preview that lied by
			// omission.
			return nil, fmt.Errorf("workspace: the %s sink planned %d of %d objects", kind, len(items), len(asked))
		}
		for _, item := range items {
			if item.Ref.Kind != kind {
				return nil, fmt.Errorf("workspace: the %s sink planned a %s", kind, item.Ref.Kind)
			}
			if _, repeated := answered[item.Ref]; repeated {
				return nil, fmt.Errorf("workspace: the %s sink planned %s twice", kind, item.Ref)
			}
			answered[item.Ref] = item
		}
	}
	return s.inOrder(objects, func(object Object) (Item, bool) {
		item, found := answered[object.Ref]
		return item, found
	})
}

// Write hands each kind its own objects and reports what landed.
func (s *Sinks) Write(ctx context.Context, caller authz.Caller, objects []Object) ([]Written, error) {
	if s == nil {
		return nil, ErrNotConfigured
	}
	grouped, err := s.group(objects)
	if err != nil {
		return nil, err
	}
	asked := make(map[Ref]struct{}, len(objects))
	for _, object := range objects {
		asked[object.Ref] = struct{}{}
	}
	written := make([]Written, 0, len(objects))
	for _, kind := range Kinds {
		batch := grouped[kind]
		if len(batch) == 0 {
			continue
		}
		done, err := s.sinks[kind].Write(ctx, caller, batch)
		// What came back is recorded whether or not the batch failed, so the
		// partial half of a failed batch is not lost. See Sink.Write.
		for _, row := range done {
			if _, wanted := asked[row.Ref()]; !wanted {
				return written, fmt.Errorf("workspace: the %s sink wrote %s, which nobody asked for", kind, row.Ref())
			}
		}
		written = append(written, done...)
		if err != nil {
			return written, fmt.Errorf("write %s: %w", kind, err)
		}
	}
	return written, nil
}

// Undo gives each kind back the rows it wrote.
func (s *Sinks) Undo(ctx context.Context, caller authz.Caller, written []Written) error {
	if s == nil {
		return ErrNotConfigured
	}
	grouped := make(map[Kind][]Written, len(Kinds))
	for _, row := range written {
		if !KnownKind(row.Kind) {
			return fmt.Errorf("%w: %q", ErrUnknownKind, row.Kind)
		}
		grouped[row.Kind] = append(grouped[row.Kind], row)
	}
	for _, kind := range Kinds {
		rows := grouped[kind]
		if len(rows) == 0 {
			continue
		}
		if err := s.sinks[kind].Undo(ctx, caller, rows); err != nil {
			return fmt.Errorf("undo %s: %w", kind, err)
		}
	}
	return nil
}

// group checks every object and sorts them by kind, keeping the order they
// arrived in inside each kind.
func (s *Sinks) group(objects []Object) (map[Kind][]Object, error) {
	grouped := make(map[Kind][]Object, len(Kinds))
	for _, object := range objects {
		if err := object.Ref.Validate(); err != nil {
			return nil, err
		}
		grouped[object.Kind] = append(grouped[object.Kind], object)
	}
	return grouped, nil
}

// inOrder rebuilds the caller's order from a set of answers keyed by ref.
func (s *Sinks) inOrder(objects []Object, answer func(Object) (Item, bool)) ([]Item, error) {
	items := make([]Item, 0, len(objects))
	for _, object := range objects {
		item, found := answer(object)
		if !found {
			return nil, fmt.Errorf("%w: %s was never planned", ErrNotFound, object.Ref)
		}
		items = append(items, item)
	}
	return items, nil
}

// Ref is the written row as a ref, which is how a set of them is keyed and how
// one is grouped by kind.
func (w Written) Ref() Ref { return Ref{Kind: w.Kind, ID: w.ID} }

var _ Writer = (*Sinks)(nil)
