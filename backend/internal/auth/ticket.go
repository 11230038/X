package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The websocket ticket exists because a browser cannot set an Authorization
// header on a WebSocket handshake: `new WebSocket(url, protocols)` takes a
// subprotocol list, not headers. The browser therefore trades its access token
// for a ticket over an ordinary HTTP request — which can carry the header — and
// then puts the ticket in the connection's URL.
//
// A URL is recorded by every hop it passes through: access logs, proxies, APM.
// The ticket does not shrink that exposure; it makes the recorded string stop
// working before anyone gets around to reading it. Everything below follows from
// that one sentence:
//
//   - the ticket is short-lived, and short is enforced at the top (see
//     config.maxTicketTTL) rather than left to whoever writes the .env;
//   - the ticket is NOT single-use. Nothing here remembers that a ticket was
//     spent, so within its TTL the same string opens a second connection. That
//     is a deliberate cost, not an oversight: tracking spent tickets means
//     either process-local state (which breaks the moment there are two
//     replicas) or a table with a cleanup job, both of them a poor trade for a
//     credential whose whole life is measured in seconds. The client contract
//     does not depend on it — the frontend asks for a fresh ticket before every
//     connection attempt — so the difference is unobservable from the browser.
//     If a future build wants true single use, it belongs behind Redeem's
//     signature, not in this file's format.
//
// The ticket carries an account id and two moments, and nothing else. Not the
// role, not the username: Redeem resolves the account from the database, so a
// ticket is a pointer to an identity rather than a claim about one.
const (
	ticketVersion = "v1"
	// ticketPrefix separates this MAC's input space from every other use of the
	// same secret. The JWT signer hashes base64url(header).base64url(payload),
	// which cannot begin with this string, so the two can never be confused —
	// but a token and a ticket are different things and the prefix says so in
	// the code rather than in an argument about encodings.
	ticketPrefix = "deeptutor.ws-ticket.v1|"
)

// ticketSigner issues and verifies websocket tickets. It shares the access
// token's secret, with the prefix above keeping the two apart.
type ticketSigner struct {
	secret []byte
	ttl    time.Duration
	// now is the same clock Service reads. See Service.now for why there is only
	// one.
	now func() time.Time
}

func newTicketSigner(secret string, ttl time.Duration) (*ticketSigner, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("JWT secret must be at least %d bytes", minSecretBytes)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("websocket ticket TTL must be positive")
	}
	return &ticketSigner{secret: []byte(secret), ttl: ttl, now: time.Now}, nil
}

// sign issues one ticket for the account and returns its expiry.
//
// The stamp travels as whole seconds, and that is not cosmetic. Redemption
// compares it against the account's token_valid_after column, which holds whole
// seconds, and refuses a ticket stamped at or before it. A stamp carrying the
// fraction of a second it was created in would slip through that comparison: a
// ticket issued at 12:00:00.400 on an account revoked at 12:00:00 would read as
// "issued after the revocation" and be admitted, while the access token issued
// in the same second is refused — the same revision model answering two ways
// depending on which credential asked. The whole-second form is what holds that
// line; TestRedeemRefusesATicketIssuedInTheSameSecondAsRevocation is the case
// that would catch a change to it.
//
// The explicit truncation is belt to the encoding's braces: it puts the expiry
// on a whole second too, so the moment handed back to the client is the same
// kind of number as the one the access-token response carries.
func (s *ticketSigner) sign(userID int64) (string, time.Time, error) {
	if userID <= 0 {
		return "", time.Time{}, ErrUserNotFound
	}
	issuedAt := s.now().UTC().Truncate(time.Second)
	expires := issuedAt.Add(s.ttl)
	payload := strings.Join([]string{
		ticketVersion,
		strconv.FormatInt(userID, 10),
		strconv.FormatInt(issuedAt.Unix(), 10),
		strconv.FormatInt(expires.Unix(), 10),
	}, "|")
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload)), expires, nil
}

// verify accepts only tickets this signer issued, and only while they last.
//
// Every failure answers ErrInvalidTicket, so a caller cannot tell a forged
// ticket from an expired one or from a string that was never a ticket at all.
func (s *ticketSigner) verify(raw string) (int64, time.Time, error) {
	encoded, signature, found := strings.Cut(raw, ".")
	if !found || encoded == "" || signature == "" {
		return 0, time.Time{}, ErrInvalidTicket
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, time.Time{}, ErrInvalidTicket
	}
	payload := string(decoded)
	// The signature is checked over the decoded text rather than over the
	// encoded form, so a ticket cannot be re-encoded into a second spelling of
	// itself that the deduplication above would not recognise.
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return 0, time.Time{}, ErrInvalidTicket
	}
	if !hmac.Equal(provided, s.mac(payload)) {
		return 0, time.Time{}, ErrInvalidTicket
	}

	parts := strings.Split(payload, "|")
	if len(parts) != 4 || parts[0] != ticketVersion {
		return 0, time.Time{}, ErrInvalidTicket
	}
	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || userID <= 0 {
		return 0, time.Time{}, ErrInvalidTicket
	}
	issuedSeconds, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, time.Time{}, ErrInvalidTicket
	}
	expirySeconds, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return 0, time.Time{}, ErrInvalidTicket
	}

	// No leeway. The access-token verifier allows 30 seconds of clock slack
	// because the token and the reader may be different machines; a ticket is
	// signed and redeemed by the same process reading the same clock, so slack
	// here would only mean a ticket outliving the TTL the deployment wrote down.
	if !s.now().UTC().Before(time.Unix(expirySeconds, 0)) {
		return 0, time.Time{}, ErrInvalidTicket
	}
	// Rebuilt from the whole seconds on the wire rather than from time.Now, so
	// redemption asks the revocation question about the moment the ticket was
	// issued rather than about the moment it was presented.
	issuedAt := time.Unix(issuedSeconds, 0).UTC()
	if issuedAt.After(time.Unix(expirySeconds, 0)) {
		return 0, time.Time{}, ErrInvalidTicket
	}
	return userID, issuedAt, nil
}

func (s *ticketSigner) mac(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(ticketPrefix))
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// IssueWebSocketTicket issues the one-shot credential a browser exchanges for a
// WebSocket connection.
//
// It does not read the database. The route that calls it sits behind
// RequireAuth, so the account has already been reloaded and its disabled flag
// already answered for this request; a second read would only be a second
// answer to a question that was just asked. The context is taken for the same
// reason Redeem takes one — an implementation that does want to write something
// down should not have to change its signature to get it.
func (s *Service) IssueWebSocketTicket(_ context.Context, userID int64) (string, time.Time, error) {
	return s.tickets.sign(userID)
}

// Redeem exchanges a ticket for the account it points at.
//
// A verified signature is not an identity, so this does not return what the
// ticket says — it returns what the account is now. The database read and the
// two checks that follow are the same ones Authenticate makes (see resolve), so
// a ticket issued before a logout, a password change, an administrative
// revocation, or a disable stops working at the same moment that account's
// access tokens do. Putting the ticket in a URL must not put a hole in the
// revocation model.
func (s *Service) Redeem(ctx context.Context, ticket string) (User, error) {
	userID, issuedAt, err := s.tickets.verify(ticket)
	if err != nil {
		return User{}, err
	}
	return s.resolve(ctx, userID, issuedAt)
}
