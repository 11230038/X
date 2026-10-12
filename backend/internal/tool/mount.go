package tool

import "sort"

// Gate mounts a group of tools when a context source is present, such as a
// knowledge base or an attachment. A gate is data rather than a predicate so
// that the whole auto-mount table can be read and tested in one place.
type Gate struct {
	// Flag names the context source this gate watches for.
	Flag string
	// Tools are mounted, in this order, when the flag is set.
	Tools []string
}

// Policy is a deployment's rules for assembling a tool plane. It is separate
// from the Registry because the registry answers "what can run" while a policy
// answers "what is offered here, and in what order".
type Policy struct {
	// Optional names the tools a user may switch on. This is an allow-list, not
	// a convenience: a tool that is registered but absent here cannot be
	// enabled by a request, so a caller cannot reach a tool the deployment did
	// not offer.
	Optional []string
	// Gates is the context-gated auto-mount table, in mount order.
	Gates []Gate
	// AlwaysOn is mounted for every turn, whatever the capability.
	AlwaysOn []string
}

// Request describes the context one tool plane is assembled for.
type Request struct {
	// Enabled names the optional tools the user switched on. It is a set, so
	// its order carries no meaning and the plane is sorted by name instead.
	Enabled []string
	// Flags reports which context sources are present for this turn.
	Flags map[string]bool
	// Capability lists the active capability's own tools in the order its
	// manifest declares them.
	Capability []string
	// Forced is mounted regardless of the gates above.
	Forced []string
	// Suppressed is removed last, whatever mounted it.
	Suppressed []string
}

// Compose returns the tool names to offer, in mount order.
//
// The order is fixed by the tool contract: user-enabled optional tools, then
// context-gated auto-mounts, then the active capability's tools, then the
// always-on set. Forced tools bypass every gate above them; suppressed tools are
// removed at the very end, so nothing can mount what was suppressed.
func (p Policy) Compose(req Request) []string {
	names := make([]string, 0, len(req.Enabled)+len(req.Capability)+len(p.AlwaysOn)+len(req.Forced))
	names = append(names, p.enabled(req.Enabled)...)
	for _, gate := range p.Gates {
		if req.Flags[gate.Flag] {
			names = append(names, gate.Tools...)
		}
	}
	names = append(names, req.Capability...)
	names = append(names, p.AlwaysOn...)
	names = append(names, req.Forced...)

	return without(unique(names), req.Suppressed)
}

// enabled returns the requested tools that the policy allows, de-duplicated and
// sorted. Sorting is what makes the plane independent of the order a client
// happened to send its toggles in.
func (p Policy) enabled(requested []string) []string {
	if len(p.Optional) == 0 || len(requested) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(p.Optional))
	for _, name := range p.Optional {
		allowed[name] = true
	}
	kept := make([]string, 0, len(requested))
	for _, name := range unique(requested) {
		if allowed[name] {
			kept = append(kept, name)
		}
	}
	sort.Strings(kept)
	return kept
}

// Referenced returns every tool name this policy can mount. A startup check
// compares it against the registry, so a name that is only ever reachable
// through this table cannot go missing until a turn tries to mount it.
func (p Policy) Referenced() []string {
	names := make([]string, 0, len(p.Optional)+len(p.AlwaysOn))
	names = append(names, p.Optional...)
	for _, gate := range p.Gates {
		names = append(names, gate.Tools...)
	}
	names = append(names, p.AlwaysOn...)
	return unique(names)
}

// DefaultPolicy is the tool plane this build ships.
//
// Nothing is user-switchable yet, and every tool it registers is always on, so
// the optional and gated tiers are empty. The tiers above are nevertheless the
// contract: V2 adds rows to them and changes nothing else here.
func DefaultPolicy() Policy {
	return Policy{
		AlwaysOn: []string{"ask_user"},
	}
}

// unique drops repeats while keeping first-seen order.
func unique(names []string) []string {
	seen := make(map[string]bool, len(names))
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		kept = append(kept, name)
	}
	return kept
}

// without removes the named tools, keeping the order of what remains.
func without(names []string, removed []string) []string {
	if len(removed) == 0 {
		return names
	}
	drop := make(map[string]bool, len(removed))
	for _, name := range removed {
		drop[name] = true
	}
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if !drop[name] {
			kept = append(kept, name)
		}
	}
	return kept
}
