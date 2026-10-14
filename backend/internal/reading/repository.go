package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Repository is the storage the reading layer needs.
//
// The shape is the one every domain in this repository uses: one method, one
// statement, and no decisions. Whether a position write is a create or a move,
// whether a mark is allowed to have no text, and whether a stale revision is a
// conflict are the service's to answer; the repository answers what is stored.
type Repository interface {
	// Materials lists the catalogue. workspaceID may be empty, and then every
	// material comes back with no tab state.
	Materials(ctx context.Context, workspaceID string) ([]Material, error)
	// Material reads one material by its id, ErrNotFound when there is none.
	Material(ctx context.Context, materialID string) (Material, error)
	// MaterialOfContent reads the material built from one document, ErrNotFound
	// when that document has none.
	MaterialOfContent(ctx context.Context, contentID string) (Material, error)
	// CreateMaterial writes one material row.
	CreateMaterial(ctx context.Context, material Material) error

	// Position reads where a reader stopped, ErrNotFound when nothing was saved.
	Position(ctx context.Context, materialID, sessionID string) (Position, error)
	// CreatePosition writes the first position for a pair at revision 1.
	// ErrDuplicate means somebody else created it first.
	CreatePosition(ctx context.Context, position Position) error
	// SavePosition moves a position from the revision it was read at to the next
	// one, and reports whether it was still there to move. False is the answer
	// this whole path exists to be able to give.
	SavePosition(ctx context.Context, materialID, sessionID string, expected int64, locator Locator, now time.Time) (bool, error)

	// Marks lists the marks on one material, oldest first.
	Marks(ctx context.Context, materialID string) ([]Mark, error)
	// Mark reads one mark by its id, ErrNotFound when there is none.
	Mark(ctx context.Context, markID string) (Mark, error)
	// CreateMark writes one mark.
	CreateMark(ctx context.Context, mark Mark) error
	// DeleteMark removes one mark, ErrNotFound when there was none to remove.
	DeleteMark(ctx context.Context, markID string) error

	// PairingOf reads the confirmed pairing a material is in, from either end of
	// it, ErrNotFound when it is in none.
	PairingOf(ctx context.Context, materialID string) (Pairing, error)
	// PairingBetween reads the confirmed pairing of one pair of materials,
	// whichever way round it was confirmed.
	PairingBetween(ctx context.Context, first, second string) (Pairing, error)
	// CreatePairing writes one confirmation. ErrDuplicate means it is already
	// there, which is the one collision this row can have.
	CreatePairing(ctx context.Context, pairing Pairing) error
	// DeletePairing removes one confirmation, ErrNotFound when there was none.
	DeletePairing(ctx context.Context, pairingID string) error
}

// GORMRepository reads and writes the reading tables through GORM (seekdb over
// the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// catalogueSQL reads the materials with the tab state of one workspace.
//
// The workspace is a bound parameter rather than a second statement. A read
// outside a workspace passes the empty string, which matches no tab row because
// a workspace id may not be blank (migration 00004's CHECK) — so the catalogue
// and a workspace's tab list are one read that differs only in what comes back
// in the last two columns.
//
// The order is most recently touched first, and NULL update times sort last,
// which is what a material nobody has written to should do.
const catalogueSQL = "SELECT m.material_id, m.content_id, m.filename, m.source_type, " +
	"m.creat_time, m.update_time, t.pinned, t.opened " +
	"FROM reading_materials m " +
	"LEFT JOIN reading_workspace_materials t " +
	"ON t.material_id = m.material_id AND t.workspace_id = ? " +
	"ORDER BY m.update_time DESC, m.material_id ASC"

// materialSQL is one material, without the tab state: the columns a single row
// has are the material's own.
const materialSQL = "SELECT material_id, content_id, filename, source_type, " +
	"creat_time, update_time, NULL AS pinned, NULL AS opened " +
	"FROM reading_materials WHERE material_id = ?"

// materialOfContentSQL reads the material built from one document.
//
// The earliest row wins when a document has more than one, and the order is
// spelled out rather than left to the storage engine: which of two materials a
// reader gets should not change between runs.
const materialOfContentSQL = "SELECT material_id, content_id, filename, source_type, " +
	"creat_time, update_time, NULL AS pinned, NULL AS opened " +
	"FROM reading_materials WHERE content_id = ? " +
	"ORDER BY creat_time ASC, material_id ASC LIMIT 1"

// materialRow is one catalogue row as it is scanned, with every nullable column
// in its nullable form.
type materialRow struct {
	MaterialID string     `gorm:"column:material_id"`
	ContentID  *string    `gorm:"column:content_id"`
	Filename   *string    `gorm:"column:filename"`
	SourceType *string    `gorm:"column:source_type"`
	CreatedAt  time.Time  `gorm:"column:creat_time"`
	UpdatedAt  *time.Time `gorm:"column:update_time"`
	Pinned     *bool      `gorm:"column:pinned"`
	Opened     *bool      `gorm:"column:opened"`
}

// material reads a row as the domain's material. An absent value is the empty
// string or the zero time, because that is what "nothing was recorded" means to
// everything downstream — the two nullable flags are the exception, and they
// keep their nil, because there false and unknown are different answers.
func (row materialRow) material() Material {
	return Material{
		MaterialID: row.MaterialID,
		ContentID:  text(row.ContentID),
		Filename:   text(row.Filename),
		SourceType: text(row.SourceType),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  deref(row.UpdatedAt),
		Pinned:     row.Pinned,
		Opened:     row.Opened,
	}
}

// Materials lists the catalogue.
func (r *GORMRepository) Materials(ctx context.Context, workspaceID string) ([]Material, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []materialRow
	err := r.db.WithContext(ctx).Raw(catalogueSQL, workspaceID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list the reading materials: %w", err)
	}
	materials := make([]Material, 0, len(rows))
	for _, row := range rows {
		materials = append(materials, row.material())
	}
	return materials, nil
}

// Material reads one material.
func (r *GORMRepository) Material(ctx context.Context, materialID string) (Material, error) {
	if err := r.ready(); err != nil {
		return Material{}, err
	}
	var row materialRow
	err := r.db.WithContext(ctx).Raw(materialSQL, materialID).Scan(&row).Error
	if err != nil {
		return Material{}, fmt.Errorf("read reading material %s: %w", materialID, err)
	}
	// A raw scan of no rows is an empty row rather than an error, and an empty row
	// is exactly what "not found" looks like here: the id is the key, so no id
	// means no material.
	if row.MaterialID == "" {
		return Material{}, fmt.Errorf("%w: material %s", ErrNotFound, materialID)
	}
	return row.material(), nil
}

// MaterialOfContent reads the material built from one document.
func (r *GORMRepository) MaterialOfContent(ctx context.Context, contentID string) (Material, error) {
	if err := r.ready(); err != nil {
		return Material{}, err
	}
	if contentID == "" {
		return Material{}, fmt.Errorf("%w: the material names no document", ErrNotFound)
	}
	var row materialRow
	err := r.db.WithContext(ctx).Raw(materialOfContentSQL, contentID).Scan(&row).Error
	if err != nil {
		return Material{}, fmt.Errorf("read the material of document %s: %w", contentID, err)
	}
	if row.MaterialID == "" {
		return Material{}, fmt.Errorf("%w: no material reads document %s", ErrNotFound, contentID)
	}
	return row.material(), nil
}

// CreateMaterial writes one material row.
//
// The columns are built as a map so that an empty content id or filename becomes
// NULL rather than an empty string: a material with no document is a material
// with no document, and the two would otherwise be indistinguishable in a column
// that allows both.
func (r *GORMRepository) CreateMaterial(ctx context.Context, material Material) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.ReadingMaterial{
		MaterialID: material.MaterialID,
		ContentID:  nullable(material.ContentID),
		Filename:   nullable(material.Filename),
		SourceType: nullable(material.SourceType),
	}
	err := r.db.WithContext(ctx).Create(&record).Error
	if err != nil {
		return fmt.Errorf("create reading material %s: %w", material.MaterialID, err)
	}
	return nil
}

// Position reads where a reader stopped.
func (r *GORMRepository) Position(ctx context.Context, materialID, sessionID string) (Position, error) {
	if err := r.ready(); err != nil {
		return Position{}, err
	}
	var record models.ReadingPosition
	err := r.db.WithContext(ctx).First(&record, "material_id = ? AND session_id = ?", materialID, sessionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Position{}, fmt.Errorf("%w: no position for material %s in conversation %s", ErrNotFound, materialID, sessionID)
	}
	if err != nil {
		return Position{}, fmt.Errorf("read the position of material %s: %w", materialID, err)
	}
	return positionOf(record)
}

// CreatePosition writes the first position for a material and a conversation.
func (r *GORMRepository) CreatePosition(ctx context.Context, position Position) error {
	if err := r.ready(); err != nil {
		return err
	}
	payload, err := encodeLocator(position.Locator)
	if err != nil {
		return err
	}
	record := models.ReadingPosition{
		MaterialID:  position.MaterialID,
		SessionID:   position.SessionID,
		LocatorJSON: payload,
		Revision:    position.Revision,
	}
	if !position.UpdatedAt.IsZero() {
		updated := position.UpdatedAt
		record.UpdatedAt = &updated
	}
	err = r.db.WithContext(ctx).Create(&record).Error
	return duplicateOr(err, "create the position of material %s in conversation %s", position.MaterialID, position.SessionID)
}

// SavePosition moves a position from the revision it was read at to the next one.
//
// `revision = revision + 1` and not a value, for the same reason the review state
// is advanced that way: the revision that lands is the one the row had plus one,
// which is what makes the guard a comparison rather than a write, and which stays
// correct when two writers reach the row in the same instant and only the first
// may count.
func (r *GORMRepository) SavePosition(
	ctx context.Context,
	materialID, sessionID string,
	expected int64,
	locator Locator,
	now time.Time,
) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	payload, err := encodeLocator(locator)
	if err != nil {
		return false, err
	}
	result := r.db.WithContext(ctx).Table(models.ReadingPosition{}.TableName()).
		Where("material_id = ? AND session_id = ? AND revision = ?", materialID, sessionID, expected).
		Updates(map[string]any{
			"locator_json": payload,
			"revision":     gorm.Expr("revision + 1"),
			"update_time":  now,
		})
	if result.Error != nil {
		return false, fmt.Errorf("save the position of material %s: %w", materialID, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// Marks lists the marks on one material.
//
// The order is the order they were written, and it is the only order available:
// a mark's place in the document lives in its locator, which is JSON, and a
// database cannot sort by a coordinate inside a document. A client that wants
// them in reading order sorts them itself, where the text it is displaying lives.
func (r *GORMRepository) Marks(ctx context.Context, materialID string) ([]Mark, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.ReadingMark
	err := r.db.WithContext(ctx).
		Where("material_id = ?", materialID).
		Order("creat_time ASC, mark_id ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list the marks of material %s: %w", materialID, err)
	}
	marks := make([]Mark, 0, len(records))
	for _, record := range records {
		mark, err := markOf(record)
		if err != nil {
			return nil, err
		}
		marks = append(marks, mark)
	}
	return marks, nil
}

// Mark reads one mark by its id.
func (r *GORMRepository) Mark(ctx context.Context, markID string) (Mark, error) {
	if err := r.ready(); err != nil {
		return Mark{}, err
	}
	var record models.ReadingMark
	err := r.db.WithContext(ctx).First(&record, "mark_id = ?", markID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Mark{}, fmt.Errorf("%w: mark %s", ErrNotFound, markID)
	}
	if err != nil {
		return Mark{}, fmt.Errorf("read mark %s: %w", markID, err)
	}
	return markOf(record)
}

// CreateMark writes one mark.
func (r *GORMRepository) CreateMark(ctx context.Context, mark Mark) error {
	if err := r.ready(); err != nil {
		return err
	}
	payload, err := encodeLocator(mark.Locator)
	if err != nil {
		return err
	}
	record := models.ReadingMark{
		MarkID:      mark.MarkID,
		MaterialID:  mark.MaterialID,
		SessionID:   nullable(mark.SessionID),
		Kind:        string(mark.Kind),
		LocatorJSON: payload,
		Body:        nullable(mark.Body),
		Revision:    mark.Revision,
	}
	err = r.db.WithContext(ctx).Create(&record).Error
	return duplicateOr(err, "create mark %s", mark.MarkID)
}

// DeleteMark removes one mark.
func (r *GORMRepository) DeleteMark(ctx context.Context, markID string) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).
		Where("mark_id = ?", markID).
		Delete(&models.ReadingMark{})
	if result.Error != nil {
		return fmt.Errorf("delete mark %s: %w", markID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: mark %s", ErrNotFound, markID)
	}
	return nil
}

// positionOf reads a stored row as a position, decoding its coordinate.
func positionOf(record models.ReadingPosition) (Position, error) {
	locator, err := decodeLocator(record.LocatorJSON,
		fmt.Sprintf("the position of material %s in conversation %s", record.MaterialID, record.SessionID))
	if err != nil {
		return Position{}, err
	}
	return Position{
		MaterialID: record.MaterialID,
		SessionID:  record.SessionID,
		Locator:    locator,
		Revision:   record.Revision,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  deref(record.UpdatedAt),
	}, nil
}

// markOf reads a stored row as a mark, decoding its coordinate.
func markOf(record models.ReadingMark) (Mark, error) {
	locator, err := decodeLocator(record.LocatorJSON, fmt.Sprintf("mark %s", record.MarkID))
	if err != nil {
		return Mark{}, err
	}
	return Mark{
		MarkID:     record.MarkID,
		MaterialID: record.MaterialID,
		SessionID:  text(record.SessionID),
		Kind:       MarkKind(record.Kind),
		Locator:    locator,
		Body:       text(record.Body),
		Revision:   record.Revision,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  deref(record.UpdatedAt),
	}, nil
}

// encodeLocator writes a coordinate for the JSON column.
func encodeLocator(locator Locator) (datatypes.JSON, error) {
	payload, err := json.Marshal(locator)
	if err != nil {
		return nil, fmt.Errorf("encode the locator of material %s: %w", locator.MaterialID, err)
	}
	return datatypes.JSON(payload), nil
}

// decodeLocator reads a coordinate out of the JSON column.
//
// A column that will not decode is an error and not an empty locator. The stored
// form is written by this package and read by this package, so a value that does
// not parse is corruption, and reporting it as "no position" would turn damage
// into silence.
func decodeLocator(payload datatypes.JSON, what string) (Locator, error) {
	if len(payload) == 0 {
		return Locator{}, fmt.Errorf("reading: %s has no locator", what)
	}
	var locator Locator
	if err := json.Unmarshal(payload, &locator); err != nil {
		return Locator{}, fmt.Errorf("reading: %s has an unreadable locator: %w", what, err)
	}
	return locator, nil
}

// duplicateOr turns a collision on a primary key into ErrDuplicate.
func duplicateOr(err error, what string, args ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: %s", ErrDuplicate, fmt.Sprintf(what, args...))
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(what, args...), err)
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("reading repository is not initialized")
	}
	return nil
}

// nullable stores the empty string as NULL, because the columns that take one
// mean "nothing was recorded" by it and the two are different answers.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func deref(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
