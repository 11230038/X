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

// Repository is the storage the mastery domain needs.
//
// Every method is one statement, and the two that are not — the reads that
// return a tree, the reads that return a path's evidence — are one statement
// each because the whole of what they return lives in one row or in one table.
// The decisions live in the service: what an edit means, what level the evidence
// implies, what happens when a lease has expired. A repository that decided any
// of that would be a repository that could not be replaced by a fake for a test
// about the rules.
//
// The transaction is the service's, which is why the only way in is Within: the
// design's one hard requirement about this domain's storage is that state and
// history land in the same transaction (§2.1), and a repository that could be
// used outside a transaction would be a repository that could write a state with
// no event explaining it.
type Repository interface {
	// Within runs work against one transaction. The repository it hands over is
	// bound to that transaction and is the only one the work may use: a write made
	// through the outer repository while a transaction is open would be committed
	// on its own and would survive the rollback that was supposed to take it back.
	Within(ctx context.Context, work func(Repository) error) error

	// Path reads one path, tree and version together. ErrNotFound when there is
	// no such path.
	Path(ctx context.Context, pathID string) (Path, error)
	// Meta reads a path's topic metadata. ErrNotFound when the path has none,
	// which a path created before this version may not.
	Meta(ctx context.Context, pathID string) (TopicMeta, error)
	// Evidences reads a path's evidence, oldest first. Every one of them: the gate
	// counts runs, and a run at the start of a path's history counts as much as
	// one at the end.
	Evidences(ctx context.Context, pathID string) ([]Evidence, error)
	// Events reads a path's history, oldest first.
	Events(ctx context.Context, pathID string) ([]Event, error)

	// CreatePath inserts the path row at version 1. ErrDuplicate when the id is
	// taken.
	CreatePath(ctx context.Context, path Path) error
	// SaveTree writes a tree over the version the caller read from, and reports
	// the version the write reached. ErrStaleState when the row has moved on.
	SaveTree(ctx context.Context, pathID string, expected int64, tree Tree, now time.Time) (int64, error)
	// SaveMeta writes a path's topic metadata, replacing it if it is there.
	SaveMeta(ctx context.Context, meta TopicMeta, now time.Time) error
	// TouchSession records that a conversation has touched a path, and moves the
	// time it was last seen.
	TouchSession(ctx context.Context, pathID string, actor Actor, now time.Time) error

	// AppendEvidence writes one observation. ErrDuplicate when the id is taken.
	AppendEvidence(ctx context.Context, evidence Evidence) error
	// AppendEvent writes one line of history. ErrDuplicate when the id is taken.
	AppendEvent(ctx context.Context, event Event) error
}

// GORMRepository reads and writes the mastery tables through GORM (seekdb over
// the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// Within runs work in one transaction.
func (r *GORMRepository) Within(ctx context.Context, work func(Repository) error) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return work(&GORMRepository{db: tx})
	})
}

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

// AppendEvidence writes one observation.
func (r *GORMRepository) AppendEvidence(ctx context.Context, evidence Evidence) error {
	if err := r.ready(); err != nil {
		return err
	}
	extra, err := json.Marshal(evidenceExtra{
		KnowledgePointID: evidence.KnowledgePoint,
		Detail:           evidence.Detail,
	})
	if err != nil {
		return fmt.Errorf("encode evidence %s: %w", evidence.ID, err)
	}
	record := models.MasteryLearningEvidence{
		MLEID:          evidence.ID,
		SessionID:      evidence.Actor.SessionID,
		TurnID:         evidence.Actor.TurnID,
		PathID:         evidence.PathID,
		Extra:          datatypes.JSON(extra),
		Result:         nullable(string(evidence.Result)),
		CreatedAt:      evidence.CreatedAt,
		Source:         nullable(evidence.Source),
		Quality:        nullable(evidence.Quality),
		AssessmentType: nullable(evidence.AssessmentType),
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record evidence %s", evidence.ID)
}

// Evidences reads a path's evidence, oldest first.
//
// The order is by creation time and then by id, and the second half is not
// decoration: creat_time is a DATETIME, which holds whole seconds, so evidence
// recorded inside the same second — two knowledge points assessed by one turn,
// which is the ordinary case — would otherwise come back in whatever order the
// engine chose. The gate counts runs, and a run is a sequence.
func (r *GORMRepository) Evidences(ctx context.Context, pathID string) ([]Evidence, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		ID             string    `gorm:"column:mle_id"`
		Extra          []byte    `gorm:"column:extra"`
		Result         *string   `gorm:"column:result"`
		CreatedAt      time.Time `gorm:"column:creat_time"`
		Source         *string   `gorm:"column:source"`
		Quality        *string   `gorm:"column:quality"`
		AssessmentType *string   `gorm:"column:assessment_type"`
	}
	err := r.db.WithContext(ctx).Table("mastery_learning_evidences").
		Select("mle_id, extra, result, creat_time, source, quality, assessment_type").
		Where("path_id = ?", pathID).
		Order("creat_time ASC, mle_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the evidence of path %s: %w", pathID, err)
	}

	evidences := make([]Evidence, 0, len(rows))
	for _, row := range rows {
		var extra evidenceExtra
		if len(row.Extra) > 0 {
			if err := json.Unmarshal(row.Extra, &extra); err != nil {
				return nil, fmt.Errorf("evidence %s: read its extra column: %w", row.ID, err)
			}
		}
		evidences = append(evidences, Evidence{
			ID:             row.ID,
			PathID:         pathID,
			KnowledgePoint: extra.KnowledgePointID,
			Result:         Result(text(row.Result)),
			Source:         text(row.Source),
			AssessmentType: text(row.AssessmentType),
			Quality:        text(row.Quality),
			Detail:         extra.Detail,
			CreatedAt:      row.CreatedAt,
		})
	}
	return evidences, nil
}

// AppendEvent writes one line of a path's history.
func (r *GORMRepository) AppendEvent(ctx context.Context, event Event) error {
	if err := r.ready(); err != nil {
		return err
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	version := int32(event.Version)
	eventType := string(event.Type)
	record := models.MasteryEvent{
		MasteryEventsID: event.ID,
		PathID:          event.PathID,
		TurnID:          event.Actor.TurnID,
		SessionID:       event.Actor.SessionID,
		Version:         &version,
		Payload:         datatypes.JSON(payload),
		EventType:       &eventType,
		CreatedAt:       event.CreatedAt,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record event %s", event.ID)
}

// Events reads a path's history, oldest first, ordered the same way Evidences is
// and for the same reason.
func (r *GORMRepository) Events(ctx context.Context, pathID string) ([]Event, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		ID        string    `gorm:"column:mastery_events_id"`
		Version   *int32    `gorm:"column:version"`
		Payload   []byte    `gorm:"column:payload_json"`
		EventType *string   `gorm:"column:event_type"`
		SessionID string    `gorm:"column:session_id"`
		TurnID    string    `gorm:"column:turn_id"`
		CreatedAt time.Time `gorm:"column:creat_time"`
	}
	err := r.db.WithContext(ctx).Table("mastery_events").
		Select("mastery_events_id, version, payload_json, event_type, session_id, turn_id, creat_time").
		Where("path_id = ?", pathID).
		Order("creat_time ASC, mastery_events_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the history of path %s: %w", pathID, err)
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event := Event{
			ID:        row.ID,
			PathID:    pathID,
			Type:      EventType(text(row.EventType)),
			Payload:   row.Payload,
			Actor:     Actor{SessionID: row.SessionID, TurnID: row.TurnID},
			CreatedAt: row.CreatedAt,
		}
		if row.Version != nil {
			event.Version = int64(*row.Version)
		}
		events = append(events, event)
	}
	return events, nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("mastery repository is not initialized")
	}
	return nil
}

// duplicateOr turns a collision on a primary key into ErrDuplicate, and says
// which statement collided — which matters because the two callers mean
// different things by it.
func duplicateOr(err error, what string, args ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: %s", ErrDuplicate, fmt.Sprintf(what, args...))
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(what, args...), err)
}

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
