package config

import (
	"fmt"
	"strings"
	"time"
)

// AuthConfig contains access-token signing settings.
type AuthConfig struct {
	Secret string
	Issuer string
	TTL    time.Duration
}

// Validate checks the access-token configuration. The secret is only checked for
// presence here; its minimum strength is enforced when the signer is built, so
// the requirement lives in one place. Failures never echo the secret value.
func (c AuthConfig) Validate() error {
	for key, value := range map[string]string{
		"AUTH_JWT_SECRET": c.Secret,
		"AUTH_JWT_ISSUER": c.Issuer,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if c.TTL <= 0 {
		return fmt.Errorf("AUTH_TOKEN_TTL must be a positive duration")
	}
	return nil
}

func loadAuthConfig(values map[string]string) (AuthConfig, error) {
	ttl, err := parsePositiveDuration(values, "AUTH_TOKEN_TTL")
	if err != nil {
		return AuthConfig{}, err
	}
	// The secret is used verbatim: trimming it would silently change the key.
	return AuthConfig{
		Secret: values["AUTH_JWT_SECRET"],
		Issuer: strings.TrimSpace(values["AUTH_JWT_ISSUER"]),
		TTL:    ttl,
	}, nil
}
