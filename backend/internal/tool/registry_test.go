package tool

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// stubTool is a tool with no behaviour, for the registry's own tests.
type stubTool struct {
	name string
}

func (s stubTool) Definition() Definition {
	return Definition{Name: s.name, Description: "stub"}
}

func (s stubTool) Invoke(context.Context, Call) (Result, error) {
	return Result{Success: true}, nil
}

func TestRegisterAndLookup(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubTool{name: "ask_user"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if _, ok := registry.Lookup("ask_user"); !ok {
		t.Error("Lookup(ask_user) did not find the registered tool")
	}
	if _, ok := registry.Lookup("nope"); ok {
		t.Error("Lookup(nope) found a tool that was never registered")
	}
	if want := []string{"ask_user"}; !reflect.DeepEqual(registry.Names(), want) {
		t.Errorf("Names() = %v, want %v", registry.Names(), want)
	}
}

func TestRegisterRejectsBadNames(t *testing.T) {
	for _, tt := range []struct {
		name string
		tool string
	}{
		{name: "blank", tool: ""},
		{name: "only spaces", tool: "   "},
		{name: "surrounding whitespace", tool: " ask_user "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := NewRegistry().Register(stubTool{name: tt.tool}); err == nil {
				t.Errorf("Register(%q) error = nil, want a rejection", tt.tool)
			}
		})
	}
}

func TestRegisterRejectsADuplicate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubTool{name: "ask_user"}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	err := registry.Register(stubTool{name: "ask_user"})
	if err == nil {
		t.Fatal("second Register() error = nil, want a rejection")
	}
	// The original must survive: a rejected registration cannot displace it.
	if _, ok := registry.Lookup("ask_user"); !ok {
		t.Error("the rejected registration removed the original tool")
	}
}

func TestNamesAreSorted(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"web_search", "ask_user", "rag"} {
		if err := registry.Register(stubTool{name: name}); err != nil {
			t.Fatalf("Register(%q) error = %v", name, err)
		}
	}
	if want := []string{"ask_user", "rag", "web_search"}; !reflect.DeepEqual(registry.Names(), want) {
		t.Errorf("Names() = %v, want %v", registry.Names(), want)
	}
}

func TestDefinitionsKeepTheGivenOrder(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"z_last", "a_first"} {
		if err := registry.Register(stubTool{name: name}); err != nil {
			t.Fatalf("Register(%q) error = %v", name, err)
		}
	}

	definitions, err := registry.Definitions([]string{"z_last", "a_first"})
	if err != nil {
		t.Fatalf("Definitions() error = %v", err)
	}
	names := []string{definitions[0].Name, definitions[1].Name}
	if want := []string{"z_last", "a_first"}; !reflect.DeepEqual(names, want) {
		t.Errorf("Definitions() returned %v, want %v: the plane order must survive", names, want)
	}
}

func TestDefinitionsRejectAnUnknownName(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubTool{name: "ask_user"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// A plane that silently dropped the missing tool would run a turn without
	// something a capability declared.
	if _, err := registry.Definitions([]string{"ask_user", "rag"}); !errors.Is(err, ErrUnknownTool) {
		t.Errorf("Definitions() error = %v, want ErrUnknownTool", err)
	}
}

func TestDefinitionsOfNothingIsEmpty(t *testing.T) {
	definitions, err := NewRegistry().Definitions(nil)
	if err != nil {
		t.Fatalf("Definitions(nil) error = %v", err)
	}
	if len(definitions) != 0 {
		t.Errorf("Definitions(nil) = %v, want nothing", definitions)
	}
}
