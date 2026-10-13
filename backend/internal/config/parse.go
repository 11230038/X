package config

import (
	"fmt"
	"strings"
	"time"
)

// The engines this build can parse a document with. The names are written here
// as well as in the parser registry because the registry is assembled later in
// startup: checking the name here means a typo is a configuration error naming
// its key, and the startup drift check against the registry then catches a name
// this file knows but this build did not register.
const (
	ParserEngineText    = "text"
	ParserEngineDocling = "docling"
)

// ParserConfig controls how a stored file becomes text.
type ParserConfig struct {
	// DefaultEngine is the engine a document is parsed with unless something
	// more specific is asked for.
	DefaultEngine string
	// DoclingBaseURL is a required key: requiredKeys names it, so the file must
	// carry a non-empty value whatever the default engine is, including in a
	// deployment that only ever parses text and never calls the service. What the
	// default engine decides is whether the value is checked at all — it is
	// validated as a base URL only when the default engine is docling. Like every
	// base URL here it is a prefix rather than an endpoint.
	DoclingBaseURL string
	// Timeout bounds one external parse call, so a document cannot hold a worker
	// indefinitely. A timeout fails that document as parse_failed rather than
	// failing the service.
	Timeout time.Duration
}

// Validate checks the parser settings.
func (c ParserConfig) Validate() error {
	switch c.DefaultEngine {
	case ParserEngineText, ParserEngineDocling:
	default:
		return fmt.Errorf("PARSER_DEFAULT_ENGINE must be %s or %s",
			ParserEngineText, ParserEngineDocling)
	}
	if c.DefaultEngine == ParserEngineDocling {
		if err := validateBaseURL(c.DoclingBaseURL); err != nil {
			return fmt.Errorf("invalid PARSER_DOCLING_BASE_URL: %w", err)
		}
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("PARSER_TIMEOUT must be a positive duration")
	}
	return nil
}

func loadParserConfig(values map[string]string) (ParserConfig, error) {
	timeout, err := parsePositiveDuration(values, "PARSER_TIMEOUT")
	if err != nil {
		return ParserConfig{}, err
	}
	return ParserConfig{
		DefaultEngine: strings.TrimSpace(values["PARSER_DEFAULT_ENGINE"]),
		// Trimmed even when it is unused, so a value left in the file keeps its
		// meaning if the default engine is later switched to docling.
		DoclingBaseURL: strings.TrimSuffix(strings.TrimSpace(values["PARSER_DOCLING_BASE_URL"]), "/"),
		Timeout:        timeout,
	}, nil
}
