package auth

// The double the credential cases run against, and the two constructors they
// share: the in-memory repository and the service built over it.
//
// It is a file of its own because it is not about one rule. Register, login,
// revocation and the lockout all reach for it, and a double that lived inside
// whichever case happened to need it first would move every time that case did.

import (
	"context"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	testPassword       = "correct-horse"
	exampleUsername    = "alice"
	exampleTokenIssuer = "x-backend"
	testTokenTTL       = time.Hour
	testSecretRepeated = "0123456789abcdef0123456789abcdef"
	otherPassword      = "0123456789"
)

type fakeRepository struct {
	records      map[string]Record
	byID         map[int64]Record
	nextID       int64
	createCalls  int
	findCalls    int
	revokeCalls  int
	revokedUser  int64
	revokedAt    time.Time
	failureCalls int
	failedUser   int64
	failedAt     time.Time
	failedPolicy FailurePolicy
	clearCalls   int
	clearedUser  int64

	replaceCalls int
	replacedUser int64
	replacedHash string
	replacedAt   time.Time
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		records: map[string]Record{},
		byID:    map[int64]Record{},
		nextID:  1,
	}
}

func (f *fakeRepository) Create(_ context.Context, params CreateParams) (Record, error) {
	f.createCalls++
	// Keyed by the exact username: casing is significant.
	if _, exists := f.records[params.Username]; exists {
		return Record{}, ErrUsernameTaken
	}
	record := Record{
		User:         User{UserID: f.nextID, Username: params.Username, Role: defaultRole},
		PasswordHash: params.PasswordHash,
	}
	f.nextID++
	f.records[params.Username] = record
	f.byID[record.User.UserID] = record
	return record, nil
}

func (f *fakeRepository) FindByUsername(_ context.Context, username string) (Record, error) {
	f.findCalls++
	record, exists := f.records[username]
	if !exists {
		return Record{}, ErrUserNotFound
	}
	return record, nil
}

func (f *fakeRepository) FindByID(_ context.Context, userID int64) (Record, error) {
	record, exists := f.byID[userID]
	if !exists {
		return Record{}, ErrUserNotFound
	}
	return record, nil
}

// RevokeTokens stands in for the one write this package makes about an account's
// tokens. It keeps the moment it was handed as well as writing it, because the
// moment is part of the claim: a repository that reached for a clock of its own
// would still pass every case about refusal and be wrong about which tokens those
// were. An account that is not there is not an error, which is what the real
// adapter does too.
func (f *fakeRepository) RevokeTokens(_ context.Context, userID int64, at time.Time) error {
	f.revokeCalls++
	f.revokedUser = userID
	f.revokedAt = at
	record, exists := f.byID[userID]
	if !exists {
		return nil
	}
	moment := at
	record.TokenValidAfter = &moment
	f.byID[userID] = record
	if byName, ok := f.records[record.User.Username]; ok {
		byName.TokenValidAfter = &moment
		f.records[record.User.Username] = byName
	}
	return nil
}

// RecordFailure records the call and the moment it was handed, and does not count
// anything. The rule it stands in for — a window, a threshold, a lock written
// into a row — is a comparison against columns this fake does not have, and a
// double cannot attest to it: it would restate the rule and then agree with
// itself. That rule is checked where it lives, in lockout_db_test.go, over the
// real adapter. What a case in this file reads off this are the calls.
func (f *fakeRepository) RecordFailure(
	_ context.Context, userID int64, at time.Time, policy FailurePolicy,
) error {
	f.failureCalls++
	f.failedUser = userID
	f.failedAt = at
	f.failedPolicy = policy
	return nil
}

// ClearFailures records the call the same way, and for the same reason.
func (f *fakeRepository) ClearFailures(_ context.Context, userID int64) error {
	f.clearCalls++
	f.clearedUser = userID
	return nil
}

// ReplacePassword records both halves of the call it is handed, and writes
// neither into a record. That is the same restraint the two above keep and it has
// the same reason — whether the account can then sign in with the new password is
// a comparison against a hash in a row, which is lockout_db_test.go's and
// credentials_db_test.go's to make over the real adapter, not this fake's to
// restate. What the cases in this file read off this are the two values passed
// down and the moment they were passed at.
func (f *fakeRepository) ReplacePassword(
	_ context.Context, userID int64, passwordHash string, at time.Time,
) error {
	f.replaceCalls++
	f.replacedUser = userID
	f.replacedHash = passwordHash
	f.replacedAt = at
	return nil
}

func (f *fakeRepository) addAccount(t *testing.T, username, password string, disabled bool) Record {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{
		User:         User{UserID: f.nextID, Username: username, Role: defaultRole},
		PasswordHash: string(hash),
		Disabled:     disabled,
	}
	f.nextID++
	f.records[username] = record
	f.byID[record.User.UserID] = record
	return record
}

func newTestService(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(Options{
		Secret:  testSecretRepeated,
		Issuer:  exampleTokenIssuer,
		TTL:     testTokenTTL,
		Lockout: lockoutPolicy,
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}
