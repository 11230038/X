package capability

import (
	"fmt"
	"strings"
)

// Manifest declares one capability. It is what the runtime routes by, what the
// tool plane is composed from, and what a client reads to discover capabilities.
//
// Tuning is deliberately absent: temperature and per-stage token limits come
// from the environment, which is the only place configuration lives, so a
// manifest that also carried defaults would be a second, conflicting source.
type Manifest struct {
	// Name is the identifier a request asks for and the runtime reports events
	// under.
	Name string
	// Description is shown to clients discovering the capability, so a blank one
	// would leave the catalogue with a nameless entry.
	Description string
	// Stages are the capability's phases in the order it walks them. The order
	// is part of the manifest because the runtime emits stage boundaries in it.
	Stages []string
	// Tools are the tools this capability adds to the plane, in mount order.
	// A capability whose needs are met by the always-on set declares none.
	Tools []string
}

// Validate reports why a manifest could not be registered.
//
// Every problem is structural rather than stylistic: a blank or repeated name
// would make registration order decide behaviour, and a blank stage or tool
// would put a name into the plane that nothing can resolve.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: name is blank", ErrInvalidManifest)
	}
	if m.Name != strings.TrimSpace(m.Name) {
		return fmt.Errorf("%w: name %q has surrounding whitespace", ErrInvalidManifest, m.Name)
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("%w: %s has no description", ErrInvalidManifest, m.Name)
	}
	if len(m.Stages) == 0 {
		return fmt.Errorf("%w: %s declares no stages", ErrInvalidManifest, m.Name)
	}
	if err := checkNames(m.Name, "stage", m.Stages); err != nil {
		return err
	}
	return checkNames(m.Name, "tool", m.Tools)
}

// checkNames rejects blank and repeated entries in an ordered name list.
func checkNames(capability, kind string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: %s has a blank %s name", ErrInvalidManifest, capability, kind)
		}
		if seen[name] {
			return fmt.Errorf("%w: %s declares %s %q twice", ErrInvalidManifest, capability, kind, name)
		}
		seen[name] = true
	}
	return nil
}
