package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{name: "lowercase with underscore", username: "test_user"},
		{name: "letters digits dash and underscore", username: "a_b-1"},
		{name: "minimum length", username: "abc"},
		{name: "maximum length", username: strings.Repeat("a", 32)},
		{name: "uppercase is allowed", username: "Alice"},
		{name: "empty", username: "", wantErr: true},
		{name: "one character below minimum", username: "ab", wantErr: true},
		{name: "one character above maximum", username: strings.Repeat("a", 33), wantErr: true},
		{name: "embedded space", username: "test user", wantErr: true},
		{name: "leading space is not trimmed", username: " test_user", wantErr: true},
		{name: "trailing space is not trimmed", username: "test_user ", wantErr: true},
		{name: "at sign", username: "user@example", wantErr: true},
		{name: "dot", username: "user.name", wantErr: true},
		{name: "non ascii letters", username: "用户名", wantErr: true},
		{name: "newline", username: "test\nuser", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.username)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidUsername) {
					t.Fatalf("ValidateUsername(%q) = %v, want ErrInvalidUsername", tt.username, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateUsername(%q) = %v, want nil", tt.username, err)
			}
		})
	}
}

func TestValidatePasswordCountsBytesNotRunes(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "minimum eight bytes", password: "12345678"},
		{name: "eight spaces are significant", password: "        "},
		{name: "maximum seventy two bytes", password: strings.Repeat("a", 72)},
		{name: "twenty four three byte runes", password: strings.Repeat("密", 24)},
		{name: "seven bytes", password: "1234567", wantErr: true},
		{name: "seventy three bytes", password: strings.Repeat("a", 73), wantErr: true},
		{name: "twenty five three byte runes", password: strings.Repeat("密", 25), wantErr: true},
		{name: "empty", password: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPassword) {
					t.Fatalf("byte length %d: got %v, want ErrInvalidPassword", len(tt.password), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("byte length %d: got %v, want nil", len(tt.password), err)
			}
		})
	}
}

func TestLoginUsernameInBounds(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{name: "typical", username: "test_user", want: true},
		{name: "longer than the registration limit", username: strings.Repeat("a", 64), want: true},
		{name: "beyond the lookup bound", username: strings.Repeat("a", 65), want: false},
		{name: "empty", username: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LoginUsernameInBounds(tt.username); got != tt.want {
				t.Fatalf("LoginUsernameInBounds(len %d) = %t, want %t", len(tt.username), got, tt.want)
			}
		})
	}
}
