package models

import "time"

// Persona is the persistence mapping for a saved prompt (migration 00020).
//
// It belongs to one account and is read by that account's turns. It carries no
// permission: a persona changes what a model is told, never what the caller may
// reach.
type Persona struct {
	PersonaID   string    `gorm:"column:persona_id;primaryKey;autoIncrement:false"`
	OwnerID     int64     `gorm:"column:owner_id;not null"`
	Name        string    `gorm:"column:name;not null"`
	Description string    `gorm:"column:description;not null;default:''"`
	Content     string    `gorm:"column:content;not null"`
	CreatedAt   time.Time `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the personas table name.
func (Persona) TableName() string { return "personas" }
