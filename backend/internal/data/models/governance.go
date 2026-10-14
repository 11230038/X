package models

// The persistence mapping for the five governance tables (migration 00016).
//
// Nothing here is a user's own material, which is why none of it is reached by
// walking a foreign key from a session: a grant is about an account, a guardian
// link is between two of them, an audit row is about what one did, and a job
// belongs to whoever started it. All five carry the account they are about,
// because there is no chain to derive it from.
//
// The one thing shared with the rest of the schema is the naming: columns are
// creat_time / update_time, and the words a column may hold are closed by a
// check constraint rather than by a constant in Go that the database never sees.

import (
	"time"

	"gorm.io/datatypes"
)

// UserGrant is one thing an account has been allowed or denied (AUTH-03).
//
// Kind and ItemID together are the subject: an empty ItemID means the decision
// is for the whole kind, which is why it is an empty string rather than NULL —
// NULL repeats in a unique index, so several rows could each claim to be "the
// decision about models" and only one of them would be found.
//
// GrantedBy is nullable on purpose and is not the record of who did it; the
// audit is. It is here so that an operator can answer "where did this come from"
// without a join across the audit table.
type UserGrant struct {
	GrantID   int64     `gorm:"column:grant_id;primaryKey;autoIncrement"`
	UserID    int64     `gorm:"column:user_id;not null"`
	ItemKind  string    `gorm:"column:item_kind;not null"`
	ItemID    string    `gorm:"column:item_id;not null;default:''"`
	Allowed   bool      `gorm:"column:allowed;not null"`
	GrantedBy *int64    `gorm:"column:granted_by"`
	CreatedAt time.Time `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:update_time;autoUpdateTime"`
}

// TableName fixes the grants table name.
func (UserGrant) TableName() string { return "user_grants" }

// GuardianLink is one account being allowed to read another's material (AUTH-04).
//
// A revoked link is a deleted row rather than a flag: what is left of it is in
// the audit, and leaving a flag would mean every read had two conditions to get
// right instead of one.
type GuardianLink struct {
	LinkID     string    `gorm:"column:link_id;primaryKey;autoIncrement:false"`
	GuardianID int64     `gorm:"column:guardian_id;not null"`
	LearnerID  int64     `gorm:"column:learner_id;not null"`
	CreatedBy  *int64    `gorm:"column:created_by"`
	CreatedAt  time.Time `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the guardian link table name.
func (GuardianLink) TableName() string { return "guardian_links" }

// GuardianPermission is one thing a link allows (AUTH-05).
//
// The row's existence is the permission. There is no boolean to be read the
// wrong way round, and adding a fifth permission is an ALTER of the check
// constraint rather than a constant somebody could forget to consult.
type GuardianPermission struct {
	LinkID     string    `gorm:"column:link_id;primaryKey;autoIncrement:false"`
	Permission string    `gorm:"column:permission;primaryKey;autoIncrement:false"`
	GrantedBy  *int64    `gorm:"column:granted_by"`
	CreatedAt  time.Time `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the guardian permission table name.
func (GuardianPermission) TableName() string { return "guardian_permissions" }

// AuditEvent is one thing that was done, kept where nothing rewrites it.
//
// There is no foreign key to users and no column that could hold a prompt, a
// conversation or a document: what an audit carries is a pointer, and the
// absence of a column for the content is what makes "we do not store it" a
// property of the schema rather than a promise about the code (§2.6).
type AuditEvent struct {
	AuditID    int64          `gorm:"column:audit_id;primaryKey;autoIncrement"`
	ActorID    *int64         `gorm:"column:actor_id"`
	ActorName  string         `gorm:"column:actor_name;not null;default:''"`
	ActorRole  string         `gorm:"column:actor_role;not null;default:''"`
	Action     string         `gorm:"column:action;not null"`
	TargetKind string         `gorm:"column:target_kind;not null"`
	TargetID   string         `gorm:"column:target_id;not null;default:''"`
	SubjectID  *int64         `gorm:"column:subject_id"`
	Result     string         `gorm:"column:result;not null"`
	Detail     datatypes.JSON `gorm:"column:detail_json;type:json"`
	RequestID  string         `gorm:"column:request_id;not null;default:''"`
	CreatedAt  time.Time      `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the audit table name.
func (AuditEvent) TableName() string { return "audit_events" }

// WorkspaceJob is one move of a workspace in or out (§2.13).
//
// Written is what makes a failed import resumable: the ids this job has already
// committed, so a second run skips them instead of writing them twice. Counts
// and Manifest are what the preview showed, and FailureCode is a code rather
// than a message because a message is free text and a code is a decision.
type WorkspaceJob struct {
	JobID       string         `gorm:"column:job_id;primaryKey;autoIncrement:false"`
	Kind        string         `gorm:"column:kind;not null"`
	State       string         `gorm:"column:state;not null"`
	OwnerID     *int64         `gorm:"column:owner_id"`
	ArchivePath string         `gorm:"column:archive_path;not null;default:''"`
	Manifest    datatypes.JSON `gorm:"column:manifest_json;type:json"`
	Written     datatypes.JSON `gorm:"column:written_json;type:json"`
	Counts      datatypes.JSON `gorm:"column:counts_json;type:json"`
	FailureCode string         `gorm:"column:failure_code;not null;default:''"`
	CreatedAt   time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt   time.Time      `gorm:"column:update_time;autoUpdateTime"`
}

// TableName fixes the workspace job table name.
func (WorkspaceJob) TableName() string { return "workspace_jobs" }
