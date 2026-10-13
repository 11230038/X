-- +goose Up
-- Two invariants that were being kept by application logic, moved into the
-- schema. Both were found by running the storage contract against a real
-- seekdb, and neither was visible to a test double: the code said the right
-- thing and the database did not enforce it.
--
-- The mechanism in both cases is the same one MySQL offers for "at most one row
-- that satisfies a condition": a generated column that is NULL for every row
-- the rule does not speak about, because a unique index permits any number of
-- NULLs and exactly one non-NULL value.
--
-- Why not a partial index, or a plain UNIQUE (is_default): the schema has no
-- partial indexes, and a unique index over a boolean column admits one FALSE
-- row and one TRUE row, which is not "at most one default" but "at most two
-- bases".
--
-- Each rule is applied in two statements, and the first one is not decoration:
-- a unique index cannot be built over data that already breaks the rule. That is
-- exactly what happened the first time this file was run — against a database
-- holding four default bases that the old code had written — and the ALTER was
-- refused with a duplicate key error until the rows were put right. A
-- deployment that ran the old code has the same rows, so the repair belongs in
-- the migration rather than in a note telling somebody to clean up first.

-- Repair: of several default bases, the oldest keeps the mark.
--
-- The oldest is what DeleteKnowledgeBase promotes and what the listing shows
-- first after the mark, so a deployment that ends up with one default keeps the
-- one its own reading would have landed on.
UPDATE knowledge_bases
SET is_default = FALSE
WHERE is_default = TRUE
  AND kb_id <> (
      SELECT kb_id FROM (
          SELECT kb_id FROM knowledge_bases
          WHERE is_default = TRUE
          ORDER BY creat_time ASC, kb_id ASC
          LIMIT 1
      ) AS oldest
  );

-- At most one default base.
--
-- CreateKnowledgeBase read the table, found no default, and claimed the mark.
-- Two creators arriving at an empty table both read "no default" and both
-- claimed it. The function took a locking read over the whole table to
-- serialise them, which does work while the table has rows — and this database
-- runs READ-COMMITTED, where a locking read over an empty table locks nothing
-- at all. Four simultaneous creators left four default bases. The window is
-- the first base ever created, which is exactly when nobody is watching.
ALTER TABLE knowledge_bases
    ADD COLUMN default_marker VARCHAR(8)
        GENERATED ALWAYS AS (IF(is_default, 'yes', NULL)) STORED,
    ADD UNIQUE KEY knowledge_bases_one_default (default_marker);

-- Repair: of several documents of one file in one place, the oldest stays.
--
-- This one removes rows, and its chunks with them through the foreign key. They
-- are the duplicates the rule is about: two documents parsed from the same
-- bytes, holding the same text, which retrieval read as two sources. The one
-- kept is the one DocumentOfFile already resolves to — the oldest, and the
-- lowest id among documents of the same second — so a deployment that had a
-- duplicate keeps the document its own readers were already being shown.
--
-- The Down migration cannot put these rows back.
DELETE FROM documents
WHERE document_id IN (
    SELECT document_id FROM (
        SELECT newer.document_id AS document_id
        FROM documents newer
        JOIN documents older
          ON older.file_id = newer.file_id
         AND (older.kb_id <=> newer.kb_id)
         AND (older.creat_time < newer.creat_time
              OR (older.creat_time = newer.creat_time
                  AND older.document_id < newer.document_id))
        GROUP BY newer.document_id
    ) AS duplicates
);

-- One document per file per place.
--
-- The service's AddDocument reads whether the file is already parsed and writes
-- a document when it is not, and its comment says the loser of a race gets the
-- winner's document back. That recovery path was unreachable: the only unique
-- key on documents was the primary key, and the id is generated at random, so
-- two callers adding one file to one base at the same time both succeeded —
-- two documents for one file, and retrieval would have returned the same text
-- twice as two sources.
--
-- The scope column folds the two places a file can live into one value: a base
-- holds it under its id, and an attachment is kb_id NULL. NULL is what keeps
-- attachments out of a unique index — `UNIQUE (file_id, kb_id)` would let a
-- file be attached any number of times over, because a unique index treats
-- every NULL as distinct — so the NULL is folded to the empty string, which no
-- base can be named (knowledge_bases_id_not_blank refuses that).
--
-- The index is on documents, which carries no vector index: the rule in
-- 00007's header, that a table with a VECTOR index must not index a column that
-- is also a foreign key, is about the table holding the vector index. The
-- cascade from library_files into documents is exercised by the contract tests
-- either way.
ALTER TABLE documents
    ADD COLUMN file_scope VARCHAR(128)
        GENERATED ALWAYS AS (IFNULL(kb_id, '')) STORED,
    ADD UNIQUE KEY documents_file_scope (file_id, file_scope);

-- +goose Down
-- Four statements rather than two, because this build refuses an index drop in
-- the same statement as a column change ("table redefinition and drop index in
-- single statement not supported"). The Up side is not affected: adding a
-- column and adding a unique key together is accepted.
ALTER TABLE documents DROP INDEX documents_file_scope;
ALTER TABLE documents DROP COLUMN file_scope;

ALTER TABLE knowledge_bases DROP INDEX knowledge_bases_one_default;
ALTER TABLE knowledge_bases DROP COLUMN default_marker;
