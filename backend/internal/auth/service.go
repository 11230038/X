package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// defaultRole is assigned to every self-registered account.
const defaultRole = "user"

// dummyPassword is only ever hashed to equalize login timing. It is not a
// credential and matches no account.
const dummyPassword = "auth timing equalization placeholder"

// Service applies registration, login, and access-token rules.
type Service struct {
	repository Repository
	signer     *TokenSigner
	dummyHash  []byte
	// now is the one clock this package reads. It is the same function the signer
	// timestamps tokens with, and that is not a detail: whether a token still
	// counts is decided by comparing the moment it was signed against the moment
	// it was revoked, so two clocks — even two that disagree by a second — would
	// have the rule answer the wrong way about its own writes. One clock, used in
	// two places.
	now func() time.Time
}

// NewService validates the signing configuration and prepares password hashing.
func NewService(options Options, repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("auth repository is required")
	}
	signer, err := NewTokenSigner(options.Secret, options.Issuer, options.TTL)
	if err != nil {
		return nil, err
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte(dummyPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("prepare password comparison: %w", err)
	}
	now := time.Now
	signer.now = now
	return &Service{repository: repository, signer: signer, dummyHash: dummyHash, now: now}, nil
}

// Register validates the input, stores a bcrypt hash, and issues an access token.
func (s *Service) Register(ctx context.Context, input RegisterInput) (AuthResult, error) {
	if err := ValidateUsername(input.Username); err != nil {
		return AuthResult{}, err
	}
	if err := ValidatePassword(input.Password); err != nil {
		return AuthResult{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthResult{}, fmt.Errorf("hash password: %w", err)
	}
	record, err := s.repository.Create(ctx, CreateParams{
		Username:     input.Username,
		PasswordHash: string(hash),
	})
	if err != nil {
		return AuthResult{}, err
	}
	return s.issue(record.User)
}

// Login verifies credentials and issues an access token.
func (s *Service) Login(ctx context.Context, input LoginInput) (AuthResult, error) {
	if !LoginUsernameInBounds(input.Username) {
		s.equalizePasswordCheck(input.Password)
		return AuthResult{}, ErrInvalidCredentials
	}
	record, err := s.repository.FindByUsername(ctx, input.Username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			s.equalizePasswordCheck(input.Password)
			return AuthResult{}, ErrInvalidCredentials
		}
		return AuthResult{}, err
	}
	if !verifyPassword(record.PasswordHash, input.Password) {
		return AuthResult{}, ErrInvalidCredentials
	}
	// The disabled state is only revealed once the password is known to be correct.
	if record.Disabled {
		return AuthResult{}, ErrAccountDisabled
	}
	return s.issue(record.User)
}

// Authenticate verifies an access token and reloads the account it belongs to.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	claims, err := s.signer.Verify(token)
	if err != nil {
		return User{}, err
	}
	userID, err := claims.UserID()
	if err != nil {
		return User{}, err
	}
	record, err := s.repository.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, ErrInvalidToken
		}
		return User{}, err
	}
	if record.Disabled {
		return User{}, ErrAccountDisabled
	}
	// A token signed at or before the account's revocation moment no longer
	// counts. Both moments are whole seconds — jwt truncates iat and the column
	// holds no fractions — so the comparison is written with <= on purpose:
	// refusing a token signed in the same second as the revocation costs the
	// account one more login, while admitting one costs a revocation that
	// silently did not happen. The disabled state above is still decided first,
	// so a token on a disabled account is answered the way it always was.
	if record.TokenValidAfter != nil && !claims.IssuedAt.Time.After(*record.TokenValidAfter) {
		return User{}, ErrInvalidToken
	}
	return record.User, nil
}

// RevokeTokens takes back every token this account holds, the one that made the
// request included: nothing in this build can tell two of an account's tokens
// apart, so there is no such thing as revoking just one of them.
func (s *Service) RevokeTokens(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	return s.repository.RevokeTokens(ctx, userID, s.now().UTC())
}

func (s *Service) issue(user User) (AuthResult, error) {
	token, expiresAt, err := s.signer.Sign(user)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: user, Token: token, ExpiresAt: expiresAt}, nil
}

// equalizePasswordCheck performs the same bcrypt work as a real comparison so an
// unknown username cannot be told apart from a wrong password by timing.
func (s *Service) equalizePasswordCheck(password string) {
	_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
}

// verifyPassword reports whether a password matches a stored hash. A malformed
// hash counts as a mismatch rather than an internal error.
func verifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
