-- +goose Up
-- The reading layer: the tables a reader's place in a material lives in, plus
-- the two columns V1 left off the workspace tab.
--
-- Nothing here is a second copy of the parsed text. A material is a row pointing
-- at a document (reading_materials.content_id, §2.8) and a locator is a
-- coordinate into that document, so the parser, the chunker, the vectors and the
-- citation path stay exactly where V2 put them. What this migration adds is the
-- part V2 has no opinion about: where somebody stopped reading, what they wrote
-- in the margin, and which two materials a reader said were the same book in two
-- languages.

-- 1) reading_quiz_pending gets the primary key the reference schema states.
--
-- V3 §0.2 第 7 条 trimmed this table down to a lone question_id when it was
-- created, and the table has never carried a row: V3 never used it. That is what
-- makes this the cheapest moment to rebuild it, and the reason to do it at all
-- is that a question generated *about a passage* is meaningless without the
-- passage — the key has to say which material and which locator it came from, or
-- two questions about two different paragraphs of two different books collide on
-- one id.
DROP TABLE reading_quiz_pending;
CREATE TABLE reading_quiz_pending (
    material_id   VARCHAR(128) NOT NULL,
    locator       VARCHAR(255) NOT NULL,
    question_id   VARCHAR(128) NOT NULL,
    question_json JSON NOT NULL,
    creat_time    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_quiz_pending_pk PRIMARY KEY (material_id, locator, question_id),
    CONSTRAINT reading_quiz_pending_material_not_blank CHECK (TRIM(material_id) <> ''),
    CONSTRAINT reading_quiz_pending_locator_not_blank CHECK (TRIM(locator) <> ''),
    CONSTRAINT reading_quiz_pending_question_not_blank CHECK (TRIM(question_id) <> ''),
    CONSTRAINT reading_quiz_pending_material_fk FOREIGN KEY (material_id)
        REFERENCES reading_materials (material_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- 2) Where the reader stopped (READ-01).
--
-- The key is (material, session) and not material alone, and that is the whole
-- of how READ-05 comes out right: two tabs reading two different books cannot
-- interfere because they are two different keys, while two tabs reading the
-- *same* book in one conversation share one position — which is what a person
-- expects, because "read on from where I left off" is about the book and the
-- person, not about the tab.
--
-- revision is what makes the write safe to do twice. The column holds what the
-- reader has saved; a writer sends the revision it read and gets a conflict when
-- somebody else has moved it, which is the same shape as the practice review
-- state and the mastery path version.
CREATE TABLE reading_positions (
    material_id  VARCHAR(128) NOT NULL,
    session_id   VARCHAR(128) NOT NULL,
    locator_json JSON NOT NULL,
    revision     INTEGER NOT NULL DEFAULT 1,
    creat_time   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time  DATETIME,
    CONSTRAINT reading_positions_pk PRIMARY KEY (material_id, session_id),
    CONSTRAINT reading_positions_material_not_blank CHECK (TRIM(material_id) <> ''),
    CONSTRAINT reading_positions_session_not_blank CHECK (TRIM(session_id) <> ''),
    CONSTRAINT reading_positions_revision_positive CHECK (revision >= 1),
    CONSTRAINT reading_positions_material_fk FOREIGN KEY (material_id)
        REFERENCES reading_materials (material_id) ON DELETE CASCADE,
    CONSTRAINT reading_positions_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_reading_positions_session_update
    ON reading_positions (session_id, update_time DESC);

-- 3) What the reader wrote in the margin (READ-01).
--
-- The two foreign keys point in opposite directions on purpose, and the
-- difference is the same sentence V3 §3 第 3 条 wrote about a different table: a
-- thing cannot be owned by two containers. The material owns the mark — delete
-- the book and the marginalia goes with it — while the session is only where it
-- was written, so deleting a conversation leaves the mark standing with no
-- session rather than taking it away. Losing what somebody wrote because they
-- tidied their list of conversations would be the wrong half of the pair to
-- keep.
CREATE TABLE reading_marks (
    mark_id      VARCHAR(128) PRIMARY KEY,
    material_id  VARCHAR(128) NOT NULL,
    session_id   VARCHAR(128) NULL,
    kind         VARCHAR(16) NOT NULL,
    locator_json JSON NOT NULL,
    body         TEXT,
    revision     INTEGER NOT NULL DEFAULT 1,
    creat_time   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time  DATETIME,
    CONSTRAINT reading_marks_id_not_blank CHECK (TRIM(mark_id) <> ''),
    CONSTRAINT reading_marks_material_not_blank CHECK (TRIM(material_id) <> ''),
    CONSTRAINT reading_marks_kind_closed CHECK (kind IN ('bookmark', 'highlight', 'note')),
    CONSTRAINT reading_marks_revision_positive CHECK (revision >= 1),
    CONSTRAINT reading_marks_material_fk FOREIGN KEY (material_id)
        REFERENCES reading_materials (material_id) ON DELETE CASCADE,
    CONSTRAINT reading_marks_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE INDEX idx_reading_marks_material_creat
    ON reading_marks (material_id, creat_time DESC);
CREATE INDEX idx_reading_marks_session
    ON reading_marks (session_id);

-- 4) The pairings a reader confirmed (READ-06).
--
-- Only confirmed ones are here, which is the whole design of §2.10: the
-- candidates an interface offers are computed by a query and never stored, so a
-- row's existence *is* the confirmation. That removes the state a nullable
-- confirmed_time would have introduced — there is no "suggested but not yet
-- accepted" row to expire, and no cleanup job to expire it.
CREATE TABLE reading_pairings (
    pairing_id              VARCHAR(128) PRIMARY KEY,
    primary_material_id     VARCHAR(128) NOT NULL,
    counterpart_material_id VARCHAR(128) NOT NULL,
    primary_language        VARCHAR(16) NOT NULL,
    counterpart_language    VARCHAR(16) NOT NULL,
    creat_time              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_pairings_id_not_blank CHECK (TRIM(pairing_id) <> ''),
    CONSTRAINT reading_pairings_primary_not_blank CHECK (TRIM(primary_material_id) <> ''),
    CONSTRAINT reading_pairings_counterpart_not_blank CHECK (TRIM(counterpart_material_id) <> ''),
    CONSTRAINT reading_pairings_distinct CHECK (primary_material_id <> counterpart_material_id),
    CONSTRAINT reading_pairings_languages_differ CHECK (primary_language <> counterpart_language),
    CONSTRAINT reading_pairings_primary_fk FOREIGN KEY (primary_material_id)
        REFERENCES reading_materials (material_id) ON DELETE CASCADE,
    CONSTRAINT reading_pairings_counterpart_fk FOREIGN KEY (counterpart_material_id)
        REFERENCES reading_materials (material_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE UNIQUE INDEX uq_reading_pairings_pair
    ON reading_pairings (primary_material_id, counterpart_material_id);

-- 5) The two columns the reference table has and ours does not.
--
-- They are nullable and stay nullable: NULL means nobody has said, which is
-- different from FALSE saying nobody has. A book that was never pinned is not
-- a book that was unpinned.
ALTER TABLE reading_workspace_materials ADD COLUMN pinned BOOLEAN NULL;
ALTER TABLE reading_workspace_materials ADD COLUMN opened BOOLEAN NULL;

-- +goose Down
ALTER TABLE reading_workspace_materials DROP COLUMN opened;
ALTER TABLE reading_workspace_materials DROP COLUMN pinned;
DROP TABLE reading_pairings;
DROP TABLE reading_marks;
DROP TABLE reading_positions;

-- The old shape, put back as 00003 wrote it. Nothing is migrated into it and
-- there is nothing to migrate: the table was empty when this migration dropped
-- it, and no version after this one writes it.
DROP TABLE reading_quiz_pending;
CREATE TABLE reading_quiz_pending (
    question_id VARCHAR(128) PRIMARY KEY,
    question TEXT NOT NULL,
    creat_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reading_quiz_pending_question_id_not_blank CHECK (TRIM(question_id) <> ''),
    CONSTRAINT reading_quiz_pending_question_not_blank CHECK (TRIM(question) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
