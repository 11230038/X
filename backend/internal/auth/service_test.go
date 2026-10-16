package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestRegisterStoresHashAndIssuesToken(t *testing.T) {
	repository := newFakeRepository()
	service := newTestService(t, repository)

	result, err := service.Register(context.Background(), RegisterInput{Username: exampleUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if result.User.Username != exampleUsername || result.User.Role != defaultRole || result.User.UserID == 0 {
		t.Errorf("user = %+v", result.User)
	}
	if result.Token == "" || result.ExpiresAt.IsZero() {
		t.Fatalf("result = %+v, want a token and expiry", result)
	}

	stored := repository.records[exampleUsername].PasswordHash
	if stored == testPassword {
		t.Fatal("stored password is the plaintext")
	}
	if !strings.HasPrefix(stored, "$2") {
		t.Fatalf("stored password is not a bcrypt hash: %q", stored)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(testPassword)); err != nil {
		t.Fatalf("stored hash does not match the password: %v", err)
	}

	claims, err := service.signer.Verify(result.Token)
	if err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	userID, err := claims.UserID()
	if err != nil || userID != result.User.UserID {
		t.Fatalf("token subject = %d (err %v), want %d", userID, err, result.User.UserID)
	}
}

func TestRegisterRejectsInvalidInputBeforeStorage(t *testing.T) {
	tests := []struct {
		name  string
		input RegisterInput
		want  error
	}{
		{name: "short username", input: RegisterInput{Username: "ab", Password: testPassword}, want: ErrInvalidUsername},
		{name: "username with space", input: RegisterInput{Username: "al ice", Password: testPassword}, want: ErrInvalidUsername},
		{name: "short password", input: RegisterInput{Username: exampleUsername, Password: "short"}, want: ErrInvalidPassword},
		{name: "password above the bcrypt limit", input: RegisterInput{Username: exampleUsername, Password: strings.Repeat("a", 73)}, want: ErrInvalidPassword},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := newFakeRepository()
			service := newTestService(t, repository)
			if _, err := service.Register(context.Background(), tt.input); !errors.Is(err, tt.want) {
				t.Fatalf("Register() error = %v, want %v", err, tt.want)
			}
			if repository.createCalls != 0 {
				t.Fatalf("repository was called %d times for invalid input", repository.createCalls)
			}
		})
	}
}

func TestRegisterMapsDuplicateUsername(t *testing.T) {
	repository := newFakeRepository()
	service := newTestService(t, repository)
	input := RegisterInput{Username: exampleUsername, Password: testPassword}

	if _, err := service.Register(context.Background(), input); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if _, err := service.Register(context.Background(), input); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("second Register() error = %v, want ErrUsernameTaken", err)
	}
}

func TestLoginReturnsTokenForValidCredentials(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)

	result, err := service.Login(context.Background(), LoginInput{Username: exampleUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.User.UserID != account.User.UserID || result.User.Username != exampleUsername {
		t.Errorf("user = %+v, want %+v", result.User, account.User)
	}
	if _, err := service.Authenticate(context.Background(), result.Token); err != nil {
		t.Fatalf("issued token does not authenticate: %v", err)
	}
}

func TestLoginUsernameIsCaseSensitive(t *testing.T) {
	repository := newFakeRepository()
	repository.addAccount(t, "Alice", testPassword, false)
	service := newTestService(t, repository)
	ctx := context.Background()

	if _, err := service.Login(ctx, LoginInput{Username: "alice", Password: testPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() with different casing = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, LoginInput{Username: "Alice", Password: testPassword}); err != nil {
		t.Fatalf("Login() with exact casing error = %v", err)
	}
}

func TestLoginRejectsUnknownUsernameAndWrongPasswordAlike(t *testing.T) {
	repository := newFakeRepository()
	repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	ctx := context.Background()

	tests := map[string]LoginInput{
		"unknown username": {Username: "nobody", Password: testPassword},
		"wrong password":   {Username: exampleUsername, Password: "wrong-password"},
		"oversized username": {
			Username: strings.Repeat("a", 65),
			Password: testPassword,
		},
		"oversized password": {
			Username: exampleUsername,
			Password: strings.Repeat("a", 73),
		},
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Login(ctx, input); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
		})
	}

	// Only the oversized username is rejected before the lookup; an oversized
	// password still resolves the account and then fails the comparison.
	if repository.findCalls != 3 {
		t.Fatalf("repository lookups = %d, want 3 (oversized username must not reach storage)", repository.findCalls)
	}
}

func TestLoginRejectsMalformedStoredHash(t *testing.T) {
	repository := newFakeRepository()
	record := repository.addAccount(t, exampleUsername, testPassword, false)
	record.PasswordHash = "not-a-bcrypt-hash"
	repository.records[exampleUsername] = record
	service := newTestService(t, repository)

	if _, err := service.Login(context.Background(), LoginInput{Username: exampleUsername, Password: testPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginOnlyRevealsDisabledAfterThePasswordMatches(t *testing.T) {
	repository := newFakeRepository()
	repository.addAccount(t, exampleUsername, testPassword, true)
	service := newTestService(t, repository)
	ctx := context.Background()

	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: "wrong-password"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() with wrong password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: testPassword}); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("Login() with correct password = %v, want ErrAccountDisabled", err)
	}
}

// TestLoginWhileLockedDoesNotRevealTheAccount is the case the order of Login's
// checks exists for.
//
// If being locked were decided before the password, anybody could point five
// wrong passwords at a username and read the answer to learn whether that account
// is real — locked means it exists, invalid credentials means it may not. So a
// locked account answers a wrong password exactly the way an account that is not
// there does, and the lock is only ever spoken of to somebody who proved the
// account is theirs.
func TestLoginWhileLockedDoesNotRevealTheAccount(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)
	ctx := context.Background()

	locked := account
	until := clock.now().Add(time.Minute)
	locked.LockedUntil = &until
	repository.byID[account.User.UserID] = locked
	repository.records[exampleUsername] = locked

	wrongPassword := LoginInput{Username: exampleUsername, Password: otherPassword}
	if result, err := service.Login(ctx, wrongPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() on a locked account with a wrong password = %v (token %q), want %v",
			err, result.Token, ErrInvalidCredentials)
	}
	// The same answer for a username nobody holds, which is the whole point: the
	// two are indistinguishable from out here.
	if _, err := service.Login(ctx, LoginInput{Username: "nobody", Password: otherPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() on a username nobody holds = %v, want %v", err, ErrInvalidCredentials)
	}
	// The failure the locked account's attempt earned was counted — locked is not
	// a reason to stop counting, it is what counting leads to.
	if repository.failureCalls != 1 || repository.failedUser != account.User.UserID {
		t.Errorf("recorded %d failure(s) for account %d, want one for %d",
			repository.failureCalls, repository.failedUser, account.User.UserID)
	}

	if result, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: testPassword}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Login() on a locked account with the right password = %v (token %q), want %v",
			err, result.Token, ErrAccountLocked)
	}
	// A refusal that clears the run would undo the lock by being attempted, and a
	// refusal that counted a failure would mean the owner's own attempt deepens it.
	if repository.clearCalls != 0 || repository.failureCalls != 1 {
		t.Errorf("the locked attempt added %d failure(s) and %d clear(s), want neither",
			repository.failureCalls-1, repository.clearCalls)
	}
}

// TestLoginRecordsTheFailureAtTheServicesClock pins what the adapter is handed.
// It cannot count anything — the rule lives with the columns — but the moment and
// the policy are decided here, and a failure counted against a clock of the
// adapter's own choosing would be measured against a window nobody configured.
func TestLoginRecordsTheFailureAtTheServicesClock(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)

	if _, err := service.Login(context.Background(),
		LoginInput{Username: exampleUsername, Password: otherPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want %v", err, ErrInvalidCredentials)
	}

	if repository.failureCalls != 1 {
		t.Fatalf("recorded %d failures, want 1", repository.failureCalls)
	}
	if repository.failedUser != account.User.UserID {
		t.Errorf("counted a failure for account %d, want %d", repository.failedUser, account.User.UserID)
	}
	if !repository.failedAt.Equal(clock.now()) {
		t.Errorf("counted the failure at %v, want the service's clock reading %v",
			repository.failedAt, clock.now())
	}
	if repository.failedPolicy != lockoutPolicy {
		t.Errorf("counted the failure under %+v, want the configured %+v",
			repository.failedPolicy, lockoutPolicy)
	}
}

func TestAuthenticateReloadsTheAccount(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	ctx := context.Background()

	token, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if user.UserID != account.User.UserID || user.Username != exampleUsername {
		t.Errorf("user = %+v, want %+v", user, account.User)
	}

	t.Run("deleted account", func(t *testing.T) {
		delete(repository.byID, account.User.UserID)
		if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Authenticate() error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("disabled account", func(t *testing.T) {
		disabled := account
		disabled.Disabled = true
		repository.byID[account.User.UserID] = disabled
		if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrAccountDisabled) {
			t.Fatalf("Authenticate() error = %v, want ErrAccountDisabled", err)
		}
	})

	t.Run("token from another signer", func(t *testing.T) {
		other, err := NewService(Options{
			Secret:    strings.Repeat("b", 32),
			Issuer:    exampleTokenIssuer,
			TTL:       testTokenTTL,
			TicketTTL: testTicketTTL,
			Lockout:   lockoutPolicy,
		}, repository)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Authenticate(ctx, token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Authenticate() error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("garbage token", func(t *testing.T) {
		if _, err := service.Authenticate(ctx, "not.a.token"); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Authenticate() error = %v, want ErrInvalidToken", err)
		}
	})
}

// testClock is the service's one clock, moved by hand.

func TestNewServiceValidatesOptions(t *testing.T) {
	repository := newFakeRepository()
	tests := []struct {
		name    string
		options Options
		repo    Repository
	}{
		// Every case below leaves exactly one thing wrong and everything else
		// right, so a rejection names the field it is about rather than whichever
		// check happens to run first.
		{name: "short secret", options: Options{Secret: "short", Issuer: exampleTokenIssuer, TTL: testTokenTTL, TicketTTL: testTicketTTL, Lockout: lockoutPolicy}, repo: repository},
		{name: "empty issuer", options: Options{Secret: testSecretRepeated, TTL: testTokenTTL, TicketTTL: testTicketTTL, Lockout: lockoutPolicy}, repo: repository},
		{name: "zero ttl", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TicketTTL: testTicketTTL, Lockout: lockoutPolicy}, repo: repository},
		{name: "zero ticket ttl", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TTL: testTokenTTL, Lockout: lockoutPolicy}, repo: repository},
		{name: "missing repository", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TTL: testTokenTTL, TicketTTL: testTicketTTL, Lockout: lockoutPolicy}},
		// A threshold of zero is reached by every failure, so the first wrong
		// password would lock the account. It is refused rather than interpreted,
		// and so are the other two, which have no reading at all.
		{name: "zero lockout threshold", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TTL: testTokenTTL, TicketTTL: testTicketTTL,
			Lockout: FailurePolicy{Window: time.Minute, Duration: time.Minute}}, repo: repository},
		{name: "zero lockout window", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TTL: testTokenTTL, TicketTTL: testTicketTTL,
			Lockout: FailurePolicy{Threshold: 5, Duration: time.Minute}}, repo: repository},
		{name: "zero lockout duration", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TTL: testTokenTTL, TicketTTL: testTicketTTL,
			Lockout: FailurePolicy{Threshold: 5, Window: time.Minute}}, repo: repository},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewService(tt.options, tt.repo); err == nil {
				t.Fatal("NewService() error = nil, want a rejection")
			}
		})
	}
}

func TestEqualizePasswordCheckDoesNotPanicOnOversizedInput(t *testing.T) {
	service := newTestService(t, newFakeRepository())
	service.equalizePasswordCheck(strings.Repeat("a", 200))
}

func TestVerifyPasswordRejectsMismatch(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(string(hash), testPassword) {
		t.Fatal("verifyPassword() = false for the matching password")
	}
	if verifyPassword(string(hash), otherPassword) {
		t.Fatal("verifyPassword() = true for a different password")
	}
}
