package capability

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func registryWith(t *testing.T, manifests ...Manifest) *Registry {
	t.Helper()
	registry := NewRegistry()
	for _, manifest := range manifests {
		if err := registry.Register(manifest); err != nil {
			t.Fatalf("Register(%s) error = %v", manifest.Name, err)
		}
	}
	return registry
}

func TestRegisterAndLookup(t *testing.T) {
	registry := registryWith(t, chatManifest())

	if _, ok := registry.Lookup("chat"); !ok {
		t.Error("Lookup(chat) did not find the registered manifest")
	}
	if _, ok := registry.Lookup("deep_solve"); ok {
		t.Error("Lookup(deep_solve) found a capability that was never registered")
	}
	if want := []string{"chat"}; !reflect.DeepEqual(registry.Names(), want) {
		t.Errorf("Names() = %v, want %v", registry.Names(), want)
	}
}

func TestRegisterRejectsInvalidAndDuplicateManifests(t *testing.T) {
	registry := registryWith(t, chatManifest())

	if err := registry.Register(Manifest{Name: "broken"}); !errors.Is(err, ErrInvalidManifest) {
		t.Errorf("Register(invalid) error = %v, want ErrInvalidManifest", err)
	}
	if err := registry.Register(chatManifest()); !errors.Is(err, ErrDuplicateCapability) {
		t.Errorf("Register(duplicate) error = %v, want ErrDuplicateCapability", err)
	}
	if want := []string{"chat"}; !reflect.DeepEqual(registry.Names(), want) {
		t.Errorf("Names() = %v, want the rejected registrations to have changed nothing", want)
	}
}

func TestManifestsAreSortedByName(t *testing.T) {
	registry := registryWith(t,
		Manifest{Name: "z_second", Description: "d", Stages: []string{"a"}},
		Manifest{Name: "a_first", Description: "d", Stages: []string{"a"}},
	)

	names := make([]string, 0, 2)
	for _, manifest := range registry.Manifests() {
		names = append(names, manifest.Name)
	}
	if want := []string{"a_first", "z_second"}; !reflect.DeepEqual(names, want) {
		t.Errorf("Manifests() returned %v, want %v", names, want)
	}
}

func TestCheckToolDriftAcceptsRegisteredTools(t *testing.T) {
	withTools := chatManifest()
	withTools.Tools = []string{"rag", "ask_user"}
	registry := registryWith(t, withTools)

	if err := registry.CheckToolDrift([]string{"ask_user", "rag", "web_search"}); err != nil {
		t.Errorf("CheckToolDrift() error = %v, want nil", err)
	}
}

func TestCheckToolDriftReportsEveryMissingTool(t *testing.T) {
	// Both offenders must be named at once: a startup error that reports one
	// per restart makes the operator run the process as many times as there are
	// mistakes.
	chat := chatManifest()
	chat.Tools = []string{"rag"}
	deepSolve := Manifest{Name: "deep_solve", Description: "d", Stages: []string{"planning"}, Tools: []string{"exec"}}
	registry := registryWith(t, chat, deepSolve)

	err := registry.CheckToolDrift([]string{"ask_user"})
	if !errors.Is(err, ErrToolDrift) {
		t.Fatalf("CheckToolDrift() error = %v, want ErrToolDrift", err)
	}
	for _, want := range []string{"rag", "chat", "exec", "deep_solve"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestCheckConfigDriftAcceptsAMatchingConfiguration(t *testing.T) {
	registry := registryWith(t, chatManifest())

	config := Config{"chat": {"exploring", "responding"}}
	if err := registry.CheckConfigDrift(config); err != nil {
		t.Errorf("CheckConfigDrift() error = %v, want nil", err)
	}
	// A registered capability nobody configured is fine: a deployment should not
	// have to tune capabilities it never runs.
	if err := registry.CheckConfigDrift(Config{}); err != nil {
		t.Errorf("CheckConfigDrift(empty) error = %v, want nil", err)
	}
}

func TestCheckConfigDriftRejectsMismatches(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config Config
		want   []string
	}{
		{
			name:   "unregistered capability",
			config: Config{"deep_solve": {"planning"}},
			want:   []string{"deep_solve", "not a registered capability"},
		},
		{
			name:   "stage the manifest does not declare",
			config: Config{"chat": {"exploring", "responding", "thinking"}},
			want:   []string{"thinking", "does not declare"},
		},
		{
			name:   "declared stage left unconfigured",
			config: Config{"chat": {"exploring"}},
			want:   []string{"responding", "unconfigured"},
		},
		{
			name:   "both directions at once",
			config: Config{"chat": {"thinking"}, "nope": {"a"}},
			want:   []string{"chat", "nope", "thinking"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := registryWith(t, chatManifest()).CheckConfigDrift(tt.config)
			if !errors.Is(err, ErrConfigDrift) {
				t.Fatalf("CheckConfigDrift() error = %v, want ErrConfigDrift", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestCheckConfigDriftReadsTheSameEveryRun(t *testing.T) {
	// The offender lists come out of maps, so this pins that they are sorted
	// rather than left in map iteration order.
	registry := registryWith(t, chatManifest())
	config := Config{"chat": {"zulu", "alpha", "mike"}}

	first := registry.CheckConfigDrift(config)
	for i := 0; i < 32; i++ {
		if got := registry.CheckConfigDrift(config); got.Error() != first.Error() {
			t.Fatalf("run %d: %q, first run: %q", i, got, first)
		}
	}
	if !strings.Contains(first.Error(), "alpha, mike, zulu") {
		t.Errorf("error %q does not list the stages in sorted order", first)
	}
}
