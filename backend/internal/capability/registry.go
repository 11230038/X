package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Registry holds the capabilities a process offers.
//
// Registration happens at startup and lookups happen per turn, so the map is
// read concurrently only after registration is finished; Register must not be
// called once turns are being served.
type Registry struct {
	manifests map[string]Manifest
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{manifests: make(map[string]Manifest)}
}

// Register adds a manifest, rejecting an invalid one and a name already taken.
// Registering a capability is the only way it becomes reachable, so a request
// for a name that is not here cannot select behaviour nobody declared.
func (r *Registry) Register(manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if _, exists := r.manifests[manifest.Name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateCapability, manifest.Name)
	}
	r.manifests[manifest.Name] = manifest
	return nil
}

// Lookup returns the named manifest.
func (r *Registry) Lookup(name string) (Manifest, bool) {
	manifest, ok := r.manifests[name]
	return manifest, ok
}

// Names returns every registered name, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.manifests))
	for name := range r.manifests {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Manifests returns every registered manifest, sorted by name so that a check
// over all of them reports its findings in the same order on every run.
func (r *Registry) Manifests() []Manifest {
	manifests := make([]Manifest, 0, len(r.manifests))
	for _, name := range r.Names() {
		manifests = append(manifests, r.manifests[name])
	}
	return manifests
}

// CheckToolDrift reports every tool the manifests declare that the tool
// registry does not offer.
//
// A non-empty result must stop the process. A manifest naming a tool that does
// not exist describes a capability that would run with part of its tool plane
// missing, which is a failure the model would otherwise discover mid-turn.
func (r *Registry) CheckToolDrift(registered []string) error {
	known := make(map[string]bool, len(registered))
	for _, name := range registered {
		known[name] = true
	}

	var problems []string
	for _, manifest := range r.Manifests() {
		for _, name := range manifest.Tools {
			if !known[name] {
				problems = append(problems, fmt.Sprintf("%s (declared by %s)", name, manifest.Name))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrToolDrift, strings.Join(problems, ", "))
}

// CheckConfigDrift reports capability tuning that no manifest can satisfy: a
// configured capability that is not registered, or a configured capability
// whose stages are not exactly the ones its manifest declares.
//
// Coverage is required in both directions because a stage's token limit has no
// default: configuring a stage the manifest does not declare is a misspelling,
// and leaving a declared stage unconfigured would send a turn into it with no
// limit at all.
//
// It is one-directional in one respect only: a registered capability that
// nobody configured is fine, since a deployment should not have to tune
// capabilities it never runs.
func (r *Registry) CheckConfigDrift(config Config) error {
	var problems []string
	for _, name := range sortedKeys(config) {
		manifest, ok := r.manifests[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is not a registered capability", name))
			continue
		}
		undeclared, unconfigured := stageDiff(manifest, config[name])
		if len(undeclared) > 0 {
			problems = append(problems, fmt.Sprintf("%s does not declare the configured stages %s",
				name, strings.Join(undeclared, ", ")))
		}
		if len(unconfigured) > 0 {
			problems = append(problems, fmt.Sprintf("%s leaves the stages %s unconfigured",
				name, strings.Join(unconfigured, ", ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrConfigDrift, strings.Join(problems, "; "))
}

// stageDiff returns the configured stages the manifest does not declare, and
// the declared stages the configuration does not cover. Both are sorted and
// de-duplicated so that the resulting error reads the same way every run.
func stageDiff(manifest Manifest, configured []string) (undeclared, unconfigured []string) {
	declared := make(map[string]bool, len(manifest.Stages))
	for _, stage := range manifest.Stages {
		declared[stage] = true
	}
	covered := make(map[string]bool, len(configured))
	for _, stage := range configured {
		if stage != "" {
			covered[stage] = true
		}
	}

	for stage := range covered {
		if !declared[stage] {
			undeclared = append(undeclared, stage)
		}
	}
	for stage := range declared {
		if !covered[stage] {
			unconfigured = append(unconfigured, stage)
		}
	}
	sort.Strings(undeclared)
	sort.Strings(unconfigured)
	return undeclared, unconfigured
}

func sortedKeys(config Config) []string {
	names := make([]string, 0, len(config))
	for name := range config {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
