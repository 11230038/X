package auth

// The login lockout, run against a real seekdb.
//
// Every part of this rule is a comparison between moments and a write into a row:
// is the run of failures still inside its window, has the count reached the
// threshold, has the lock's moment gone by. A fake repository can only restate
// that rule and then agree with itself, so the cases that make those claims build
// the service over the real adapter — the same login path a deployment runs, with
// the real clock handed to it by the test.
//
// The two boundaries are what a reader should look at. A failure while the window
// is still open is one more of the same run; a failure at the moment the window
// ends is the first of a new one. And a lock ends at the moment it names rather
// than a second afterwards, which is the difference between being refused and
// being let in when the account's owner tries again.
//
// Nothing here calls t.Parallel: one database per package, empty at each test.

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// lockoutPassword is hashed into the account these cases sign in as, at the
// cheapest cost bcrypt offers: what is being tested is the counting, not the
// hashing, and the production cost would put seconds into every failure.
const lockoutPassword = "correct-horse-battery-staple"

// lockoutPolicy is the shape these cases run under. The lock is deliberately
// shorter than the window, because that is what makes "the lock ran out but the
// run of failures has not" a state a case can stand in.
var lockoutPolicy = FailurePolicy{
	Threshold: 3,
	Window:    15 * time.Minute,
	Duration:  10 * time.Minute,
}

// newLockoutService builds the whole sign-in path over real storage: the service,
// the adapter, and one account whose stored password is a real hash of
// lockoutPassword.
func newLockoutService(t *testing.T, policy FailurePolicy) (*Service, *GORMRepository, *testClock, Record) {
	t.Helper()
	repo, _ := newDBRepository(t)
	service, err := NewService(Options{
		Secret:    testSecretRepeated,
		Issuer:    exampleTokenIssuer,
		TTL:       testTokenTTL,
		TicketTTL: testTicketTTL,
		Lockout:   policy,
	}, repo)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	clock := withTestClock(service)
	hash, err := bcrypt.GenerateFromPassword([]byte(lockoutPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	account, err := repo.Create(context.Background(), CreateParams{
		Username:     exampleUsername,
		PasswordHash: string(hash),
	})
	if err != nil {
		t.Fatalf("create the account: %v", err)
	}
	return service, repo, clock, account
}

// failLogin sends one wrong password and insists on the only answer a wrong
// password ever gets. It is not always ErrInvalidCredentials' business to be the
// answer — being locked is not being told — so a case that expects something else
// does not use this.
func failLogin(t *testing.T, service *Service) {
	t.Helper()
	result, err := service.Login(context.Background(), LoginInput{
		Username: exampleUsername, Password: "not-the-password",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() with a wrong password = %v (token %q), want %v",
			err, result.Token, ErrInvalidCredentials)
	}
}

func succeedLogin(t *testing.T, service *Service) {
	t.Helper()
	result, err := service.Login(context.Background(), LoginInput{
		Username: exampleUsername, Password: lockoutPassword,
	})
	if err != nil {
		t.Fatalf("Login() with the right password = %v", err)
	}
	if result.Token == "" {
		t.Error("a sign-in that reported success issued no token")
	}
}

// lockoutState is the three columns as one comparable value, read back through the
// adapter's own lookup. What the login decided and what the table holds are two
// answers, and only the second one outlives the process.
type lockoutState struct {
	Attempts    int
	WindowStart *time.Time
	LockedUntil *time.Time
}

func readLockout(t *testing.T, repo *GORMRepository, userID int64) lockoutState {
	t.Helper()
	record, err := repo.FindByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("read the account back: %v", err)
	}
	return lockoutState{
		Attempts:    record.FailedAttempts,
		WindowStart: record.FailedWindowStart,
		LockedUntil: record.LockedUntil,
	}
}

func TestLoginLocksAfterThresholdAndRefusesTheRightPassword(t *testing.T) {
	service, repo, clock, account := newLockoutService(t, lockoutPolicy)

	// One short of the threshold, every one of them just a wrong password.
	for i := 1; i < lockoutPolicy.Threshold; i++ {
		failLogin(t, service)
	}
	state := readLockout(t, repo, account.User.UserID)
	if state.Attempts != lockoutPolicy.Threshold-1 || state.LockedUntil != nil {
		t.Fatalf("after %d failures the account reads %+v, want that count and no lock",
			lockoutPolicy.Threshold-1, state)
	}

	// The one that reaches the threshold is the one that locks it, and what it
	// writes is a moment the configured duration away — read off the service's
	// clock rather than the database's, because the window it will be compared
	// against was read off the same clock.
	failLogin(t, service)
	state = readLockout(t, repo, account.User.UserID)
	if state.Attempts != lockoutPolicy.Threshold {
		t.Errorf("the count is %d, want %d", state.Attempts, lockoutPolicy.Threshold)
	}
	if want := clock.now().Add(lockoutPolicy.Duration); state.LockedUntil == nil || !state.LockedUntil.Equal(want) {
		t.Fatalf("the lock runs until %v, want %v", state.LockedUntil, want)
	}

	// The account's owner cannot get in either, and it is the right password that
	// reveals it: this is the lock working, and the cost it was accepted with.
	result, err := service.Login(context.Background(), LoginInput{
		Username: exampleUsername, Password: lockoutPassword,
	})
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Login() with the right password on a locked account = %v (token %q), want %v",
			err, result.Token, ErrAccountLocked)
	}
	if result.Token != "" {
		t.Error("a locked account was issued a token")
	}
}

func TestFailedAttemptsExpireWithTheWindow(t *testing.T) {
	service, repo, clock, account := newLockoutService(t, lockoutPolicy)

	// A run that stops one short of the threshold, and the moment it started.
	for i := 1; i < lockoutPolicy.Threshold; i++ {
		failLogin(t, service)
	}
	start := readLockout(t, repo, account.User.UserID).WindowStart
	if start == nil {
		t.Fatal("the failures were counted without opening a window")
	}

	// Waiting out the window ends the run: the next failure is the first of a new
	// one rather than the one that reaches the threshold. The moment chosen is the
	// window's own end, because the two sides to check are "still inside" and
	// "already past", and this is where they meet.
	clock.advance(lockoutPolicy.Window)
	failLogin(t, service)
	state := readLockout(t, repo, account.User.UserID)
	if state.Attempts != 1 {
		t.Errorf("a failure at the end of the window counts %d, want a new run of 1", state.Attempts)
	}
	if state.LockedUntil != nil {
		t.Errorf("the account is locked until %v after one failure of a new run", state.LockedUntil)
	}
	if state.WindowStart == nil || !state.WindowStart.Equal(clock.now()) {
		t.Errorf("the new run started at %v, want the moment of its first failure %v",
			state.WindowStart, clock.now())
	}
	if state.WindowStart != nil && state.WindowStart.Equal(*start) {
		t.Error("the run kept the start it had, want a new window")
	}

	// And a window running out is not a lock: the right password still gets in.
	succeedLogin(t, service)
}

func TestSuccessfulLoginClearsTheFailureCount(t *testing.T) {
	service, repo, clock, account := newLockoutService(t, lockoutPolicy)

	for i := 1; i < lockoutPolicy.Threshold; i++ {
		failLogin(t, service)
	}
	succeedLogin(t, service)

	// Signing in leaves nothing behind: not the count, and not the window it was
	// counted in either, because a window whose count is zero is a window that has
	// nothing to say.
	if state := readLockout(t, repo, account.User.UserID); state != (lockoutState{}) {
		t.Errorf("after signing in the account reads %+v, want the count and both moments cleared", state)
	}

	// And the clear is real rather than cosmetic: the same failures again do not
	// lock the account, which they would if the count had been carried over.
	clock.advance(time.Minute)
	for i := 1; i < lockoutPolicy.Threshold; i++ {
		failLogin(t, service)
	}
	state := readLockout(t, repo, account.User.UserID)
	if state.Attempts != lockoutPolicy.Threshold-1 || state.LockedUntil != nil {
		t.Errorf("after signing in and failing again the account reads %+v, want %d and no lock",
			state, lockoutPolicy.Threshold-1)
	}
	succeedLogin(t, service)
}

func TestLockExpiresAfterTheDuration(t *testing.T) {
	service, repo, clock, account := newLockoutService(t, lockoutPolicy)
	ctx := context.Background()

	for i := 0; i < lockoutPolicy.Threshold; i++ {
		failLogin(t, service)
	}

	// One second short of the lock's length, still refused.
	clock.advance(lockoutPolicy.Duration - time.Second)
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Login() one second before the lock ends = %v, want %v", err, ErrAccountLocked)
	}

	// At the moment itself it is not locked any more. "Until" means until, so the
	// refusal ends at the moment it names rather than a second later — and that is
	// the side the boundary is decided on: waiting a second longer to be told the
	// lock is over would be a second nobody could explain.
	clock.advance(time.Second)
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: "not-the-password"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() with a wrong password the moment the lock ends = %v, want %v", err, ErrInvalidCredentials)
	}
	// That failure locked it again, because the run it belongs to is not over: the
	// count is at the threshold and the window has not passed. A lock running out
	// is not a pardon, which is the difference between this and the case above —
	// there the window had run out, here only the lock had.
	state := readLockout(t, repo, account.User.UserID)
	if want := clock.now().Add(lockoutPolicy.Duration); state.LockedUntil == nil || !state.LockedUntil.Equal(want) {
		t.Fatalf("the failure at the end of the lock left it until %v, want %v", state.LockedUntil, want)
	}
	if _, err := service.Login(ctx, LoginInput{Username: exampleUsername, Password: lockoutPassword}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Login() with the right password after the relock = %v, want %v", err, ErrAccountLocked)
	}

	// Waiting out the whole lock, and the account's owner is let in.
	clock.advance(lockoutPolicy.Duration)
	succeedLogin(t, service)
}
