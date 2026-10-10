package library

import (
	"context"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// PostgresRepository persists file metadata through GORM.
type PostgresRepository struct {
	db *gorm.DB
}

// NewPostgresRepository creates a metadata adapter over an existing GORM handle.
func NewPostgresRepository(db *gorm.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts one library file record without modifying the schema.
func (r *PostgresRepository) Create(ctx context.Context, record FileRecord) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("library repository is not initialized")
	}
	model := models.LibraryFile{
		ID: record.ID, SHA256: record.SHA256, Filename: record.Filename,
		MIMEType: record.MIMEType, SizeBytes: record.SizeBytes, LibraryPath: record.LibraryPath,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create library file metadata: %w", err)
	}
	return nil
}

var _ MetadataRepository = (*PostgresRepository)(nil)
