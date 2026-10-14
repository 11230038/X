-- +goose Up
-- The five tables governance is stored in (V5 §2.1–§2.3, §2.6, §2.13).
--
-- They are one migration because they are one decision seen from five sides: an
-- account may be given things (user_grants), one account may be allowed to read
-- another's (guardian_links), what that reading allows is a closed set
-- (guardian_permissions), what was done about any of it is kept where nothing
-- can rewrite it (audit_events), and a workspace move is a row that outlives the
-- request that started it (workspace_jobs).
--
-- Nothing here has a trigger, and no table here has an UPDATE path written for
-- it: a grant that is revoked is deleted, a permission that is not held is a row
-- that is not there, and an audit row is appended to and never changed. "Is this
-- allowed" is then a question about existence, which is the only shape that can
-- be answered by an index and cannot be answered wrongly by a forgotten flag.

CREATE TABLE user_grants (
    grant_id   BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    item_kind  VARCHAR(32) NOT NULL,
    -- The empty string means "everything of this kind". The unique key below is
    -- what lets a whole-kind decision and a per-item decision sit side by side,
    -- and an empty string is a value rather than NULL: NULL repeats in a unique
    -- index, which would turn "the decision for this kind" into several rows that
    -- all claim to be it.
    item_id    VARCHAR(128) NOT NULL DEFAULT '',
    allowed    BOOLEAN NOT NULL,
    -- Who granted it. Nullable and SET NULL: deleting an account that once
    -- granted something must not take the grants away with it, and RESTRICT
    -- would make such an account undeletable. Who did it is in the audit either
    -- way.
    granted_by BIGINT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT user_grants_user_fk FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
    CONSTRAINT user_grants_granted_by_fk FOREIGN KEY (granted_by) REFERENCES users (user_id) ON DELETE SET NULL,
    CONSTRAINT user_grants_kind_supported CHECK (item_kind IN
        ('model', 'capability', 'surface', 'kb', 'tool', 'exec', 'learning_policy')),
    CONSTRAINT user_grants_unique UNIQUE (user_id, item_kind, item_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE guardian_links (
    link_id     VARCHAR(128) PRIMARY KEY,
    guardian_id BIGINT NOT NULL,
    learner_id  BIGINT NOT NULL,
    created_by  BIGINT NULL,
    creat_time  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT guardian_links_guardian_fk FOREIGN KEY (guardian_id) REFERENCES users (user_id) ON DELETE CASCADE,
    CONSTRAINT guardian_links_learner_fk  FOREIGN KEY (learner_id)  REFERENCES users (user_id) ON DELETE CASCADE,
    CONSTRAINT guardian_links_created_by_fk FOREIGN KEY (created_by) REFERENCES users (user_id) ON DELETE SET NULL,
    -- Two of AUTH-04's three prohibitions. The third — that the learner has to be
    -- a learner and the guardian must not be one — reads users.preset, which a
    -- check constraint cannot reach across tables for; it is enforced in the
    -- service's transaction, in the same place 00007's "exactly one default base"
    -- and V4's "one live interaction per path" are.
    CONSTRAINT guardian_links_no_self CHECK (guardian_id <> learner_id),
    CONSTRAINT guardian_links_pair_unique UNIQUE (guardian_id, learner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- Revoking is a DELETE rather than a flag (§2.3), so there is no revoked column
-- and the history of a revocation is in the audit.

CREATE TABLE guardian_permissions (
    link_id    VARCHAR(128) NOT NULL,
    permission VARCHAR(32) NOT NULL,
    granted_by BIGINT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT guardian_permissions_pk PRIMARY KEY (link_id, permission),
    CONSTRAINT guardian_permissions_link_fk FOREIGN KEY (link_id)
        REFERENCES guardian_links (link_id) ON DELETE CASCADE,
    CONSTRAINT guardian_permissions_granted_by_fk FOREIGN KEY (granted_by)
        REFERENCES users (user_id) ON DELETE SET NULL,
    -- AUTH-05's four and not one more. Adding a fifth is an ALTER, and that cost
    -- is deliberate: what it buys is that "may this guardian do this" is a
    -- question about whether a row exists, which everything downstream can ask
    -- without knowing the vocabulary (§2.3).
    CONSTRAINT guardian_permissions_supported CHECK (permission IN
        ('view_reports', 'assign_materials', 'manage_restrictions', 'reset_credentials'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE audit_events (
    audit_id    BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    -- Deliberately no foreign key (§2.6). CASCADE would delete an account's
    -- audit along with the account, and RESTRICT would make an account that ever
    -- left a trace undeletable. A record of what happened has to outlive the
    -- thing it happened to.
    actor_id    BIGINT NULL,
    -- The name as it was at the time. What an audit answers is who did it then,
    -- not who that account is now: reading back a record from three months ago
    -- after a rename should show the name that was on it.
    actor_name  VARCHAR(64) NOT NULL DEFAULT '',
    actor_role  VARCHAR(64) NOT NULL DEFAULT '',
    action      VARCHAR(64) NOT NULL,
    target_kind VARCHAR(32) NOT NULL,
    -- A pointer, not a copy. No conversation, no material, no prompt is in here,
    -- so "we do not store prompt text" is not a filter anybody has to remember to
    -- apply — there is no column that could hold it (§2.6).
    target_id   VARCHAR(128) NOT NULL DEFAULT '',
    -- The account an entry is about. When a guardian reads a report the actor and
    -- the subject are two different accounts, and this column is the only way to
    -- ask for the audit of one of them.
    subject_id  BIGINT NULL,
    result      VARCHAR(16) NOT NULL,
    -- Ids, counts and reason codes only: no free text, no user input.
    detail_json JSON NULL,
    request_id  VARCHAR(64) NOT NULL DEFAULT '',
    creat_time  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT audit_events_action_not_blank CHECK (TRIM(action) <> ''),
    CONSTRAINT audit_events_result_supported CHECK (result IN ('ok', 'denied', 'failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_audit_events_actor_creat   ON audit_events (actor_id, creat_time DESC);
CREATE INDEX idx_audit_events_subject_creat ON audit_events (subject_id, creat_time DESC);
-- The retention window's index (§0.2 item 10: cleanup is one statement over a
-- window, not a job).
CREATE INDEX idx_audit_events_creat ON audit_events (creat_time);

CREATE TABLE workspace_jobs (
    job_id        VARCHAR(128) PRIMARY KEY,
    kind          VARCHAR(16) NOT NULL,
    state         VARCHAR(16) NOT NULL,
    owner_id      BIGINT NULL,
    archive_path  VARCHAR(512) NOT NULL DEFAULT '',
    -- What is in the archive: written by an export, read by an import.
    manifest_json JSON NULL,
    -- Which ids this job has already written. Recovery is driven by this rather
    -- than by a rollback: a ZIP does not fit in a transaction, and a rollback
    -- would be riding on a transaction that was never committed (§2.13).
    written_json  JSON NULL,
    counts_json   JSON NULL,
    failure_code  VARCHAR(64) NOT NULL DEFAULT '',
    creat_time    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT workspace_jobs_kind_supported CHECK (kind IN ('export', 'import')),
    CONSTRAINT workspace_jobs_state_supported CHECK (state IN
        ('preview', 'running', 'committed', 'failed', 'recovered')),
    CONSTRAINT workspace_jobs_owner_fk FOREIGN KEY (owner_id)
        REFERENCES users (user_id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_workspace_jobs_owner_creat ON workspace_jobs (owner_id, creat_time DESC);

-- +goose Down
DROP TABLE workspace_jobs;
DROP TABLE audit_events;
DROP TABLE guardian_permissions;
DROP TABLE guardian_links;
DROP TABLE user_grants;
