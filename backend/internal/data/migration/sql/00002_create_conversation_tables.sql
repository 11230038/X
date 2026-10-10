-- +goose Up
CREATE TABLE turn_event_types (
    type_id SMALLINT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    CONSTRAINT turn_event_types_name_unique UNIQUE (name),
    CONSTRAINT turn_event_types_name_not_blank CHECK (btrim(name) <> '')
);

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
    session_id TEXT PRIMARY KEY,
    title TEXT,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    preferences_json JSON,
    workspace_mode TEXT,
    CONSTRAINT sessions_id_not_blank CHECK (btrim(session_id) <> '')
);

CREATE INDEX idx_sessions_update_time ON sessions (update_time DESC);

CREATE TABLE messages (
    message_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    content TEXT,
    role TEXT NOT NULL,
    extra JSON,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata_json JSON,
    attachments_json JSON,
    event_json JSON,
    capability TEXT,
    CONSTRAINT messages_id_not_blank CHECK (btrim(message_id) <> ''),
    CONSTRAINT messages_role_not_blank CHECK (btrim(role) <> ''),
    CONSTRAINT messages_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
);

CREATE INDEX idx_messages_session_creat
    ON messages (session_id, creat_time, message_id);

CREATE TABLE summaries (
    summary_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    compressed_summary TEXT,
    summary_up_to_msg_id JSON,
    revision BIGINT NOT NULL,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT summaries_id_not_blank CHECK (btrim(summary_id) <> ''),
    CONSTRAINT summaries_revision_positive CHECK (revision >= 1),
    CONSTRAINT summaries_message_ids_array CHECK (
        summary_up_to_msg_id IS NULL
        OR json_typeof(summary_up_to_msg_id) = 'array'
    ),
    CONSTRAINT summaries_session_revision_unique UNIQUE (session_id, revision),
    CONSTRAINT summaries_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
);

CREATE INDEX idx_summaries_session_revision
    ON summaries (session_id, revision DESC);

CREATE TABLE turns (
    turn_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    extra JSON,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    finish_time TIMESTAMPTZ,
    own_id TEXT,
    fencing_token BIGINT,
    state_version BIGINT NOT NULL DEFAULT 1,
    status TEXT NOT NULL,
    capability TEXT,
    error TEXT,
    retryable SMALLINT,
    failure_code TEXT,
    assistant_message_id TEXT,
    CONSTRAINT turns_id_not_blank CHECK (btrim(turn_id) <> ''),
    CONSTRAINT turns_status_not_blank CHECK (btrim(status) <> ''),
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
);

CREATE INDEX idx_turns_session_update
    ON turns (session_id, update_time DESC);
CREATE INDEX idx_turns_session_status
    ON turns (session_id, status, update_time DESC);
CREATE UNIQUE INDEX idx_turns_assistant_message
    ON turns (assistant_message_id)
    WHERE assistant_message_id IS NOT NULL;

CREATE TABLE turn_events (
    turn_events_id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL,
    seq BIGINT NOT NULL,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    timestamp TIMESTAMPTZ NOT NULL,
    type SMALLINT NOT NULL,
    extra JSON,
    metadata_json JSON,
    content TEXT,
    source TEXT,
    stage TEXT,
    CONSTRAINT turn_events_id_not_blank CHECK (btrim(turn_events_id) <> ''),
    CONSTRAINT turn_events_seq_nonnegative CHECK (seq >= 0),
    CONSTRAINT turn_events_turn_seq_unique UNIQUE (turn_id, seq),
    CONSTRAINT turn_events_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE,
    CONSTRAINT turn_events_type_fk FOREIGN KEY (type)
        REFERENCES turn_event_types (type_id)
);

-- +goose Down
DROP TABLE turn_events;
DROP TABLE turns;
DROP TABLE summaries;
DROP TABLE messages;
DROP TABLE sessions;
DROP TABLE turn_event_types;
