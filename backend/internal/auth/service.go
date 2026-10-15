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
	lockout    FailurePolicy
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
	// There is no default policy. A threshold that arrived as zero would lock on
	// the first wrong password, and one that arrived as a negative number is not a
	// number of failures at all: both are deployments that meant something the
	// configuration cannot say, so they stop here rather than at the first login.
	if options.Lockout.Threshold <= 0 {
		return nil, fmt.Errorf("the lockout threshold must be positive")
	}
	if options.Lockout.Window <= 0 {
		return nil, fmt.Errorf("the lockout window must be positive")
	}
	if options.Lockout.Duration <= 0 {
		return nil, fmt.Errorf("the lockout duration must be positive")
	}
	now := time.Now
	signer.now = now
	return &Service{
		repository: repository,
		signer:     signer,
		dummyHash:  dummyHash,
		lockout:    options.Lockout,
		now:        now,
	}, nil
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
//
// The order of the checks is the design, so it is written out as a sequence
// rather than folded into conditions (§2.3):
//
//  1. a username outside the accepted bounds is refused without a lookup;
//  2. a username that is not there is refused the same way;
//  3. a wrong password is counted — this is the only step that writes a failure,
//     and it runs only after the password is known to be wrong, so the count is
//     about guesses rather than about asking;
//  4. a correct password on a locked account is refused as locked;
//  5. a correct password on a disabled account is refused as disabled;
//  6. a correct password on an account that is neither clears the run and signs in.
//
// Steps 4 and 5 are both after the password, and that is the point: telling
// somebody the account is locked or disabled before they have proved they own it
// would turn this route into a way to ask whether a username exists. The cost is
// stated in the design and accepted: a locked account gives no warning, so the
// person it belongs to learns it by typing the right password.
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
	// One reading for the two uses below: the moment a failure is counted at and
	// the moment the lock is compared against are the same login, so they are the
	// same now.
	now := s.now().UTC()
	if !verifyPassword(record.PasswordHash, input.Password) {
		// A failure that could not be counted is not a login this can answer for:
		// the whole point of counting in the database is that the count survives,
		// and reporting a plain refusal over a counter that did not move would hide
		// the reason it did not.
		if err := s.repository.RecordFailure(ctx, record.User.UserID, now, s.lockout); err != nil {
			return AuthResult{}, err
		}
		return AuthResult{}, ErrInvalidCredentials
	}
	if record.LockedUntil != nil && record.LockedUntil.After(now) {
		return AuthResult{}, ErrAccountLocked
	}
	// The disabled state is only revealed once the password is known to be correct.
	if record.Disabled {
		return AuthResult{}, ErrAccountDisabled
	}
	if err := s.repository.ClearFailures(ctx, record.User.UserID); err != nil {
		return AuthResult{}, err
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

// Unlock clears an account's run of failed sign-ins and the lock it earned, so
// its owner can get in now rather than waiting the lock out.
//
// Nothing here asks whether the account exists. Both of this route's writes —
// this one and the revocation beside it — are statements about a state, not
// creations: "this account's tokens do not count" and "this account is not
// locked" are true of an account that was never there, and a caller that named
// no account has got what it asked for either way. The account's own sign-in
// clears the same three columns on a row it has just read.
func (s *Service) Unlock(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	return s.repository.ClearFailures(ctx, userID)
}

// ChangePassword replaces the account's password and takes back every token it
// holds, the one that made this request included.
//
// The old password is required, and that is this route's whole reason for
// existing apart from a plain write: a token proves what an account may reach,
// not who is holding it, so a stolen token would otherwise be enough to take the
// account over — the thief sets a new password and the owner is outside. Asking
// for the password again is the one thing that distinguishes the two.
//
// The order below is the same rule Login states from the other side: nothing
// about the request is answered until the caller has proved it is the account.
// A wrong old password is the whole answer, whether or not the new one would
// have been accepted, so a caller that guessed the old one learns nothing from
// this route about what a password must look like.
//
// The new password is judged by the same policy a registration is, because it is
// the same field with the same readers, and a rule that held only at registration
// would hold only against accounts that never changed their password.
//
// Replacing it takes back every token, so the client signs in again: that is
// §2.2's decision, and the reason there is no exemption for the token that made
// the request is that nothing in this build can tell one of an account's tokens
// from another.
func (s *Service) ChangePassword(ctx context.Context, userID int64, oldPassword, newPassword string) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	record, err := s.repository.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if !verifyPassword(record.PasswordHash, oldPassword) {
		return ErrWrongPassword
	}
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.repository.ReplacePassword(ctx, userID, string(hash), s.now().UTC())
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
