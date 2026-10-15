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
	// ErrAccountLocked reports an account that has failed too many logins in a row
	// and is refused for a while. It is not ErrAccountDisabled: a lock ends on its
	// own, and it only ever refuses a login — the tokens an account already holds
	// keep working until they are taken back (§2.3).
	ErrAccountLocked = errors.New("account locked")
	// ErrWrongPassword reports a password that does not match the account's stored
	// hash. It exists for the one place a caller has to prove an identity it is
	// already signed in as — changing its own password — where Login's
	// ErrInvalidCredentials would be the wrong sentence: that one deliberately
	// covers "no such account" as well, and here the account is not in question.
	ErrWrongPassword = errors.New("wrong password")
	// ErrInvalidToken reports a token that failed signature, format, or claim checks.
	ErrInvalidToken = errors.New("invalid token")
)

// FailurePolicy is how many failed sign-ins, counted over how long, earn a lock,
// and how long that lock lasts. All three are the deployment's to choose and all
// three are required to be positive: a threshold of zero would be reached by the
// first failure.
type FailurePolicy struct {
	Threshold int
	Window    time.Duration
	Duration  time.Duration
}

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
	// RecordFailure counts one failed sign-in and locks the account once the run of
	// them reaches the policy's threshold. The moment is the caller's for the same
	// reason: the window and the lock are both decided against moments already in
	// the row.
	RecordFailure(context.Context, int64, time.Time, FailurePolicy) error
	// ClearFailures forgets the run of failed sign-ins. It is what a successful
	// sign-in writes, and it is also what an administrator's unlock writes: one
	// state, two ways to reach it.
	ClearFailures(context.Context, int64) error
	// ReplacePassword writes a new hash and the moment the tokens signed with the
	// old one stopped counting, as one write. The two are one change rather than
	// two: a password that has been replaced while tokens signed with the old one
	// still work is a state no caller asked for and none of them can name. The
	// moment is the caller's, for the reason RevokeTokens gives.
	ReplacePassword(context.Context, int64, string, time.Time) error
}

// Options configure password hashing, token issuance, and the lockout that
// guards login.
type Options struct {
	Secret string
	Issuer string
	TTL    time.Duration
	// Lockout has no default here on purpose: its three numbers come from the
	// configuration file, and a service constructed without them is a deployment
	// whose lockout nobody chose.
	Lockout FailurePolicy
}
