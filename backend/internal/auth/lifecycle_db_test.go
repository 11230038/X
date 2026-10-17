package auth

// The account-lifecycle half of the service, run against a real seekdb.
//
// The claims that only a row can settle are here: that a created account really
// can sign in with the password it was given and carries the role and preset
// that were chosen, that a reset really replaces the hash and really takes back
// the tokens signed before it, that disabling really stops a login on the next
// request, and that deleting an account really removes what hangs off it.
//
// Nothing here calls t.Parallel: one database per package, empty at each test.

import (
	"context"
	"errors"
	"testing"

	"backend/internal/data"
)

// newLifecycleService builds the service over real storage, with the clock a
// case can move.
func newLifecycleService(
	t *testing.T, superAdminIDs []int64,
) (*Service, *GORMRepository, *data.Resource, *testClock) {
	t.Helper()
	repository, resource := newDBRepository(t)
	service, err := NewService(Options{
		Secret:            testSecretRepeated,
		Issuer:            exampleTokenIssuer,
		TTL:               testTokenTTL,
		TicketTTL:         testTicketTTL,
		Lockout:           lockoutPolicy,
		SuperAdminUserIDs: superAdminIDs,
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	clock := withTestClock(service)
	return service, repository, resource, clock
}

func storedAccountOf(t *testing.T, resource *data.Resource, userID int64) storedAccount {
	t.Helper()
	var account storedAccount
	err := resource.GORM().
		Raw("SELECT user_id, username, role, preset, avatar, disable, learner_profile, extra "+
			"FROM users WHERE user_id = ?", userID).
		Scan(&account).Error
	if err != nil {
		t.Fatalf("read the account %d: %v", userID, err)
	}
	return account
}

func TestCreateAccountOverRealStorageCanSignInAndCarriesItsRoleAndPreset(t *testing.T) {
	service, _, resource, _ := newLifecycleService(t, nil)
	const password = "correct-horse-battery"

	user, err := service.CreateAccount(context.Background(), CreateAccountInput{
		Username: "newcomer",
		Password: password,
		Role:     "admin",
		Preset:   "learner",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	// The table's answer, not the adapter's: the role and preset are what the
	// administrator chose, and the hash is a hash.
	stored := storedAccountOf(t, resource, user.UserID)
	if stored.Role != "admin" || stored.Preset != "learner" {
		t.Errorf("stored role=%q preset=%q, want admin and learner", stored.Role, stored.Preset)
	}
	if stored.Username != "newcomer" {
		t.Errorf("stored username = %q, want newcomer", stored.Username)
	}

	// And the account is usable: it signs in with the password it was given.
	result, err := service.Login(context.Background(), LoginInput{
		Username: "newcomer", Password: password,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.User.Role != "admin" || result.User.UserID != user.UserID {
		t.Errorf("login answered %+v, want account %d as an administrator",
			result.User, user.UserID)
	}
}

func TestCreateAccountOverRealStorageRefusesARepeatedUsername(t *testing.T) {
	service, _, _, _ := newLifecycleService(t, nil)
	input := CreateAccountInput{
		Username: "newcomer", Password: "correct-horse-battery", Role: "user", Preset: "standard",
	}
	if _, err := service.CreateAccount(context.Background(), input); err != nil {
		t.Fatalf("first CreateAccount() error = %v", err)
	}
	if _, err := service.CreateAccount(context.Background(), input); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("second CreateAccount() error = %v, want ErrUsernameTaken", err)
	}
}

func TestResetPasswordOverRealStorageReplacesTheHashAndTakesBackTokens(t *testing.T) {
	service, _, _, clock := newLifecycleService(t, nil)
	ctx := context.Background()
	if _, err := service.CreateAccount(ctx, CreateAccountInput{
		Username: "newcomer", Password: "correct-horse-battery", Role: "user", Preset: "standard",
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	before, err := service.Login(ctx, LoginInput{Username: "newcomer", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	// One second later, so the token's `iat` and the revocation moment cannot be
	// confused by the whole-second truncation both sides use.
	clock.at = clock.at.Add(1)
	if err := service.ResetPassword(ctx, before.User.UserID, "a-much-longer-one"); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}

	if _, err := service.Authenticate(ctx, before.Token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Authenticate() with the old token = %v, want ErrInvalidToken", err)
	}
	if _, err := service.Login(ctx, LoginInput{Username: "newcomer", Password: "correct-horse-battery"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() with the old password = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, LoginInput{Username: "newcomer", Password: "a-much-longer-one"}); err != nil {
		t.Fatalf("Login() with the new password error = %v", err)
	}
}

func TestSetDisabledOverRealStorageRefusesTheNextRequestImmediately(t *testing.T) {
	service, _, _, _ := newLifecycleService(t, nil)
	ctx := context.Background()
	if _, err := service.CreateAccount(ctx, CreateAccountInput{
		Username: "newcomer", Password: "correct-horse-battery", Role: "user", Preset: "standard",
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	session, err := service.Login(ctx, LoginInput{Username: "newcomer", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if err := service.SetDisabled(ctx, session.User.UserID, true); err != nil {
		t.Fatalf("SetDisabled(true) error = %v", err)
	}
	// Two separate answers, and the order matters: the token is refused because
	// the account is disabled, and the login is refused for the same reason after
	// the password is known to be right.
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("Authenticate() = %v, want ErrAccountDisabled", err)
	}
	if _, err := service.Login(ctx, LoginInput{
		Username: "newcomer", Password: "correct-horse-battery",
	}); !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("Login() = %v, want ErrAccountDisabled", err)
	}

	if err := service.SetDisabled(ctx, session.User.UserID, false); err != nil {
		t.Fatalf("SetDisabled(false) error = %v", err)
	}
	if _, err := service.Login(ctx, LoginInput{
		Username: "newcomer", Password: "correct-horse-battery",
	}); err != nil {
		t.Fatalf("Login() after enabling error = %v", err)
	}
	// Enabling does not bring back the token that was taken back with the
	// disable: that person signs in again, which is the only way to get one.
	if _, err := service.Authenticate(ctx, session.Token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Authenticate() with the pre-disable token = %v, want ErrInvalidToken", err)
	}
}

func TestDeleteAccountOverRealStorageCascadesWhatItOwns(t *testing.T) {
	service, _, resource, _ := newLifecycleService(t, nil)
	ctx := context.Background()
	user, err := service.CreateAccount(ctx, CreateAccountInput{
		Username: "newcomer", Password: "correct-horse-battery", Role: "user", Preset: "standard",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	err = resource.GORM().Exec(
		"INSERT INTO sessions (session_id, title, owner_id) VALUES (?, ?, ?)",
		"ses_owned", "owned by the account", user.UserID,
	).Error
	if err != nil {
		t.Fatalf("write a session owned by the account: %v", err)
	}

	if err := service.DeleteAccount(ctx, user.UserID); err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}

	var users int64
	if err := resource.GORM().Raw(
		"SELECT COUNT(*) FROM users WHERE user_id = ?", user.UserID).Scan(&users).Error; err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Errorf("users rows = %d, want 0", users)
	}
	var sessions int64
	if err := resource.GORM().Raw(
		"SELECT COUNT(*) FROM sessions WHERE owner_id = ?", user.UserID).Scan(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Errorf("sessions rows = %d, want 0: the cascade did not run", sessions)
	}

	// The account is gone, and a second delete says so rather than pretending it
	// did the work a second time.
	if err := service.DeleteAccount(ctx, user.UserID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("second DeleteAccount() error = %v, want ErrUserNotFound", err)
	}
}

func TestVerifySuperAdminsOverRealStorageChecksTheRow(t *testing.T) {
	service, repository, _, _ := newLifecycleService(t, nil)
	user, err := service.CreateAccount(context.Background(), CreateAccountInput{
		Username: "newcomer", Password: "correct-horse-battery", Role: "admin", Preset: "standard",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	// A second service over the same storage with the account on its allowlist:
	// the allowlist is configuration, and what is being checked is the row it
	// points at, not what the first service knew.
	allowed, err := NewService(Options{
		Secret:            testSecretRepeated,
		Issuer:            exampleTokenIssuer,
		TTL:               testTokenTTL,
		TicketTTL:         testTicketTTL,
		Lockout:           lockoutPolicy,
		SuperAdminUserIDs: []int64{user.UserID},
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := allowed.VerifySuperAdmins(context.Background()); err != nil {
		t.Fatalf("VerifySuperAdmins() error = %v with the account present", err)
	}

	missing, err := NewService(Options{
		Secret:            testSecretRepeated,
		Issuer:            exampleTokenIssuer,
		TTL:               testTokenTTL,
		TicketTTL:         testTicketTTL,
		Lockout:           lockoutPolicy,
		SuperAdminUserIDs: []int64{user.UserID + 1000},
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := missing.VerifySuperAdmins(context.Background()); err == nil {
		t.Fatal("VerifySuperAdmins() accepted an id with no row behind it")
	}
}
