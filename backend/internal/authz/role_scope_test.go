package authz

// The one case that is not about this package's behaviour but about everyone
// else's: nobody outside internal/authz may compare an account's role (§7.1).
//
// It is a grep rather than an assertion because the thing being defended is a
// shape of the source, and it is a *test* rather than a lint rule because it
// has to keep being true as the repository grows — a rule checked once is a
// rule somebody's next handler does not know about.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Two patterns, and between them they are the account role vocabulary rather
// than the word "role".
//
// The first is the constants this package exports, named by their qualified
// name: naming one outside here is how a second, private reading of "is this
// the administrator" gets written. The second is the stored literals, which is
// how the same decision gets written without naming anything.
//
// The boundary is worth being explicit about, because a grep cannot enforce a
// discipline — it can only make one shape of violation fail. These two catch
// the shape that gets written: a role compared to a constant or to the string
// the database stores. What they do not catch is a role compared to a variable
// that holds one, and what they must not catch is the other two vocabularies
// that spell the same name: `llm.RoleUser` and `session.RoleUser` are who said
// something in a conversation, not who is asking, and neither has anything to
// do with whether an account is the administrator.
var accountRolePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bauthz\.Role(Admin|User)\b`),
	regexp.MustCompile(`(?i)\brole\s*[!=]=\s*"(admin|user)"`),
}

func TestOnlyAuthzComparesARole(t *testing.T) {
	root := filepath.Join("..", "..")
	var offences []string
	scanned := 0

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			// This package is where the comparison is allowed to be.
			if entry.Name() == "authz" && filepath.Dir(path) == filepath.Join(root, "internal") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		for number, line := range strings.Split(string(content), "\n") {
			for _, pattern := range accountRolePatterns {
				if !pattern.MatchString(line) {
					continue
				}
				relative, _ := filepath.Rel(root, path)
				offences = append(offences, relative+":"+strconv.Itoa(number+1)+": "+strings.TrimSpace(line))
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	if scanned == 0 {
		t.Fatalf("no Go files were scanned: the walk started in the wrong place")
	}
	if len(offences) > 0 {
		t.Errorf("a role is compared outside internal/authz, where the four layers are combined:\n  %s\n"+
			"Ask Caller.IsAdmin or Decider.Decide instead — a second private decision is one nothing can audit.",
			strings.Join(offences, "\n  "))
	}
}
