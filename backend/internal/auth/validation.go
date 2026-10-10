package auth

import "regexp"

const (
	minPasswordBytes = 8
	// maxPasswordBytes matches the bcrypt input limit: GenerateFromPassword and
	// CompareHashAndPassword both reject anything longer.
	maxPasswordBytes = 72
	// maxLoginUsernameBytes bounds the lookup key on login. It is deliberately
	// larger than the registration limit so accounts created before this policy
	// existed can still sign in.
	maxLoginUsernameBytes = 64
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// ValidateUsername enforces the registration policy. Usernames are stored and
// compared exactly as written, so casing is significant.
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}

// ValidatePassword enforces the length policy in bytes. Passwords are never
// trimmed, so leading and trailing spaces are meaningful.
func ValidatePassword(password string) error {
	if len(password) < minPasswordBytes || len(password) > maxPasswordBytes {
		return ErrInvalidPassword
	}
	return nil
}

// LoginUsernameInBounds reports whether a login username is short enough to look up.
func LoginUsernameInBounds(username string) bool {
	return len(username) > 0 && len(username) <= maxLoginUsernameBytes
}
