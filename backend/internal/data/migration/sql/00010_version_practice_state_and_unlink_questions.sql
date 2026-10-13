-- +goose Up
-- Two columns and two foreign keys. Nothing here is a new table: the six tables
-- of the notebook and the practice loop were created in 00003 and have carried
-- models ever since.
--
-- They have also never carried a row. V1 and V2 built the tables and put nothing
-- in them — there is no repository, no service and no endpoint for any of them
-- yet — and that is what makes this the cheap moment for a change to their
-- shape. The repair step that 00008 and 00009 both needed, for the data an
-- earlier version had already written, has nothing to repair here.

-- 1) QN-04 asks for concurrent review submissions to be serialised, and there is
-- no column to hold the number they serialise on.
--
-- The write path reads a row's version along with the rest of its state and then
-- writes with `WHERE ... AND version = ?`. No rows affected means somebody else
-- got there first, which is a different outcome from the write having failed and
-- has to be reported as one. Starting at 0 is what makes the first update of a
-- row written by an older deployment indistinguishable from any other.
ALTER TABLE practice_review_state
    ADD COLUMN version BIGINT NOT NULL DEFAULT 0;

-- 2) QN-01 filters the notebook by the material a question came from, and no
-- column here can express that. Without it that one filter has no implementation.
--
-- A plain string, not a foreign key, and deliberately so: a material id means
-- whatever the storage behind its source says it means, and an attachment's id
-- and a knowledge base document's id are not drawn from one space. There is
-- nothing here for a foreign key to reference. The source column, written in the
-- same breath by the same caller, is what says how to read it.
ALTER TABLE notebook_entries
    ADD COLUMN material_id VARCHAR(128) NULL;

-- 3) A question outlives the conversation it was asked in.
--
-- question_id is unique across this whole table, where the reference schema
-- scoped it to a session. That decision makes a question a shared asset several
-- conversations draw on — and the two foreign keys below were still CASCADE,
-- which is the other half of the same decision contradicting it. An asset cannot
-- be owned by one of its containers. Deleting one conversation would have taken
-- every question it produced, and with them each question's review state and its
-- entire review history, since both of those cascade from notebook_entries in
-- turn.
--
-- These two columns keep the meaning they can defend — where this question was
-- first asked — and give up the one they cannot. NULL is a truthful value for
-- them, and one the schema already allowed: V4's reading and mastery sources
-- never have a conversation to point at, which is why they were nullable from
-- the start.
--
-- A drop and an add written as two statements, which is not tidiness: this
-- engine refuses a foreign key change that shares a statement with other DDL
-- ("Add/modify foreign key together with other DDLs not supported", ERROR 1235).
-- It is the same wall 00009's Down is split around, met from the other side —
-- there the index drop could not share a statement, here the constraint change
-- cannot, and the two together mean nothing that touches a foreign key goes in a
-- statement with anything else.
ALTER TABLE notebook_entries
    DROP FOREIGN KEY notebook_entries_session_fk;
ALTER TABLE notebook_entries
    ADD CONSTRAINT notebook_entries_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE SET NULL;

ALTER TABLE notebook_entries
    DROP FOREIGN KEY notebook_entries_turn_fk;
ALTER TABLE notebook_entries
    ADD CONSTRAINT notebook_entries_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE SET NULL;

-- 4) The review index gains the column added above it. "Questions from this
-- material, still unresolved, newest first" is then a range on one index instead
-- of a scan of everything unresolved.
DROP INDEX idx_notebook_entries_review ON notebook_entries;
CREATE INDEX idx_notebook_entries_review
    ON notebook_entries (source, material_id, resolved, creat_time DESC);

-- +goose Down
-- The index first, because it names a column that has to outlive it.
DROP INDEX idx_notebook_entries_review ON notebook_entries;
CREATE INDEX idx_notebook_entries_review
    ON notebook_entries (source, resolved, creat_time DESC);

-- Back to CASCADE. This cannot fail on a row that outlived its session, because
-- the two halves of that are what the previous statement pair made possible and
-- this one undoes: a NULL session_id is not a dangling reference.
ALTER TABLE notebook_entries
    DROP FOREIGN KEY notebook_entries_session_fk;
ALTER TABLE notebook_entries
    ADD CONSTRAINT notebook_entries_session_fk FOREIGN KEY (session_id)
        REFERENCES sessions (session_id) ON DELETE CASCADE;

ALTER TABLE notebook_entries
    DROP FOREIGN KEY notebook_entries_turn_fk;
ALTER TABLE notebook_entries
    ADD CONSTRAINT notebook_entries_turn_fk FOREIGN KEY (turn_id)
        REFERENCES turns (turn_id) ON DELETE CASCADE;

-- One column per statement: this build refuses a column change in the same
-- statement as an index drop ("table redefinition and drop index in single
-- statement not supported"), and it is not worth finding out how far that
-- extends to several column changes at once.
ALTER TABLE notebook_entries DROP COLUMN material_id;
ALTER TABLE practice_review_state DROP COLUMN version;
