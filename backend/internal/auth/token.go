package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	minSecretBytes = 32
	tokenLeeway    = 30 * time.Second
)

// Claims are the signed contents of one access token.
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// UserID returns the account id carried in the token subject.
func (c Claims) UserID() (int64, error) {
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalidToken
	}
	return id, nil
}

// TokenSigner issues and verifies HS256 access tokens.
type TokenSigner struct {
	secret []byte
	issuer string
	ttl    time.Duration
	now    func() time.Time
}

// NewTokenSigner validates the signing configuration.
func NewTokenSigner(secret, issuer string, ttl time.Duration) (*TokenSigner, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("JWT secret must be at least %d bytes", minSecretBytes)
	}
	if issuer == "" {
		return nil, fmt.Errorf("JWT issuer must not be empty")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("token TTL must be positive")
	}
	return &TokenSigner{secret: []byte(secret), issuer: issuer, ttl: ttl, now: time.Now}, nil
}

// Sign issues one access token for the account and returns its expiry.
func (s *TokenSigner) Sign(user User) (string, time.Time, error) {
	now := s.now().UTC()
	expires := now.Add(s.ttl)
	claims := Claims{
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   strconv.FormatInt(user.UserID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expires, nil
}

// Verify accepts only tokens signed by this signer for this issuer. The signing
// method is pinned instead of read from the token, and every failure collapses
// to ErrInvalidToken so no library detail reaches the client.
func (s *TokenSigner) Verify(raw string) (Claims, error) {
	var claims Claims
	if _, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) {
		return s.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(tokenLeeway),
	); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if claims.Username == "" {
		return Claims{}, ErrInvalidToken
	}
	if _, err := claims.UserID(); err != nil {
		return Claims{}, err
	}
	return claims, nil
}
