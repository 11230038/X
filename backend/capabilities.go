package main

// What this build offers: the capabilities it registers, the tuning each is built
// from, and the checks that refuse a configuration describing a different build.
//
// It is apart from assemble.go because it answers a different question. assemble
// builds a process — a database, a socket, a knowledge base — and needs all of
// them to be reachable; this file reads the deployment's file and the manifests in
// the tree and decides whether the two describe the same program. Nothing here
// touches a database, which is what lets a test ask what the build offers on its
// own (see buildCapabilities).

import (
	"fmt"
	"sort"
	"strings"

	"backend/internal/askquestions"
	"backend/internal/authz"
	"backend/internal/capability"
	"backend/internal/chat"
	"backend/internal/config"
	"backend/internal/deepquestion"
	"backend/internal/deepsolve"
	"backend/internal/masterypath"
	"backend/internal/reading"
	"backend/internal/tool"
)

// checkDrift refuses a configuration that does not describe this build.
//
// Each check is about a pair of things that are written down twice: the tools a
// manifest names and the tools that were registered, and the capabilities and
// stages the tuning names and the ones the manifests declare. A mismatch means
// the process would start and then fail on the first turn that touched it.
func checkDrift(capabilities *capability.Registry, policy tool.Policy, registered []string, configured capability.Config) error {
	if err := capabilities.CheckToolDrift(registered); err != nil {
		return fmt.Errorf("capability manifests name tools that are not registered: %w", err)
	}
	if err := capabilities.CheckConfigDrift(configured); err != nil {
		return err
	}
	known := make(map[string]bool, len(registered))
	for _, name := range registered {
		known[name] = true
	}
	for _, name := range policy.Referenced() {
		if !known[name] {
			return fmt.Errorf("the tool policy names %s, which is not a registered tool", name)
		}
	}
	return nil
}

// checkAuthzDrift refuses a learner's baseline that describes a different build.
//
// It is the same check as the tool and Tuning ones and it is about the same
// pair of things written down twice: §5 says AUTHZ_LEARNER_CAPABILITIES and
// AUTHZ_LEARNER_SURFACES must name "这个 build 注册过的", and the two files
// that know what this build registered are the capability registry and
// authz.Surfaces. A name in neither is refused here rather than absorbed,
// because absorbing it is silent both ways — a capability nobody implemented
// grants nothing, and a surface with no route grants nothing either, so a
// deployment that misspelled one would start up and hand out a learner who
// cannot reach what the operator believes they can.
//
// A profile name is read through authz.NormalizePreset, so the check is the
// same reading the policy does rather than a second list of the three presets.
func checkAuthzDrift(authzConfig config.AuthzConfig, capabilities *capability.Registry) error {
	if normalized := authz.NormalizePreset(authzConfig.DefaultPreset); normalized != authzConfig.DefaultPreset {
		return fmt.Errorf("AUTHZ_DEFAULT_PRESET must be one of standard, learner or custom")
	}
	registered := make(map[string]bool, len(capabilities.Names()))
	for _, name := range capabilities.Names() {
		registered[name] = true
	}
	for _, name := range authzConfig.LearnerCapabilities {
		if !registered[name] {
			return fmt.Errorf("AUTHZ_LEARNER_CAPABILITIES names %s, which is not a capability this build registers", name)
		}
	}
	for _, name := range authzConfig.LearnerSurfaces {
		if !authz.KnownSurface(name) {
			return fmt.Errorf("AUTHZ_LEARNER_SURFACES names %s, which is not a surface this build serves", name)
		}
	}
	return nil
}

// buildCapabilities registers every capability this build offers.
//
// It is a function rather than a block inside assemble so that the list of
// capabilities, and the tuning each is built from, can be checked without a
// database, a socket and a knowledge base service: what a process offers is a
// property of the build, and a test can ask about it on its own.
func buildCapabilities(cfg config.Config) (*capability.Registry, error) {
	registry := capability.NewRegistry()
	for _, register := range []struct {
		name     string
		manifest func() capability.Manifest
		build    func(capability.Settings) (capability.Capability, error)
	}{
		{chat.Name, chat.Manifest, func(s capability.Settings) (capability.Capability, error) {
			return chat.New(s)
		}},
		{askquestions.Name, askquestions.Manifest, func(s capability.Settings) (capability.Capability, error) {
			return askquestions.New(s)
		}},
		{deepsolve.Name, deepsolve.Manifest, func(s capability.Settings) (capability.Capability, error) {
			return deepsolve.New(s)
		}},
		// The mastery path is the one capability whose tool calls need something
		// the capability itself cannot hold: the path, which belongs to the turn.
		// The capability is one per process and the path is one per turn, so the
		// capability is built like any other and the runtime installs the path
		// around the turn's tools (masteryPath.Claim).
		{masterypath.Name, masterypath.Manifest, func(s capability.Settings) (capability.Capability, error) {
			return masterypath.New(s)
		}},
		// The question writer takes two more numbers than the others, and they are
		// the deployment's own: how many questions a request that says nothing
		// gets, and how many one request may ask for. Both are read from
		// configuration here rather than looked up inside the package, so that a
		// capability has no way to reach the environment.
		{deepquestion.Name, deepquestion.Manifest, func(s capability.Settings) (capability.Capability, error) {
			return deepquestion.New(s, deepquestion.Limits{
				Default: cfg.Practice.QuestionDefaultCount,
				Max:     cfg.Practice.QuestionMaxCount,
			})
		}},
		// Immersive reading is built like the general assistant and differs from
		// it in one thing: the passage the reader selected. That is material the
		// turn carries rather than something the capability looks up, so it needs
		// no seam of its own — the runtime puts it in the survey every grounded
		// turn already runs (§2.8).
		{reading.Name, reading.Manifest, func(s capability.Settings) (capability.Capability, error) {
			return reading.New(s)
		}},
	} {
		settings, err := capabilitySettings(cfg, register.manifest())
		if err != nil {
			return nil, err
		}
		built, err := register.build(settings)
		if err != nil {
			return nil, fmt.Errorf("initialize the %s capability: %w", register.name, err)
		}
		if err := registry.Register(built); err != nil {
			return nil, fmt.Errorf("register the %s capability: %w", register.name, err)
		}
	}
	return registry, nil
}

// capabilitySettings is one capability's tuning, resolved from configuration.
//
// A capability the deployment has not named runs on the general assistant's
// tuning. Most of them are the same model answering the same user in the same
// language, with one stage instead of two, so a deployment that never tunes them
// should not have to write the same numbers out again under three more names —
// and a capability being registered should not be a reason for a process that
// started yesterday to refuse to start today. Naming one in
// LLM_CAPABILITY_CONFIG_JSON overrides it, which is also how it is tuned.
//
// The stage limits are the one part that cannot be inherited blindly: they are
// keyed by stage name, so a capability whose stages are its own — the question
// writer has two, and neither is chat's — would receive a tuning with no key for
// anything it runs. That is refused here with the key to write, rather than left
// to fail inside the capability, because it is a fact about the deployment's
// file: it runs a build whose question writer it never configured.
func capabilitySettings(cfg config.Config, manifest capability.Manifest) (capability.Settings, error) {
	params, ok := cfg.LLM.Capabilities[manifest.Name]
	if !ok {
		params = cfg.LLM.Capabilities[chat.Name]
	}
	stages := make(map[string]int, len(params.Stages))
	for stage, tuning := range params.Stages {
		stages[stage] = tuning.MaxTokens
	}
	// A tuning with no stages at all is not an inherited one but an absent one:
	// "nothing is configured" is the capability's own settings refusing, and it
	// says more about what is missing than this check could.
	if len(stages) > 0 {
		missing := make([]string, 0, len(manifest.Stages))
		for _, stage := range manifest.Stages {
			if stages[stage] <= 0 {
				missing = append(missing, stage)
			}
		}
		if len(missing) > 0 {
			return capability.Settings{}, fmt.Errorf(
				"%s runs the stages %s and the capability tuning covers %s: give %s an entry of its own "+
					"in LLM_CAPABILITY_CONFIG_JSON naming those stages (per-stage limits are looked up "+
					"by stage name, CFG-01)",
				manifest.Name, strings.Join(missing, ", "), strings.Join(sortedStages(stages), ", "), manifest.Name)
		}
	}
	return capability.Settings{
		Model:               cfg.LLM.Model,
		Temperature:         params.Temperature,
		ContextBudgetTokens: params.ContextBudgetTokens,
		MaxRounds:           cfg.Turn.MaxRounds,
		StageMaxTokens:      stages,
	}, nil
}

// sortedStages lists the stage names a tuning covers, for a message that says
// which ones it has when the one being asked for is not among them.
func sortedStages(stages map[string]int) []string {
	names := make([]string, 0, len(stages))
	for stage := range stages {
		names = append(names, stage)
	}
	sort.Strings(names)
	return names
}

// capabilityConfig is which stages each configured capability declares, which
// is what the drift check compares against the manifests.
func capabilityConfig(cfg config.Config) capability.Config {
	configured := make(capability.Config, len(cfg.LLM.Capabilities))
	for name, params := range cfg.LLM.Capabilities {
		stages := make([]string, 0, len(params.Stages))
		for stage := range params.Stages {
			stages = append(stages, stage)
		}
		sort.Strings(stages)
		configured[name] = stages
	}
	return configured
}
