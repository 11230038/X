-- +goose Up
-- The three-layer memory (§2.6): what was observed, what was admitted, and the
-- evidence chain between them.
--
-- Only two of the three layers are tables. L1 is memory_traces and L2 is
-- memory_facts with memory_fact_sources beside it; L3 — the synthesis a reader
-- is shown — is a pure function over L2 and has no table at all. That is a
-- decision and not an omission: a snapshot table would be a second source of
-- truth that every edit, retraction and merge would have to remember to refresh,
-- and the symptom of forgetting is exactly the failure MEM-02 names — a fact the
-- user took back that is still in the synthesis. With L3 computed, retracting a
-- fact changes the synthesis immediately, because there is nowhere else for the
-- old answer to live. The cost is stated in §2.6: L3 has no history, and no
-- requirement asks for one.
--
-- session_id and turn_id are ordinary columns rather than foreign keys: deleting
-- a conversation must not destroy what was observed in it. This is the same
-- sentence V3 §3 第 3 条 wrote about a mark and its material — a thing cannot be
-- owned by two containers — and the lifetime of "somebody said this once" is
-- longer than the lifetime of the conversation it was said in. It also keeps
-- this migration entirely out of the ERROR 4016 risk: no foreign key here points
-- at a table carrying a vector or full-text index.
--
-- owner_id is this repository's first account column on business data (MEM-04).
-- It *is* a foreign key, and it cascades: deleting an account deletes its memory,
-- which is PRD §11.3's "the user must be able to ... delete Memory" taken to its
-- end. The users table carries neither a full-text nor a vector index, so this
-- foreign key is not in the 4016 risk either.

-- 1) L1: what a surface said, and that it said it.
--
-- Append-only is a rule of the code, not of the engine (this engine has no way to
-- forbid an UPDATE on a table). What the schema does say is that a trace is
-- complete on its own: the statement, the surface it came from, and the
-- fingerprint of what it asserts. Nothing here is a claim about the world — a
-- claim is what memory_facts is for.
CREATE TABLE memory_traces (
    trace_id     VARCHAR(128) PRIMARY KEY,
    owner_id     BIGINT NOT NULL,
    surface      VARCHAR(64) NOT NULL,
    kind         VARCHAR(64) NOT NULL,
    session_id   VARCHAR(128) NULL,
    turn_id      VARCHAR(128) NULL,
    statement    TEXT NOT NULL,
    fingerprint  CHAR(64) NOT NULL,
    payload_json JSON,
    creat_time   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT memory_traces_id_not_blank CHECK (TRIM(trace_id) <> ''),
    CONSTRAINT memory_traces_surface_not_blank CHECK (TRIM(surface) <> ''),
    CONSTRAINT memory_traces_kind_not_blank CHECK (TRIM(kind) <> ''),
    CONSTRAINT memory_traces_statement_not_blank CHECK (TRIM(statement) <> ''),
    CONSTRAINT memory_traces_fingerprint_shaped CHECK (fingerprint REGEXP '^[0-9a-f]{64}$'),
    CONSTRAINT memory_traces_owner_fk FOREIGN KEY (owner_id)
        REFERENCES users (user_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- The read the interface makes is "my observations, newest first"; the second is
-- the gate's question, "which observations are already on record for this
-- assertion"; the third is what a turn asks to find the observations it made
-- itself, which is how two statements in one turn count once.
CREATE INDEX idx_memory_traces_owner_creat
    ON memory_traces (owner_id, creat_time DESC);
CREATE INDEX idx_memory_traces_owner_fingerprint
    ON memory_traces (owner_id, fingerprint);
CREATE INDEX idx_memory_traces_turn
    ON memory_traces (turn_id);

-- 2) L2: what the system admitted, and how sure it is.
--
-- A row here is a claim the system is prepared to act on, which is why there are
-- three statuses rather than two: active is in force, retracted is what the user
-- took back, and superseded is a fact that was merged into another one by
-- dedupe. The third is not a kind of deletion — the row stays, names the fact it
-- was folded into, and its evidence is kept — because a merge that erased the
-- loser would lose the audit trail MEM-02 asks for, and the loser's own wording
-- along with it.
--
-- confidence is the number the *model* reported, kept as reported. It is stored
-- rather than recomputed so that what the gate decided can be explained later;
-- the gate itself treats it as the weakest of its three conditions, because a
-- model's confidence is the model talking (§2.7).
CREATE TABLE memory_facts (
    fact_id      VARCHAR(128) PRIMARY KEY,
    owner_id     BIGINT NOT NULL,
    surface      VARCHAR(64) NOT NULL,
    scope        VARCHAR(128) NULL,
    statement    TEXT NOT NULL,
    fingerprint  CHAR(64) NOT NULL,
    confidence   SMALLINT NOT NULL,
    status       VARCHAR(16) NOT NULL,
    sensitive    BOOLEAN NOT NULL DEFAULT FALSE,
    confirmed_by VARCHAR(16) NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1,
    creat_time   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time  DATETIME,
    retract_time DATETIME,
    CONSTRAINT memory_facts_id_not_blank CHECK (TRIM(fact_id) <> ''),
    CONSTRAINT memory_facts_surface_not_blank CHECK (TRIM(surface) <> ''),
    CONSTRAINT memory_facts_statement_not_blank CHECK (TRIM(statement) <> ''),
    CONSTRAINT memory_facts_fingerprint_shaped CHECK (fingerprint REGEXP '^[0-9a-f]{64}$'),
    CONSTRAINT memory_facts_confidence_percent CHECK (confidence BETWEEN 0 AND 100),
    CONSTRAINT memory_facts_status_closed CHECK (
        status IN ('active', 'retracted', 'superseded')
    ),
    CONSTRAINT memory_facts_confirmer_closed CHECK (
        confirmed_by IN ('gate', 'user')
    ),
    CONSTRAINT memory_facts_version_positive CHECK (version >= 1),
    CONSTRAINT memory_facts_owner_fk FOREIGN KEY (owner_id)
        REFERENCES users (user_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- The listing is "my facts, most recently changed first", and the gate asks by
-- fingerprint whether the assertion is already admitted.
CREATE INDEX idx_memory_facts_owner_status
    ON memory_facts (owner_id, status, update_time DESC);
CREATE INDEX idx_memory_facts_owner_fingerprint
    ON memory_facts (owner_id, fingerprint);

-- 3) The evidence chain (MEM-01's evidence links).
--
-- One row means "this observation is part of why this fact is a fact", and the
-- key is the pair: the same observation can support two facts, and a fact is
-- admitted by several. Both foreign keys cascade, and the two directions mean
-- different things: deleting the fact drops its evidence list (which is now
-- meaningless), while deleting a trace withdraws the support that trace gave —
-- and a fact that loses its last piece of support is what the gate's second
-- condition is about.
CREATE TABLE memory_fact_sources (
    fact_id    VARCHAR(128) NOT NULL,
    trace_id   VARCHAR(128) NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT memory_fact_sources_pk PRIMARY KEY (fact_id, trace_id),
    CONSTRAINT memory_fact_sources_fact_fk FOREIGN KEY (fact_id)
        REFERENCES memory_facts (fact_id) ON DELETE CASCADE,
    CONSTRAINT memory_fact_sources_trace_fk FOREIGN KEY (trace_id)
        REFERENCES memory_traces (trace_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_memory_fact_sources_trace ON memory_fact_sources (trace_id);

-- 4) The audit trail (MEM-02).
--
-- Every action taken on memory writes one row here, including the ones that
-- change nothing — a retraction of a fact that was already retracted is still a
-- thing the user asked for. Append-only is again the code's rule: the repository
-- has no UPDATE and no DELETE path over this table, and a test asserts the shape
-- of that interface rather than trusting the convention.
--
-- target_id is nullable because some actions are about a set rather than a row —
-- a cleanup reports which facts it looked at in detail_json and names no single
-- target.
CREATE TABLE memory_audit (
    audit_id    VARCHAR(128) PRIMARY KEY,
    owner_id    BIGINT NOT NULL,
    actor       VARCHAR(64) NOT NULL,
    action      VARCHAR(64) NOT NULL,
    target_kind VARCHAR(16) NOT NULL,
    target_id   VARCHAR(128) NULL,
    result      VARCHAR(16) NOT NULL,
    detail_json JSON,
    creat_time  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT memory_audit_id_not_blank CHECK (TRIM(audit_id) <> ''),
    CONSTRAINT memory_audit_actor_not_blank CHECK (TRIM(actor) <> ''),
    CONSTRAINT memory_audit_action_not_blank CHECK (TRIM(action) <> ''),
    CONSTRAINT memory_audit_target_kind_not_blank CHECK (TRIM(target_kind) <> ''),
    CONSTRAINT memory_audit_result_not_blank CHECK (TRIM(result) <> ''),
    CONSTRAINT memory_audit_owner_fk FOREIGN KEY (owner_id)
        REFERENCES users (user_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_memory_audit_owner_creat ON memory_audit (owner_id, creat_time DESC);
CREATE INDEX idx_memory_audit_target ON memory_audit (target_kind, target_id);

-- +goose Down
DROP TABLE memory_fact_sources;
DROP TABLE memory_facts;
DROP TABLE memory_traces;
DROP TABLE memory_audit;
