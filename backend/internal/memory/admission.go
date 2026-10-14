package memory

import "strings"

// The admission gate: how one model output becomes a fact (§2.7).
//
// MEM-03 forbids turning an unverified model output straight into a long-term
// fact, and the mechanism that forbids it is this file. An observation that does
// not clear the gate is not deleted and not marked as rejected: it stays in L1,
// visible, and a later observation of the same thing can be the one that carries
// it over.
//
// Three conditions, all of which have to hold:
//
//  1. the confidence the surface reported is at or above
//     MEMORY_CONFIDENCE_THRESHOLD. This is the weakest of the three, and
//     deliberately: a model's self-reported confidence is the model talking
//     about itself, so this condition *narrows* the field rather than settling
//     anything.
//  2. at least MEMORY_MIN_CORROBORATION independent observations of the same
//     fingerprint, from at least MEMORY_MIN_SURFACES different surfaces.
//     Independence is by turn — the same thing said twice in one turn is one
//     voice — and this is where "未经证实" earns the word 证实: the same thing
//     said again in the same place is repetition, and said in two places it is
//     evidence.
//  3. the kind is not one of MEMORY_SENSITIVE_KINDS. This condition is absolute:
//     it does not look at confidence and does not look at corroboration, because
//     the reason it exists is that for these kinds there is no amount of
//     corroboration a machine is allowed to call proof.
//
// And the fourth path: an observation the *user* wrote down themselves skips all
// three. The user's own word is not an exception to MEM-03, it is what MEM-03 is
// protecting: a fact the user stated is a fact the user confirmed.
//
// That path is recognised by the surface *and* the kind together, which is one
// word more than the design asks for and closes a hole it does not name. A model
// does not choose a surface — the surfaces are constants in the packages that
// write them — but the kind travels with what a model saw, and a gate that keyed
// the fourth path on the kind alone would be a gate a prompt could walk through
// by asserting the user said something. So the route is SurfaceUser and
// KindUserStated, and neither alone is a route at all.
//
// Everything here is a pure function of its arguments. It reads no clock, no
// storage and no configuration beyond the Options it is handed, which is what
// lets every case about the gate be a case about the rules.

// admission is what the gate decided about one observation, and why.
//
// The counts are kept whether the answer was yes or no: a refused observation's
// counts are the answer to "how far off was it", which is what the interface
// needs in order to show a proposed observation as something other than a
// silence (§7.3).
type admission struct {
	// admitted is true when the observation is evidence for a fact.
	admitted bool
	// confirmedBy is which route admitted it.
	confirmedBy Confirmer
	// confidence is the number the fact is written with: the one the admitting
	// observation reported. The earlier observations keep their own story — on
	// their surfaces, in their conversations and in the order they arrived — and
	// they are not asked to be confident in hindsight.
	confidence int
	// sensitive is true when the kind is one of the deployment's sensitive ones.
	// It is only ever set on a fact the user wrote: for everyone else it is the
	// condition that stopped the admission.
	sensitive bool
	// sources are the L1 rows that are this fact's evidence (MEM-01).
	sources []string
	// observations is the number of independent observations counted, and
	// surfaces the number of distinct surfaces they came from. One turn counts
	// once however many times it spoke.
	observations int
	surfaces     int
}

// decide is the gate.
//
// stored is what the account's L1 holds for this fingerprint *including this
// observation's own row*, which the caller has already written: an observation is
// stored whether or not it is believed, so the row exists by the time anyone
// asks what it means, and counting rows is one rule instead of two.
func decide(observation Observation, stored []Trace, options Options) admission {
	counted := gather(stored)
	held := admission{observations: counted.observations, surfaces: counted.surfaces}
	admitted := func(by Confirmer) admission {
		return admission{
			admitted:     true,
			confirmedBy:  by,
			confidence:   observation.Confidence,
			sensitive:    options.IsSensitive(string(observation.Kind)),
			sources:      counted.sources,
			observations: counted.observations,
			surfaces:     counted.surfaces,
		}
	}

	switch {
	case observation.writtenByUser():
		// The fourth path. It skips the three conditions below — and the evidence
		// is still collected, because what the user asserts and what the surfaces
		// already noticed are the same claim, and MEM-01 asks for the chain.
		return admitted(ConfirmerUser)
	case options.IsSensitive(string(observation.Kind)):
		return held
	case observation.Confidence < options.ConfidenceThreshold:
		return held
	case counted.observations < options.MinCorroboration, counted.surfaces < options.MinSurfaces:
		return held
	default:
		return admitted(ConfirmerGate)
	}
}

// corroboration is what the account's L1 says about one fingerprint: how many
// voices, from how many places, and which rows.
type corroboration struct {
	observations int
	surfaces     int
	sources      []string
}

// gather counts the independent observations among the rows for one fingerprint.
//
// Selecting the rows is the caller's job and it happens in SQL, where the
// fingerprint is an index lookup: reading an account's whole L1 to find the
// handful of rows that say the same thing would be a scan that grows with
// everything the account has ever been told. What is counted here is the rules'
// business, which is why it is here and not in a query.
func gather(stored []Trace) corroboration {
	var counted corroboration
	voices := make(map[string]bool, len(stored))
	places := make(map[Surface]bool, len(stored))
	for _, trace := range stored {
		voice := trace.TurnID
		if voice == "" {
			// An observation made outside any turn is its own voice: there is no
			// turn for it to be folded into, and the row's id is the only thing
			// that can tell two such observations apart.
			voice = trace.ID
		}
		if !voices[voice] {
			voices[voice] = true
			counted.observations++
		}
		places[trace.Surface] = true
		counted.sources = append(counted.sources, trace.ID)
	}
	counted.surfaces = len(places)
	return counted
}

// IsSensitive reports whether a kind is one that never passes the gate by
// itself.
//
// Case and outer whitespace are folded, because the two sides of this
// comparison are written by different people — one in a deployment's file, one
// in code — and a spelling difference must not be the thing that decides whether
// a health observation is admitted without the user being asked about it.
func (o Options) IsSensitive(kind string) bool {
	folded := strings.ToLower(strings.TrimSpace(kind))
	for _, sensitive := range o.SensitiveKinds {
		if strings.ToLower(strings.TrimSpace(sensitive)) == folded {
			return true
		}
	}
	return false
}
