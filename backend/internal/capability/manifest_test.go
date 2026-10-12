package capability

import (
	"errors"
	"testing"
)

// chatManifest is the shape the one V1 capability registers with.
func chatManifest() Manifest {
	return Manifest{
		Name:        "chat",
		Description: "通用对话，适合问答、解释与讨论",
		Stages:      []string{"exploring", "responding"},
	}
}

func TestManifestValidateAcceptsTheShippedShape(t *testing.T) {
	if err := chatManifest().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	// A capability whose needs the always-on tools already meet declares none.
	withoutTools := chatManifest()
	withoutTools.Tools = nil
	if err := withoutTools.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want a capability with no tools to be valid", err)
	}
}

func TestManifestValidateRejectsBadManifests(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "blank name", mutate: func(m *Manifest) { m.Name = "" }},
		{name: "whitespace name", mutate: func(m *Manifest) { m.Name = "  " }},
		{name: "padded name", mutate: func(m *Manifest) { m.Name = " chat " }},
		{name: "blank description", mutate: func(m *Manifest) { m.Description = "   " }},
		{name: "no stages", mutate: func(m *Manifest) { m.Stages = nil }},
		{name: "blank stage", mutate: func(m *Manifest) { m.Stages = []string{"exploring", ""} }},
		{name: "repeated stage", mutate: func(m *Manifest) { m.Stages = []string{"exploring", "exploring"} }},
		{name: "blank tool", mutate: func(m *Manifest) { m.Tools = []string{"rag", " "} }},
		{name: "repeated tool", mutate: func(m *Manifest) { m.Tools = []string{"rag", "rag"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manifest := chatManifest()
			tt.mutate(&manifest)
			err := manifest.Validate()
			if !errors.Is(err, ErrInvalidManifest) {
				t.Errorf("Validate() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}
