package auth

import (
	"context"
	"errors"
	"strings"
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
	records     map[string]Record
	byID        map[int64]Record
	nextID      int64
	createCalls int
	findCalls   int
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
		Secret: testSecretRepeated,
		Issuer: exampleTokenIssuer,
		TTL:    testTokenTTL,
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

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
			Secret: strings.Repeat("b", 32),
			Issuer: exampleTokenIssuer,
			TTL:    testTokenTTL,
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

func TestNewServiceValidatesOptions(t *testing.T) {
	repository := newFakeRepository()
	tests := []struct {
		name    string
		options Options
		repo    Repository
	}{
		{name: "short secret", options: Options{Secret: "short", Issuer: exampleTokenIssuer, TTL: testTokenTTL}, repo: repository},
		{name: "empty issuer", options: Options{Secret: testSecretRepeated, TTL: testTokenTTL}, repo: repository},
		{name: "zero ttl", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer}, repo: repository},
		{name: "missing repository", options: Options{Secret: testSecretRepeated, Issuer: exampleTokenIssuer, TTL: testTokenTTL}},
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
