package migration

import (
	"database/sql"
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
