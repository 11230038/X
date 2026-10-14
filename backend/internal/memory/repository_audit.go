package memory

// The audit trail's storage: an append and a read, and nothing else.
//
// There is no third method over `memory_audit` and there is not going to be one,
// because append-only has to be enforced somewhere and this engine offers nothing
// that would do it. audit.go explains the decision; audit_test.go reads this
// interface and fails if a method that could change a line ever appears.

import (
	"context"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/datatypes"
)

// AppendAudit writes one line of the audit trail.
func (r *GORMRepository) AppendAudit(ctx context.Context, audit Audit) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.MemoryAudit{
		AuditID:    audit.ID,
		OwnerID:    audit.OwnerID,
		Actor:      string(audit.Actor),
		Action:     string(audit.Action),
		TargetKind: string(audit.TargetKind),
		TargetID:   nullable(audit.TargetID),
		Result:     string(audit.Result),
		Detail:     datatypes.JSON(payloadOf(audit.Detail)),
		CreatedAt:  audit.CreatedAt,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record audit %s", audit.ID)
}

// Audits reads one account's audit trail, newest first.
//
// The order is by creation time and then by id, for the reason the trace listing
// gives: `creat_time` holds whole seconds, and a merge that writes one audit row
// per group in a single call writes several in one second. An order that is not
// decided is an order a reader cannot follow.
func (r *GORMRepository) Audits(ctx context.Context, query AuditQuery) ([]Audit, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	statement := r.db.WithContext(ctx).Model(&models.MemoryAudit{}).Where("owner_id = ?", query.OwnerID)
	if query.Action != "" {
		statement = statement.Where("action = ?", string(query.Action))
	}
	if query.Limit > 0 {
		statement = statement.Limit(query.Limit)
	}

	var rows []models.MemoryAudit
	if err := statement.Order("creat_time DESC, audit_id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the audit trail of account %d: %w", query.OwnerID, err)
	}
	audits := make([]Audit, 0, len(rows))
	for _, row := range rows {
		audits = append(audits, auditOf(row))
	}
	return audits, nil
}

// auditOf reads one stored row as the domain's audit line.
func auditOf(row models.MemoryAudit) Audit {
	return Audit{
		ID:         row.AuditID,
		OwnerID:    row.OwnerID,
		Actor:      Actor(row.Actor),
		Action:     Action(row.Action),
		TargetKind: TargetKind(row.TargetKind),
		TargetID:   text(row.TargetID),
		Result:     Result(row.Result),
		Detail:     []byte(row.Detail),
		CreatedAt:  row.CreatedAt,
	}
}
