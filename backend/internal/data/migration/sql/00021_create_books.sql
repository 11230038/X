-- +goose Up
-- A book is a generated reading artifact: a title, a snapshot of the material it
-- was made from, and a confirmed outline. The snapshot is a column rather than a
-- reference because an outline and every chapter have to be written from the
-- same bytes; a source that grows or is re-parsed between chapter three and four
-- would make the book disagree with itself. It is bounded by the service, and it
-- never leaves this row through an API response.
--
-- Progress lives on the book because a book has exactly one reader in this
-- version: its owner. A shared book would need a (book_id, user_id) table, and
-- this column would be migrated into it; inventing that table now would be a row
-- per book joined on every shelf read for a feature that does not exist.
CREATE TABLE books (
    book_id              VARCHAR(128) NOT NULL,
    owner_id             BIGINT NOT NULL,
    title                VARCHAR(200) NOT NULL,
    source_kind          VARCHAR(32) NOT NULL,
    source_id            VARCHAR(128) NOT NULL DEFAULT '',
    source_title         VARCHAR(200) NOT NULL DEFAULT '',
    source_text          MEDIUMTEXT NOT NULL,
    source_chars         INT NOT NULL DEFAULT 0,
    source_truncated     TINYINT(1) NOT NULL DEFAULT 0,
    outline_confirmed    TINYINT(1) NOT NULL DEFAULT 0,
    outline_revision     INT NOT NULL DEFAULT 0,
    last_ordinal         INT NOT NULL DEFAULT 0,
    progress_update_time DATETIME NULL,
    creat_time           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (book_id),
    KEY idx_books_owner_update (owner_id, update_time),
    CONSTRAINT fk_books_owner FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE,
    CONSTRAINT chk_books_source_kind CHECK (source_kind IN ('session', 'knowledge_base', 'file', 'question_bank')),
    CONSTRAINT chk_books_source_chars CHECK (source_chars >= 0),
    CONSTRAINT chk_books_outline_revision CHECK (outline_revision >= 0),
    CONSTRAINT chk_books_last_ordinal CHECK (last_ordinal >= 0),
    CONSTRAINT chk_books_title_not_blank CHECK (TRIM(title) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- One row per chapter of the outline. The body is Markdown written in one
-- request; the status column records whether that request has produced one yet.
-- content is NULL rather than the empty string so that "never generated" and
-- "generated to nothing" cannot be confused, and so a failed regeneration can
-- keep the old body without a separate version row.
--
-- outline_revision is the guard a chapter write is made under. Replacing an
-- outline increments it on the book, deletes these rows and inserts new ones at
-- the new revision; a generation that started under the old outline updates
-- nothing rather than writing a body under a title it was not written for.
CREATE TABLE book_chapters (
    chapter_id       VARCHAR(128) NOT NULL,
    book_id          VARCHAR(128) NOT NULL,
    ordinal          INT NOT NULL,
    title            VARCHAR(200) NOT NULL,
    summary          VARCHAR(500) NOT NULL DEFAULT '',
    content          MEDIUMTEXT NULL,
    status           VARCHAR(16) NOT NULL DEFAULT 'pending',
    error_code       VARCHAR(64) NOT NULL DEFAULT '',
    outline_revision INT NOT NULL DEFAULT 0,
    creat_time       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (chapter_id),
    UNIQUE KEY uk_book_chapters_book_ordinal (book_id, ordinal),
    CONSTRAINT fk_book_chapters_book FOREIGN KEY (book_id) REFERENCES books (book_id) ON DELETE CASCADE,
    CONSTRAINT chk_book_chapters_status CHECK (status IN ('pending', 'ready', 'failed')),
    CONSTRAINT chk_book_chapters_ordinal CHECK (ordinal >= 1),
    CONSTRAINT chk_book_chapters_title_not_blank CHECK (TRIM(title) <> '')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- +goose Down
DROP TABLE IF EXISTS book_chapters;
DROP TABLE IF EXISTS books;
