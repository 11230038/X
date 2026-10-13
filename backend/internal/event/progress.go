package event

// The metadata keys a progress event carries.
//
// Three keys and no more, which is what makes the payload readable by a client
// that has never heard of the capability publishing it: a turn that walks a plan
// is on step N of M, and the step has a name. Anything a particular capability
// wants to say beyond that belongs on the tool events, which are the record.
const (
	// MetaStep is the step the turn is on, counting from one.
	MetaStep = "step"
	// MetaTotal is how many steps the turn's plan has.
	MetaTotal = "total"
	// MetaLabel is that step's own text.
	MetaLabel = "label"
)
