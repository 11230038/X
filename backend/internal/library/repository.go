package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// Search defaults and cap. They are the numbers a listing is drawn under, and
// they are here rather than in configuration because neither is a deployment's
// decision: a page has to have a size, and a client asking for a bigger one is
// asking for more than the screen can draw.
const (
	defaultSearchLimit = 50
	maxSearchLimit     = 200
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
// A missing row is ErrNotFound rather than GORM's own sentinel, so that the only
// thing a caller above this line has to know about storage is that the file is
// not here.
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
	return toFileRecord(model), nil
}

// FindByDigest reads the row holding this content (§2.14).
//
// The ordering is the whole of the dedup policy: a live row comes before a
// binned one, and among equals the oldest comes first. So an account that
// uploads the same bytes twice is answered with the file it already has, and one
// that uploads bytes it had thrown away is answered with the row that is still
// holding the path — which is what keeps `library_files_path_unique` from being
// violated by the restore (§2.14).
func (r *GORMRepository) FindByDigest(ctx context.Context, sha256 string, caller authz.Caller) (FileRecord, error) {
	if r == nil || r.db == nil {
		return FileRecord{}, fmt.Errorf("library repository is not initialized")
	}
	var model models.LibraryFile
	query := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.
		Where("sha256 = ?", sha256).
		Order("is_deleted ASC, created_at ASC, id ASC").
		First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return FileRecord{}, fmt.Errorf("%w: no file of yours holds that content", ErrNotFound)
	}
	if err != nil {
		return FileRecord{}, fmt.Errorf("read library file metadata by digest: %w", err)
	}
	return toFileRecord(model), nil
}

// Search lists one account's files, live or binned.
//
// The filename filter is INSTR rather than LIKE, and the reason is the column's
// collation: library_files is utf8mb4_bin, so a LIKE here is a byte comparison —
// "report" would not find "Report.pdf" — and its `%` and `_` are wildcards that
// a user typing a filename would have to escape. INSTR is a literal substring
// search with neither problem, and LOWER on both sides answers the case. What it
// costs is the index, which was never going to serve this: the rows are already
// narrowed to one account, and a name-contains search over one account's library
// is a scan of it either way.
func (r *GORMRepository) Search(ctx context.Context, query SearchQuery) ([]FileRecord, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("library repository is not initialized")
	}
	statement := r.db.WithContext(ctx).
		Where("is_deleted = ?", query.Deleted).
		Scopes(func(db *gorm.DB) *gorm.DB {
			return data.OwnerScope(db, query.Caller.UserID, query.Caller.IsAdmin(), "owner_id")
		})
	if name := normalizedFilename(query.Filename); name != "" {
		// The newline is stripped because INSTR would otherwise match it: the
		// term comes from a query string and a filename never holds one, so a
		// search for "a\nb" is a search nobody typed.
		statement = statement.Where("INSTR(LOWER(filename), LOWER(?)) > 0", name)
	}
	if query.Category != "" {
		// The category is not a column: it is the directory the bytes are in, so
		// it is matched against the path's first segment. Asking for one the
		// library does not write is not an error — it is a listing with nothing
		// in it, which is what a user who typed a filter should get.
		statement = statement.Where("SUBSTRING_INDEX(library_path, '/', 1) = ?", query.Category)
	}
	if !query.Since.IsZero() {
		statement = statement.Where("created_at >= ?", query.Since.UTC())
	}
	if !query.Until.IsZero() {
		// Until is exclusive, which is what makes two adjacent ranges meet
		// exactly: a file created at midnight belongs to the day that starts
		// there, not to both.
		statement = statement.Where("created_at < ?", query.Until.UTC())
	}

	var records []models.LibraryFile
	err := statement.
		Order("created_at DESC, id ASC").
		Limit(searchLimit(query.Limit)).
		Offset(max(query.Offset, 0)).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("search library files: %w", err)
	}
	files := make([]FileRecord, 0, len(records))
	for _, record := range records {
		files = append(files, toFileRecord(record))
	}
	return files, nil
}

// SetDeleted moves a file into or out of the recycle bin.
//
// Both columns are written together because the table's check constraint
// requires them to agree, so a state where one was updated and the other was not
// is rejected by the database rather than stored. That constraint is the whole
// reason this is one call and not two.
func (r *GORMRepository) SetDeleted(ctx context.Context, id string, deleted bool, now time.Time, caller authz.Caller) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("library repository is not initialized")
	}
	updates := map[string]any{"is_deleted": deleted, "updated_at": now}
	if deleted {
		updates["deleted_at"] = now
	} else {
		updates["deleted_at"] = nil
	}
	scoped := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	result := scoped.Model(&models.LibraryFile{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("move library file into or out of the recycle bin: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// Zero rows is not "the row is not there", and the difference matters:
		// an update that matched a row and changed nothing reports zero too. The
		// service has already read the row and decided the state, so a read here
		// is only to tell the two apart.
		return r.confirmAbsent(ctx, id, caller)
	}
	return nil
}

// Delete removes one file's metadata for good.
//
// The bytes are not this method's business: a row is a fact about a file, and
// the file is on disk. The service removes the bytes first and the row second,
// and it is the only place that has both — which is why this method is a delete
// and not a "purge".
func (r *GORMRepository) Delete(ctx context.Context, id string, caller authz.Caller) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("library repository is not initialized")
	}
	scoped := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	result := scoped.Delete(&models.LibraryFile{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete library file metadata: %w", result.Error)
	}
	// A delete reports the rows it removed, which is the question being asked:
	// there is no "the row was there but the value did not change" case to be
	// confused by, as there is on the update above.
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

// References reports everything that still points at a file.
//
// Two questions, because a file is used in two ways and only the first one names
// it. A document carries the file id directly; a reading material points at a
// document and never at a file, so a material's claim on a file is invisible to
// anybody who only looks at the file's own column. The second query is the one
// that makes AC-15 true: deleting the document would leave a material whose
// content_id names a row that is gone, and a reader opening it would be shown a
// book that no longer exists.
//
// Neither table has a delete flag of its own, so every row counts. That is not an
// omission: a document is removed by deleting it, and a material by deleting it,
// and a row that still exists is a row that still points at this file.
func (r *GORMRepository) References(ctx context.Context, id string) ([]Reference, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("library repository is not initialized")
	}
	var documents []models.Document
	err := r.db.WithContext(ctx).
		Select("document_id", "kb_id", "filename").
		Where("file_id = ?", id).
		Order("document_id ASC").
		Find(&documents).Error
	if err != nil {
		return nil, fmt.Errorf("read the documents parsed from a file: %w", err)
	}
	references := make([]Reference, 0, len(documents))
	documentIDs := make([]string, 0, len(documents))
	for _, document := range documents {
		references = append(references, Reference{
			Kind: ReferenceDocument, ID: document.DocumentID,
			Name: documentName(document.KbID),
		})
		documentIDs = append(documentIDs, document.DocumentID)
	}
	if len(documentIDs) == 0 {
		return references, nil
	}

	var materials []models.ReadingMaterial
	err = r.db.WithContext(ctx).
		Select("material_id", "filename", "content_id").
		Where("content_id IN ?", documentIDs).
		Order("material_id ASC").
		Find(&materials).Error
	if err != nil {
		return nil, fmt.Errorf("read the reading materials built on a file: %w", err)
	}
	for _, material := range materials {
		references = append(references, Reference{
			Kind: ReferenceReadingMaterial, ID: material.MaterialID,
			Name: materialName(material.Filename),
		})
	}
	return references, nil
}

// confirmAbsent tells "no such row" from "the row did not need changing".
//
// It is a read after a write that reported nothing, which is the only shape a
// store that reports row counts offers: an update matching an unchanged row and
// an update matching no row are the same answer.
func (r *GORMRepository) confirmAbsent(ctx context.Context, id string, caller authz.Caller) error {
	if _, err := r.Find(ctx, id, caller); err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

func toFileRecord(model models.LibraryFile) FileRecord {
	record := FileRecord{
		ID: model.ID, SHA256: model.SHA256, Filename: model.Filename,
		MIMEType: model.MIMEType, SizeBytes: model.SizeBytes,
		LibraryPath: model.LibraryPath, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
		Deleted: model.IsDeleted, DeletedAt: model.DeletedAt,
	}
	if model.OwnerID != nil {
		record.OwnerID = *model.OwnerID
	}
	return record
}

// documentName is what a document is called where a person would see it: the
// knowledge base it is in, or the fact that it belongs to none.
//
// A document with a NULL kb_id is an attachment — it exists for the turns that
// carried it — and "attachment" is the word the rest of this repository uses for
// that. Naming it anything else would make a refusal point at a thing the user
// cannot find in their own library.
func documentName(kbID *string) string {
	if kbID == nil || *kbID == "" {
		return "attachment"
	}
	return *kbID
}

func materialName(filename *string) string {
	if filename == nil || *filename == "" {
		return ""
	}
	return *filename
}

// normalizedFilename cleans a search term. A term that is only whitespace is no
// filter at all, so it becomes the empty string rather than a term that matches
// nothing.
func normalizedFilename(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\n", ""))
}

func searchLimit(value int) int {
	switch {
	case value <= 0:
		return defaultSearchLimit
	case value > maxSearchLimit:
		return maxSearchLimit
	default:
		return value
	}
}

var _ MetadataRepository = (*GORMRepository)(nil)
