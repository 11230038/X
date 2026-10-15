package config

import (
	"fmt"
	"strings"
	"time"
)

// AuthConfig contains access-token signing settings and the login lockout that
// guards them.
//
// The lockout is a field of this section rather than a section of its own because
// it is one of the things a login answers, and because keeping it here means the
// composition needs no new line: a fourth struct in config.go would be a fourth
// thing that file has to know about.
type AuthConfig struct {
	Secret string
	Issuer string
	TTL    time.Duration
	// The lockout is three numbers rather than one, because "how many failures",
	// "over how long" and "locked for how long" are three questions that can be
	// argued with separately. The values shipped in .env.example make them the same
	// fifteen minutes, which is what makes the rule read as one sentence.
	LockoutThreshold int
	LockoutWindow    time.Duration
	LockoutDuration  time.Duration
}

// Validate checks the access-token configuration and the lockout that guards it.
// The secret is only checked for presence here; its minimum strength is enforced
// when the signer is built, so the requirement lives in one place. Failures never
// echo the secret value.
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
	// Zero would not mean "no lockout": a threshold of zero is reached by every
	// failure, so the first wrong password would lock the account. It has to be
	// refused here, because there is no reading of it that a deployment meant.
	if c.LockoutThreshold <= 0 {
		return fmt.Errorf("AUTH_LOCKOUT_THRESHOLD must be a positive integer")
	}
	if c.LockoutWindow <= 0 {
		return fmt.Errorf("AUTH_LOCKOUT_WINDOW must be a positive duration")
	}
	if c.LockoutDuration <= 0 {
		return fmt.Errorf("AUTH_LOCKOUT_DURATION must be a positive duration")
	}
	return nil
}

func loadAuthConfig(values map[string]string) (AuthConfig, error) {
	ttl, err := parsePositiveDuration(values, "AUTH_TOKEN_TTL")
	if err != nil {
		return AuthConfig{}, err
	}
	threshold, err := parsePositiveInt(values, "AUTH_LOCKOUT_THRESHOLD")
	if err != nil {
		return AuthConfig{}, err
	}
	window, err := parsePositiveDuration(values, "AUTH_LOCKOUT_WINDOW")
	if err != nil {
		return AuthConfig{}, err
	}
	duration, err := parsePositiveDuration(values, "AUTH_LOCKOUT_DURATION")
	if err != nil {
		return AuthConfig{}, err
	}
	// The secret is used verbatim: trimming it would silently change the key.
	return AuthConfig{
		Secret:           values["AUTH_JWT_SECRET"],
		Issuer:           strings.TrimSpace(values["AUTH_JWT_ISSUER"]),
		TTL:              ttl,
		LockoutThreshold: threshold,
		LockoutWindow:    window,
		LockoutDuration:  duration,
	}, nil
}
