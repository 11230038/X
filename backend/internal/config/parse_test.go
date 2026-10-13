package config

import (
	"strings"
	"testing"
	"time"

	"backend/internal/parse"
)

func validParserConfig() ParserConfig {
	return ParserConfig{
		DefaultEngine:  ParserEngineText,
		DoclingBaseURL: "http://127.0.0.1:5001",
		Timeout:        2 * time.Minute,
	}
}

// The engine names are written in both packages — here so that a typo in the
// key is a configuration error naming it, and in the registry because that is
// where the engines are. This keeps the pair from drifting apart: a name only
// one side knows would be accepted at startup and then fail as an unavailable
// engine on the first document.
func TestParserEngineNamesMatchTheRegistry(t *testing.T) {
	if ParserEngineText != parse.EngineText {
		t.Errorf("ParserEngineText = %q, want %q", ParserEngineText, parse.EngineText)
	}
	if ParserEngineDocling != parse.EngineDocling {
		t.Errorf("ParserEngineDocling = %q, want %q", ParserEngineDocling, parse.EngineDocling)
	}

	known := make(map[string]bool)
	for _, name := range parse.EngineNames() {
		known[name] = true
	}
	for _, name := range []string{ParserEngineText, ParserEngineDocling} {
		if !known[name] {
			t.Errorf("this package offers the engine name %q, which the registry does not have; "+
				"it has %s", name, strings.Join(parse.EngineNames(), ", "))
		}
	}
}

func TestParserConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validParserConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestParserConfigValidateRejectsAnUnknownEngine(t *testing.T) {
	cfg := validParserConfig()
	// A name no build registers must be refused here, where the message can name
	// the key, rather than later as a document that failed to parse.
	cfg.DefaultEngine = "unstructured"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "PARSER_DEFAULT_ENGINE") {
		t.Fatalf("Validate() error = %v, want one naming PARSER_DEFAULT_ENGINE", err)
	}
}

// A deployment that parses text only must not be made to name a service it
// never calls, and one that selects docling must not be allowed to leave it
// blank: the engine would then fail on the first document instead of at startup.
func TestParserConfigValidateRequiresTheDoclingURLOnlyForDocling(t *testing.T) {
	cfg := validParserConfig()
	cfg.DoclingBaseURL = ""

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v with the text engine selected", err)
	}

	cfg.DefaultEngine = ParserEngineDocling
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "PARSER_DOCLING_BASE_URL") {
		t.Fatalf("Validate() error = %v, want one naming PARSER_DOCLING_BASE_URL", err)
	}

	cfg.DoclingBaseURL = "docling:5001"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil for a base URL without a scheme")
	}
}

func TestParserConfigValidateRejectsANonPositiveTimeout(t *testing.T) {
	cfg := validParserConfig()
	cfg.Timeout = 0

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "PARSER_TIMEOUT") {
		t.Fatalf("Validate() error = %v, want one naming PARSER_TIMEOUT", err)
	}
}

func TestLoadRejectsInvalidParserSettings(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "engine this build does not have", key: "PARSER_DEFAULT_ENGINE", value: "marker"},
		{name: "blank engine", key: "PARSER_DEFAULT_ENGINE", value: " "},
		{name: "timeout", key: "PARSER_TIMEOUT", value: "0s"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
		})
	}
}

func TestLoadKeepsTheDoclingURLWhenTheTextEngineIsSelected(t *testing.T) {
	values := validValues()
	values["PARSER_DEFAULT_ENGINE"] = "text"
	values["PARSER_DOCLING_BASE_URL"] = "http://docling.internal:5001/"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Loading it either way means switching the default engine is one edit to
	// this file rather than two, and the trailing slash goes for the same reason
	// it does on the model base URLs.
	if cfg.Parser.DoclingBaseURL != "http://docling.internal:5001" {
		t.Fatalf("Parser.DoclingBaseURL = %q", cfg.Parser.DoclingBaseURL)
	}
}
