package config

import (
	"fmt"
	"strings"
	"time"
)

// maxTicketTTL is the longest a websocket ticket may stay usable. It is a
// ceiling rather than a suggestion: see AuthConfig.Validate.
const maxTicketTTL = 5 * time.Minute

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
	// TicketTTL bounds the one-shot credential a browser trades for a WebSocket
	// connection. It is a separate key from TTL rather than a fraction of it
	// because the two answer different questions: TTL is how long a session lasts,
	// TicketTTL is how long a string that will be written into a URL stays usable.
	// Those are not the same kind of number and a deployment may well want to move
	// one without the other.
	TicketTTL time.Duration
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
	// The ticket's TTL is bounded from above, which no other duration here is.
	// The whole reason a ticket is allowed to travel in a URL — where every hop
	// records it — is that it stops working long before anyone can read that
	// record. A ticket that lives as long as an access token has quietly become
	// one, and the only thing that would have said so is this line.
	if c.TicketTTL <= 0 {
		return fmt.Errorf("WS_TICKET_TTL must be a positive duration")
	}
	if c.TicketTTL > maxTicketTTL {
		return fmt.Errorf("WS_TICKET_TTL must not exceed %s", maxTicketTTL)
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
	ticketTTL, err := parsePositiveDuration(values, "WS_TICKET_TTL")
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
		TicketTTL:        ticketTTL,
		LockoutThreshold: threshold,
		LockoutWindow:    window,
		LockoutDuration:  duration,
	}, nil
}
