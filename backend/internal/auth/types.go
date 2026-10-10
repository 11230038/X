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
}

// Options configure password hashing and token issuance.
type Options struct {
	Secret string
	Issuer string
	TTL    time.Duration
}
