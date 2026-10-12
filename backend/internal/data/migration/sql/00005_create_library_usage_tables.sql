-- +goose Up
-- seekdb/MySQL notes:
--   * TEXT is neither indexable nor defaultable, so keyed columns are VARCHAR(n).
--   * '~' becomes REGEXP and json_typeof() becomes JSON_TYPE(), which returns the
--     type name in upper case.
--   * Backslashes are doubled so the regex engine still sees '\.' — a single
--     backslash is consumed by the string literal parser.
--   * split_part(a,b,c) is spelled SUBSTRING_INDEX; taking the last segment needs
--     a negative count, which is why the path-to-id check nests three times.
CREATE TABLE library_files (
    id VARCHAR(128) PRIMARY KEY,
    sha256 VARCHAR(64) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    mime_type VARCHAR(128) NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    library_path VARCHAR(512) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at DATETIME,
    CONSTRAINT library_files_id_not_blank CHECK (id REGEXP '[^[:space:]]'),
    CONSTRAINT library_files_sha256_format CHECK (sha256 REGEXP '^[0-9a-f]{64}$'),
    CONSTRAINT library_files_filename_not_blank CHECK (filename REGEXP '[^[:space:]]'),
    CONSTRAINT library_files_size_nonnegative CHECK (size_bytes >= 0),
    CONSTRAINT library_files_path_not_blank CHECK (library_path REGEXP '[^[:space:]]'),
    CONSTRAINT library_files_path_relative CHECK (
        library_path REGEXP '^(images/lib_[0-9a-f]{32}\\.(jpg|png|gif|webp)|pdf/lib_[0-9a-f]{32}\\.pdf|ppt/lib_[0-9a-f]{32}\\.(ppt|pptx)|doc/lib_[0-9a-f]{32}\\.(doc|docx)|md/lib_[0-9a-f]{32}\\.md|mp3/lib_[0-9a-f]{32}\\.mp3)$'
        AND SUBSTRING_INDEX(SUBSTRING_INDEX(SUBSTRING_INDEX(library_path, '/', 2), '/', -1), '.', 1) = id
    ),
    CONSTRAINT library_files_deleted_state CHECK (
        (is_deleted = FALSE AND deleted_at IS NULL)
        OR (is_deleted = TRUE AND deleted_at IS NOT NULL)
    ),
    CONSTRAINT library_files_path_unique UNIQUE (library_path)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_library_files_sha256
    ON library_files (sha256);
-- seekdb has no partial indexes; this one only ever served live rows.
CREATE INDEX idx_library_files_active_created
    ON library_files (created_at DESC, id);

CREATE TABLE llm_calls (
    call_id VARCHAR(128) PRIMARY KEY,
    started_at DATETIME NOT NULL,
    session_id VARCHAR(128) NOT NULL,
    turn_id VARCHAR(128) NOT NULL,
    source VARCHAR(64) NOT NULL,
    usage_json JSON NOT NULL,
    CONSTRAINT llm_calls_id_not_blank CHECK (call_id REGEXP '[^[:space:]]'),
    CONSTRAINT llm_calls_session_not_blank CHECK (session_id REGEXP '[^[:space:]]'),
    CONSTRAINT llm_calls_turn_not_blank CHECK (turn_id REGEXP '[^[:space:]]'),
    CONSTRAINT llm_calls_source_not_blank CHECK (source REGEXP '[^[:space:]]'),
    CONSTRAINT llm_calls_usage_object CHECK (JSON_TYPE(usage_json) = 'OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_llm_calls_started_at
    ON llm_calls (started_at DESC);
CREATE INDEX idx_llm_calls_session_started
    ON llm_calls (session_id, started_at DESC, call_id);
CREATE INDEX idx_llm_calls_turn_started
    ON llm_calls (turn_id, started_at DESC, call_id);

-- +goose Down
DROP TABLE llm_calls;
DROP TABLE library_files;
