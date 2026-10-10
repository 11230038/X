package auth

import (
	"context"
	"errors"
	"fmt"

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
	return &Service{repository: repository, signer: signer, dummyHash: dummyHash}, nil
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
	return record.User, nil
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
