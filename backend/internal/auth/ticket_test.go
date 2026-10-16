package auth

// The websocket ticket: what it is worth, what it is not worth, and the two
// rules that make it safe to put in a URL.
//
// The ticket half of this package is worth its own file because its failure
// modes are not the token's. A token arrives in a header the browser controls; a
// ticket arrives in a URL, gets recorded by every hop, and is fetched seconds
// before it is spent. So the cases here are mostly about what a ticket must NOT
// buy — and about the revocation model surviving the trip through the URL.

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

// newTicketService builds a service with a fake repository holding one account.
func newTicketService(t *testing.T) (*Service, *fakeRepository, Record, *testClock) {
	t.Helper()
	repository := newFakeRepository()
	account := repository.addAccount(t, exampleUsername, testPassword, false)
	service := newTestService(t, repository)
	clock := withTestClock(service)
	return service, repository, account, clock
}

func TestIssueAndRedeemRoundTrip(t *testing.T) {
	service, _, account, _ := newTicketService(t)
	ctx := context.Background()

	ticket, expires, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatalf("IssueWebSocketTicket() error = %v", err)
	}
	if ticket == "" {
		t.Fatal("IssueWebSocketTicket() returned an empty ticket")
	}
	if want := service.now().UTC().Add(testTicketTTL); !expires.Equal(want) {
		t.Fatalf("ticket expires at %v, want %v", expires, want)
	}

	user, err := service.Redeem(ctx, ticket)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	// The role comes from the account row, not from the ticket: the ticket
	// carries an id and two moments and nothing else.
	if user.UserID != account.User.UserID || user.Role != account.User.Role {
		t.Fatalf("Redeem() = %+v, want account %+v", user, account.User)
	}
}

// TestATicketIsNotSingleUse records a deliberate property rather than a defect.
//
// Nothing remembers that a ticket was spent, so the same string opens a second
// connection while it lasts. The client contract does not depend on it — the
// frontend asks for a fresh ticket before every attempt — so nothing observable
// changes, and the alternative (process-local bookkeeping, or a table with a
// cleanup job) is a poor trade for a credential that lives for seconds. If a
// later build makes tickets single-use, this case is the one that goes red.
func TestATicketIsNotSingleUse(t *testing.T) {
	service, _, account, _ := newTicketService(t)
	ctx := context.Background()

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := service.Redeem(ctx, ticket); err != nil {
			t.Fatalf("Redeem() attempt %d error = %v, want the ticket to still work "+
				"inside its TTL", attempt, err)
		}
	}
}

func TestRedeemRejectsAnExpiredTicket(t *testing.T) {
	service, _, account, clock := newTicketService(t)
	ctx := context.Background()

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	// It works first, so what the second half reads is the expiry rather than a
	// ticket that never worked.
	if _, err := service.Redeem(ctx, ticket); err != nil {
		t.Fatalf("Redeem() before the expiry = %v", err)
	}

	clock.advance(testTicketTTL + time.Second)
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Redeem() after the TTL = %v, want ErrInvalidTicket", err)
	}
}

// TestRedeemAllowsNoLeewayOnTheExpiry is the ticket's own version of a rule the
// token states differently, and it is written on the boundary rather than past
// it.
//
// The access-token verifier forgives 30 seconds of clock skew, because the token
// and the reader are different machines. A ticket is signed and redeemed by the
// same process reading the same clock, so the same forgiveness would mean
// nothing but a ticket outliving the TTL the deployment wrote down: at
// WS_TICKET_TTL=30s it would live 60. The moment the TTL names is therefore the
// last moment it is good for — copied from the token's verifier, this case goes
// red.
func TestRedeemAllowsNoLeewayOnTheExpiry(t *testing.T) {
	service, _, account, clock := newTicketService(t)
	ctx := context.Background()

	ticket, expires, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(testTicketTTL)
	if !clock.now().Equal(expires) {
		t.Fatalf("the clock reads %v and the ticket expires at %v; this case means to "+
			"stand on the expiry itself", clock.now(), expires)
	}
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Redeem() at the moment of expiry = %v, want ErrInvalidTicket", err)
	}
}

func TestRedeemRejectsAForgedTicket(t *testing.T) {
	service, _, account, _ := newTicketService(t)
	ctx := context.Background()

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, signature, found := strings.Cut(ticket, ".")
	if !found {
		t.Fatalf("the issued ticket %q has no signature", ticket)
	}

	// A different payload wearing the signature that was issued. The prefix keeps
	// the version and the id stands where the account's does, so the only thing
	// refusing this is the MAC over the payload.
	forgedPayload := base64.RawURLEncoding.EncodeToString([]byte("v1|1|1000000|9999999999"))

	// One bit flipped in the MAC itself.
	decoded, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		t.Fatal(err)
	}
	decoded[0] ^= 0x01
	flippedMAC := base64.RawURLEncoding.EncodeToString(decoded)

	tests := []struct {
		name   string
		ticket string
	}{
		{name: "payload swapped under the signature", ticket: forgedPayload + "." + signature},
		{name: "one bit of the signature flipped", ticket: encoded + "." + flippedMAC},
		{name: "no separator at all", ticket: ticket[:len(ticket)/2]},
		{name: "nothing signed", ticket: encoded + "."},
		{name: "not base64", ticket: "!!!.???"},
		{name: "empty", ticket: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.Redeem(ctx, tt.ticket); !errors.Is(err, ErrInvalidTicket) {
				t.Fatalf("Redeem() = %v, want ErrInvalidTicket", err)
			}
		})
	}
}

func TestRedeemRejectsATicketFromAnotherSecret(t *testing.T) {
	service, repository, account, _ := newTicketService(t)
	ctx := context.Background()

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
	ticket, _, err := other.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Redeem() with a ticket signed by another deployment = %v, want ErrInvalidTicket", err)
	}
}

// TestTheTwoCredentialsDoNotSubstituteForEachOther pins the domain separation
// between the token signer and the ticket signer. They share a secret; they must
// not share an input space.
func TestTheTwoCredentialsDoNotSubstituteForEachOther(t *testing.T) {
	service, _, account, _ := newTicketService(t)
	ctx := context.Background()

	token, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, token); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Redeem(access token) = %v, want ErrInvalidTicket", err)
	}

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, ticket); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Authenticate(ticket) = %v, want ErrInvalidToken", err)
	}
}

// TestRedeemRefusesATicketWhoseAccountIsGone covers both ways an account can stop
// being there for this route: it has no row, or it has been disabled.
func TestRedeemRefusesATicketWhoseAccountIsGone(t *testing.T) {
	service, repository, account, _ := newTicketService(t)
	ctx := context.Background()

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}

	disabled := account
	disabled.Disabled = true
	repository.byID[account.User.UserID] = disabled
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("Redeem() on a disabled account = %v, want ErrAccountDisabled", err)
	}

	delete(repository.byID, account.User.UserID)
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Redeem() on an account with no row = %v, want ErrInvalidToken", err)
	}
}

// TestRedeemRefusesATicketIssuedBeforeRevocation is the reason the ticket is a
// pointer rather than a claim.
//
// A ticket is minted moments before it is spent, so a build that verified the
// signature and trusted the account id inside it would pass every other case in
// this file — and would hand a working socket to anyone still holding a
// pre-logout ticket. Redeem resolves the account instead, so the ticket answers
// to the same revocation moment the account's tokens do.
func TestRedeemRefusesATicketIssuedBeforeRevocation(t *testing.T) {
	service, _, account, clock := newTicketService(t)
	ctx := context.Background()

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, ticket); err != nil {
		t.Fatalf("Redeem() before any revocation = %v", err)
	}

	clock.advance(2 * time.Second)
	if err := service.RevokeTokens(ctx, account.User.UserID); err != nil {
		t.Fatalf("RevokeTokens() error = %v", err)
	}
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Redeem() with a ticket issued before the revocation = %v, want ErrInvalidToken", err)
	}

	// A ticket minted after the moment counts, so the case above is about the
	// revocation and not about tickets having stopped working altogether.
	clock.advance(2 * time.Second)
	later, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, later); err != nil {
		t.Fatalf("Redeem() with a ticket issued after the revocation = %v", err)
	}
}

// TestATicketAndATokenMintedInTheSameMomentAreRevokedTogether is the boundary
// case, stated as the thing it is: the two credentials must not disagree.
//
// The account's token_valid_after column holds whole seconds, and the rule
// refuses a credential minted at or before it. A token's iat is whole seconds by
// construction. A ticket's stamp is only whole seconds because this package
// makes it so — and a stamp carrying the fraction of a second it was created in
// would read as "minted after the revocation" and be admitted, while the access
// token minted in the same instant is refused. One revision model, two answers.
//
// The token is used here as the reference rather than as a second subject: it
// cannot be got wrong this way, so if the ticket and it ever part company, the
// ticket is the one that moved.
func TestATicketAndATokenMintedInTheSameMomentAreRevokedTogether(t *testing.T) {
	service, repository, account, clock := newTicketService(t)
	ctx := context.Background()

	// Both credentials are minted at 400ms into a second, so the only whole
	// second either of them can name is the one it is inside of. The revocation
	// is written at that second's start.
	at := clock.now().Add(400 * time.Millisecond)
	service.tickets.now = func() time.Time { return at }
	service.signer.now = func() time.Time { return at }
	service.now = func() time.Time { return at }

	ticket, _, err := service.IssueWebSocketTicket(ctx, account.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := service.signer.Sign(account.User)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.RevokeTokens(ctx, account.User.UserID, at.Truncate(time.Second)); err != nil {
		t.Fatalf("RevokeTokens() error = %v", err)
	}

	if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Authenticate() = %v, want ErrInvalidToken: the reference credential "+
			"is not being refused, so this case is not measuring anything", err)
	}
	if _, err := service.Redeem(ctx, ticket); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Redeem() = %v, want ErrInvalidToken: a ticket minted in the same instant "+
			"as a token that was refused must be refused too", err)
	}
}
