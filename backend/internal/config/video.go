package config

import (
	"fmt"
	"strings"
	"time"
)

// VideoConfig holds the public video site the learning page searches.
//
// There is no key in this section, and that is not an omission: the search is a
// public request, signed with a key the site itself hands out (see
// internal/video/wbi.go). What a deployment configures is where to ask, how
// long to wait, and how long an answer may be reused.
type VideoConfig struct {
	// BaseURL is a prefix, not an endpoint: the client appends /x/web-interface...
	// to it.
	BaseURL string
	// Timeout bounds one call. A search answers within a second when the site is
	// well; this is the point at which waiting longer stops being worth it.
	Timeout time.Duration
	// CacheTTL is how long one keyword's page is reused. It is here rather than
	// in the domain because it is a deployment's judgement about how fresh a
	// search result has to be.
	CacheTTL time.Duration
}

// Validate checks the section. An empty base URL is refused rather than
// defaulted: this service reads every key from the file and invents none
// (§2.6), and a default here would hide a deployment that meant to point this at
// something else.
func (c VideoConfig) Validate() error {
	if err := validateBaseURL(c.BaseURL); err != nil {
		return fmt.Errorf("invalid BILIBILI_BASE_URL: %w", err)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("BILIBILI_TIMEOUT must be a positive duration")
	}
	if c.CacheTTL <= 0 {
		return fmt.Errorf("BILIBILI_CACHE_TTL must be a positive duration")
	}
	return nil
}

func loadVideoConfig(values map[string]string) (VideoConfig, error) {
	timeout, err := parsePositiveDuration(values, "BILIBILI_TIMEOUT")
	if err != nil {
		return VideoConfig{}, err
	}
	cacheTTL, err := parsePositiveDuration(values, "BILIBILI_CACHE_TTL")
	if err != nil {
		return VideoConfig{}, err
	}
	return VideoConfig{
		// A trailing slash would double up when the client appends its path.
		BaseURL:  strings.TrimSuffix(strings.TrimSpace(values["BILIBILI_BASE_URL"]), "/"),
		Timeout:  timeout,
		CacheTTL: cacheTTL,
	}, nil
}
