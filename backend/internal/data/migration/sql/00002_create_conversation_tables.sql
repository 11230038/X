-- +goose Up
-- seekdb/MySQL: TEXT is neither indexable nor defaultable, so keyed columns are
-- VARCHAR(n). utf8mb4_bin preserves the byte-exact comparison the previous
-- PostgreSQL schema had by default.
CREATE TABLE turn_event_types (
    type_id SMALLINT PRIMARY KEY,
    name VARCHAR(64) NOT NULL,
    description VARCHAR(255) NOT NULL,
    CONSTRAINT turn_event_types_name_unique UNIQUE (name),
    CONSTRAINT turn_event_types_name_not_blank CHECK (TRIM(name) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

INSERT INTO turn_event_types (type_id, name, description) VALUES
    (1, 'stage_start', '阶段开始'),
    (2, 'stage_end', '阶段结束'),
    (3, 'thinking', '思考过程或推理状态'),
    (4, 'observation', '观察到的信息或中间结果'),
    (5, 'content', '助手输出的文本内容'),
    (6, 'tool_call', '发起工具调用'),
    (7, 'tool_result', '工具返回结果'),
    (8, 'progress', '进度更新'),
    (9, 'sources', '来源、引用或检索结果'),
    (10, 'result', '某个阶段或能力的结果'),
    (11, 'error', '错误事件'),
    (12, 'session', '会话相关事件'),
    (13, 'session_meta', '会话元数据更新'),
    (14, 'done', '整个 turn 完成'),
    (15, 'wait_for_input', '等待用户输入');

CREATE TABLE sessions (
    session_id VARCHAR(128) PRIMARY KEY,
    title TEXT,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    preferences_json JSON,
    workspace_mode VARCHAR(64),
    CONSTRAINT sessions_id_not_blank CHECK (TRIM(session_id) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_sessions_update_time ON sessions (update_time DESC);

CREATE TABLE messages (
    message_id VARCHAR(128) PRIMARY KEY,
    session_id VARCHAR(128) NOT NULL,
    content TEXT,
    role VARCHAR(64) NOT NULL,
    extra JSON,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata_json JSON,
    attachments_json JSON,
    event_json JSON,
    capability VARCHAR(64),
    CONSTRAINT messages_id_not_blank CHECK (TRIM(message_id) <> ''),
    CONSTRAINT messages_role_not_blank CHECK (TRIM(role) <> ''),
    CONSTRAINT messages_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_messages_session_creat
    ON messages (session_id, creat_time, message_id);

CREATE TABLE summaries (
    summary_id VARCHAR(128) PRIMARY KEY,
    session_id VARCHAR(128) NOT NULL,
    compressed_summary TEXT,
    summary_up_to_msg_id JSON,
    revision BIGINT NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT summaries_id_not_blank CHECK (TRIM(summary_id) <> ''),
    CONSTRAINT summaries_revision_positive CHECK (revision >= 1),
    -- MySQL spells the type test as a function and returns the name in upper case.
    CONSTRAINT summaries_message_ids_array CHECK (
        summary_up_to_msg_id IS NULL
        OR JSON_TYPE(summary_up_to_msg_id) = 'ARRAY'
    ),
    CONSTRAINT summaries_session_revision_unique UNIQUE (session_id, revision),
    CONSTRAINT summaries_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_summaries_session_revision
    ON summaries (session_id, revision DESC);

CREATE TABLE turns (
    turn_id VARCHAR(128) PRIMARY KEY,
    session_id VARCHAR(128) NOT NULL,
    extra JSON,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME,
    finish_time DATETIME,
    own_id VARCHAR(128),
    fencing_token BIGINT,
    state_version BIGINT NOT NULL DEFAULT 1,
    status VARCHAR(64) NOT NULL,
    capability VARCHAR(64),
    error TEXT,
    retryable SMALLINT,
    failure_code VARCHAR(128),
    assistant_message_id VARCHAR(128),
    CONSTRAINT turns_id_not_blank CHECK (TRIM(turn_id) <> ''),
    CONSTRAINT turns_status_not_blank CHECK (TRIM(status) <> ''),
    CONSTRAINT turns_fencing_token_nonnegative CHECK (
        fencing_token IS NULL OR fencing_token >= 0
    ),
    CONSTRAINT turns_state_version_positive CHECK (state_version >= 1),
    CONSTRAINT turns_retryable_boolean CHECK (
        retryable IS NULL OR retryable IN (0, 1)
    ),
    CONSTRAINT turns_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE,
    CONSTRAINT turns_assistant_message_fk FOREIGN KEY (assistant_message_id)
        REFERENCES messages (message_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_turns_session_update
    ON turns (session_id, update_time DESC);
CREATE INDEX idx_turns_session_status
    ON turns (session_id, status, update_time DESC);
-- seekdb has no partial indexes. MySQL treats NULLs as distinct in a unique
-- index, so dropping the WHERE keeps the same "many NULLs, one value" rule.
CREATE UNIQUE INDEX idx_turns_assistant_message
    ON turns (assistant_message_id);

CREATE TABLE turn_events (
    turn_events_id VARCHAR(128) PRIMARY KEY,
    turn_id VARCHAR(128) NOT NULL,
    seq BIGINT NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `timestamp` DATETIME NOT NULL,
    type SMALLINT NOT NULL,
    extra JSON,
    metadata_json JSON,
    content TEXT,
    source VARCHAR(64),
    stage VARCHAR(64),
    CONSTRAINT turn_events_id_not_blank CHECK (TRIM(turn_events_id) <> ''),
    CONSTRAINT turn_events_seq_nonnegative CHECK (seq >= 0),
    CONSTRAINT turn_events_turn_seq_unique UNIQUE (turn_id, seq),
    CONSTRAINT turn_events_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE,
    CONSTRAINT turn_events_type_fk FOREIGN KEY (type)
        REFERENCES turn_event_types (type_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- +goose Down
DROP TABLE turn_events;
DROP TABLE turns;
DROP TABLE summaries;
DROP TABLE messages;
DROP TABLE sessions;
DROP TABLE turn_event_types;
