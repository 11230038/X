package workspace

// What a preview says, object by object.
//
// §7.3's eighth item is the shape of this file: the screen has to be able to
// say, one object at a time, what will be written, what it collides with, and
// what it needs that the package does not carry. Three answers — and no error,
// because a collision is an item in a list rather than a refusal. The user is
// reading a plan, not a failure report, and a preview that answered "conflict"
// as an error would answer the question by refusing to answer it.
//
// The three actions are also the contract between the two requests. What the
// preview calls `create` is exactly what the commit writes; `conflict` and
// `missing` are the two things it will not. That alignment is what makes the
// preview worth reading rather than a second opinion.

import "fmt"

// Action is what an import will do to one object.
type Action string

const (
	// ActionCreate is an object that will be written.
	ActionCreate Action = "create"
	// ActionConflict is an object this instance already has. It is not an error
	// and it is not an overwrite: an import never replaces what is already here,
	// because the package is somebody's copy of their own work and the row it
	// collides with is the row they already have. Which of the two is newer is
	// not a question a file's modification time or a job's timestamps can answer,
	// so the import answers the only question it can: leave it alone.
	ActionConflict Action = "conflict"
	// ActionMissing is an object that will not be written because something it
	// needs is not in the package. §2.13 names the case: a question pointing at a
	// material that is not in the pack. The alternative — writing it anyway — is
	// a row that dangles, and a dangle reported as a write is worse than a skip
	// reported as a skip.
	ActionMissing Action = "missing"
)

// Actions is every action, for validation and for a reader that wants to cover
// them all rather than the ones somebody remembered.
var Actions = []Action{ActionCreate, ActionConflict, ActionMissing}

// KnownAction reports whether a name is an action this build reports.
func KnownAction(action Action) bool {
	for _, known := range Actions {
		if known == action {
			return true
		}
	}
	return false
}

// Reasons an item carries. They are codes rather than sentences for the reason
// the failure codes are: a sentence is free text, and free text in a column a
// client renders is a column where a filename lands.
const (
	// ReasonAlreadyPresent is a conflict's reason: the id is in use.
	ReasonAlreadyPresent = "already_present"
	// ReasonDependsOn is a miss's reason: what is named in Needs is not in the
	// package.
	ReasonDependsOn = "depends_on"
)

// Item is one line of a preview: one object, and what will happen to it.
//
// Needs is a list because a miss can have more than one cause, and naming only
// the first would make a second preview necessary to find the second.
type Item struct {
	Ref    Ref    `json:"ref"`
	Action Action `json:"action"`
	// Reason is the code above, and it is empty on a create.
	Reason string `json:"reason,omitempty"`
	// Needs are the refs the object depends on that the package does not carry.
	// They are what a user has to go and pack — or find on this instance — before
	// the object can be written.
	Needs []Ref `json:"needs,omitempty"`
}

// validate refuses an item this build may not report.
//
// A `create` carrying a reason, or a `missing` carrying none, is a line whose
// two halves disagree; a client that rendered it would have to decide which half
// to believe.
func (i Item) validate() error {
	if err := i.Ref.Validate(); err != nil {
		return err
	}
	if !KnownAction(i.Action) {
		return fmt.Errorf("workspace: %q is not an action this build reports", i.Action)
	}
	switch i.Action {
	case ActionCreate:
		if i.Reason != "" || len(i.Needs) != 0 {
			return fmt.Errorf("workspace: %s is a create and cannot carry a reason or a need", i.Ref)
		}
	case ActionConflict:
		if i.Reason != ReasonAlreadyPresent || len(i.Needs) != 0 {
			return fmt.Errorf("workspace: %s is a conflict without the conflict's reason", i.Ref)
		}
	case ActionMissing:
		if i.Reason != ReasonDependsOn || len(i.Needs) == 0 {
			return fmt.Errorf("workspace: %s is a miss and has to name what it needs", i.Ref)
		}
	}
	for _, need := range i.Needs {
		if err := need.Validate(); err != nil {
			return err
		}
		if need == i.Ref {
			return fmt.Errorf("workspace: %s needs itself", i.Ref)
		}
	}
	return nil
}

// Preview is the answer to a preview request.
//
// JobID is what the commit names. A preview that answered only a list of items
// would leave the second request to guess which package was previewed — and the
// whole of §2.13's first discipline is that the thing that was looked at is the
// thing that gets written.
type Preview struct {
	JobID string `json:"job_id"`
	// Items are every object in the package, in the package's own order, each
	// with what will happen to it.
	Items []Item `json:"items"`
	// Objects is how many the package holds, so that a client does not have to
	// count the list to say "247 objects".
	Objects int `json:"objects"`
	// Actions is how many objects each action accounts for. A client draws the
	// three numbers before it draws the list, and the numbers are here rather
	// than computed there for the same reason the counts on an export are: two
	// places that add up the same thing is two places that can disagree.
	Actions map[string]int `json:"actions"`
}

// newPreview summarises the items, and refuses a set this build may not report.
func newPreview(jobID string, items []Item) (Preview, error) {
	summary := make(map[string]int, len(Actions))
	for _, item := range items {
		if err := item.validate(); err != nil {
			return Preview{}, err
		}
		summary[string(item.Action)]++
	}
	return Preview{JobID: jobID, Items: items, Objects: len(items), Actions: summary}, nil
}

// creates is the objects a preview said would be written, as refs.
//
// It is a set and not a count because the commit asks "is this one of them" once
// per object it is about to write, and a list would make that a scan.
type creates map[Ref]struct{}

func createsOf(items []Item) creates {
	held := make(creates)
	for _, item := range items {
		if item.Action == ActionCreate {
			held[item.Ref] = struct{}{}
		}
	}
	return held
}

// filter keeps the objects an item set called creatable, in the order given.
//
// The order is the package's, so two commits of one package write the same rows
// in the same sequence — which is what makes a failure in a batch reproducible
// rather than a race.
func (c creates) filter(objects []Object) []Object {
	kept := make([]Object, 0, len(objects))
	for _, object := range objects {
		if _, wanted := c[object.Ref]; wanted {
			kept = append(kept, object)
		}
	}
	return kept
}
