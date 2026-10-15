package auth

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidUsername reports a username that violates the registration policy.
	ErrInvalidUsername = errors.New("invalid username")
	// ErrInvalidPassword reports a password outside the accepted length range.
	ErrInvalidPassword = errors.New("invalid password")
	// ErrUsernameTaken reports a username that already exists.
	ErrUsernameTaken = errors.New("username taken")
	// ErrUserNotFound reports a missing account.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials deliberately covers both unknown accounts and wrong
	// passwords so callers cannot tell them apart.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAccountDisabled reports an account that exists but may not sign in.
	ErrAccountDisabled = errors.New("account disabled")
	// ErrInvalidToken reports a token that failed signature, format, or claim checks.
	ErrInvalidToken = errors.New("invalid token")
)

// User is the projection that is safe to return to HTTP callers.
type User struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Record is the persistence view used only inside this package. It carries the
// password hash and disabled flag and is never serialized.
type Record struct {
	User         User
	PasswordHash string
	Disabled     bool
	// TokenValidAfter is when this account's tokens stopped counting. Nil is
	// "never revoked", which is not the same as an early moment: a token is
	// refused when it was signed at or before this value, and a nil value refuses
	// nothing.
	TokenValidAfter *time.Time
	// FailedAttempts and FailedWindowStart are the run of failed logins the login
	// path counts, and LockedUntil is when a lock ends — nil and a moment in the
	// past both mean the account is not locked.
	FailedAttempts    int
	FailedWindowStart *time.Time
	LockedUntil       *time.Time
}

// RegisterInput describes one registration attempt.
type RegisterInput struct {
	Username string
	Password string
}

// LoginInput describes one login attempt.
type LoginInput struct {
	Username string
	Password string
}

// AuthResult is a successful register or login.
type AuthResult struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

// CreateParams carries a new account to the repository.
type CreateParams struct {
	Username     string
	PasswordHash string
}

// Repository persists accounts without modifying the schema.
type Repository interface {
	Create(context.Context, CreateParams) (Record, error)
	FindByUsername(context.Context, string) (Record, error)
	FindByID(context.Context, int64) (Record, error)
	// RevokeTokens records the moment at or before which this account's tokens
	// stopped counting. The moment is the caller's: see Service.now.
	RevokeTokens(context.Context, int64, time.Time) error
}

// Options configure password hashing and token issuance.
type Options struct {
	Secret string
	Issuer string
	TTL    time.Duration
}
