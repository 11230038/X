package auth

// Revocation: the moment a token stops being accepted, and the clock the cases
// move by hand to put that moment where they want it.
//
// This half is separate from the credential half because it is the half with a
// clock in it. Everything here is about a comparison between two instants, and
// the setup it needs — one clock shared by the signer and the service — is not
// the setup register and login need.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A rule measured in whole seconds cannot be tested against a clock that moves on
// its own: whether a token was signed before the revocation or in the same second
// as it is a difference the test has to choose, not wait for. The clock is
// anchored at the real now because the token's expiry is still checked against
// the real one — jwt does not ask this package what time it is.
type testClock struct{ at time.Time }

func (c *testClock) now() time.Time { return c.at }

func (c *testClock) advance(d time.Duration) { c.at = c.at.Add(d) }

// withTestClock hands the service and its signer the same clock, which is the
// arrangement NewService makes in production. Two clocks would put the two
// moments this rule compares — a token's iat and the revocation — on two scales,
// and that is the whole of what "one clock, two uses" is for.
func withTestClock(service *Service) *testClock {
	clock := &testClock{at: time.Now().UTC().Truncate(time.Second)}
	service.now = clock.now
	service.signer.now = clock.now
	service.tickets.now = clock.now
	return clock
}

func TestAuthenticateRejectsTokenIssuedBeforeRevocation(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)
	ctx := context.Background()

	issued, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	// It works up to the moment it is taken back, so that what the second half
	// reads is the revocation and not a token that never worked.
	if _, err := service.Authenticate(ctx, issued); err != nil {
		t.Fatalf("Authenticate() before any revocation = %v", err)
	}

	clock.advance(2 * time.Second)
	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("RevokeTokens() error = %v", err)
	}
	if repository.revokedUser != account.User.UserID || !repository.revokedAt.Equal(clock.now()) {
		t.Fatalf("the repository was handed account %d at %v, want %d at %v",
			repository.revokedUser, repository.revokedAt, account.User.UserID, clock.now())
	}

	if _, err := service.Authenticate(ctx, issued); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Authenticate() with a token signed before the revocation = %v, want ErrInvalidToken", err)
	}
}

func TestAuthenticateAcceptsTokenIssuedAfterRevocation(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)
	ctx := context.Background()

	before, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("RevokeTokens() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, before); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Authenticate() = %v, want the old token refused", err)
	}

	// What was taken back is what had been signed, not the account: the token
	// signing in again produces is stamped later than the moment, and it counts.
	clock.advance(2 * time.Second)
	after, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, after); err != nil {
		t.Fatalf("Authenticate() with a token signed after the revocation = %v", err)
	}
}

// TestRevocationAtTheSameSecondStillRefuses pins the side the boundary is decided
// on.
//
// A token's iat is whole seconds and so is the column, so "signed and revoked in
// the same second" cannot be told apart from "signed first". The rule refuses at
// or before the moment — the fail-closed side — because refusing costs the
// account one more sign-in while the other choice costs a revocation that
// quietly did not happen. Written as iat < tva, this case goes red.
func TestRevocationAtTheSameSecondStillRefuses(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	withTestClock(service)
	ctx := context.Background()

	issued, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("RevokeTokens() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, issued); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Authenticate() = %v, want ErrInvalidToken: a token signed in the same "+
			"second as the revocation must be refused", err)
	}
}

// TestLogoutInvalidatesEveryTokenOfTheAccount is the claim the sign-out route
// rests on: one call is enough for tokens signed at different moments, and there
// is no narrower form. Nothing in a token tells it apart from a later one, so
// there is no "this device" to sign out (§2.4).
func TestLogoutInvalidatesEveryTokenOfTheAccount(t *testing.T) {
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)
	ctx := context.Background()

	var issued []string
	for _, wait := range []time.Duration{0, time.Minute, time.Hour} {
		clock.advance(wait)
		token, _, err := service.signer.Sign(account.User)
		if err != nil {
			t.Fatal(err)
		}
		issued = append(issued, token)
	}

	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("RevokeTokens() error = %v", err)
	}
	for i, token := range issued {
		if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("token %d of %d still authenticates after the account signed out: %v",
				i+1, len(issued), err)
		}
	}

	// Signing out twice is the same write rather than an error, because a client
	// that has lost track of whether it signed out will make it.
	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("second RevokeTokens() error = %v", err)
	}
}

func TestRevokeTokensRefusesAnAccountThatCannotBeNamed(t *testing.T) {
	repository := newFakeRepository()
	service := newTestService(t, repository)

	if err := service.RevokeTokens(context.Background(), 0); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("RevokeTokens(0) error = %v, want ErrUserNotFound", err)
	}
	if repository.revokeCalls != 0 {
		t.Fatalf("a request naming no account reached storage %d time(s)", repository.revokeCalls)
	}
}
