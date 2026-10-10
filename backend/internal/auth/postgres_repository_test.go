package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

func TestMapRepositoryError(t *testing.T) {
	sentinel := errors.New("connection reset")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "record not found", err: gorm.ErrRecordNotFound, want: ErrUserNotFound},
		{name: "wrapped record not found", err: fmt.Errorf("find account: %w", gorm.ErrRecordNotFound), want: ErrUserNotFound},
		{name: "duplicated key", err: gorm.ErrDuplicatedKey, want: ErrUsernameTaken},
		{name: "wrapped duplicated key", err: fmt.Errorf("insert account: %w", gorm.ErrDuplicatedKey), want: ErrUsernameTaken},
		{name: "unrelated failure keeps its identity", err: fmt.Errorf("query: %w", sentinel), want: sentinel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapRepositoryError(tt.err)
			if !errors.Is(got, tt.want) {
				t.Fatalf("mapRepositoryError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapRepositoryErrorDoesNotExposeStorageDetail(t *testing.T) {
	for _, err := range []error{gorm.ErrRecordNotFound, gorm.ErrDuplicatedKey} {
		got := mapRepositoryError(err)
		if strings.Contains(got.Error(), "record not found") || strings.Contains(got.Error(), "duplicated") {
			t.Fatalf("mapped error leaked storage detail: %v", got)
		}
	}
}

func TestRecordFromModelKeepsHashOutOfTheUserProjection(t *testing.T) {
	const hash = "$2a$10$abcdefghijklmnopqrstuv"
	record := recordFromModel(models.User{
		UserID:       7,
		Username:     "alice",
		Role:         "admin",
		PasswordHash: hash,
		Disable:      true,
	})

	if record.PasswordHash != hash {
		t.Errorf("PasswordHash = %q, want %q", record.PasswordHash, hash)
	}
	if !record.Disabled {
		t.Error("Disabled = false, want true")
	}
	if record.User.UserID != 7 || record.User.Username != "alice" || record.User.Role != "admin" {
		t.Errorf("User = %+v", record.User)
	}

	encoded, err := json.Marshal(record.User)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), hash) || strings.Contains(string(encoded), "password") {
		t.Fatalf("serialized user leaked credential material: %s", encoded)
	}
}
