package mastery

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

// The path row, its topic metadata, and the link between a path and the
// conversations that have touched it. It is the half of this domain's storage
// that is about *what a path is*; evidence.go and repository.go hold the half
// that is about what has been seen and what has changed.

// pathSQL reads a path. The version is coalesced on the way out and written
// through the same expression on the way back in, so a row whose version is NULL
// — one left behind by a deployment that never wrote a tree — reads as version 0
// and can be written by the same statement as any other. Without that, the first
// write to such a row would have to be a special case, and a special case in a
// compare-and-swap is where lost updates come from.
const pathSQL = "SELECT mastery_path_id, owner_session_id, state_json, " +
	"COALESCE(version, 0) AS version, creat_time, update_time " +
	"FROM mastery_paths WHERE mastery_path_id = ?"

// Path reads one path.
func (r *GORMRepository) Path(ctx context.Context, pathID string) (Path, error) {
	if err := r.ready(); err != nil {
		return Path{}, err
	}
	var row struct {
		ID             string     `gorm:"column:mastery_path_id"`
		OwnerSessionID string     `gorm:"column:owner_session_id"`
		State          []byte     `gorm:"column:state_json"`
		Version        int64      `gorm:"column:version"`
		CreatedAt      time.Time  `gorm:"column:creat_time"`
		UpdatedAt      *time.Time `gorm:"column:update_time"`
	}
	err := r.db.WithContext(ctx).Raw(pathSQL, pathID).Scan(&row).Error
	if err != nil {
		return Path{}, fmt.Errorf("read mastery path %s: %w", pathID, err)
	}
	if row.ID == "" {
		return Path{}, fmt.Errorf("%w: path %s", ErrNotFound, pathID)
	}
	tree, err := DecodeTree(row.State)
	if err != nil {
		return Path{}, fmt.Errorf("path %s: %w", pathID, err)
	}
	path := Path{
		ID:             row.ID,
		OwnerSessionID: row.OwnerSessionID,
		Tree:           tree,
		Version:        row.Version,
		CreatedAt:      row.CreatedAt,
	}
	if row.UpdatedAt != nil {
		path.UpdatedAt = *row.UpdatedAt
	}
	return path, nil
}

// pathListSQL reads every path with its topic metadata and the last time a
// conversation touched it.
//
// The metadata is joined in rather than read per row, and the last-seen time is
// a correlated subquery rather than a second pass, because a listing is the one
// read in this domain that is naturally over *all* the rows: a list that made
// two more calls per path would be a list whose cost grew with how much the
// person reading it had studied, which is the opposite of what a listing is for.
//
// The subquery reads the link table on the leading column of its primary key, so
// it is one indexed lookup per path and not a scan.
const pathListSQL = "SELECT p.mastery_path_id, p.owner_session_id, p.state_json, " +
	"COALESCE(p.version, 0) AS version, p.creat_time, p.update_time, " +
	"m.goal, m.description, m.emoji, m.status, " +
	"(SELECT MAX(s.last_seen_time) FROM mastery_path_sessions s " +
	" WHERE s.path_id = p.mastery_path_id) AS last_seen " +
	"FROM mastery_paths p LEFT JOIN mastery_topic_meta m ON m.path_id = p.mastery_path_id " +
	"ORDER BY last_seen DESC, p.mastery_path_id ASC"

// pathListOfSessionSQL is the same read narrowed to the paths one conversation
// has touched.
//
// It is a join and not the same query with a filter bolted on: the question "the
// paths this conversation has worked on" is asked *of the link table*, which is
// where the answer is, and the paths are what it resolves to. Every row of this
// read is linked by construction, so last_seen is a column here and not a
// subquery.
const pathListOfSessionSQL = "SELECT p.mastery_path_id, p.owner_session_id, p.state_json, " +
	"COALESCE(p.version, 0) AS version, p.creat_time, p.update_time, " +
	"m.goal, m.description, m.emoji, m.status, s.last_seen_time AS last_seen " +
	"FROM mastery_path_sessions s JOIN mastery_paths p ON p.mastery_path_id = s.path_id " +
	"LEFT JOIN mastery_topic_meta m ON m.path_id = p.mastery_path_id " +
	"WHERE s.session_id = ? " +
	"ORDER BY s.last_seen_time DESC, p.mastery_path_id ASC"

// pathListRow is what both listing reads return. The metadata columns are
// nullable because a path created before this version has no metadata row, and a
// LEFT JOIN says so the same way the table does: with NULLs.
type pathListRow struct {
	ID             string     `gorm:"column:mastery_path_id"`
	OwnerSessionID string     `gorm:"column:owner_session_id"`
	State          []byte     `gorm:"column:state_json"`
	Version        int64      `gorm:"column:version"`
	CreatedAt      time.Time  `gorm:"column:creat_time"`
	UpdatedAt      *time.Time `gorm:"column:update_time"`
	Goal           *string    `gorm:"column:goal"`
	Description    *string    `gorm:"column:description"`
	Emoji          *string    `gorm:"column:emoji"`
	Status         *string    `gorm:"column:status"`
	LastSeen       *time.Time `gorm:"column:last_seen"`
}

// PathRow is one path as a listing read returns it: the tree with its version,
// the metadata that names it, and when a conversation last worked on it.
//
// The last-seen time is zero for a path no conversation has touched, which is a
// path that exists and has not been worked on rather than a path that is
// missing.
type PathRow struct {
	Path       Path
	Meta       TopicMeta
	LastSeenAt time.Time
}

// Paths reads every path, most recently worked on first, or only the paths one
// conversation has touched when sessionID is set.
func (r *GORMRepository) Paths(ctx context.Context, sessionID string) ([]PathRow, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var (
		rows []pathListRow
		err  error
	)
	if sessionID == "" {
		err = r.db.WithContext(ctx).Raw(pathListSQL).Scan(&rows).Error
	} else {
		err = r.db.WithContext(ctx).Raw(pathListOfSessionSQL, sessionID).Scan(&rows).Error
	}
	if err != nil {
		return nil, fmt.Errorf("read mastery paths: %w", err)
	}

	listed := make([]PathRow, 0, len(rows))
	for _, row := range rows {
		tree, err := DecodeTree(row.State)
		if err != nil {
			return nil, fmt.Errorf("path %s: %w", row.ID, err)
		}
		path := Path{
			ID:             row.ID,
			OwnerSessionID: row.OwnerSessionID,
			Tree:           tree,
			Version:        row.Version,
			CreatedAt:      row.CreatedAt,
		}
		if row.UpdatedAt != nil {
			path.UpdatedAt = *row.UpdatedAt
		}
		listed = append(listed, PathRow{
			Path: path,
			Meta: TopicMeta{
				PathID:      row.ID,
				Goal:        text(row.Goal),
				Description: text(row.Description),
				Emoji:       text(row.Emoji),
				Status:      text(row.Status),
			},
			LastSeenAt: deref(row.LastSeen),
		})
	}
	return listed, nil
}

// CreatePath inserts the path row at version 1.
//
// Version 1 and not 0: the schema's CHECK refuses a version below one, and the
// reference implementation's own first revision is 1 ("revision = 1 → 路径创建").
// A row at 0 is therefore either one this package has not written yet or one
// that was never written at all, and the two mean the same thing — a tree nobody
// has changed.
func (r *GORMRepository) CreatePath(ctx context.Context, path Path) error {
	if err := r.ready(); err != nil {
		return err
	}
	state, err := EncodeTree(path.Tree)
	if err != nil {
		return err
	}
	version := int32(path.Version)
	record := models.MasteryPath{
		MasteryPathID:  path.ID,
		OwnerSessionID: path.OwnerSessionID,
		State:          datatypes.JSON(state),
		Version:        &version,
		CreatedAt:      path.CreatedAt,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "create mastery path %s", path.ID)
}

// SaveTree writes a tree over the version the caller read, and reports the
// version the write reached.
//
// `version = COALESCE(version, 0) + 1` and not a value the caller computed: the
// version that lands is the one the row had plus one, which is what makes the
// guard a comparison rather than a write, and which stays correct when two
// writers reach the row in the same instant and only the first may count. The
// second writer's WHERE matches nothing, and zero rows affected is the answer
// this whole path exists to be able to give — it is the difference between
// "somebody changed the tree since you read it" and "your write failed".
func (r *GORMRepository) SaveTree(ctx context.Context, pathID string, expected int64, tree Tree, now time.Time) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	state, err := EncodeTree(tree)
	if err != nil {
		return 0, err
	}
	result := r.db.WithContext(ctx).Table("mastery_paths").
		Where("mastery_path_id = ? AND COALESCE(version, 0) = ?", pathID, expected).
		Updates(map[string]any{
			"state_json":  state,
			"version":     gorm.Expr("COALESCE(version, 0) + 1"),
			"update_time": now,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("write the state of mastery path %s: %w", pathID, result.Error)
	}
	if result.RowsAffected == 0 {
		return 0, fmt.Errorf("%w: path %s was at %d", ErrStaleState, pathID, expected)
	}
	return expected + 1, nil
}

// Meta reads a path's topic metadata.
func (r *GORMRepository) Meta(ctx context.Context, pathID string) (TopicMeta, error) {
	if err := r.ready(); err != nil {
		return TopicMeta{}, err
	}
	var row struct {
		PathID      string  `gorm:"column:path_id"`
		Goal        *string `gorm:"column:goal"`
		Description *string `gorm:"column:description"`
		Emoji       *string `gorm:"column:emoji"`
		Status      *string `gorm:"column:status"`
	}
	err := r.db.WithContext(ctx).Table("mastery_topic_meta").
		Select("path_id, goal, description, emoji, status").
		Where("path_id = ?", pathID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TopicMeta{}, fmt.Errorf("%w: the topic metadata of path %s", ErrNotFound, pathID)
	}
	if err != nil {
		return TopicMeta{}, fmt.Errorf("read the topic metadata of path %s: %w", pathID, err)
	}
	return TopicMeta{
		PathID:      row.PathID,
		Goal:        text(row.Goal),
		Description: text(row.Description),
		Emoji:       text(row.Emoji),
		Status:      text(row.Status),
	}, nil
}

// SaveMeta writes a path's topic metadata, replacing it if it is there.
//
// The columns are named one by one rather than derived from the model, and that
// is the point: this statement must not touch path_id and must not write a
// created time, because both belong to the row that is already there.
func (r *GORMRepository) SaveMeta(ctx context.Context, meta TopicMeta, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	columns := map[string]any{
		"goal":        meta.Goal,
		"description": meta.Description,
		"emoji":       meta.Emoji,
		"status":      meta.Status,
		"update_time": now,
	}
	result := r.db.WithContext(ctx).Table("mastery_topic_meta").
		Where("path_id = ?", meta.PathID).Updates(columns)
	if result.Error != nil {
		return fmt.Errorf("write the topic metadata of path %s: %w", meta.PathID, result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	// Nothing was updated, so this call is the one that puts the row there —
	// unless somebody else did between the two statements, which the primary key
	// on path_id reports as a duplicate rather than as two rows. Both of those
	// mean the row is there with this metadata, because the racing writer wrote
	// the same thing.
	insert := map[string]any{
		"path_id":     meta.PathID,
		"goal":        meta.Goal,
		"description": meta.Description,
		"emoji":       meta.Emoji,
		"status":      meta.Status,
		"creat_time":  now,
	}
	err := r.db.WithContext(ctx).Table("mastery_topic_meta").Create(insert).Error
	if err == nil || errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil
	}
	return fmt.Errorf("start the topic metadata of path %s: %w", meta.PathID, err)
}

// TouchSession records that a conversation has touched a path.
//
// It is an upsert on the composite key and it moves last_seen_time, which is the
// only column that changes after the first write: "which conversations have
// touched this path" and "when was the last time" are one row's two questions,
// and the first one never stops being true.
func (r *GORMRepository) TouchSession(ctx context.Context, pathID string, actor Actor, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Table("mastery_path_sessions").
		Where("path_id = ? AND session_id = ?", pathID, actor.SessionID).
		Updates(map[string]any{"last_seen_time": now})
	if result.Error != nil {
		return fmt.Errorf("touch path %s from session %s: %w", pathID, actor.SessionID, result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	insert := map[string]any{
		"path_id":        pathID,
		"session_id":     actor.SessionID,
		"creat_time":     now,
		"last_seen_time": now,
	}
	err := r.db.WithContext(ctx).Table("mastery_path_sessions").Create(insert).Error
	if err == nil || errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil
	}
	return fmt.Errorf("link path %s to session %s: %w", pathID, actor.SessionID, err)
}

// evidenceExtra is the shape of mastery_learning_evidences.extra.
//
// The knowledge point's coordinate lives here because migration 00004 gave this
// table no column for it (see Evidence). Putting it in a struct with the
// evidence's own detail rather than beside it in a second field keeps the column
// to one JSON document, so a reader decodes one thing and not two.
type evidenceExtra struct {
	KnowledgePointID string          `json:"knowledge_point_id"`
	Detail           json.RawMessage `json:"detail,omitempty"`
}
