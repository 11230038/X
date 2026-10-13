package main

import (
	"testing"

	"backend/internal/config"
)

// namedChatOnly is the configuration a deployment has when it predates the
// capabilities added after the general assistant: one entry, under chat's name.
func namedChatOnly() config.Config {
	return config.Config{
		LLM: config.LLMConfig{
			Model: "deepseek-chat",
			Capabilities: map[string]config.CapabilityParams{
				"chat": {
					Temperature:         0.7,
					ContextBudgetTokens: 32000,
					Stages: map[string]config.StageParams{
						"exploring":  {MaxTokens: 1024},
						"responding": {MaxTokens: 4096},
					},
				},
			},
		},
		Turn: config.TurnConfig{MaxRounds: 12},
	}
}

// TestEveryCapabilityRunsOnAConfigThatOnlyNamesChat is the reason the tuning
// lookup falls back to chat's: adding a capability must not be the same thing as
// adding a key a deployment has to write before the process will start. Every
// capability this build registers has to come out of this configuration with
// tuning it can actually run on.
func TestEveryCapabilityRunsOnAConfigThatOnlyNamesChat(t *testing.T) {
	cfg := namedChatOnly()

	registry, err := buildCapabilities(cfg)
	if err != nil {
		t.Fatalf("buildCapabilities() error = %v", err)
	}

	names := registry.Names()
	if len(names) == 0 {
		t.Fatal("the build offers no capabilities")
	}
	for _, manifest := range registry.Manifests() {
		settings := capabilitySettings(cfg, manifest.Name)
		if err := settings.Validate(manifest); err != nil {
			t.Errorf("%s would not run: %v", manifest.Name, err)
		}
	}
	// The general assistant is the one the configuration names, so it is also the
	// one a request that names nothing gets (RT-03).
	if _, ok := registry.Lookup("chat"); !ok {
		t.Error("chat is not registered")
	}
	for _, name := range []string{"ask_questions"} {
		if _, ok := registry.Lookup(name); !ok {
			t.Errorf("%s is not registered", name)
		}
	}
}

// TestACapabilityNamedInConfigurationUsesItsOwnTuning is the other half of the
// fallback: naming a capability has to change it, or the fallback would be the
// only behaviour and the key would be a decoration.
func TestACapabilityNamedInConfigurationUsesItsOwnTuning(t *testing.T) {
	cfg := namedChatOnly()
	cfg.LLM.Capabilities["ask_questions"] = config.CapabilityParams{
		Temperature:         0.1,
		ContextBudgetTokens: 8000,
		Stages:              map[string]config.StageParams{"responding": {MaxTokens: 1536}},
	}

	settings := capabilitySettings(cfg, "ask_questions")
	if settings.Temperature != 0.1 || settings.ContextBudgetTokens != 8000 {
		t.Errorf("settings = %+v, want the capability's own", settings)
	}
	if settings.StageMaxTokens["responding"] != 1536 {
		t.Errorf("StageMaxTokens = %v", settings.StageMaxTokens)
	}
	// The model and the round limit are not per capability: one process answers
	// with one model, and how many times a turn may call it is the turn's bound.
	if settings.Model != cfg.LLM.Model || settings.MaxRounds != cfg.Turn.MaxRounds {
		t.Errorf("settings = %+v", settings)
	}
}

// TestTheRegisteredCapabilitiesAreWhatTheBuildOffers guards the list itself: a
// capability that compiles but is never registered is one no client can select,
// which is the failure mode of forgetting a line here rather than of writing one
// wrongly.
func TestTheRegisteredCapabilitiesAreWhatTheBuildOffers(t *testing.T) {
	registry, err := buildCapabilities(namedChatOnly())
	if err != nil {
		t.Fatalf("buildCapabilities() error = %v", err)
	}

	want := []string{"ask_questions", "chat"}
	got := registry.Names()
	if len(got) != len(want) {
		t.Fatalf("registered %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("registered %v, want %v", got, want)
		}
	}
	// Every manifest has to satisfy the registry's own rules, since a manifest
	// that would not register is a capability the process silently does not have.
	for _, manifest := range registry.Manifests() {
		if err := manifest.Validate(); err != nil {
			t.Errorf("%s would not register: %v", manifest.Name, err)
		}
	}
}
