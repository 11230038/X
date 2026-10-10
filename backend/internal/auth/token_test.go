package auth

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testSecret = "0123456789abcdef0123456789abcdef"
	testIssuer = "test-issuer"
)

func newTestSigner(t *testing.T) *TokenSigner {
	t.Helper()
	signer, err := NewTokenSigner(testSecret, testIssuer, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner() error = %v", err)
	}
	return signer
}

func TestTokenSignerRoundTrip(t *testing.T) {
	signer := newTestSigner(t)
	before := time.Now()

	token, expiresAt, err := signer.Sign(User{UserID: 42, Username: "alice", Role: "admin"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if !expiresAt.After(before) || expiresAt.After(time.Now().Add(time.Hour+time.Minute)) {
		t.Fatalf("expiresAt = %s, want about one hour from now", expiresAt)
	}

	claims, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.Username != "alice" || claims.Role != "admin" {
		t.Errorf("claims = %+v", claims)
	}
	if claims.Issuer != testIssuer {
		t.Errorf("issuer = %q, want %q", claims.Issuer, testIssuer)
	}
	userID, err := claims.UserID()
	if err != nil {
		t.Fatalf("UserID() error = %v", err)
	}
	if userID != 42 {
		t.Errorf("userID = %d, want 42", userID)
	}
}

func TestNewTokenSignerValidatesOptions(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		issuer string
		ttl    time.Duration
	}{
		{name: "empty secret", secret: "", issuer: testIssuer, ttl: time.Hour},
		{name: "short secret", secret: strings.Repeat("a", 31), issuer: testIssuer, ttl: time.Hour},
		{name: "empty issuer", secret: testSecret, issuer: "", ttl: time.Hour},
		{name: "zero ttl", secret: testSecret, issuer: testIssuer, ttl: 0},
		{name: "negative ttl", secret: testSecret, issuer: testIssuer, ttl: -time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewTokenSigner(tt.secret, tt.issuer, tt.ttl); err == nil {
				t.Fatal("NewTokenSigner() error = nil, want a rejection")
			}
		})
	}

	if _, err := NewTokenSigner(testSecret, testIssuer, time.Hour); err != nil {
		t.Fatalf("NewTokenSigner() rejected valid options: %v", err)
	}
}

func TestTokenSignerRejectsExpiredToken(t *testing.T) {
	signer := newTestSigner(t)
	signer.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }

	token, _, err := signer.Sign(User{UserID: 1, Username: "alice", Role: "user"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	assertInvalidToken(t, signer, token)
}

func TestTokenSignerRejectsOtherSigners(t *testing.T) {
	signer := newTestSigner(t)
	token, _, err := signer.Sign(User{UserID: 1, Username: "alice", Role: "user"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	otherSecret, err := NewTokenSigner(strings.Repeat("b", 32), testIssuer, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assertInvalidToken(t, otherSecret, token)

	otherIssuer, err := NewTokenSigner(testSecret, "another-issuer", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assertInvalidToken(t, otherIssuer, token)
}

func TestTokenSignerRejectsTamperedTokens(t *testing.T) {
	signer := newTestSigner(t)
	token, _, err := signer.Sign(User{UserID: 1, Username: "alice", Role: "user"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}

	t.Run("flipped payload character", func(t *testing.T) {
		payload := []byte(parts[1])
		if payload[0] == 'A' {
			payload[0] = 'B'
		} else {
			payload[0] = 'A'
		}
		assertInvalidToken(t, signer, parts[0]+"."+string(payload)+"."+parts[2])
	})

	t.Run("flipped signature character", func(t *testing.T) {
		signature := []byte(parts[2])
		if signature[0] == 'A' {
			signature[0] = 'B'
		} else {
			signature[0] = 'A'
		}
		assertInvalidToken(t, signer, parts[0]+"."+parts[1]+"."+string(signature))
	})
}

func TestTokenSignerRejectsUnpinnedAlgorithms(t *testing.T) {
	signer := newTestSigner(t)

	t.Run("none", func(t *testing.T) {
		token, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(1, "alice")).
			SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatal(err)
		}
		assertInvalidToken(t, signer, token)
	})

	t.Run("hs512", func(t *testing.T) {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, validClaims(1, "alice")).SignedString([]byte(testSecret))
		if err != nil {
			t.Fatal(err)
		}
		assertInvalidToken(t, signer, token)
	})
}

func TestTokenSignerRejectsMalformedTokens(t *testing.T) {
	signer := newTestSigner(t)
	tests := map[string]string{
		"empty":             "",
		"single segment":    "abc",
		"two segments":      "abc.def",
		"four segments":     "abc.def.ghi.jkl",
		"invalid base64":    "!!!.!!!.!!!",
		"valid header only": strings.Split(mustSign(t, signer), ".")[0] + ".e30.",
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			assertInvalidToken(t, signer, token)
		})
	}
}

func TestTokenSignerRejectsInvalidClaimSets(t *testing.T) {
	signer := newTestSigner(t)

	tests := map[string]Claims{
		"missing subject": {
			Username: "alice", Role: "user",
			RegisteredClaims: registeredClaims("", time.Now()),
		},
		"non numeric subject": {
			Username: "alice", Role: "user",
			RegisteredClaims: registeredClaims("not-a-number", time.Now()),
		},
		"zero subject": {
			Username: "alice", Role: "user",
			RegisteredClaims: registeredClaims("0", time.Now()),
		},
		"empty username": {
			RegisteredClaims: registeredClaims("1", time.Now()),
		},
	}

	for name, claims := range tests {
		t.Run(name, func(t *testing.T) {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
			if err != nil {
				t.Fatal(err)
			}
			assertInvalidToken(t, signer, token)
		})
	}
}

func TestVerifyErrorDoesNotLeakConfiguration(t *testing.T) {
	signer := newTestSigner(t)
	token, _, err := signer.Sign(User{UserID: 1, Username: "alice", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Verify(token + "x"); err == nil {
		t.Fatal("Verify() error = nil, want a rejection")
	} else if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("error leaked the signing secret: %v", err)
	}
}

func assertInvalidToken(t *testing.T, signer *TokenSigner, token string) {
	t.Helper()
	if _, err := signer.Verify(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func validClaims(userID int64, username string) Claims {
	return Claims{
		Username:         username,
		Role:             "user",
		RegisteredClaims: registeredClaims(strconv.FormatInt(userID, 10), time.Now()),
	}
}

func registeredClaims(subject string, now time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer:    testIssuer,
		Subject:   subject,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}
}

func mustSign(t *testing.T, signer *TokenSigner) string {
	t.Helper()
	token, _, err := signer.Sign(User{UserID: 1, Username: "alice", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	return token
}
