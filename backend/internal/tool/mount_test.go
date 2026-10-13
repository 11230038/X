package tool

import (
	"reflect"
	"testing"
)

// testPolicy exercises every tier. The optional list is deliberately not in
// alphabetical order so that a test expecting sorted output fails if the
// sorting is dropped.
func testPolicy() Policy {
	return Policy{
		Optional: []string{"web_search", "notebook"},
		Gates: []Gate{
			{Flag: "has_kb", Tools: []string{"rag", "kb_files"}},
			{Flag: "has_attachment", Tools: []string{"read_attachment"}},
		},
		AlwaysOn: []string{"ask_user"},
	}
}

func TestComposeMountOrder(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  Request
		want []string
	}{
		{
			name: "every tier in contract order",
			req: Request{
				Enabled:    []string{"web_search", "notebook"},
				Flags:      map[string]bool{"has_kb": true, "has_attachment": true},
				Capability: []string{"cap_a", "cap_b"},
			},
			want: []string{"notebook", "web_search", "rag", "kb_files", "read_attachment", "cap_a", "cap_b", "ask_user"},
		},
		{
			name: "no gates set leaves the gated tier empty",
			req:  Request{Capability: []string{"cap_a"}},
			want: []string{"cap_a", "ask_user"},
		},
		{
			name: "gates mount in table order",
			req:  Request{Flags: map[string]bool{"has_attachment": true, "has_kb": true}},
			want: []string{"rag", "kb_files", "read_attachment", "ask_user"},
		},
		{
			name: "forced bypasses the gates and lands after the always-on set",
			req:  Request{Capability: []string{"cap_a"}, Forced: []string{"exec"}},
			want: []string{"cap_a", "ask_user", "exec"},
		},
		{
			name: "suppressed is removed last, from any tier",
			req: Request{
				Flags:      map[string]bool{"has_kb": true},
				Capability: []string{"cap_a"},
				Forced:     []string{"exec"},
				Suppressed: []string{"ask_user", "exec", "rag"},
			},
			want: []string{"kb_files", "cap_a"},
		},
		{
			name: "suppressing what was never mounted changes nothing",
			req:  Request{Capability: []string{"cap_a"}, Suppressed: []string{"rag", "exec"}},
			want: []string{"cap_a", "ask_user"},
		},
		{
			name: "a tool outside the optional list cannot be enabled",
			req:  Request{Enabled: []string{"exec", "notebook"}},
			want: []string{"notebook", "ask_user"},
		},
		{
			name: "duplicates collapse and the first tier keeps the position",
			req: Request{
				Enabled:    []string{"notebook"},
				Flags:      map[string]bool{"has_kb": true},
				Capability: []string{"kb_files", "notebook"},
			},
			want: []string{"notebook", "rag", "kb_files", "ask_user"},
		},
		{
			name: "an empty request mounts only the always-on set",
			req:  Request{},
			want: []string{"ask_user"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := testPolicy().Compose(tt.req); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Compose() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComposeWithNoPolicyMountsNothing(t *testing.T) {
	if got := (Policy{}).Compose(Request{}); len(got) != 0 {
		t.Errorf("Compose() = %v, want nothing", got)
	}
}

func TestComposeIsStableAcrossRuns(t *testing.T) {
	policy := testPolicy()
	req := Request{
		Enabled:    []string{"web_search", "notebook", "web_search"},
		Flags:      map[string]bool{"has_kb": true, "has_attachment": true},
		Capability: []string{"cap_a"},
	}
	first := policy.Compose(req)
	for i := 0; i < 32; i++ {
		if got := policy.Compose(req); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d returned %v, first run returned %v", i, got, first)
		}
	}
}

func TestReferencedCoversEveryMountableName(t *testing.T) {
	// Every name the policy can mount, from any tier, in one list.
	policy := testPolicy()
	policy.Gates = append(policy.Gates, Gate{Flag: "has_notebook", Tools: []string{"notebook"}})

	want := []string{"web_search", "notebook", "rag", "kb_files", "read_attachment", "ask_user"}
	if got := policy.Referenced(); !reflect.DeepEqual(got, want) {
		t.Errorf("Referenced() = %v, want %v", got, want)
	}
}

func TestDefaultPolicyIsThePlaneThisBuildShips(t *testing.T) {
	policy := DefaultPolicy()
	// Every name it can mount, in one list, so the startup check has something
	// to compare the registry against: a name only ever reachable through a gate
	// would otherwise go missing until a turn tried to mount it.
	if want := []string{"retrieve", "read_source", "ask_user"}; !reflect.DeepEqual(policy.Referenced(), want) {
		t.Errorf("Referenced() = %v, want %v", policy.Referenced(), want)
	}

	for _, test := range []struct {
		name  string
		flags map[string]bool
		want  []string
	}{
		{
			name: "a turn carrying nothing",
			// Name for name and in the order V1 sent it, which is the plane a
			// turn got before there was any material to mount for.
			want: []string{"ask_user"},
		},
		{
			name:  "a turn carrying attachments",
			flags: map[string]bool{FlagAttachments: true},
			want:  []string{"read_source", "ask_user"},
		},
		{
			name:  "a turn carrying collections",
			flags: map[string]bool{FlagKnowledgeBase: true},
			want:  []string{"retrieve", "ask_user"},
		},
		{
			name:  "a turn carrying both",
			flags: map[string]bool{FlagAttachments: true, FlagKnowledgeBase: true},
			want:  []string{"retrieve", "read_source", "ask_user"},
		},
		{
			name:  "a flag no gate watches",
			flags: map[string]bool{"notebook": true},
			want:  []string{"ask_user"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := policy.Compose(Request{Flags: test.flags}); !reflect.DeepEqual(got, test.want) {
				t.Errorf("Compose() = %v, want %v", got, test.want)
			}
		})
	}

	// Nothing is switchable yet, so a request cannot widen the plane: the
	// optional list is an allow-list, and this one is empty.
	if got := policy.Compose(Request{Enabled: []string{"retrieve", "read_source"}}); !reflect.DeepEqual(got, []string{"ask_user"}) {
		t.Errorf("Compose() = %v, want just the always-on tool", got)
	}
}
