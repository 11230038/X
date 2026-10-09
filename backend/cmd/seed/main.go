package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/data/migration"
	"backend/internal/data/models"
	"golang.org/x/crypto/bcrypt"
)

var seedUsers = []struct {
	username string
	password string
	role     string
	preset   string
}{
	{username: "test_admin", password: "TestAdmin-2026!", role: "admin", preset: "standard"},
	{username: "test_user", password: "TestUser-2026!", role: "user", preset: "standard"},
}

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatal("load configuration")
	}
	resource, err := data.Open(cfg.Database)
	if err != nil {
		log.Fatal("open database")
	}
	defer resource.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := resource.Ping(ctx); err != nil {
		log.Fatal("database is unavailable")
	}
	if err := migration.CheckLatest(ctx, resource.SQLDB(), cfg.Database.Schema); err != nil {
		log.Fatal("database schema is not current")
	}

	for _, seed := range seedUsers {
		hash, err := bcrypt.GenerateFromPassword([]byte(seed.password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatal("hash seed password")
		}
		user := models.User{
			Username:       seed.username,
			PasswordHash:   string(hash),
			Disable:        false,
			Avatar:         "",
			Role:           seed.role,
			Preset:         seed.preset,
			LearnerProfile: []byte(`{}`),
			Extra:          []byte(`{}`),
		}
		result := resource.GORM().Where("username = ?", seed.username).FirstOrCreate(&user)
		if result.Error != nil {
			log.Fatalf("seed user %s", seed.username)
		}
		fmt.Printf("seeded user: %s (user_id=%d)\n", seed.username, user.UserID)
	}
}
