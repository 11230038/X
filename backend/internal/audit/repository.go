package audit

// The store behind the audit: one insert, one select and one delete by window.
//
// What is *not* here is the interesting part. There is no Update and no
// Delete-by-id, and their absence is what "append-only" means in a codebase
// rather than in a comment: an audit entry cannot be edited because there is no
// method that edits one, and the next person who wants to correct a row has to
// add one, which is a visible thing to do in review.
//
// Prune is the one delete, and it is by age alone. §0.2's tenth item makes
// cleanup a statement a caller runs rather than a job the process schedules —
// the same shape as "a lease expiring is a comparison" — and keeping it out of
// the entry API is what stops "delete these two rows" from ever being a thing
// this package can do.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Store is the persistence the audit service needs. It is declared on the
// consumer's side, as every store in this repository is, so that the rules about
// what may be written are testable without a database.
type Store interface {
	// Append writes one entry and returns its id. It never updates: an entry
	// that already exists is not something this store can reach.
	Append(ctx context.Context, event Event) (int64, error)
	// Page reads entries, newest first, narrowed by the filter.
	Page(ctx context.Context, filter Filter) ([]Event, error)
	// Prune removes everything older than a moment and reports how many went.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// GORMStore persists audit_events through GORM.
type GORMStore struct {
	db *gorm.DB
}

// NewGORMStore creates a store over an existing handle.
func NewGORMStore(db *gorm.DB) *GORMStore { return &GORMStore{db: db} }

func (s *GORMStore) ready() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("audit store is not initialized")
	}
	return nil
}

// Append writes one entry.
//
// The event is validated here as well as in the service, and the duplication is
// the point: this is the only door into the table, and a check that lives only
// above the door holds exactly as long as every caller remembers to use it.
func (s *GORMStore) Append(ctx context.Context, event Event) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if err := event.Validate(); err != nil {
		return 0, err
	}
	detail, err := marshalDetail(event.Detail)
	if err != nil {
		return 0, err
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	row := models.AuditEvent{
		// Zero is "no account": actor_id and subject_id are nullable on purpose, so
		// that an entry about a deleted account keeps its name snapshot and loses its
		// id, and so that an action about the actor's own things has no subject at
		// all rather than a subject of zero.
		ActorID:    data.OwnerPointer(event.Actor.ID),
		ActorName:  event.Actor.Name,
		ActorRole:  event.Actor.Role,
		Action:     string(event.Action),
		TargetKind: string(event.Target.Kind),
		TargetID:   event.Target.ID,
		SubjectID:  data.OwnerPointer(event.SubjectID),
		Result:     string(event.Result),
		Detail:     detail,
		RequestID:  event.RequestID,
		CreatedAt:  createdAt,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, fmt.Errorf("append audit entry: %w", err)
	}
	return row.AuditID, nil
}

// Page reads entries, newest first.
//
// The order is (creat_time DESC, audit_id DESC) and not creat_time alone,
// because DATETIME keeps whole seconds: a burst of entries written in the same
// second would otherwise come back in whatever order the storage engine chose,
// and two reads of the same page would disagree. The id breaks the tie in the
// only direction that is stable — the order they were written.
func (s *GORMStore) Page(ctx context.Context, filter Filter) ([]Event, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	query := s.db.WithContext(ctx).Model(&models.AuditEvent{})
	if filter.ActorID != 0 {
		query = query.Where("actor_id = ?", filter.ActorID)
	}
	if filter.SubjectID != 0 {
		query = query.Where("subject_id = ?", filter.SubjectID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", string(filter.Action))
	}
	if filter.TargetKind != "" {
		query = query.Where("target_kind = ?", string(filter.TargetKind))
	}
	if filter.TargetID != "" {
		query = query.Where("target_id = ?", filter.TargetID)
	}
	if !filter.Since.IsZero() {
		query = query.Where("creat_time >= ?", filter.Since)
	}
	if !filter.Until.IsZero() {
		query = query.Where("creat_time < ?", filter.Until)
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	var rows []models.AuditEvent
	if err := query.Order("creat_time DESC, audit_id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read audit entries: %w", err)
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		detail, err := unmarshalDetail(row.Detail)
		if err != nil {
			return nil, err
		}
		event := Event{
			ID:        row.AuditID,
			Actor:     Actor{Name: row.ActorName, Role: row.ActorRole},
			Action:    Action(row.Action),
			Target:    Ref{Kind: TargetKind(row.TargetKind), ID: row.TargetID},
			Result:    Result(row.Result),
			Detail:    detail,
			RequestID: row.RequestID,
			CreatedAt: row.CreatedAt,
		}
		if row.ActorID != nil {
			event.Actor.ID = *row.ActorID
		}
		if row.SubjectID != nil {
			event.SubjectID = *row.SubjectID
		}
		events = append(events, event)
	}
	return events, nil
}

// Prune removes everything written before a moment.
//
// It is bounded by time and by nothing else: there is no way to name the rows it
// removes, which is the property that makes a retention window a window rather
// than an editing tool. A window that removed nothing is not an error — a quiet
// instance is exactly what this is supposed to look like.
func (s *GORMStore) Prune(ctx context.Context, before time.Time) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	result := s.db.WithContext(ctx).
		Where("creat_time < ?", before).
		Delete(&models.AuditEvent{})
	if result.Error != nil {
		return 0, fmt.Errorf("prune audit entries: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// marshalDetail turns the detail into the column, or into NULL when there is
// nothing to say.
//
// NULL and `{}` mean the same thing to a reader and different things to anybody
// counting: an entry with no detail is the ordinary case for a great many
// actions, and storing an empty object for each of them would make "how many
// entries carry a reason" a question about the JSON type rather than about the
// audit.
//
// The encoding is the type's own, tags and all, so that what is stored and what
// is served are the same shape rather than two spellings that have to be kept in
// step.
func marshalDetail(detail Detail) (datatypes.JSON, error) {
	if detail.Empty() {
		return nil, nil
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil, fmt.Errorf("encode the audit detail: %w", err)
	}
	return datatypes.JSON(encoded), nil
}

// unmarshalDetail reads the column back.
//
// It refuses a body it does not understand rather than returning what it could
// parse, because a reader that silently drops a field answers "this entry does
// not say why" for an entry that does.
func unmarshalDetail(raw datatypes.JSON) (Detail, error) {
	if len(raw) == 0 {
		return Detail{}, nil
	}
	var detail Detail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return Detail{}, fmt.Errorf("decode the audit detail: %w", err)
	}
	return detail, nil
}

var _ Store = (*GORMStore)(nil)
