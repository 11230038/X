package auth

// Changing a password, and the two governance writes that stand beside it, run
// against a real seekdb.
//
// What these cases are about is that what was written into users is what the
// next request reads: a revocation only means anything if Authenticate sees the
// moment on the row it loads for every request, and a change of password only
// means anything if the hash the next sign-in compares against is the new one.
// A fake repository would restate both rules and then agree with itself about
// them, so these build the service over the real adapter and ask it through the
// same calls a deployment uses.
//
// The administrator's half of "an admin revokes" and "an admin unlocks" is the
// route, and it is pinned in internal/handler. What is proved here is the half
// only storage can answer: that the account whose tokens were taken back is the
// one whose token stopped working, that it is the only one, and that a refused
// password change wrote nothing.
//
// Nothing here calls t.Parallel: one database per package, empty at each test.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// credentialsPassword is the password these cases move an account onto. It is
// long enough for the registration policy, which the new password is judged by.
const credentialsPassword = "another-correct-horse-battery"

// registerAndSignIn creates one account and returns the token that registering
// it earned, signed by the service under test rather than assembled here: what
// these cases are about is what the service does with a token it issued itself.
func registerAndSignIn(t *testing.T, service *Service, username string) AuthResult {
	t.Helper()
	result, err := service.Register(context.Background(), RegisterInput{
		Username: username, Password: credentialsPassword,
	})
	if err != nil {
		t.Fatalf("register %q: %v", username, err)
	}
	if result.Token == "" {
		t.Fatalf("registering %q issued no token", username)
	}
	return result
}

func TestAdminRevocationKillsTheTargetsLiveTokenOnly(t *testing.T) {
	service, _, clock, account := newLockoutService(t, lockoutPolicy)
	ctx := context.Background()
	bystander := registerAndSignIn(t, service, "somebody-else")

	// The target signs in, so the token this is about is one the service issued
	// on the ordinary path rather than one the test built.
	target, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword})
	if err != nil {
		t.Fatalf("the target signs in: %v", err)
	}
	// A second later, because the revocation is a moment and the token it must
	// catch was signed before it. The corner where the two land in the same
	// second — where the token *was* signed before it and is still refused — has
	// its own case in service_test.go, against the same rule.
	clock.advance(time.Second)
	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("RevokeTokens() = %v", err)
	}

	if _, err := service.Authenticate(ctx, target.Token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("the target's token after the revocation = %v, want %v", err, ErrInvalidToken)
	}
	// The other account is untouched. This half is here rather than assumed
	// because a revocation that reached further than the account it named would
	// look exactly like one that worked.
	if _, err := service.Authenticate(ctx, bystander.Token); err != nil {
		t.Errorf("the other account's token = %v, want it to still work", err)
	}

	// And the account can still sign in. Taking its tokens back is one column and
	// the lock is another (§2.3): an administrator ending somebody's sessions is
	// not the same act as stopping them signing in again, and the two have two
	// routes because they are two decisions.
	clock.advance(time.Second)
	again, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword})
	if err != nil {
		t.Fatalf("signing in after a revocation = %v", err)
	}
	if _, err := service.Authenticate(ctx, again.Token); err != nil {
		t.Errorf("the token the next sign-in earned = %v, want it to work", err)
	}
}

func TestAdminUnlockLetsTheAccountInImmediately(t *testing.T) {
	service, repo, clock, account := newLockoutService(t, lockoutPolicy)
	ctx := context.Background()

	for i := 0; i < lockoutPolicy.Threshold; i++ {
		failLogin(t, service)
	}
	// The right password is what says the account is locked: a wrong one would
	// only say it was wrong (that is the case in lockout_db_test.go).
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("signing in on the locked account = %v, want %v", err, ErrAccountLocked)
	}

	// The administrator's write, through the call the route makes.
	if err := service.Unlock(ctx, account.User.UserID); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}

	// One second of a ten-minute lock has passed, so what let the account in is
	// the unlock and not the clock running out.
	clock.advance(time.Second)
	if state := readLockout(t, repo, account.User.UserID); state != (lockoutState{}) {
		t.Errorf("after the unlock the account reads %+v, want all three columns clear", state)
	}
	succeedLogin(t, service)

	// The run of failures went with the lock rather than staying behind it: the
	// next failure is the first of a new run, which is the difference between an
	// unlock and a lock whose moment has passed.
	failLogin(t, service)
	if state := readLockout(t, repo, account.User.UserID); state.Attempts != 1 {
		t.Errorf("the failure after the unlock counted %d, want a new run of 1", state.Attempts)
	}
	succeedLogin(t, service)
}

func TestChangePasswordRequiresTheOldPassword(t *testing.T) {
	service, _, _, account := newLockoutService(t, lockoutPolicy)
	ctx := context.Background()

	err := service.ChangePassword(ctx, account.User.UserID, "not-the-password", credentialsPassword)
	if !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("ChangePassword() with the wrong current password = %v, want %v", err, ErrWrongPassword)
	}

	// Nothing was written, and this is the half storage has to answer: a version
	// that hashed the new password before checking the old one would pass a case
	// written against a stub and lock the owner out here.
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword}); err != nil {
		t.Errorf("signing in with the old password after a refused change = %v", err)
	}
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: credentialsPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("signing in with the password that was refused = %v, want %v", err, ErrInvalidCredentials)
	}
}

func TestChangePasswordInvalidatesEveryToken(t *testing.T) {
	service, _, clock, account := newLockoutService(t, lockoutPolicy)
	ctx := context.Background()

	// Two sign-ins, so the account holds two tokens and the second is the one
	// that asks for the change. This build cannot tell them apart, so both have
	// to stop counting: a change of password that left another device signed in
	// would be the change not having happened (§2.4).
	first, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword})
	if err != nil {
		t.Fatalf("the first sign-in: %v", err)
	}
	clock.advance(time.Second)
	second, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword})
	if err != nil {
		t.Fatalf("the second sign-in: %v", err)
	}
	clock.advance(time.Second)
	if err := service.ChangePassword(ctx, account.User.UserID, lockoutPassword, credentialsPassword); err != nil {
		t.Fatalf("ChangePassword() = %v", err)
	}

	for _, token := range []struct {
		name string
		text string
	}{
		{"the token that asked", second.Token},
		{"the token issued before it", first.Token},
	} {
		if _, err := service.Authenticate(ctx, token.text); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s = %v, want %v", token.name, err, ErrInvalidToken)
		}
	}

	// And the credential itself has changed, both ways round. The clock moves on
	// first because a token carries the moment it was signed, and a token signed
	// in the same second as the revocation is the one fail-closed refuses — by
	// design, and with its own case in service_test.go.
	clock.advance(time.Second)
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword}); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("signing in with the old password after the change = %v, want %v", err, ErrInvalidCredentials)
	}
	replacement, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: credentialsPassword})
	if err != nil {
		t.Fatalf("signing in with the new password = %v", err)
	}
	if _, err := service.Authenticate(ctx, replacement.Token); err != nil {
		t.Errorf("the token the new password earned = %v, want it to work", err)
	}
}
