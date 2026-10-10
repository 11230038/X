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
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	seedSessionID          = "seed_session_conversation"
	seedUserMessageID      = "seed_message_user"
	seedAssistantMessageID = "seed_message_assistant"
	seedSummaryID          = "seed_summary_revision_1"
	seedTurnID             = "seed_turn_completed"
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

	if err := resource.GORM().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := seedUserAccounts(tx); err != nil {
			return err
		}
		return seedConversation(tx)
	}); err != nil {
		log.Fatal("seed development data")
	}

	fmt.Println("seeded users: test_admin, test_user")
	fmt.Printf("seeded conversation: %s\n", seedSessionID)
}

func seedUserAccounts(tx *gorm.DB) error {
	for _, seed := range seedUsers {
		var user models.User
		result := tx.Where("username = ?", seed.username).First(&user)
		if result.Error == nil {
			continue
		}
		if result.Error != gorm.ErrRecordNotFound {
			return fmt.Errorf("find seed user %s: %w", seed.username, result.Error)
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(seed.password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash seed password: %w", err)
		}
		user = models.User{
			Username:       seed.username,
			PasswordHash:   string(hash),
			Disable:        false,
			Avatar:         "",
			Role:           seed.role,
			Preset:         seed.preset,
			LearnerProfile: datatypes.JSON(`{}`),
			Extra:          datatypes.JSON(`{}`),
		}
		if err := tx.Create(&user).Error; err != nil {
			return fmt.Errorf("create seed user %s: %w", seed.username, err)
		}
	}
	return nil
}

func seedConversation(tx *gorm.DB) error {
	title := "Seed conversation"
	workspaceMode := "standard"
	capability := "chat"
	userContent := "这是一条开发测试消息。"
	assistantContent := "这是一条开发测试回复。"
	summaryText := "开发测试会话包含一轮完整对话。"
	status := "completed"
	source := "seed"
	stage := "responding"
	retryable := int16(0)
	fencingToken := int64(1)
	now := time.Now().UTC()

	records := []any{
		&models.Session{
			SessionID:     seedSessionID,
			Title:         &title,
			Preferences:   datatypes.JSON(`{"language":"zh-CN"}`),
			WorkspaceMode: &workspaceMode,
		},
		&models.Message{
			MessageID:  seedUserMessageID,
			SessionID:  seedSessionID,
			Content:    &userContent,
			Role:       "user",
			Capability: &capability,
		},
		&models.Message{
			MessageID:  seedAssistantMessageID,
			SessionID:  seedSessionID,
			Content:    &assistantContent,
			Role:       "assistant",
			Capability: &capability,
		},
		&models.Summary{
			SummaryID:             seedSummaryID,
			SessionID:             seedSessionID,
			CompressedSummary:     &summaryText,
			SummaryUpToMessageIDs: datatypes.JSON(`["seed_message_user","seed_message_assistant"]`),
			Revision:              1,
		},
		&models.Turn{
			TurnID:             seedTurnID,
			SessionID:          seedSessionID,
			UpdatedAt:          &now,
			FinishedAt:         &now,
			FencingToken:       &fencingToken,
			StateVersion:       2,
			Status:             status,
			Capability:         &capability,
			Retryable:          &retryable,
			AssistantMessageID: stringPointer(seedAssistantMessageID),
		},
		&models.TurnEvent{
			TurnEventID: "seed_turn_event_content",
			TurnID:      seedTurnID,
			Sequence:    0,
			Timestamp:   now,
			TypeID:      models.TurnEventTypeContent,
			Content:     &assistantContent,
			Source:      &source,
			Stage:       &stage,
		},
		&models.TurnEvent{
			TurnEventID: "seed_turn_event_done",
			TurnID:      seedTurnID,
			Sequence:    1,
			Timestamp:   now,
			TypeID:      models.TurnEventTypeDone,
			Source:      &source,
		},
	}

	for _, record := range records {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error; err != nil {
			return fmt.Errorf("create seed conversation record: %w", err)
		}
	}
	return nil
}

func stringPointer(value string) *string {
	return &value
}
