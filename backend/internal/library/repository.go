package library

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// GORMRepository persists file metadata through GORM (seekdb via the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a metadata adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// Create inserts one library file record without modifying the schema.
func (r *GORMRepository) Create(ctx context.Context, record FileRecord) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("library repository is not initialized")
	}
	model := models.LibraryFile{
		ID: record.ID, SHA256: record.SHA256, Filename: record.Filename,
		MIMEType: record.MIMEType, SizeBytes: record.SizeBytes, LibraryPath: record.LibraryPath,
		OwnerID:   data.OwnerPointer(record.OwnerID),
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create library file metadata: %w", err)
	}
	return nil
}

// Find reads one file's metadata back by id.
//
// A missing row is ErrNotFound rather than GORM's own sentinel, so that the
// only thing a caller above this line has to know about storage is that the
// file is not here.
func (r *GORMRepository) Find(ctx context.Context, id string, caller authz.Caller) (FileRecord, error) {
	if r == nil || r.db == nil {
		return FileRecord{}, fmt.Errorf("library repository is not initialized")
	}
	var model models.LibraryFile
	query := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Where("id = ?", id).First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return FileRecord{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return FileRecord{}, fmt.Errorf("read library file metadata: %w", err)
	}
	record := FileRecord{
		ID: model.ID, SHA256: model.SHA256, Filename: model.Filename,
		MIMEType: model.MIMEType, SizeBytes: model.SizeBytes,
		LibraryPath: model.LibraryPath, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
	if model.OwnerID != nil {
		record.OwnerID = *model.OwnerID
	}
	return record, nil
}

var _ MetadataRepository = (*GORMRepository)(nil)
