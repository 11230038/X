package migration

import (
	"database/sql"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProviderRejectsInvalidSchema(t *testing.T) {
	if _, err := Provider(&sql.DB{}, "public.schema"); err == nil {
		t.Fatal("Provider() error = nil, want invalid schema error")
	}
}

func TestQuoteIdentifier(t *testing.T) {
	if got := quoteIdentifier("app_data"); got != `"app_data"` {
		t.Fatalf("quoteIdentifier() = %q", got)
	}
}

func TestEmbeddedMigrationsMatchLatestVersion(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, "sql")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	versions := make([]int, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			t.Fatalf("migration %q has no version prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("migration %q has invalid version: %v", entry.Name(), err)
		}
		versions = append(versions, version)
	}
	sort.Ints(versions)
	if len(versions) == 0 {
		t.Fatal("no embedded migrations")
	}
	for index, version := range versions {
		want := index + 1
		if version != want {
			t.Fatalf("migration version at index %d = %d, want %d", index, version, want)
		}
	}
	if got := int64(versions[len(versions)-1]); got != LatestVersion() {
		t.Fatalf("embedded latest version = %d, LatestVersion() = %d", got, LatestVersion())
	}
}
