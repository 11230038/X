package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the storage the reading layer needs.
//
// The shape is the one every domain in this repository uses: one method, one
// statement, and no decisions. Whether a position write is a create or a move,
// whether a mark is allowed to have no text, and whether a stale revision is a
// conflict are the service's to answer; the repository answers what is stored.
//
// Every read here takes a caller and narrows itself to that account's rows.
// Ownership is a filter rather than a check: a material — or a position, a mark
// or a pairing hanging off one — belonging to somebody else is not refused, it
// is absent, which is why a request for it is answered exactly as a request for
// an id that was never issued (AUTH-06).
//
// Only reading_materials carries an owner column (migration 00014). The rows
// that hang off a material — positions, marks and pairings — have none, because
// their lifetime is the material's: a position or a mark is reached per account
// by narrowing on the material in it, and a pairing requires both of its ends to
// be the caller's so that no row here can name a material they cannot read.
type Repository interface {
	// Materials lists the catalogue for one caller. workspaceID may be empty, and
	// then every material comes back with no tab state.
	Materials(ctx context.Context, workspaceID string, caller authz.Caller) ([]Material, error)
	// Material reads one material by its id, ErrNotFound when there is none the
	// caller may see.
	Material(ctx context.Context, materialID string, caller authz.Caller) (Material, error)
	// MaterialOfContent reads the material built from one document, ErrNotFound
	// when that document has none the caller may see.
	MaterialOfContent(ctx context.Context, contentID string, caller authz.Caller) (Material, error)
	// CreateMaterial writes one material row. The owner travels in the record
	// rather than as a caller, the same way it does on a session: there is no
	// row yet to narrow against, so the caller only supplies the owner value.
	CreateMaterial(ctx context.Context, material Material) error
	// DeleteMaterial removes one material, ErrNotFound when there was none the
	// caller may see. The rows that hang off it go with it, because the schema's
	// foreign keys say so rather than because this statement knows about them.
	//
	// It is the inverse of CreateMaterial and it has exactly one caller: a
	// workspace import that did not finish, taking back the rows it wrote
	// (§2.13). Nothing in the product removes a material — the catalogue has no
	// delete — which is why there is no service method around this one.
	DeleteMaterial(ctx context.Context, materialID string, caller authz.Caller) error

	// Position reads where a reader stopped, ErrNotFound when nothing was saved
	// or the material is not the caller's.
	Position(ctx context.Context, materialID, sessionID string, caller authz.Caller) (Position, error)
	// CreatePosition writes the first position for a pair at revision 1.
	// ErrDuplicate means somebody else created it first, and ErrNotFound means
	// the material is not the caller's.
	CreatePosition(ctx context.Context, position Position, caller authz.Caller) error
	// SavePosition moves a position from the revision it was read at to the next
	// one, and reports whether it was still there to move. False is the answer
	// this whole path exists to be able to give.
	SavePosition(ctx context.Context, materialID, sessionID string, expected int64, locator Locator, now time.Time, caller authz.Caller) (bool, error)

	// Marks lists the marks on one material, oldest first.
	Marks(ctx context.Context, materialID string, caller authz.Caller) ([]Mark, error)
	// Bookmarks lists every bookmark the caller has saved, newest first, with the
	// material each was saved in. Marks are otherwise reached one material at a
	// time; a favourites page crosses materials, and this is the read that does.
	Bookmarks(ctx context.Context, caller authz.Caller) ([]Bookmark, error)
	// Mark reads one mark by its id, ErrNotFound when there is none the caller
	// may see.
	Mark(ctx context.Context, markID string, caller authz.Caller) (Mark, error)
	// CreateMark writes one mark, ErrNotFound when its material is not the
	// caller's.
	CreateMark(ctx context.Context, mark Mark, caller authz.Caller) error
	// DeleteMark removes one mark, ErrNotFound when there was none to remove or
	// the material it hangs off is not the caller's.
	DeleteMark(ctx context.Context, markID string, caller authz.Caller) error

	// PairingOf reads the confirmed pairing a material is in, from either end of
	// it, ErrNotFound when it is in none.
	PairingOf(ctx context.Context, materialID string, caller authz.Caller) (Pairing, error)
	// PairingBetween reads the confirmed pairing of one pair of materials,
	// whichever way round it was confirmed.
	PairingBetween(ctx context.Context, first, second string, caller authz.Caller) (Pairing, error)
	// CreatePairing writes one confirmation. ErrDuplicate means it is already
	// there, which is the one collision this row can have; ErrNotFound means one
	// of the two materials is not the caller's.
	CreatePairing(ctx context.Context, pairing Pairing, caller authz.Caller) error
	// DeletePairing removes one confirmation, ErrNotFound when there was none.
	DeletePairing(ctx context.Context, pairingID string, caller authz.Caller) error
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

// ownedMaterials is the ids of the materials one caller may read, as a query.
//
// It is the whole of how a table with no owner column is narrowed: a position,
// a mark or a pairing reaches its account through the material in it, and each of
// those reads says `material_id IN (this)`. Building it here rather than four
// times means the rule "a derived row's owner is its material's" is written once,
// which is the same reason documentScope exists in the knowledge base.
func ownedMaterials(ctx context.Context, db *gorm.DB, caller authz.Caller) *gorm.DB {
	return data.OwnerScope(
		db.WithContext(ctx).Model(&models.ReadingMaterial{}).Select("material_id"),
		caller.UserID, caller.IsAdmin(), "owner_id",
	)
}

// requireMaterial reports a material that is not the caller's, checking by
// reading it rather than by what an insert or an update of a child reports.
//
// It is the scoped, locking read a derived write is made under (rule 5): a stale
// material id from another account must not be a way to attach a position or a
// mark to a row this caller cannot read, and the lock is what keeps the answer
// true while the row it guards about is written. A material another account owns
// is reported as missing rather than as forbidden, so a write aimed at somebody
// else's material says nothing about whether it exists.
func requireMaterial(tx *gorm.DB, materialID string, caller authz.Caller) error {
	var stored models.ReadingMaterial
	query := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&stored, "material_id = ?", materialID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: material %s", ErrNotFound, materialID)
	}
	if err != nil {
		return fmt.Errorf("look for material %s: %w", materialID, err)
	}
	return nil
}

// The three catalogue reads are built with the query builder rather than written
// out as SQL, and the reason is ownership: every one of them is narrowed to the
// caller through data.OwnerScope, and that predicate cannot be spliced into a
// Raw statement — the `?`s in a Raw are values, not the query fragments the
// scope is. The builder is the shape §2.4's convention uses everywhere else, so
// this is also one predicate in one place rather than a fourth hand-written copy.

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
//
// The workspace is a bound parameter rather than a second statement. A read
// outside a workspace passes the empty string, which matches no tab row because
// a workspace id may not be blank (migration 00004's CHECK) — so the catalogue
// and a workspace's tab list are one read that differs only in what comes back
// in the last two columns.
//
// The order is most recently touched first, and NULL update times sort last,
// which is what a material nobody has written to should do.
func (r *GORMRepository) Materials(ctx context.Context, workspaceID string, caller authz.Caller) ([]Material, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []materialRow
	query := r.db.WithContext(ctx).
		Model(&models.ReadingMaterial{}).
		Select("reading_materials.material_id, reading_materials.content_id, "+
			"reading_materials.filename, reading_materials.source_type, "+
			"reading_materials.creat_time, reading_materials.update_time, t.pinned, t.opened").
		Joins("LEFT JOIN reading_workspace_materials t "+
			"ON t.material_id = reading_materials.material_id AND t.workspace_id = ?", workspaceID).
		Order("reading_materials.update_time DESC, reading_materials.material_id ASC")
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "reading_materials.owner_id")
	if err := query.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list the reading materials: %w", err)
	}
	materials := make([]Material, 0, len(rows))
	for _, row := range rows {
		materials = append(materials, row.material())
	}
	return materials, nil
}

// materialQuery is one material's own columns, narrowed to the caller, without
// the tab state: the two tab columns belong to a workspace and are absent here,
// which is what leaves materialRow's flags nil — the same nothing a single row
// says about a workspace it was not read against.
func (r *GORMRepository) materialQuery(ctx context.Context, caller authz.Caller) *gorm.DB {
	query := r.db.WithContext(ctx).
		Model(&models.ReadingMaterial{}).
		Select("material_id, content_id, filename, source_type, creat_time, update_time")
	return data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
}

// Material reads one material.
func (r *GORMRepository) Material(ctx context.Context, materialID string, caller authz.Caller) (Material, error) {
	if err := r.ready(); err != nil {
		return Material{}, err
	}
	var row materialRow
	err := r.materialQuery(ctx, caller).Where("material_id = ?", materialID).Scan(&row).Error
	if err != nil {
		return Material{}, fmt.Errorf("read reading material %s: %w", materialID, err)
	}
	// A scan of no rows leaves the id empty rather than erroring, and an empty id
	// is exactly what "not found" looks like here: the id is the key, so no id
	// means no material — including one whose owner is another account, which the
	// scope removed before this could see it.
	if row.MaterialID == "" {
		return Material{}, fmt.Errorf("%w: material %s", ErrNotFound, materialID)
	}
	return row.material(), nil
}

// MaterialOfContent reads the material built from one document.
//
// The earliest row wins when a document has more than one, and the order is
// spelled out rather than left to the storage engine: which of two materials a
// reader gets should not change between runs.
func (r *GORMRepository) MaterialOfContent(ctx context.Context, contentID string, caller authz.Caller) (Material, error) {
	if err := r.ready(); err != nil {
		return Material{}, err
	}
	if contentID == "" {
		return Material{}, fmt.Errorf("%w: the material names no document", ErrNotFound)
	}
	var row materialRow
	err := r.materialQuery(ctx, caller).
		Where("content_id = ?", contentID).
		Order("creat_time ASC, material_id ASC").
		Limit(1).
		Scan(&row).Error
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
		CreatedAt:  material.CreatedAt,
		// A zero owner becomes SQL NULL and not 0, which is the difference between
		// a material that belongs to nobody and one that belongs to account zero and
		// can never be read back. OwnerPointer owns that decision, so this is the
		// only place the record's owner is set.
		OwnerID: data.OwnerPointer(material.OwnerID),
	}
	// Written rather than left to the database, and it is the column the
	// catalogue is ordered by: a material nobody has written to yet was touched
	// when it was made, and leaving it NULL would file the newest material at the
	// bottom of the list rather than the top.
	if !material.UpdatedAt.IsZero() {
		updated := material.UpdatedAt
		record.UpdatedAt = &updated
	}
	err := r.db.WithContext(ctx).Create(&record).Error
	if err != nil {
		return fmt.Errorf("create reading material %s: %w", material.MaterialID, err)
	}
	return nil
}

// DeleteMaterial removes one material.
//
// One statement is the whole of it: positions, marks, workspace tabs and
// pairings all reference reading_materials with ON DELETE CASCADE (00012), so
// taking the material out takes them with it — and the two rows that reference a
// material loosely are SET NULL (00004), which is what a row that is about
// mastery rather than about a document should do when the document goes.
//
// Rows affected is the not-found test rather than a read first. A read and then
// a delete has a window between them, and the answer it gives — "it was there a
// moment ago" — is not one any caller can act on.
func (r *GORMRepository) DeleteMaterial(ctx context.Context, materialID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	query := data.OwnerScope(r.db.WithContext(ctx).Model(&models.ReadingMaterial{}),
		caller.UserID, caller.IsAdmin(), "owner_id")
	result := query.Where("material_id = ?", materialID).Delete(&models.ReadingMaterial{})
	if result.Error != nil {
		return fmt.Errorf("delete reading material %s: %w", materialID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: material %s", ErrNotFound, materialID)
	}
	return nil
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
