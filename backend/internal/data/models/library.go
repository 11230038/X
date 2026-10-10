package models

import "time"

// LibraryFile stores metadata for one file whose content is kept on local disk.
type LibraryFile struct {
	ID          string     `gorm:"column:id;primaryKey;autoIncrement:false"`
	SHA256      string     `gorm:"column:sha256;not null"`
	Filename    string     `gorm:"column:filename;not null"`
	MIMEType    string     `gorm:"column:mime_type;not null"`
	SizeBytes   int64      `gorm:"column:size_bytes;not null"`
	LibraryPath string     `gorm:"column:library_path;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;autoUpdateTime"`
	IsDeleted   bool       `gorm:"column:is_deleted;not null"`
	DeletedAt   *time.Time `gorm:"column:deleted_at"`
}

func (LibraryFile) TableName() string { return "library_files" }
