package memory

// L1's storage: what a surface said, as rows.

import (
	"context"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/datatypes"
)

// AppendTrace writes one observation.
func (r *GORMRepository) AppendTrace(ctx context.Context, trace Trace) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.MemoryTrace{
		TraceID:     trace.ID,
		OwnerID:     trace.OwnerID,
		Surface:     string(trace.Surface),
		Kind:        string(trace.Kind),
		SessionID:   nullable(trace.SessionID),
		TurnID:      nullable(trace.TurnID),
		Statement:   trace.Statement,
		Fingerprint: trace.Fingerprint,
		Payload:     datatypes.JSON(payloadOf(trace.Payload)),
		CreatedAt:   trace.CreatedAt,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record trace %s", trace.ID)
}

// Traces reads L1, newest first.
//
// The order is by creation time and then by id, and the second half is not
// decoration: creat_time is a DATETIME, which holds whole seconds, so two
// observations made in the same second — a surface reporting two things at once
// is the ordinary case — would otherwise come back in whatever order the engine
// chose. A listing whose order is not decided is a listing a reader cannot page.
func (r *GORMRepository) Traces(ctx context.Context, query TraceQuery) ([]Trace, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	statement := r.db.WithContext(ctx).Model(&models.MemoryTrace{}).Where("owner_id = ?", query.OwnerID)
	if query.Fingerprint != "" {
		statement = statement.Where("fingerprint = ?", query.Fingerprint)
	}
	if query.Surface != "" {
		statement = statement.Where("surface = ?", string(query.Surface))
	}
	if query.Kind != "" {
		statement = statement.Where("kind = ?", string(query.Kind))
	}
	if !query.Since.IsZero() {
		statement = statement.Where("creat_time >= ?", query.Since)
	}
	if !query.Until.IsZero() {
		statement = statement.Where("creat_time <= ?", query.Until)
	}
	if query.Limit > 0 {
		statement = statement.Limit(query.Limit)
	}

	var rows []models.MemoryTrace
	if err := statement.Order("creat_time DESC, trace_id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the memory traces of account %d: %w", query.OwnerID, err)
	}
	traces := make([]Trace, 0, len(rows))
	for _, row := range rows {
		traces = append(traces, traceOf(row))
	}
	return traces, nil
}

// TracesOf reads one fact's evidence, oldest first.
//
// The join is the evidence chain (MEM-01) rather than a column on the fact: one
// observation can support two facts and one fact rests on several, so the pairing
// is the only place the relation can live. Oldest first is the order the chain is
// read in — "why does the system think this" is answered by the order it came to
// think it, and the newest line of a chain read backwards reads as a pile.
func (r *GORMRepository) TracesOf(ctx context.Context, factID string) ([]Trace, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []models.MemoryTrace
	err := r.db.WithContext(ctx).Model(&models.MemoryTrace{}).
		Joins("JOIN memory_fact_sources ON memory_fact_sources.trace_id = memory_traces.trace_id").
		Where("memory_fact_sources.fact_id = ?", factID).
		Order("memory_traces.creat_time ASC, memory_traces.trace_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the evidence of fact %s: %w", factID, err)
	}
	traces := make([]Trace, 0, len(rows))
	for _, row := range rows {
		traces = append(traces, traceOf(row))
	}
	return traces, nil
}

// traceOf reads one stored row as the domain's trace.
func traceOf(row models.MemoryTrace) Trace {
	return Trace{
		ID:          row.TraceID,
		OwnerID:     row.OwnerID,
		Surface:     Surface(row.Surface),
		Kind:        Kind(row.Kind),
		SessionID:   text(row.SessionID),
		TurnID:      text(row.TurnID),
		Statement:   row.Statement,
		Fingerprint: row.Fingerprint,
		Payload:     []byte(row.Payload),
		CreatedAt:   row.CreatedAt,
	}
}
