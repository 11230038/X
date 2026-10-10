package main

import (
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	seedMasteryPathID      = "seed_mastery_path"
	seedMasteryEvidenceID  = "seed_mastery_evidence"
	seedMasteryInteraction = "seed_mastery_interaction"
	seedMasteryEventID     = "seed_mastery_event"
	seedReadingMaterialID  = "seed_reading_material"
	seedReadingWorkspaceID = "seed_reading_workspace"
)

func seedMasteryReading(tx *gorm.DB) error {
	if err := seedMastery(tx); err != nil {
		return err
	}
	return seedReading(tx)
}

func seedMastery(tx *gorm.DB) error {
	result := "correct"
	source := "seed"
	quality := "verified"
	assessmentType := "practice"
	userAnswer := "UNIQUE"
	eventType := "assessment.recorded"
	goal := "掌握 PostgreSQL 基础约束"
	description := "用于验证 Mastery Path 持久化结构。"
	emoji := "🧭"
	status := "active"
	version := int32(1)
	now := time.Now().UTC()

	records := []struct {
		record       any
		label        string
		primaryKey   string
		primaryValue any
	}{
		{&models.MasteryPath{
			MasteryPathID:  seedMasteryPathID,
			OwnerSessionID: seedSessionID,
			State:          datatypes.JSON(`{"topic":"postgresql constraints","progress":1}`),
			Version:        &version,
			UpdatedAt:      &now,
		}, "mastery path", "mastery_path_id", seedMasteryPathID},
		{&models.MasteryLearningEvidence{
			MLEID:          seedMasteryEvidenceID,
			SessionID:      seedSessionID,
			TurnID:         seedTurnID,
			PathID:         seedMasteryPathID,
			Extra:          datatypes.JSON(`{"source":"seed"}`),
			Result:         &result,
			Source:         &source,
			Quality:        &quality,
			AssessmentType: &assessmentType,
		}, "mastery evidence", "mle_id", seedMasteryEvidenceID},
		{&models.MasteryPathLease{
			PathID: seedMasteryPathID, SessionID: seedSessionID, TurnID: seedTurnID,
		}, "mastery lease", "path_id", seedMasteryPathID},
		{&models.MasteryInteraction{
			InteractionID: seedMasteryInteraction,
			PathID:        seedMasteryPathID,
			TurnID:        seedTurnID,
			SessionID:     seedSessionID,
			UserAnswer:    &userAnswer,
			Result:        datatypes.JSON(`{"is_correct":true}`),
			Question:      datatypes.JSON(`{"question":"Which constraint enforces uniqueness?"}`),
			UpdatedAt:     &now,
		}, "mastery interaction", "interaction_id", seedMasteryInteraction},
		{&models.MasteryEvent{
			MasteryEventsID: seedMasteryEventID,
			PathID:          seedMasteryPathID,
			TurnID:          seedTurnID,
			SessionID:       seedSessionID,
			Version:         &version,
			Payload:         datatypes.JSON(`{"result":"correct"}`),
			EventType:       &eventType,
		}, "mastery event", "mastery_events_id", seedMasteryEventID},
		{&models.MasteryPathSession{
			PathID: seedMasteryPathID, SessionID: seedSessionID, LastSeenTime: &now,
		}, "mastery path session", "path_id = ? AND session_id = ?", []any{seedMasteryPathID, seedSessionID}},
		{&models.MasteryTopicMeta{
			PathID: seedMasteryPathID, Goal: &goal, Description: &description,
			Emoji: &emoji, Status: &status, UpdatedAt: &now,
		}, "mastery topic metadata", "path_id", seedMasteryPathID},
	}

	for _, item := range records {
		if err := ensureSeedRecord(tx, item.record, item.label, item.primaryKey, item.primaryValue); err != nil {
			return err
		}
	}
	return nil
}

func seedReading(tx *gorm.DB) error {
	contentID := "seed_reading_content"
	filename := "seed-reading.md"
	sourceType := "seed"
	title := "Seed reading workspace"
	description := "用于验证 Reading Workspace 持久化结构。"
	tabOrder := int32(0)
	now := time.Now().UTC()

	records := []struct {
		record       any
		label        string
		primaryKey   string
		primaryValue any
	}{
		{&models.ReadingMaterial{
			MaterialID: seedReadingMaterialID, ContentID: &contentID, Filename: &filename,
			SourceType: &sourceType, UpdatedAt: &now,
		}, "reading material", "material_id", seedReadingMaterialID},
		{&models.ReadingWorkspace{
			WorkspaceID: seedReadingWorkspaceID, ActiveMaterialID: stringPointer(seedReadingMaterialID),
			Title: &title, Description: &description, UpdatedAt: &now,
		}, "reading workspace", "workspace_id", seedReadingWorkspaceID},
		{&models.ReadingWorkspaceMaterial{
			WorkspaceID: seedReadingWorkspaceID, MaterialID: seedReadingMaterialID, TabOrder: &tabOrder,
		}, "reading workspace material", "workspace_id = ? AND material_id = ?", []any{seedReadingWorkspaceID, seedReadingMaterialID}},
		{&models.ReadingWorkspaceSession{
			WorkspaceID: seedReadingWorkspaceID, SessionID: seedSessionID,
			ActiveMaterialID: stringPointer(seedReadingMaterialID), UpdatedAt: &now,
		}, "reading workspace session", "workspace_id = ? AND session_id = ?", []any{seedReadingWorkspaceID, seedSessionID}},
	}

	for _, item := range records {
		if err := ensureSeedRecord(tx, item.record, item.label, item.primaryKey, item.primaryValue); err != nil {
			return fmt.Errorf("seed reading data: %w", err)
		}
	}
	return nil
}
