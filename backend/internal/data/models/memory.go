package models

// The persistence mapping for the memory tables (migration 00013).
//
// Four tables, three layers (§2.6): a trace is what a surface said (L1), a fact
// is what the system admitted (L2) with its evidence beside it, and the
// synthesis a reader is shown is not here at all because it is computed. The
// audit table is the record of what was done to the other two.

import (
	"time"

	"gorm.io/datatypes"
)

// MemoryTrace is one observation: something a surface said, once (L1).
//
// SessionID and TurnID are plain columns rather than associations, because the
// conversation is only where the observation was made — deleting the
// conversation must not delete what was observed in it. TurnID is what makes two
// statements in one turn count once: two traces in one turn are one piece of
// evidence, because a model repeating itself is not a second source.
type MemoryTrace struct {
	TraceID     string         `gorm:"column:trace_id;primaryKey;autoIncrement:false"`
	OwnerID     int64          `gorm:"column:owner_id;not null"`
	Surface     string         `gorm:"column:surface;not null"`
	Kind        string         `gorm:"column:kind;not null"`
	SessionID   *string        `gorm:"column:session_id"`
	TurnID      *string        `gorm:"column:turn_id"`
	Statement   string         `gorm:"column:statement;not null"`
	Fingerprint string         `gorm:"column:fingerprint;not null"`
	Payload     datatypes.JSON `gorm:"column:payload_json;type:json"`
	CreatedAt   time.Time      `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the memory trace table name.
func (MemoryTrace) TableName() string {
	return "memory_traces"
}

// MemoryFact is one thing the system is prepared to act on (L2).
//
// Status has three values and not two, and the difference between the last two
// is what happened to it: Retracted is what the user took back, Superseded is a
// fact dedupe merged into another one. Neither is a deletion — the row stays
// with its evidence, which is what MEM-02 asks for: an action with a record,
// never a disappearing one.
//
// Confidence is the number the model reported, kept as it was reported so that
// what the gate decided can still be explained. ConfirmedBy says which of the
// two ways in this fact came through: the gate, or the user writing it down
// themselves. RetractTime is set only while the fact is out of force, and it is
// what a restore clears.
type MemoryFact struct {
	FactID      string     `gorm:"column:fact_id;primaryKey;autoIncrement:false"`
	OwnerID     int64      `gorm:"column:owner_id;not null"`
	Surface     string     `gorm:"column:surface;not null"`
	Scope       *string    `gorm:"column:scope"`
	Statement   string     `gorm:"column:statement;not null"`
	Fingerprint string     `gorm:"column:fingerprint;not null"`
	Confidence  int16      `gorm:"column:confidence;not null"`
	Status      string     `gorm:"column:status;not null"`
	Sensitive   bool       `gorm:"column:sensitive;not null"`
	ConfirmedBy string     `gorm:"column:confirmed_by;not null"`
	Version     int32      `gorm:"column:version;not null"`
	CreatedAt   time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt   *time.Time `gorm:"column:update_time;autoUpdateTime:false"`
	RetractTime *time.Time `gorm:"column:retract_time"`
}

// TableName fixes the memory fact table name.
func (MemoryFact) TableName() string {
	return "memory_facts"
}

// MemoryFactSource is one piece of evidence for one fact (MEM-01).
//
// The key is the pair, because both halves repeat: one observation can support
// two facts and one fact is admitted by several observations. There is no
// identifier of its own for that reason — the pair is the identity.
type MemoryFactSource struct {
	FactID    string    `gorm:"column:fact_id;primaryKey;autoIncrement:false"`
	TraceID   string    `gorm:"column:trace_id;primaryKey;autoIncrement:false"`
	CreatedAt time.Time `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the memory fact source table name.
func (MemoryFactSource) TableName() string {
	return "memory_fact_sources"
}

// MemoryAudit is one action taken on memory (MEM-02).
//
// The repository has no UPDATE and no DELETE path over this table, and that is
// the whole of how append-only is enforced: this engine has no table property
// that forbids a change, so the guarantee lives in the interface a caller is
// given. TargetID is nullable because some actions are about a batch — a cleanup
// names what it looked at in Detail and names no single row.
type MemoryAudit struct {
	AuditID    string         `gorm:"column:audit_id;primaryKey;autoIncrement:false"`
	OwnerID    int64          `gorm:"column:owner_id;not null"`
	Actor      string         `gorm:"column:actor;not null"`
	Action     string         `gorm:"column:action;not null"`
	TargetKind string         `gorm:"column:target_kind;not null"`
	TargetID   *string        `gorm:"column:target_id"`
	Result     string         `gorm:"column:result;not null"`
	Detail     datatypes.JSON `gorm:"column:detail_json;type:json"`
	CreatedAt  time.Time      `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the memory audit table name.
func (MemoryAudit) TableName() string {
	return "memory_audit"
}
