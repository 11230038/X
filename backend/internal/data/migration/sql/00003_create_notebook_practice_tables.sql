-- +goose Up
-- seekdb/MySQL: keyed columns are VARCHAR(n) because TEXT cannot be indexed.
CREATE TABLE notebook_entries (
    notebook_entries_id VARCHAR(128) PRIMARY KEY,
    question_id VARCHAR(128) NOT NULL,
    question TEXT NOT NULL,
    question_type VARCHAR(64) NOT NULL,
    question_illustration TEXT,
    extra JSON,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME,
    difficulty VARCHAR(64),
    user_answer TEXT,
    user_answer_image JSON,
    options JSON,
    correct_answer TEXT,
    explanation TEXT,
    quality VARCHAR(64),
    assessment_type VARCHAR(64),
    is_correct BOOLEAN,
    result VARCHAR(64),
    resolved BOOLEAN,
    attempt_count INTEGER,
    bookmarked BOOLEAN,
    source VARCHAR(64),
    mastery_path_id VARCHAR(128),
    knowledge_point_id VARCHAR(128),
    session_id VARCHAR(128),
    turn_id VARCHAR(128),
    CONSTRAINT notebook_entries_id_not_blank CHECK (TRIM(notebook_entries_id) <> ''),
    CONSTRAINT notebook_entries_question_id_not_blank CHECK (TRIM(question_id) <> ''),
    CONSTRAINT notebook_entries_question_not_blank CHECK (TRIM(question) <> ''),
    CONSTRAINT notebook_entries_question_type_not_blank CHECK (TRIM(question_type) <> ''),
    CONSTRAINT notebook_entries_question_id_unique UNIQUE (question_id),
    CONSTRAINT notebook_entries_attempt_count_nonnegative CHECK (
        attempt_count IS NULL OR attempt_count >= 0
    ),
    CONSTRAINT notebook_entries_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE,
    CONSTRAINT notebook_entries_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_notebook_entries_session_creat
    ON notebook_entries (session_id, creat_time DESC);
CREATE INDEX idx_notebook_entries_turn_creat
    ON notebook_entries (turn_id, creat_time DESC);
CREATE INDEX idx_notebook_entries_review
    ON notebook_entries (source, resolved, creat_time DESC);
CREATE INDEX idx_notebook_entries_mastery_knowledge
    ON notebook_entries (mastery_path_id, knowledge_point_id);
-- seekdb has no partial indexes; this one only ever served bookmarked rows.
CREATE INDEX idx_notebook_entries_bookmarked
    ON notebook_entries (bookmarked, creat_time DESC);

CREATE TABLE notebook_categories (
    notebook_categories_id VARCHAR(128) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT notebook_categories_id_not_blank CHECK (TRIM(notebook_categories_id) <> ''),
    CONSTRAINT notebook_categories_name_not_blank CHECK (TRIM(name) <> ''),
    CONSTRAINT notebook_categories_name_unique UNIQUE (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE reading_quiz_pending (
    question_id VARCHAR(128) PRIMARY KEY,
    question TEXT NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_quiz_pending_question_id_not_blank CHECK (TRIM(question_id) <> ''),
    CONSTRAINT reading_quiz_pending_question_not_blank CHECK (TRIM(question) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE notebook_entry_categories (
    notebook_entries_id VARCHAR(128) NOT NULL,
    notebook_categories_id VARCHAR(128) NOT NULL,
    CONSTRAINT notebook_entry_categories_pk
        PRIMARY KEY (notebook_entries_id, notebook_categories_id),
    CONSTRAINT notebook_entry_categories_entry_id_not_blank
        CHECK (TRIM(notebook_entries_id) <> ''),
    CONSTRAINT notebook_entry_categories_category_id_not_blank
        CHECK (TRIM(notebook_categories_id) <> ''),
    CONSTRAINT notebook_entry_categories_entry_fk FOREIGN KEY (notebook_entries_id)
        REFERENCES notebook_entries (notebook_entries_id) ON DELETE CASCADE,
    CONSTRAINT notebook_entry_categories_category_fk FOREIGN KEY (notebook_categories_id)
        REFERENCES notebook_categories (notebook_categories_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_notebook_entry_categories_category
    ON notebook_entry_categories (notebook_categories_id, notebook_entries_id);

CREATE TABLE practice_review_state (
    notebook_entries_id VARCHAR(128) PRIMARY KEY,
    first_wrong_time DATETIME,
    due_time DATETIME,
    last_review_time DATETIME,
    is_mistake BOOLEAN,
    -- `case` is reserved in MySQL, so the previous bare "case" needs quoting.
    `case` DOUBLE PRECISION,
    streak INTEGER,
    review_count INTEGER,
    lapses INTEGER,
    extra JSON,
    CONSTRAINT practice_review_state_entry_id_not_blank
        CHECK (TRIM(notebook_entries_id) <> ''),
    CONSTRAINT practice_review_state_streak_nonnegative CHECK (
        streak IS NULL OR streak >= 0
    ),
    CONSTRAINT practice_review_state_review_count_nonnegative CHECK (
        review_count IS NULL OR review_count >= 0
    ),
    CONSTRAINT practice_review_state_lapses_nonnegative CHECK (
        lapses IS NULL OR lapses >= 0
    ),
    CONSTRAINT practice_review_state_entry_fk FOREIGN KEY (notebook_entries_id)
        REFERENCES notebook_entries (notebook_entries_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_practice_review_state_due
    ON practice_review_state (due_time, notebook_entries_id);

CREATE TABLE practice_review_events (
    request_id VARCHAR(128) PRIMARY KEY,
    notebook_entries_id VARCHAR(128) NOT NULL,
    user_answer TEXT,
    rating DOUBLE PRECISION,
    outcome_json JSON,
    review_time DATETIME,
    CONSTRAINT practice_review_events_request_id_not_blank
        CHECK (TRIM(request_id) <> ''),
    CONSTRAINT practice_review_events_entry_fk FOREIGN KEY (notebook_entries_id)
        REFERENCES notebook_entries (notebook_entries_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_practice_review_events_entry_time
    ON practice_review_events (notebook_entries_id, review_time DESC);

-- +goose Down
DROP TABLE practice_review_events;
DROP TABLE practice_review_state;
DROP TABLE notebook_entry_categories;
DROP TABLE reading_quiz_pending;
DROP TABLE notebook_categories;
DROP TABLE notebook_entries;
