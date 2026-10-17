package main

// The shapes of the import graph this repository keeps, checked rather than
// remembered.
//
// Go already refuses a cycle, and it refuses one loudly. What it does not
// refuse is a direction: nothing stops a domain package from importing the
// HTTP boundary, or a service from reading configuration directly, because
// neither is a cycle — both compile. Each such edge is invisible in review
// until the day the lower layer cannot be used without the upper one, and by
// then it is a design, not a mistake.
//
// This is a test rather than a lint rule for the same reason role_scope_test.go
// is: it has to keep being true as the repository grows, and a rule checked
// once is a rule somebody's next package does not know about.
//
// The graph comes from `go list`, not from parsing files, so build tags and
// files without imports cost nothing here. Test imports are deliberately
// absent from `go list`'s Imports: a test may wire a real dependency across a
// boundary, and it is not what ships.

import (
	"os/exec"
	"strings"
	"testing"
)

// TestImportBoundaries holds the two directions that matter:
//
//   - the transport boundary (handler, server) is the top of the graph. Only
//     the composition root and the server may import it; everything else is
//     below it.
//   - configuration is read at the edges. A domain package that imports
//     internal/config has taken the deployment's file into itself, and with it
//     a dependency that cannot be satisfied by passing a value.
func TestImportBoundaries(t *testing.T) {
	module := modulePath(t)
	graph := packageImports(t)

	// A graph that is empty, or missing a package this test is about, would let
	// every assertion below pass by vacuity. Name the anchors.
	for _, anchor := range []string{
		module,
		module + "/internal/handler",
		module + "/internal/server",
		module + "/internal/config",
	} {
		if _, ok := graph[anchor]; !ok {
			t.Fatalf("go list did not report %s", anchor)
		}
	}

	boundaries := []struct {
		guarded string
		reason  string
		allowed func(string) bool
	}{
		{
			guarded: module + "/internal/handler",
			reason:  "the transport boundary is consumed by the server and the composition root only",
			allowed: func(pkg string) bool {
				return pkg == module || pkg == module+"/internal/server"
			},
		},
		{
			guarded: module + "/internal/server",
			reason:  "the server is constructed by the composition root only",
			allowed: func(pkg string) bool {
				return pkg == module
			},
		},
		{
			guarded: module + "/internal/config",
			reason:  "configuration belongs to the composition edge, not to a domain package",
			allowed: func(pkg string) bool {
				// The root and the commands are composition; internal/data and
				// internal/data/testdb receive the database configuration they
				// were constructed with; the server receives the HTTP one.
				return pkg == module ||
					strings.HasPrefix(pkg, module+"/cmd/") ||
					pkg == module+"/internal/data" ||
					pkg == module+"/internal/data/testdb" ||
					pkg == module+"/internal/server"
			},
		},
	}

	for _, boundary := range boundaries {
		for pkg, imports := range graph {
			if pkg == boundary.guarded || !imports[boundary.guarded] {
				continue
			}
			if boundary.allowed(pkg) {
				continue
			}
			t.Errorf("%s imports %s, but %s", pkg, boundary.guarded, boundary.reason)
		}
	}
}

// modulePath asks the toolchain for the module's import path rather than
// repeating it here. A rename then fails in the same commit it happens, which
// is the point of a guard.
func modulePath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// packageImports returns every package's production imports, keyed by package.
func packageImports(t *testing.T) map[string]map[string]bool {
	t.Helper()
	out, err := exec.Command(
		"go", "list", "-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./...",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, out)
	}

	graph := make(map[string]map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		path, rest, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("unexpected go list line %q", line)
		}
		imports := make(map[string]bool)
		for _, imported := range strings.Fields(rest) {
			imports[imported] = true
		}
		graph[path] = imports
	}
	return graph
}
