-- +goose Up
CREATE TABLE library_files (
    id TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    library_path TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT library_files_id_not_blank CHECK (id ~ '[^[:space:]]'),
    CONSTRAINT library_files_sha256_format CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT library_files_filename_not_blank CHECK (filename ~ '[^[:space:]]'),
    CONSTRAINT library_files_size_nonnegative CHECK (size_bytes >= 0),
    CONSTRAINT library_files_path_not_blank CHECK (library_path ~ '[^[:space:]]'),
    CONSTRAINT library_files_path_relative CHECK (
        library_path ~ '^(images/lib_[0-9a-f]{32}\.(jpg|png|gif|webp)|pdf/lib_[0-9a-f]{32}\.pdf|ppt/lib_[0-9a-f]{32}\.(ppt|pptx)|doc/lib_[0-9a-f]{32}\.(doc|docx)|md/lib_[0-9a-f]{32}\.md|mp3/lib_[0-9a-f]{32}\.mp3)$'
        AND split_part(split_part(library_path, '/', 2), '.', 1) = id
    ),
    CONSTRAINT library_files_deleted_state CHECK (
        (is_deleted = FALSE AND deleted_at IS NULL)
        OR (is_deleted = TRUE AND deleted_at IS NOT NULL)
    ),
    CONSTRAINT library_files_path_unique UNIQUE (library_path)
);

CREATE INDEX idx_library_files_sha256
    ON library_files (sha256);
CREATE INDEX idx_library_files_active_created
    ON library_files (created_at DESC, id)
    WHERE is_deleted = FALSE;

CREATE TABLE llm_calls (
    call_id TEXT PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    source TEXT NOT NULL,
    usage_json JSON NOT NULL,
    CONSTRAINT llm_calls_id_not_blank CHECK (call_id ~ '[^[:space:]]'),
    CONSTRAINT llm_calls_session_not_blank CHECK (session_id ~ '[^[:space:]]'),
    CONSTRAINT llm_calls_turn_not_blank CHECK (turn_id ~ '[^[:space:]]'),
    CONSTRAINT llm_calls_source_not_blank CHECK (source ~ '[^[:space:]]'),
    CONSTRAINT llm_calls_usage_object CHECK (json_typeof(usage_json) = 'object')
);

CREATE INDEX idx_llm_calls_started_at
    ON llm_calls (started_at DESC);
CREATE INDEX idx_llm_calls_session_started
    ON llm_calls (session_id, started_at DESC, call_id);
CREATE INDEX idx_llm_calls_turn_started
    ON llm_calls (turn_id, started_at DESC, call_id);

-- +goose Down
DROP TABLE llm_calls;
DROP TABLE library_files;
