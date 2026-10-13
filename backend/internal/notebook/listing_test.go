package notebook

import "testing"

// The two pure functions the listing is built on. Both exist to keep something
// out of a LIKE pattern — a wildcard a user typed, or a page size that is a
// typo — and both are cheaper to pin here than through a query.

// TestEscapeLike keeps a user's text the text they searched for. Without it,
// searching for "50%" asks for everything that starts with 50, and searching for
// "a_b" matches "axb" — a search that quietly answers a different question than
// the one that was asked.
func TestEscapeLike(t *testing.T) {
	cases := []struct {
		name  string
		given string
		want  string
	}{
		{"plain text", "closure", "closure"},
		{"Chinese text", "闭包", "闭包"},
		{"a percent", "50%", `50\%`},
		{"an underscore", "a_b", `a\_b`},
		{"a backslash", `back\slash`, `back\\slash`},
		{"all three", `100%\_x`, `100\%\\\_x`},
		{"empty", "", ""},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if got := escapeLike(scenario.given); got != scenario.want {
				t.Errorf("escapeLike(%q) = %q, want %q", scenario.given, got, scenario.want)
			}
		})
	}
}

// TestClampLimit is the rule that a listing always has a bound. A page size of
// zero or less is a caller that did not say, and one above the maximum is a
// caller asking for more than it should get: neither is worth refusing, and the
// second one is worth capping.
func TestClampLimit(t *testing.T) {
	cases := []struct {
		name  string
		given int
		want  int
	}{
		{"not said", 0, DefaultListLimit},
		{"negative", -1, DefaultListLimit},
		{"one", 1, 1},
		{"the default", DefaultListLimit, DefaultListLimit},
		{"the maximum", MaxListLimit, MaxListLimit},
		{"above the maximum", MaxListLimit + 1, MaxListLimit},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if got := clampLimit(scenario.given); got != scenario.want {
				t.Errorf("clampLimit(%d) = %d, want %d", scenario.given, got, scenario.want)
			}
		})
	}
}
