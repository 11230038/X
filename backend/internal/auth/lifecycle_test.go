package auth

// The account-lifecycle half of the service, over the in-memory double.
//
// These cases are about what the service decides and what it hands down: which
// role and preset reach the repository, which inputs never reach it at all,
// which moment a revocation is stamped with, and whether the allowlist is
// projected onto every credential. Whether the row then really changes is asked
// against a real database in lifecycle_db_test.go — a fake that also rewrote
// the row would let this file agree with itself about SQL it does not run.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateAccountWritesTheRoleAndPresetTheAdministratorChose(t *testing.T) {
	repository := newFakeRepository()
	service := newTestService(t, repository)

	user, err := service.CreateAccount(context.Background(), CreateAccountInput{
		Username: "newcomer",
		Password: testPassword,
		Role:     "admin",
		Preset:   "learner",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	if repository.lastCreate.Role != "admin" || repository.lastCreate.Preset != "learner" {
		t.Errorf("wrote role=%q preset=%q, want admin and learner",
			repository.lastCreate.Role, repository.lastCreate.Preset)
	}
	if user.Username != "newcomer" || user.Role != "admin" {
		t.Errorf("user = %+v, want newcomer as an administrator", user)
	}
	// No token is minted: the administrator is creating somebody else's
	// credential, and signing that person in would be a session nobody asked for.
	if service.IsSuperAdmin(user.UserID) {
		t.Error("the created account is a super administrator without being on the allowlist")
	}
}

func TestCreateAccountRefusesWhatTheBuildDoesNotWrite(t *testing.T) {
	cases := []struct {
		name  string
		input CreateAccountInput
		want  error
	}{
		{"a username outside the policy",
			CreateAccountInput{Username: "a", Password: testPassword, Role: "user", Preset: "standard"},
			ErrInvalidUsername},
		{"a password outside the policy",
			CreateAccountInput{Username: "newcomer", Password: "short", Role: "user", Preset: "standard"},
			ErrInvalidPassword},
		{"a third role",
			CreateAccountInput{Username: "newcomer", Password: testPassword, Role: "root", Preset: "standard"},
			ErrUnknownRole},
		{"a preset this build does not write",
			CreateAccountInput{Username: "newcomer", Password: testPassword, Role: "user", Preset: "everything"},
			authz.ErrUnknownPreset},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newFakeRepository()
			service := newTestService(t, repository)
			_, err := service.CreateAccount(context.Background(), testCase.input)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("CreateAccount() error = %v, want %v", err, testCase.want)
			}
			// Refused before hashing and before the row: a rejected role is not
			// half an account.
			if repository.createCalls != 0 {
				t.Error("a refused creation reached the repository")
			}
		})
	}
}

func TestTheAllowlistIsProjectedOntoEveryCredential(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestServiceWithSuperAdmins(t, repository, []int64{account.User.UserID})

	login, err := service.Login(context.Background(), LoginInput{
		Username: exampleUsername, Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if !login.User.SuperAdmin {
		t.Error("Login did not project the allowlist onto the account")
	}

	authenticated, err := service.Authenticate(context.Background(), login.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !authenticated.SuperAdmin {
		t.Error("Authenticate did not project the allowlist onto the account")
	}

	// The same account reached by a deployment without the allowlist: the flag
	// is configuration, so it changes with the configuration and not with the
	// token that was minted under an older one.
	plain := newTestService(t, repository)
	authenticated, err = plain.Authenticate(context.Background(), login.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if authenticated.SuperAdmin {
		t.Error("an empty allowlist still marked the account as a super administrator")
	}
}

func TestRegistrationProjectsAnAllowlistedFirstAccount(t *testing.T) {
	repository := newFakeRepository()
	service := newTestServiceWithSuperAdmins(t, repository, []int64{1})

	result, err := service.Register(context.Background(), RegisterInput{
		Username: exampleUsername, Password: testPassword,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if result.User.UserID != 1 || !result.User.SuperAdmin {
		t.Errorf("user = %+v, want account 1 on the allowlist", result.User)
	}
}

func TestVerifySuperAdminsRequiresEveryNamedAccount(t *testing.T) {
	repository := newFakeRepository()
	repository.addAccount(t, exampleUsername, testPassword, false)

	present := newTestServiceWithSuperAdmins(t, repository, []int64{1})
	if err := present.VerifySuperAdmins(context.Background()); err != nil {
		t.Fatalf("VerifySuperAdmins() error = %v with the account present", err)
	}

	// The failure this check exists for: an id written before the account it
	// names. An auto-increment column would hand it to whoever registers next.
	missing := newTestServiceWithSuperAdmins(t, repository, []int64{1, 2})
	err := missing.VerifySuperAdmins(context.Background())
	if err == nil || !strings.Contains(err.Error(), "2") {
		t.Fatalf("VerifySuperAdmins() error = %v, want one naming account 2", err)
	}
}

func TestNewServiceRefusesAnIDThatCannotBeAnAccount(t *testing.T) {
	for _, userID := range []int64{0, -1} {
		_, err := NewService(Options{
			Secret:            testSecretRepeated,
			Issuer:            exampleTokenIssuer,
			TTL:               testTokenTTL,
			TicketTTL:         testTicketTTL,
			Lockout:           lockoutPolicy,
			SuperAdminUserIDs: []int64{userID},
		}, newFakeRepository())
		if err == nil {
			t.Errorf("NewService accepted super administrator id %d", userID)
		}
	}
}

func TestResetPasswordJudgesTheNewPasswordBeforeTouchingStorage(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)

	err := service.ResetPassword(context.Background(), account.User.UserID, "short")
	if !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("ResetPassword() error = %v, want ErrInvalidPassword", err)
	}
	if repository.replaceCalls != 0 {
		t.Error("a password outside the policy reached the repository")
	}
}

func TestResetPasswordWritesANewHashAndOneRevocationMoment(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)

	const replacement = "a-much-longer-one"
	if err := service.ResetPassword(context.Background(), account.User.UserID, replacement); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if repository.replaceCalls != 1 || repository.replacedUser != account.User.UserID {
		t.Fatalf("replace calls = %d for user %d, want one for %d",
			repository.replaceCalls, repository.replacedUser, account.User.UserID)
	}
	if !verifyPassword(repository.replacedHash, replacement) {
		t.Error("the stored hash does not match the new password")
	}
	if repository.replacedAt != clock.now() {
		t.Errorf("revoked at %v, want the service clock's %v",
			repository.replacedAt, clock.now())
	}
}

func TestResetPasswordAnswersAMissingAccountRatherThanSucceeding(t *testing.T) {
	repository := newFakeRepository()
	service := newTestService(t, repository)

	err := service.ResetPassword(context.Background(), 99, "a-much-longer-one")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ResetPassword() error = %v, want ErrUserNotFound", err)
	}
	if repository.replaceCalls != 0 {
		t.Error("a missing account reached the write")
	}
}

func TestSetDisabledWritesTheFlagAndTheRevocationMoment(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)

	if err := service.SetDisabled(context.Background(), account.User.UserID, true); err != nil {
		t.Fatalf("SetDisabled(true) error = %v", err)
	}
	if repository.setDisabledCalls != 1 || !repository.disabledValue {
		t.Fatalf("wrote disabled=%t in %d calls, want true in one",
			repository.disabledValue, repository.setDisabledCalls)
	}
	if repository.disabledAt != clock.now() {
		t.Errorf("written at %v, want the service clock's %v",
			repository.disabledAt, clock.now())
	}
	record, err := repository.FindByID(context.Background(), account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Disabled {
		t.Error("the account is not disabled after disabling it")
	}
	if record.TokenValidAfter == nil {
		t.Error("disabling did not take back the tokens the account holds")
	}

	// Enabling turns the flag back but cannot bring the tokens back: they were
	// taken back, and this operation does not mint them again.
	if err := service.SetDisabled(context.Background(), account.User.UserID, false); err != nil {
		t.Fatalf("SetDisabled(false) error = %v", err)
	}
	record, err = repository.FindByID(context.Background(), account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Disabled {
		t.Error("the account is still disabled after enabling it")
	}
	if record.TokenValidAfter == nil {
		t.Error("enabling erased the revocation moment")
	}
}

func TestSetDisabledAnswersAMissingAccount(t *testing.T) {
	service := newTestService(t, newFakeRepository())
	if err := service.SetDisabled(context.Background(), 99, true); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("SetDisabled() error = %v, want ErrUserNotFound", err)
	}
}

func TestDeleteAccountRemovesTheRowAndAnswersASecondDelete(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)

	if err := service.DeleteAccount(context.Background(), account.User.UserID); err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}
	if _, err := repository.FindByID(context.Background(), account.User.UserID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("FindByID() error = %v after deletion, want ErrUserNotFound", err)
	}
	// "It was already gone" is an answer a caller should hear rather than a
	// success it did not achieve.
	if err := service.DeleteAccount(context.Background(), account.User.UserID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("second DeleteAccount() error = %v, want ErrUserNotFound", err)
	}
}

// The moment a lifecycle write is stamped with comes from the service's clock.
// Pinning the helper itself keeps a future write from reaching for time.Now and
// passing every case above by being roughly right.
func TestLifecycleWritesUseTheServiceClock(t *testing.T) {
	repository := newFakeRepository()
	repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	fixed := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	if err := service.SetDisabled(context.Background(), 1, true); err != nil {
		t.Fatal(err)
	}
	if !repository.disabledAt.Equal(fixed) {
		t.Errorf("disabledAt = %v, want %v", repository.disabledAt, fixed)
	}
	if err := service.ResetPassword(context.Background(), 1, "a-much-longer-one"); err != nil {
		t.Fatal(err)
	}
	if !repository.replacedAt.Equal(fixed) {
		t.Errorf("replacedAt = %v, want %v", repository.replacedAt, fixed)
	}
}

// A hash written by ResetPassword is a bcrypt hash at the build's cost, not a
// plain copy: the one property this file can check without a row.
func TestResetPasswordStoresABcryptHashNotNull(t *testing.T) {
	repository := newFakeRepository()
	repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	const replacement = "a-much-longer-one"
	if err := service.ResetPassword(context.Background(), 1, replacement); err != nil {
		t.Fatal(err)
	}
	if repository.replacedHash == replacement {
		t.Fatal("the password was stored verbatim")
	}
	if _, err := bcrypt.Cost([]byte(repository.replacedHash)); err != nil {
		t.Fatalf("the stored value is not a bcrypt hash: %v", err)
	}
}
