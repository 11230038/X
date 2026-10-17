package config

// The cases for SUPER_ADMIN_USER_IDS.
//
// The value is a grant of power over every other account, so what these pin is
// not the parse but the refusals: a list that is being edited, one that is not
// numeric, one that names an account that cannot exist, and one that says the
// same thing twice are all rejected at startup, where the message can name the
// key. An empty value is not one of them — it is the ordinary deployment, and
// it means nobody may perform the four operations.

import (
	"strings"
	"testing"
)

func TestParseSuperAdminIDsAcceptsTheOrdinaryShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []int64
	}{
		{"empty is nobody", "", nil},
		{"whitespace is nobody", "   ", nil},
		{"one account", "2000001", []int64{2000001}},
		{"several, with spaces", "1, 2,3", []int64{1, 2, 3}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseIDList("SUPER_ADMIN_USER_IDS", testCase.raw)
			if err != nil {
				t.Fatalf("parseIDList(%q) error = %v", testCase.raw, err)
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("ids = %v, want %v", got, testCase.want)
			}
			for i := range got {
				if got[i] != testCase.want[i] {
					t.Fatalf("ids = %v, want %v", got, testCase.want)
				}
			}
		})
	}
}

func TestParseSuperAdminIDsRefusesAListThatIsStillBeingEdited(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"a trailing comma", "7,"},
		{"a leading comma", ",7"},
		{"a gap between two", "1,,2"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := parseIDList("SUPER_ADMIN_USER_IDS", testCase.raw)
			if err == nil || !strings.Contains(err.Error(), "SUPER_ADMIN_USER_IDS") {
				t.Fatalf("parseIDList(%q) error = %v, want one naming the key", testCase.raw, err)
			}
		})
	}
}

func TestParseSuperAdminIDsRefusesWhatIsNotAnAccount(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"a username", "admin"},
		{"a negative id", "-1"},
		{"zero", "0"},
		{"a fraction", "1.5"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := parseIDList("SUPER_ADMIN_USER_IDS", testCase.raw)
			if err == nil || !strings.Contains(err.Error(), "SUPER_ADMIN_USER_IDS") {
				t.Fatalf("parseIDList(%q) error = %v, want one naming the key", testCase.raw, err)
			}
		})
	}
}

func TestParseSuperAdminIDsRefusesTheSameAccountTwice(t *testing.T) {
	_, err := parseIDList("SUPER_ADMIN_USER_IDS", "7,7")
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("parseIDList(7,7) error = %v, want one about the repetition", err)
	}
}

// The other three keys are still required and still non-empty; the allowlist is
// optional and its emptiness must not be read as a blank entry.
func TestAuthzConfigValidateDoesNotRequireASuperAdmin(t *testing.T) {
	cfg := AuthzConfig{
		DefaultPreset:       "standard",
		LearnerCapabilities: []string{"chat"},
		LearnerSurfaces:     []string{"chat"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v with no super administrator named", err)
	}
}
