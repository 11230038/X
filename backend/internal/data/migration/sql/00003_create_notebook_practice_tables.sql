-- +goose Up
CREATE TABLE notebook_entries (
    notebook_entries_id TEXT PRIMARY KEY,
    question_id TEXT NOT NULL,
    question TEXT NOT NULL,
    question_type TEXT NOT NULL,
    question_illustration TEXT,
    extra JSON,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMPTZ,
    difficulty TEXT,
    user_answer TEXT,
    user_answer_image JSON,
    options JSON,
    correct_answer TEXT,
    explanation TEXT,
    quality TEXT,
    assessment_type TEXT,
    is_correct BOOLEAN,
    result TEXT,
    resolved BOOLEAN,
    attempt_count INTEGER,
    bookmarked BOOLEAN,
    source TEXT,
    mastery_path_id TEXT,
    knowledge_point_id TEXT,
    session_id TEXT,
    turn_id TEXT,
    CONSTRAINT notebook_entries_id_not_blank CHECK (btrim(notebook_entries_id) <> ''),
    CONSTRAINT notebook_entries_question_id_not_blank CHECK (btrim(question_id) <> ''),
    CONSTRAINT notebook_entries_question_not_blank CHECK (btrim(question) <> ''),
    CONSTRAINT notebook_entries_question_type_not_blank CHECK (btrim(question_type) <> ''),
    CONSTRAINT notebook_entries_question_id_unique UNIQUE (question_id),
    CONSTRAINT notebook_entries_attempt_count_nonnegative CHECK (
        attempt_count IS NULL OR attempt_count >= 0
    ),
    CONSTRAINT notebook_entries_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE,
    CONSTRAINT notebook_entries_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE
);

CREATE INDEX idx_notebook_entries_session_creat
    ON notebook_entries (session_id, creat_time DESC);
CREATE INDEX idx_notebook_entries_turn_creat
    ON notebook_entries (turn_id, creat_time DESC);
CREATE INDEX idx_notebook_entries_review
    ON notebook_entries (source, resolved, creat_time DESC);
CREATE INDEX idx_notebook_entries_mastery_knowledge
    ON notebook_entries (mastery_path_id, knowledge_point_id);
CREATE INDEX idx_notebook_entries_bookmarked
    ON notebook_entries (bookmarked, creat_time DESC)
    WHERE bookmarked = TRUE;

CREATE TABLE notebook_categories (
    notebook_categories_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT notebook_categories_id_not_blank CHECK (btrim(notebook_categories_id) <> ''),
    CONSTRAINT notebook_categories_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT notebook_categories_name_unique UNIQUE (name)
);

CREATE TABLE reading_quiz_pending (
    question_id TEXT PRIMARY KEY,
    question TEXT NOT NULL,
    creat_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_quiz_pending_question_id_not_blank CHECK (btrim(question_id) <> ''),
    CONSTRAINT reading_quiz_pending_question_not_blank CHECK (btrim(question) <> '')
);

CREATE TABLE notebook_entry_categories (
    notebook_entries_id TEXT NOT NULL,
    notebook_categories_id TEXT NOT NULL,
    CONSTRAINT notebook_entry_categories_pk
        PRIMARY KEY (notebook_entries_id, notebook_categories_id),
    CONSTRAINT notebook_entry_categories_entry_id_not_blank
        CHECK (btrim(notebook_entries_id) <> ''),
    CONSTRAINT notebook_entry_categories_category_id_not_blank
        CHECK (btrim(notebook_categories_id) <> ''),
    CONSTRAINT notebook_entry_categories_entry_fk FOREIGN KEY (notebook_entries_id)
        REFERENCES notebook_entries (notebook_entries_id) ON DELETE CASCADE,
    CONSTRAINT notebook_entry_categories_category_fk FOREIGN KEY (notebook_categories_id)
        REFERENCES notebook_categories (notebook_categories_id) ON DELETE CASCADE
);

CREATE INDEX idx_notebook_entry_categories_category
    ON notebook_entry_categories (notebook_categories_id, notebook_entries_id);

CREATE TABLE practice_review_state (
    notebook_entries_id TEXT PRIMARY KEY,
    first_wrong_time TIMESTAMPTZ,
    due_time TIMESTAMPTZ,
    last_review_time TIMESTAMPTZ,
    is_mistake BOOLEAN,
    "case" DOUBLE PRECISION,
    streak INTEGER,
    review_count INTEGER,
    lapses INTEGER,
    extra JSON,
    CONSTRAINT practice_review_state_entry_id_not_blank
        CHECK (btrim(notebook_entries_id) <> ''),
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
);

CREATE INDEX idx_practice_review_state_due
    ON practice_review_state (due_time, notebook_entries_id);

CREATE TABLE practice_review_events (
    request_id TEXT PRIMARY KEY,
    notebook_entries_id TEXT NOT NULL,
    user_answer TEXT,
    rating DOUBLE PRECISION,
    outcome_json JSON,
    review_time TIMESTAMPTZ,
    CONSTRAINT practice_review_events_request_id_not_blank
        CHECK (btrim(request_id) <> ''),
    CONSTRAINT practice_review_events_entry_fk FOREIGN KEY (notebook_entries_id)
        REFERENCES notebook_entries (notebook_entries_id) ON DELETE CASCADE
);

CREATE INDEX idx_practice_review_events_entry_time
    ON practice_review_events (notebook_entries_id, review_time DESC);

-- +goose Down
DROP TABLE practice_review_events;
DROP TABLE practice_review_state;
DROP TABLE notebook_entry_categories;
DROP TABLE reading_quiz_pending;
DROP TABLE notebook_categories;
DROP TABLE notebook_entries;
