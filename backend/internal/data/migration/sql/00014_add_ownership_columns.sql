-- +goose Up
-- Ownership, retrofitted onto tables that were built without it (V5 §2.4).
--
-- Until now exactly one thing in this schema had an account column: memory_*
-- (00013). Everything else belonged to nobody, and that was correct while the
-- database held one person's work. It stops being correct the moment a second
-- account exists, and the failure has no symptom — every read that lists rows
-- keeps working, it just lists everybody's.
--
-- Which tables get the column is decided by one question, not by whether a
-- table looks like a user's: does this row's lifetime hang off a session? If it
-- does, the owner is reachable by walking the foreign key and adding a column
-- here would be a second place for the same fact to be wrong. If it does not,
-- the row has to carry the owner itself, because there is no chain to walk.
--
-- By that rule the derived tables (messages, summaries, turns, turn_events,
-- notebook_entry_categories, practice_*, the seven mastery_* children, the
-- reading workspace tables, documents and document_chunks) get nothing, and
-- these eight get a column:
--
--   sessions             the root the derived ones walk to
--   notebook_entries     session_id is nullable, so the chain can be absent
--   notebook_categories  a category can exist with nothing filed under it
--   mastery_paths        owner_session_id is the session that started it, and
--                        the path outlives that session
--   library_files        an upload belongs to no conversation
--   knowledge_bases      a collection is built, not talked into existence
--   reading_materials    a material is made from a file, not from a session
--   llm_calls            quota is summed per account, and joining through a
--                        session would drop the short calls that have none
--
-- Every one of them starts NULL, and this migration assigns nothing. NULL is a
-- state with a meaning here, not an oversight: it reads as "history from before
-- V5, belonging to no account". Only an administrator can see such rows, and
-- claiming one is an explicit action that leaves an audit row. The alternative
-- — handing the existing rows to the first administrator — is right in every
-- deployment that has only ever had one person in it, which is nearly all of
-- them, and catastrophic in the one where it is not: the mistake is somebody
-- else's conversation appearing in your list, and nothing anywhere reports it.
--
-- All eight foreign keys cascade. The users table is the one table whose rows
-- can be deleted, and deleting an account should take that account's work with
-- it. This is the same sentence 00013 wrote about memory, and users carries
-- neither a full-text nor a vector index, so none of these are in the ERROR
-- 4016 risk either.

ALTER TABLE sessions            ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE notebook_entries    ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE notebook_categories ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE mastery_paths       ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE library_files       ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE knowledge_bases     ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE reading_materials   ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE llm_calls           ADD COLUMN owner_id BIGINT NULL;

ALTER TABLE sessions ADD CONSTRAINT sessions_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE notebook_entries ADD CONSTRAINT notebook_entries_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE notebook_categories ADD CONSTRAINT notebook_categories_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE mastery_paths ADD CONSTRAINT mastery_paths_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE library_files ADD CONSTRAINT library_files_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE knowledge_bases ADD CONSTRAINT knowledge_bases_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE reading_materials ADD CONSTRAINT reading_materials_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;
ALTER TABLE llm_calls ADD CONSTRAINT llm_calls_owner_fk
    FOREIGN KEY (owner_id) REFERENCES users (user_id) ON DELETE CASCADE;

-- Category names were unique across the whole database. That is right for one
-- person and wrong for two: with the constraint as it stands, the second
-- account to want a category called "第三章" cannot have one — and what it gets
-- back is "that name is already taken", pointing at a row it is not allowed to
-- see. That is the kind of failure that gets diagnosed as a haunting.
--
-- The replacement is unique per owner, and it can be built over the existing
-- data without touching it: every current row has a NULL owner, and a unique
-- index admits any number of NULLs, so no two legacy rows collide on
-- (NULL, name) no matter what they are called.
ALTER TABLE notebook_categories DROP INDEX notebook_categories_name_unique;
CREATE UNIQUE INDEX notebook_categories_owner_name
    ON notebook_categories (owner_id, name);

-- preset has been a dead column since 00001: the model carries it, the
-- registration path writes '', and nothing has ever read it. V5 reads it, so it
-- gets a constraint — and the constraint has to admit '', because '' is what
-- every account registered before this migration actually holds. AUTH-03 names
-- three presets and '' is not among them; it is read as 'standard' at the one
-- place the four authorization layers are combined, on the grounds that an
-- account that could do everything before the upgrade is not made restricted by
-- the upgrade. Writing the constraint without '' would refuse the whole
-- existing population.
ALTER TABLE users ADD CONSTRAINT users_preset_supported
    CHECK (preset IN ('', 'standard', 'learner', 'custom'));

-- own_id stores a worker's identity in a VARCHAR, while StartRequest.OwnerID —
-- added in V4 for memory — is an int64 account id. Two different things share a
-- name, a type apart, on a table that already carries fencing_token beside it.
-- Renamed rather than documented: the next person to read this column will use
-- it as an account if it keeps the name.
ALTER TABLE turns CHANGE COLUMN own_id worker_id VARCHAR(128) NULL;

-- +goose Down
-- Restoring UNIQUE (name) can be impossible, and when it is, this has to fail
-- rather than succeed by deleting somebody's data. Two accounts with a category
-- of the same name is a legitimate state after this migration and a violation
-- after the rollback, so the rollback is refused with a readable reason and the
-- rows are left alone.
--
-- There is no SIGNAL outside a stored program, so the refusal is a guard table
-- whose CHECK cannot be satisfied by the value the query below inserts: zero
-- duplicate names inserts 1 and passes, any duplicates insert 0 and stop the
-- rollback with a constraint violation.
CREATE TABLE v5_down_guard (
    ok TINYINT NOT NULL,
    CONSTRAINT v5_down_guard_ok CHECK (ok = 1)
);
INSERT INTO v5_down_guard (ok)
SELECT IF(COUNT(*) = 0, 1, 0)
FROM (
    SELECT name FROM notebook_categories GROUP BY name HAVING COUNT(*) > 1
) AS duplicate_names;
DROP TABLE v5_down_guard;

ALTER TABLE turns CHANGE COLUMN worker_id own_id VARCHAR(128) NULL;

ALTER TABLE users DROP CONSTRAINT users_preset_supported;

-- The foreign keys go first, so that dropping the composite index below cannot
-- be refused for being the index the constraint was using.
ALTER TABLE llm_calls DROP FOREIGN KEY llm_calls_owner_fk;
ALTER TABLE reading_materials DROP FOREIGN KEY reading_materials_owner_fk;
ALTER TABLE knowledge_bases DROP FOREIGN KEY knowledge_bases_owner_fk;
ALTER TABLE library_files DROP FOREIGN KEY library_files_owner_fk;
ALTER TABLE mastery_paths DROP FOREIGN KEY mastery_paths_owner_fk;
ALTER TABLE notebook_categories DROP FOREIGN KEY notebook_categories_owner_fk;
ALTER TABLE notebook_entries DROP FOREIGN KEY notebook_entries_owner_fk;
ALTER TABLE sessions DROP FOREIGN KEY sessions_owner_fk;

DROP INDEX notebook_categories_owner_name ON notebook_categories;
ALTER TABLE notebook_categories ADD CONSTRAINT notebook_categories_name_unique
    UNIQUE (name);

ALTER TABLE llm_calls           DROP COLUMN owner_id;
ALTER TABLE reading_materials   DROP COLUMN owner_id;
ALTER TABLE knowledge_bases     DROP COLUMN owner_id;
ALTER TABLE library_files       DROP COLUMN owner_id;
ALTER TABLE mastery_paths       DROP COLUMN owner_id;
ALTER TABLE notebook_categories DROP COLUMN owner_id;
ALTER TABLE notebook_entries    DROP COLUMN owner_id;
ALTER TABLE sessions            DROP COLUMN owner_id;
